package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
)

const token = "test-token"

func handler(t *testing.T, s *Server) http.Handler {
	t.Helper()
	if s.Schemas == nil {
		set, err := schema.Load()
		if err != nil {
			t.Fatal(err)
		}
		s.Schemas = set
	}
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var bearer = map[string]string{"Authorization": "Bearer " + token}

func errorCodes(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not an error body: %s", rec.Body)
	}
	var out []string
	for _, is := range body.Errors {
		out = append(out, is.Path+" "+is.Code)
	}
	return out
}

func TestConfigFromEnv(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	if _, err := ConfigFromEnv(env(nil)); !errors.Is(err, ErrNoToken) {
		t.Errorf("no token: got %v, want ErrNoToken", err)
	}
	if _, err := ConfigFromEnv(env(map[string]string{"ALLOW_UNAUTHENTICATED": "1"})); !errors.Is(err, ErrNoToken) {
		t.Error(`ALLOW_UNAUTHENTICATED="1" accepted; only "true" opts out`)
	}
	c, err := ConfigFromEnv(env(map[string]string{"ALLOW_UNAUTHENTICATED": "true"}))
	if err != nil || !c.AllowUnauthenticated || c.ListenAddr != ":8080" {
		t.Errorf("dev opt-out: %+v %v", c, err)
	}
	c, err = ConfigFromEnv(env(map[string]string{"ADMIN_TOKEN": "x", "LISTEN_ADDR": ":9000"}))
	if err != nil || c.AdminToken != "x" || c.ListenAddr != ":9000" {
		t.Errorf("token: %+v %v", c, err)
	}
}

func TestHandlerRefusesOpenAPI(t *testing.T) {
	if _, err := (&Server{}).Handler(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("got %v, want ErrNoToken", err)
	}
}

func TestAuth(t *testing.T) {
	h := handler(t, &Server{Config: Config{AdminToken: token}})
	unauthorized := []map[string]string{
		nil,
		{"Authorization": "Bearer wrong"},
		{"Authorization": "Bearer " + token + "x"},
		{"Authorization": "bearer " + token},
		{"Authorization": "Basic dGVzdDp0ZXN0"},
		{"Authorization": token},
	}
	for _, hdr := range unauthorized {
		rec := do(h, "POST", "/api/audience/count", "{}", hdr)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%v: status %d", hdr, rec.Code)
		}
		if got := errorCodes(t, rec); !slices.Equal(got, []string{" unauthorized"}) {
			t.Errorf("%v: %v", hdr, got)
		}
	}
	if rec := do(h, "GET", "/api/does-not-exist", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown /api path without token: %d, want 401 (no route mapping)", rec.Code)
	}
	if rec := do(h, "GET", "/api/does-not-exist", "", bearer); rec.Code != http.StatusNotFound {
		t.Errorf("unknown /api path with token: %d, want 404", rec.Code)
	}
	if rec := do(h, "GET", "/api/audience/count", "", bearer); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET count: %d, want 405", rec.Code)
	}
}

func TestAllowUnauthenticated(t *testing.T) {
	open := handler(t, &Server{Config: Config{AllowUnauthenticated: true}})
	if rec := do(open, "POST", "/api/audience/count", "{", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("dev mode without token: %d, want 400 from the handler", rec.Code)
	}
	// A configured token is enforced even when the opt-out is also set.
	both := handler(t, &Server{Config: Config{AdminToken: token, AllowUnauthenticated: true}})
	if rec := do(both, "POST", "/api/audience/count", "{", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("token and opt-out: %d, want 401", rec.Code)
	}
}

func TestCountRequestErrors(t *testing.T) {
	h := handler(t, &Server{Config: Config{AdminToken: token}})
	valid := `{"version":1,"match":"all","conditions":[{"kind":"enum_attribute","field":"city","op":"eq","value":"istanbul"}]}`
	cases := []struct {
		name, body string
		status     int
		want       []string
	}{
		{"broken JSON", `{`, 400, []string{" invalid_json"}},
		{"two JSON values", `{"rule":` + valid + `} {}`, 400, []string{" invalid_json"}},
		{"not an object", `[]`, 422, []string{" invalid_type"}},
		{"missing rule", `{}`, 422, []string{"/rule required"}},
		{"extra key", `{"rule":` + valid + `,"sql":"1=1"}`, 422, []string{"/sql unknown_property"}},
		{"SQL in a city", `{"rule":{"version":1,"match":"all","conditions":[{"kind":"enum_attribute","field":"city","op":"eq","value":"x'; DROP TABLE users;--"}]}}`,
			422, []string{"/rule/conditions/0/value unknown_value"}},
		{"consent as a field", `{"rule":{"version":1,"match":"all","conditions":[{"kind":"enum_attribute","field":"consent_sms","op":"eq","value":"false"}]}}`,
			422, []string{"/rule/conditions/0/field unknown_value"}},
		{"body over 64 KiB", `{"rule":"` + strings.Repeat("a", 70_000) + `"}`, 413, []string{" body_too_large"}},
	}
	for _, c := range cases {
		rec := do(h, "POST", "/api/audience/count", c.body, bearer)
		if rec.Code != c.status {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rec.Code, c.status, rec.Body)
			continue
		}
		if got := errorCodes(t, rec); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: content type %q", c.name, ct)
		}
	}
}

func TestRequestID(t *testing.T) {
	h := handler(t, &Server{Config: Config{AdminToken: token}})
	generated := regexp.MustCompile(`^[0-9a-f]{16}$`)
	if got := do(h, "GET", "/healthz", "", map[string]string{"X-Request-ID": "abc-123"}).Header().Get("X-Request-ID"); got != "abc-123" {
		t.Errorf("valid id not kept: %q", got)
	}
	for _, bad := range []string{"", "has space", "new\nline", strings.Repeat("a", 65), "semi;colon"} {
		got := do(h, "GET", "/healthz", "", map[string]string{"X-Request-ID": bad}).Header().Get("X-Request-ID")
		if !generated.MatchString(got) {
			t.Errorf("%q: got %q, want a generated id", bad, got)
		}
	}
}

func TestHealthz(t *testing.T) {
	rec := do(handler(t, &Server{Config: Config{AdminToken: token}}), "GET", "/healthz", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
