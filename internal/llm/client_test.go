package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockllm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/retry"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

// These tests run the client against internal/mockllm, the same fake the
// generation tests use, so there is one fake of the Messages API to keep
// right. The external test package keeps mockllm free to never import llm.

func schemas(t *testing.T) *schema.Set {
	t.Helper()
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func mock(t *testing.T) (*mockllm.Server, *httptest.Server) {
	t.Helper()
	m := mockllm.New(mockllm.Options{})
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return m, srv
}

func client(t *testing.T, baseURL string, mod func(*llm.Config)) *llm.Client {
	t.Helper()
	cfg := llm.Config{BaseURL: baseURL, APIKey: "test-placeholder", Schemas: schemas(t), StallTimeout: 5 * time.Second, TotalTimeout: 10 * time.Second}
	if mod != nil {
		mod(&cfg)
	}
	c, err := llm.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func decode(t *testing.T, r mockllm.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatalf("recorded body is not JSON: %v", err)
	}
	return body
}

// systemText reads the system prompt, sent as a string or as text blocks.
func systemText(body map[string]any) string {
	switch s := body["system"].(type) {
	case string:
		return s
	case []any:
		var sb strings.Builder
		for _, b := range s {
			if m, ok := b.(map[string]any); ok {
				t, _ := m["text"].(string)
				sb.WriteString(t)
			}
		}
		return sb.String()
	}
	return ""
}

func messages(body map[string]any) []map[string]any {
	list, _ := body["messages"].([]any)
	var out []map[string]any
	for _, m := range list {
		mm, _ := m.(map[string]any)
		out = append(out, mm)
	}
	return out
}

// blocks reads a message's content as blocks; a plain string is one text block.
func blocks(m map[string]any) []map[string]any {
	switch c := m["content"].(type) {
	case string:
		return []map[string]any{{"type": "text", "text": c}}
	case []any:
		var out []map[string]any
		for _, b := range c {
			bm, _ := b.(map[string]any)
			out = append(out, bm)
		}
		return out
	}
	return nil
}

func messageText(m map[string]any) string {
	var sb strings.Builder
	for _, b := range blocks(m) {
		if b["type"] == "text" {
			s, _ := b["text"].(string)
			sb.WriteString(s)
		}
	}
	return sb.String()
}

// normalize round-trips a value through encoding/json, so two decoded
// schemas compare equal whatever number types they used.
func normalize(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRequestShape pins what goes over the wire for each stage: model,
// adaptive thinking, explicit effort, JSON outputs with the API-derived
// schema, streaming, the refusal fallback, and nothing the API rejects. The
// mock also answers 400 to anything the real API rejects.
func TestRequestShape(t *testing.T) {
	set := schemas(t)
	cases := []struct {
		stage  llm.Stage
		name   schema.Name
		effort string
	}{
		{llm.Segment, schema.SegmentRule, "low"},
		{llm.Content, schema.Content, "medium"},
	}
	for _, c := range cases {
		m, srv := mock(t)
		if _, err := client(t, srv.URL, nil).Call(context.Background(), llm.NewConversation(c.stage, "first turn"), nil); err != nil {
			t.Fatalf("%s: %v", c.stage, err)
		}
		reqs := m.Requests()
		if len(reqs) != 1 {
			t.Fatalf("%s: %d requests, want 1", c.stage, len(reqs))
		}
		body, h := decode(t, reqs[0]), reqs[0].Header
		if body["model"] != llm.DefaultModel || body["stream"] != true {
			t.Errorf("%s: model %v, stream %v", c.stage, body["model"], body["stream"])
		}
		if th, _ := body["thinking"].(map[string]any); th["type"] != "adaptive" {
			t.Errorf("%s: thinking %v, want adaptive (Opus 5.5 cannot disable it)", c.stage, body["thinking"])
		}
		oc, _ := body["output_config"].(map[string]any)
		if oc["effort"] != c.effort {
			t.Errorf("%s: effort %v, want %s set explicitly", c.stage, oc["effort"], c.effort)
		}
		format, _ := oc["format"].(map[string]any)
		want, err := set.ForAPI(c.name)
		if err != nil {
			t.Fatal(err)
		}
		if format["type"] != "json_schema" || !reflect.DeepEqual(normalize(t, format["schema"]), normalize(t, want)) {
			t.Errorf("%s: output_config.format is not json_schema with schema.ForAPI(%s)", c.stage, c.name)
		}
		for _, banned := range []string{"temperature", "top_p", "top_k", "tools", "tool_choice"} {
			if _, ok := body[banned]; ok {
				t.Errorf("%s: request has %q", c.stage, banned)
			}
		}
		if body["fallbacks"] != "default" || !strings.Contains(strings.Join(h.Values("anthropic-beta"), ","), llm.FallbackBeta) {
			t.Errorf("%s: fallbacks %v, anthropic-beta %q; want the refusal fallback", c.stage, body["fallbacks"], h.Values("anthropic-beta"))
		}
		if h.Get("x-api-key") != "test-placeholder" {
			t.Errorf("%s: x-api-key %q", c.stage, h.Get("x-api-key"))
		}
		if !strings.Contains(systemText(body), llm.SystemPrompt(c.stage)) {
			t.Errorf("%s: system prompt not sent", c.stage)
		}
		if msgs := messages(body); len(msgs) != 1 || msgs[0]["role"] != "user" || messageText(msgs[0]) != "first turn" {
			t.Errorf("%s: messages %v", c.stage, msgs)
		}
	}
}

// The SDK reads credentials and the base URL from the environment by
// default. The client must use only its Config, so a key in the environment
// can never reach the mock, and mock mode can never reach the real API.
func TestClientIgnoresSDKEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-value-must-not-be-sent")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token-must-not-be-sent")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:9")
	m, srv := mock(t)
	if _, err := client(t, srv.URL, nil).Call(context.Background(), llm.NewConversation(llm.Segment, "x"), nil); err != nil {
		t.Fatalf("the request did not reach Config.BaseURL: %v", err)
	}
	h := m.Requests()[0].Header
	if h.Get("x-api-key") != "test-placeholder" || h.Get("Authorization") != "" {
		t.Errorf("credentials from the environment were sent: x-api-key %q, Authorization %q", h.Get("x-api-key"), h.Get("Authorization"))
	}
}

// Text deltas reach onText as they arrive and rebuild the text. Stop reason,
// usage and latency are captured.
func TestStreamingResponse(t *testing.T) {
	m, srv := mock(t)
	answer := `{"sms":{"body":"` + strings.Repeat("Merhaba {{first_name}} ", 8) + `"},"email":{"subject":"Sepet","body":"Selam"}}`
	m.Script(mockllm.Content, mockllm.Reply{Text: answer})
	var deltas []string
	resp, err := client(t, srv.URL, nil).Call(context.Background(), llm.NewConversation(llm.Content, "x"), func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != answer || strings.Join(deltas, "") != answer || len(deltas) < 2 {
		t.Errorf("text %q from %d deltas, want %q in several deltas", resp.Text, len(deltas), answer)
	}
	if resp.StopReason != "end_turn" || resp.Model == "" || resp.InputTokens <= 0 || resp.OutputTokens <= 0 || resp.Latency <= 0 {
		t.Errorf("response %+v", resp)
	}
}

// A refusal or max_tokens is a complete HTTP exchange, not a transport
// error. The caller decides: a refusal is never retried, max_tokens is.
func TestStopReasonsAreNotErrors(t *testing.T) {
	m, srv := mock(t)
	m.Script(mockllm.Content, mockllm.Refusal(), mockllm.MaxTokens(`{"sms":{"bo`))
	cl := client(t, srv.URL, nil)
	for _, stop := range []string{"refusal", "max_tokens"} {
		resp, err := cl.Call(context.Background(), llm.NewConversation(llm.Content, "x"), nil)
		if err != nil || resp.StopReason != stop {
			t.Errorf("%s: resp %+v, err %v", stop, resp, err)
		}
	}
}

// SDK retries are off: every retry goes through internal/retry. A failing
// call is classified once, with the server's Retry-After.
func TestErrorsAreClassifiedAndNotRetried(t *testing.T) {
	cases := []struct {
		reply  mockllm.Reply
		status int
		class  retry.Class
		after  time.Duration
	}{
		{mockllm.Status(500), 500, retry.ServerError, 0},
		{mockllm.Status(529), 529, retry.ServerError, 0},
		{mockllm.RateLimited("7"), 429, retry.RateLimited, 7 * time.Second},
		{mockllm.Status(400), 400, retry.ClientError, 0},
		{mockllm.Status(401), 401, retry.ClientError, 0},
	}
	for _, c := range cases {
		m, srv := mock(t)
		m.Script(mockllm.Segment, c.reply)
		_, err := client(t, srv.URL, nil).Call(context.Background(), llm.NewConversation(llm.Segment, "x"), nil)
		var ce *llm.CallError
		if !errors.As(err, &ce) || ce.Status != c.status || ce.Class != c.class || ce.RetryAfter != c.after {
			t.Errorf("%d: error %v, want a CallError with class %s, retry-after %v", c.status, err, c.class, c.after)
		}
		if n := len(m.Requests()); n != 1 {
			t.Errorf("%d: %d requests; the SDK retried on its own", c.status, n)
		}
	}
}

func TestConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	_, err = client(t, "http://"+addr, nil).Call(context.Background(), llm.NewConversation(llm.Segment, "x"), nil)
	var ce *llm.CallError
	if !errors.As(err, &ce) || ce.Class != retry.ConnRefused || ce.Status != 0 {
		t.Errorf("error %v, want a CallError with class conn_refused", err)
	}
}

// The watchdog cancels a stream with no event for StallTimeout, including
// the wait for the response headers, and any stream older than TotalTimeout.
// The last two cases need a server the mock does not model, so they use a
// plain handler.
func TestWatchdog(t *testing.T) {
	release := make(chan struct{})
	m, stalling := mock(t)
	m.Script(mockllm.Content, mockllm.Stall(`{"sms":{"body":"Mer`))
	silent := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(silent.Close)
	// Pings keep the stall timer from firing; only the total timeout can.
	pinging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for {
			if _, err := w.Write([]byte("event: ping\ndata: {\"type\":\"ping\"}\n\n")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-release:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(pinging.Close)
	// Runs first (cleanups are LIFO): the silent handler never reads the
	// body, so it cannot see the client go away and only release ends it.
	t.Cleanup(func() { close(release) })

	cases := []struct {
		name         string
		url          string
		stall, total time.Duration
		want         error
	}{
		{"stall mid-stream", stalling.URL, 150 * time.Millisecond, 10 * time.Second, llm.ErrStalled},
		{"stall before headers", silent.URL, 150 * time.Millisecond, 10 * time.Second, llm.ErrStalled},
		{"total timeout with pings", pinging.URL, 2 * time.Second, 300 * time.Millisecond, llm.ErrTotalTimeout},
	}
	for _, c := range cases {
		cl := client(t, c.url, func(cfg *llm.Config) { cfg.StallTimeout, cfg.TotalTimeout = c.stall, c.total })
		start := time.Now()
		_, err := cl.Call(context.Background(), llm.NewConversation(llm.Content, "x"), nil)
		if took := time.Since(start); took > 3*time.Second {
			t.Errorf("%s: took %v; the watchdog did not fire", c.name, took)
		}
		var ce *llm.CallError
		if !errors.Is(err, c.want) || !errors.As(err, &ce) || ce.Class != retry.Timeout {
			t.Errorf("%s: error %v, want %v as a CallError with class timeout, so it is retried", c.name, err, c.want)
		}
	}
	if llm.DefaultStallTimeout != 20*time.Second || llm.DefaultTotalTimeout != 120*time.Second {
		t.Errorf("defaults %v and %v, want 20s and 120s", llm.DefaultStallTimeout, llm.DefaultTotalTimeout)
	}
}

// The repair turn is append-only (preserved thinking on Opus 5.5 forbids
// editing earlier turns): earlier messages are resent unchanged, the
// assistant turn goes back with its thinking block, and the next user turn
// carries the validation errors as JSON.
func TestRepairTurn(t *testing.T) {
	m, srv := mock(t)
	answers := []string{`{"sms":{"body":"Hi {{password}}"}}`, `{"sms":{"body":"Hi {{first_name}}"}}`, `{}`}
	m.Script(mockllm.Content, mockllm.Reply{Text: answers[0]}, mockllm.Reply{Text: answers[1]}, mockllm.Reply{Text: answers[2]})
	cl := client(t, srv.URL, nil)
	conv := llm.NewConversation(llm.Content, "first turn")
	ctx := context.Background()

	issues := [][]validation.Issue{
		{
			{Path: "/sms/body", Code: validation.UnknownVariable, Message: `variable "password" is not allowed`},
			{Path: "/email", Code: validation.Required, Message: `missing required key "email"`},
		},
		{{Path: "/email", Code: validation.Required, Message: `missing required key "email"`}},
	}
	for i := range 3 {
		r, err := cl.Call(ctx, conv, nil)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			conv.Repair(r, issues[i])
		}
	}

	reqs := m.Requests()
	if len(reqs) != 3 {
		t.Fatalf("%d requests, want 3", len(reqs))
	}
	b1, b2, b3 := decode(t, reqs[0]), decode(t, reqs[1]), decode(t, reqs[2])
	m1, m2, m3 := messages(b1), messages(b2), messages(b3)
	if len(m1) != 1 || len(m2) != 3 || len(m3) != 5 {
		t.Fatalf("message counts %d, %d, %d; want 1, 3, 5", len(m1), len(m2), len(m3))
	}
	if !reflect.DeepEqual(m2[:1], m1) || !reflect.DeepEqual(m3[:3], m2) {
		t.Error("an earlier turn was changed; the conversation must be append-only")
	}
	checkAssistant(t, m2[1], answers[0])
	checkAssistant(t, m3[3], answers[1])
	checkRepairErrors(t, m2[2], issues[0])
	checkRepairErrors(t, m3[4], issues[1])
	// The cached prefix stays the same across attempts.
	for _, k := range []string{"system", "output_config", "model", "thinking"} {
		if !reflect.DeepEqual(b1[k], b2[k]) || !reflect.DeepEqual(b2[k], b3[k]) {
			t.Errorf("%s changed between attempts", k)
		}
	}
}

// checkAssistant requires the assistant turn to carry a thinking block with
// its signature and exactly the text that was received.
func checkAssistant(t *testing.T, m map[string]any, text string) {
	t.Helper()
	if m["role"] != "assistant" {
		t.Fatalf("role %v, want assistant", m["role"])
	}
	var thinking int
	var texts []string
	for _, b := range blocks(m) {
		switch b["type"] {
		case "thinking":
			thinking++
			if s, _ := b["signature"].(string); s == "" {
				t.Error("thinking block sent back without its signature")
			}
		case "text":
			s, _ := b["text"].(string)
			texts = append(texts, s)
		}
	}
	if thinking != 1 || !slices.Equal(texts, []string{text}) {
		t.Errorf("assistant turn: %d thinking blocks, texts %q; want 1 and [%s] unchanged", thinking, texts, text)
	}
}

// checkRepairErrors finds the JSON array in the user turn and requires it to
// be exactly the validation issues.
func checkRepairErrors(t *testing.T, m map[string]any, want []validation.Issue) {
	t.Helper()
	if m["role"] != "user" {
		t.Fatalf("role %v, want user", m["role"])
	}
	text := messageText(m)
	i, j := strings.Index(text, "["), strings.LastIndex(text, "]")
	if i < 0 || j < i {
		t.Fatalf("repair turn has no JSON array: %q", text)
	}
	var got []validation.Issue
	if err := json.Unmarshal([]byte(text[i:j+1]), &got); err != nil {
		t.Fatalf("repair turn errors are not JSON: %v: %q", err, text)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("repair turn errors %+v, want %+v", got, want)
	}
}

// ConfigFromEnv fails closed. Mock mode never reads the API key, so a
// misconfigured worker cannot send it to the mock.
func TestConfigFromEnv(t *testing.T) {
	run := func(env map[string]string) (llm.Config, []string, error) {
		var asked []string
		cfg, err := llm.ConfigFromEnv(func(k string) string {
			asked = append(asked, k)
			return env[k]
		})
		return cfg, asked, err
	}
	cfg, asked, err := run(map[string]string{"LLM_MODE": "mock", "ANTHROPIC_API_KEY": "must-not-be-read", "ANTHROPIC_AUTH_TOKEN": "must-not-be-read"})
	if err != nil || cfg.BaseURL != llm.MockBaseURL || cfg.APIKey == "" || cfg.APIKey == "must-not-be-read" {
		t.Errorf("mock mode: %+v %v", cfg, err)
	}
	if slices.Contains(asked, "ANTHROPIC_API_KEY") || slices.Contains(asked, "ANTHROPIC_AUTH_TOKEN") {
		t.Errorf("mock mode read the key: asked %v", asked)
	}
	if _, _, err := run(map[string]string{"LLM_MODE": "anthropic"}); !errors.Is(err, llm.ErrNoAPIKey) {
		t.Errorf("anthropic mode without a key: %v, want ErrNoAPIKey", err)
	}
	for _, mode := range []string{"", "Mock", "openai"} {
		if _, _, err := run(map[string]string{"LLM_MODE": mode, "ANTHROPIC_API_KEY": "placeholder-not-a-real-key"}); !errors.Is(err, llm.ErrUnknownMode) {
			t.Errorf("LLM_MODE=%q: %v, want ErrUnknownMode", mode, err)
		}
	}
}
