package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type tc struct {
	name string
	json string
}

func mustLoad(t *testing.T) *Set {
	t.Helper()
	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func decode(t *testing.T, s string) any {
	t.Helper()
	v, err := Decode(strings.NewReader(s))
	if err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

func rule(conds ...string) string {
	return `{"version":1,"match":"all","conditions":[` + strings.Join(conds, ",") + `]}`
}

const (
	cityEq   = `{"kind":"enum_attribute","field":"city","op":"eq","value":"istanbul"}`
	cartAdd  = `{"kind":"event","event":"cart_add","within_days":30,"negate":false}`
	noBuy    = `{"kind":"event","event":"purchase","within_days":30,"negate":true}`
	newUsers = `{"kind":"number_attribute","field":"days_since_signup","op":"lte","value":30}`
)

func content(sms, subject, body string) string {
	b, _ := json.Marshal(map[string]any{
		"sms":   map[string]any{"body": sms},
		"email": map[string]any{"subject": subject, "body": body},
	})
	return string(b)
}

func validRules(t *testing.T) []tc {
	example, err := os.ReadFile("../../docs/examples/istanbul_cart_abandoners.json")
	if err != nil {
		t.Fatal(err)
	}
	ten := make([]string, 10)
	for i := range ten {
		ten[i] = cartAdd
	}
	return []tc{
		{"docs example (spec brief)", string(example)},
		{"match any", `{"version":1,"match":"any","conditions":[` + cityEq + `,` + newUsers + `]}`},
		{"city in list", rule(`{"kind":"enum_attribute","field":"city","op":"in","value":["istanbul","ankara"]}`)},
		{"all ten cities", rule(`{"kind":"enum_attribute","field":"city","op":"not_in","value":["istanbul","ankara","izmir","bursa","antalya","adana","konya","gaziantep","kocaeli","eskisehir"]}`)},
		{"language neq", rule(`{"kind":"enum_attribute","field":"language","op":"neq","value":"en"}`)},
		{"loyalty tier in", rule(`{"kind":"enum_attribute","field":"loyalty_tier","op":"in","value":["gold"]}`)},
		{"days_since_signup 0", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":0}`)},
		{"days_since_signup 3650", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"gt","value":3650}`)},
		{"within_days 1", rule(`{"kind":"event","event":"page_view","within_days":1,"negate":false}`)},
		{"within_days 365", rule(`{"kind":"event","event":"purchase","within_days":365,"negate":true}`)},
		{"ten conditions", rule(ten...)},
	}
}

func TestSegmentRule(t *testing.T) {
	s := mustLoad(t)
	for _, c := range validRules(t) {
		if err := s.Validate(SegmentRule, decode(t, c.json)); err != nil {
			t.Errorf("valid %q rejected: %v", c.name, err)
		}
	}
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = cartAdd
	}
	invalid := []tc{
		{"missing version", `{"match":"all","conditions":[` + cityEq + `]}`},
		{"version 2", `{"version":2,"match":"all","conditions":[` + cityEq + `]}`},
		{"unknown match", `{"version":1,"match":"some","conditions":[` + cityEq + `]}`},
		{"no conditions", rule()},
		{"eleven conditions", rule(eleven...)},
		{"extra top-level field", `{"version":1,"match":"all","conditions":[` + cityEq + `],"sql":"1=1"}`},
		{"extra condition field", rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":"istanbul","raw":"OR 1=1"}`)},
		{"unknown kind", rule(`{"kind":"sql","field":"city","op":"eq","value":"istanbul"}`)},
		{"field outside catalog", rule(`{"kind":"enum_attribute","field":"email","op":"eq","value":"a@example.com"}`)},
		{"consent is not a field", rule(`{"kind":"enum_attribute","field":"consent_sms","op":"eq","value":"false"}`)},
		{"SQL in a value", rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":"x'; DROP TABLE users;--"}`)},
		{"capitalized city (normalization is the validator's job)", rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":"Istanbul"}`)},
		{"Turkish dotted capital", rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":"İstanbul"}`)},
		{"gt on enum field", rule(`{"kind":"enum_attribute","field":"city","op":"gt","value":"istanbul"}`)},
		{"in with one string", rule(`{"kind":"enum_attribute","field":"city","op":"in","value":"istanbul"}`)},
		{"eq with a list", rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":["istanbul"]}`)},
		{"empty list", rule(`{"kind":"enum_attribute","field":"city","op":"in","value":[]}`)},
		{"duplicate list values", rule(`{"kind":"enum_attribute","field":"city","op":"in","value":["istanbul","istanbul"]}`)},
		{"language value from city list", rule(`{"kind":"enum_attribute","field":"language","op":"eq","value":"istanbul"}`)},
		{"negative days", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":-1}`)},
		{"days over max", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":3651}`)},
		{"fractional days", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":30.5}`)},
		{"days as string", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":"30"}`)},
		{"in on number field", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"in","value":30}`)},
		{"within_days 0", rule(`{"kind":"event","event":"cart_add","within_days":0,"negate":false}`)},
		{"within_days 366", rule(`{"kind":"event","event":"cart_add","within_days":366,"negate":false}`)},
		{"missing negate", rule(`{"kind":"event","event":"cart_add","within_days":30}`)},
		{"negate as string", rule(`{"kind":"event","event":"cart_add","within_days":30,"negate":"false"}`)},
		{"unknown event", rule(`{"kind":"event","event":"signup","within_days":30,"negate":false}`)},
	}
	for _, c := range invalid {
		if err := s.Validate(SegmentRule, decode(t, c.json)); err == nil {
			t.Errorf("invalid %q accepted", c.name)
		}
	}
}

func validContents() []tc {
	return []tc{
		{"Turkish copy with variables", content(
			"Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! %30 indirim için: {{campaign_url}}",
			"{{first_name}}, sepetin seni bekliyor",
			"Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor.")},
		{"sms exactly 160 runes of ş", content(strings.Repeat("ş", 160), "s", "b")},
		{"subject exactly 80", content("x", strings.Repeat("a", 80), "b")},
		{"email body exactly 3000", content("x", "s", strings.Repeat("a", 3000))},
	}
}

func TestContent(t *testing.T) {
	s := mustLoad(t)
	for _, c := range validContents() {
		if err := s.Validate(Content, decode(t, c.json)); err != nil {
			t.Errorf("valid %q rejected: %v", c.name, err)
		}
	}
	invalid := []tc{
		{"sms 161 runes", content(strings.Repeat("ş", 161), "s", "b")},
		{"empty sms", content("", "s", "b")},
		{"subject 81", content("x", strings.Repeat("a", 81), "b")},
		{"empty subject", content("x", "", "b")},
		{"email body 3001", content("x", "s", strings.Repeat("a", 3001))},
		{"missing email", `{"sms":{"body":"x"}}`},
		{"extra html field", `{"sms":{"body":"x"},"email":{"subject":"s","body":"b","html":"<b>x</b>"}}`},
		{"sms body as number", `{"sms":{"body":160},"email":{"subject":"s","body":"b"}}`},
	}
	for _, c := range invalid {
		if err := s.Validate(Content, decode(t, c.json)); err == nil {
			t.Errorf("invalid %q accepted", c.name)
		}
	}
}

func TestRevision(t *testing.T) {
	s := mustLoad(t)
	good := content("x", "s", "b")
	cases := []struct {
		name  string
		json  string
		valid bool
	}{
		{"segment and content", `{"segment":` + rule(cityEq) + `,"content":` + good + `}`, true},
		{"invalid nested segment", `{"segment":` + rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":"paris"}`) + `,"content":` + good + `}`, false},
		{"invalid nested content", `{"segment":` + rule(cityEq) + `,"content":` + content(strings.Repeat("a", 161), "s", "b") + `}`, false},
		{"extra field", `{"segment":` + rule(cityEq) + `,"content":` + good + `,"approved":true}`, false},
	}
	for _, c := range cases {
		err := s.Validate(Revision, decode(t, c.json))
		if c.valid && err != nil {
			t.Errorf("%s: rejected: %v", c.name, err)
		}
		if !c.valid && err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func TestDecodeRejectsTrailingData(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"version":1} {"version":2}`)); err == nil {
		t.Fatal("trailing value accepted")
	}
}

func TestEveryObjectIsClosed(t *testing.T) {
	s := mustLoad(t)
	for name, doc := range s.raw {
		walk(doc, string(name), func(path string, m map[string]any) {
			if m["type"] == "object" && m["additionalProperties"] != false {
				t.Errorf("%s: object without additionalProperties: false", path)
			}
		})
	}
}

func TestForAPI(t *testing.T) {
	s := mustLoad(t)
	cases := map[Name][]tc{SegmentRule: validRules(t), Content: validContents()}
	for name, valid := range cases {
		api, err := s.ForAPI(name)
		if err != nil {
			t.Fatalf("ForAPI(%s): %v", name, err)
		}
		walk(api, string(name), func(path string, m map[string]any) {
			for k, v := range m {
				if unsupportedForAPI[k] {
					t.Errorf("%s: unsupported keyword %q survived", path, k)
				}
				if k == "minItems" {
					if n, _ := v.(json.Number).Int64(); n > 1 {
						t.Errorf("%s: minItems %d survived", path, n)
					}
				}
				if ref, ok := v.(string); ok && k == "$ref" && !strings.HasPrefix(ref, "#/") {
					t.Errorf("%s: external $ref %q", path, ref)
				}
			}
		})
		// The derived schema is looser, never stricter: everything valid
		// against the full schema must be valid against it.
		derived := compileDoc(t, api)
		for _, c := range valid {
			if err := derived.Validate(decode(t, c.json)); err != nil {
				t.Errorf("%s: %q valid against the full schema but not the API schema: %v", name, c.name, err)
			}
		}
	}

	// The gap local validation covers: a 161-rune SMS passes the API schema
	// and fails the full one.
	api, _ := s.ForAPI(Content)
	long := decode(t, content(strings.Repeat("a", 161), "s", "b"))
	if err := compileDoc(t, api).Validate(long); err != nil {
		t.Errorf("API schema unexpectedly enforces maxLength: %v", err)
	}
	if err := s.Validate(Content, long); err == nil {
		t.Error("full schema accepted a 161-rune SMS")
	}

	// Revision references other files, which the API cannot resolve.
	if _, err := s.ForAPI(Revision); err == nil {
		t.Error("ForAPI(Revision) accepted an external $ref")
	}
}

func TestForAPIDoesNotAliasStoredSchema(t *testing.T) {
	s := mustLoad(t)
	api, err := s.ForAPI(SegmentRule)
	if err != nil {
		t.Fatal(err)
	}
	city := api["$defs"].(map[string]any)["city"].(map[string]any)
	city["enum"] = append(city["enum"].([]any), "paris")
	city["enum"].([]any)[0] = "paris"

	again, _ := s.ForAPI(SegmentRule)
	enum := again["$defs"].(map[string]any)["city"].(map[string]any)["enum"].([]any)
	if enum[0] != "istanbul" || len(enum) != 10 {
		t.Fatalf("mutating ForAPI output changed the stored schema: %v", enum)
	}
}

func walk(node any, path string, fn func(path string, m map[string]any)) {
	switch v := node.(type) {
	case map[string]any:
		fn(path, v)
		for k, child := range v {
			walk(child, path+"/"+k, fn)
		}
	case []any:
		for i, child := range v {
			walk(child, fmt.Sprintf("%s/%d", path, i), fn)
		}
	}
}

func compileDoc(t *testing.T, doc map[string]any) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	const url = "https://schemas.composer.local/derived.json"
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		t.Fatalf("derived schema does not compile: %v", err)
	}
	return sch
}
