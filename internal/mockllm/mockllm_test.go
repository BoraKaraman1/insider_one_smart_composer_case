package mockllm_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockllm"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
)

const turkishBrief = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder."

func schemas(t *testing.T) *schema.Set {
	t.Helper()
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func serve(t *testing.T, o mockllm.Options) (*mockllm.Server, *httptest.Server) {
	t.Helper()
	m := mockllm.New(o)
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return m, srv
}

// body builds a request the way internal/llm sends it: the API-derived
// schema of the stage, adaptive thinking, explicit effort, no sampling
// parameters.
func body(t *testing.T, stage mockllm.Stage, stream bool, userTurns ...string) map[string]any {
	t.Helper()
	name, effort := schema.SegmentRule, "low"
	if stage == mockllm.Content {
		name, effort = schema.Content, "medium"
	}
	s, err := schemas(t).ForAPI(name)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []any
	for i, u := range userTurns {
		if i > 0 {
			msgs = append(msgs, map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": "", "signature": "sig"},
				map[string]any{"type": "text", "text": `{"broken":true}`},
			}})
		}
		msgs = append(msgs, map[string]any{"role": "user", "content": u})
	}
	return roundTrip(t, map[string]any{
		"model":         "claude-opus-5-5",
		"max_tokens":    4096,
		"stream":        stream,
		"system":        "system prompt",
		"thinking":      map[string]any{"type": "adaptive"},
		"output_config": map[string]any{"effort": effort, "format": map[string]any{"type": "json_schema", "schema": s}},
		"fallbacks":     "default",
		"messages":      msgs,
	})
}

// roundTrip turns any JSON value into plain maps, so tests can mutate it.
func roundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var apiHeaders = map[string]string{"x-api-key": "mock-placeholder", "anthropic-version": "2023-06-01", "content-type": "application/json"}

func post(t *testing.T, srv *httptest.Server, b map[string]any, headers map[string]string) *http.Response {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", srv.URL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

type sse struct {
	Event string
	Data  map[string]any
}

func readSSE(t *testing.T, r io.Reader) []sse {
	t.Helper()
	var out []sse
	var cur sse
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.Event != "" || cur.Data != nil {
				out = append(out, cur)
			}
			cur = sse{}
		case strings.HasPrefix(line, "event: "):
			cur.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.Data); err != nil {
				t.Fatalf("data is not JSON: %q", line)
			}
		}
	}
	return out
}

// text concatenates the text deltas of a stream.
func text(events []sse) (string, int) {
	var sb strings.Builder
	n := 0
	for _, e := range events {
		d, _ := e.Data["delta"].(map[string]any)
		if e.Event == "content_block_delta" && d["type"] == "text_delta" {
			s, _ := d["text"].(string)
			sb.WriteString(s)
			n++
		}
	}
	return sb.String(), n
}

func apiErrorType(t *testing.T, resp *http.Response) string {
	t.Helper()
	var e struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("not an API error body: %v", err)
	}
	if e.Type != "error" || e.Error.Message == "" {
		t.Errorf("error body %+v", e)
	}
	return e.Error.Type
}

// at walks into a decoded body by keys.
func at(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		m, _ = m[k].(map[string]any)
	}
	return m
}

// TestRejectsWhatTheAPIRejects makes drift in our requests fail in tests,
// not in production: each case is a shape the real API answers with a 400.
func TestRejectsWhatTheAPIRejects(t *testing.T) {
	_, srv := serve(t, mockllm.Options{})
	contentSchema := func(b map[string]any) map[string]any { return at(b, "output_config", "format", "schema") }
	smsBody := func(b map[string]any) map[string]any {
		return at(contentSchema(b), "properties", "sms", "properties", "body")
	}
	cases := []struct {
		name   string
		mutate func(b map[string]any)
	}{
		{"temperature", func(b map[string]any) { b["temperature"] = 0.2 }},
		{"top_p", func(b map[string]any) { b["top_p"] = 0.9 }},
		{"top_k", func(b map[string]any) { b["top_k"] = 5 }},
		{"forced tool_choice any", func(b map[string]any) {
			b["tools"] = []any{map[string]any{"name": "emit", "input_schema": map[string]any{"type": "object"}}}
			b["tool_choice"] = map[string]any{"type": "any"}
		}},
		{"forced tool_choice tool", func(b map[string]any) {
			b["tools"] = []any{map[string]any{"name": "emit", "input_schema": map[string]any{"type": "object"}}}
			b["tool_choice"] = map[string]any{"type": "tool", "name": "emit"}
		}},
		{"thinking disabled", func(b map[string]any) { b["thinking"] = map[string]any{"type": "disabled"} }},
		{"thinking budget", func(b map[string]any) { b["thinking"] = map[string]any{"type": "enabled", "budget_tokens": 2048} }},
		{"minLength", func(b map[string]any) { smsBody(b)["minLength"] = 1 }},
		{"maxLength", func(b map[string]any) { smsBody(b)["maxLength"] = 160 }},
		{"minimum", func(b map[string]any) { smsBody(b)["type"] = "integer"; smsBody(b)["minimum"] = 0 }},
		{"maximum", func(b map[string]any) { smsBody(b)["type"] = "integer"; smsBody(b)["maximum"] = 9 }},
		{"minItems above 1", func(b map[string]any) {
			at(contentSchema(b), "properties")["tags"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 2}
		}},
		{"external $ref", func(b map[string]any) {
			at(contentSchema(b), "properties")["sms"] = map[string]any{"$ref": "https://schemas.example/other.json#/x"}
		}},
		{"additionalProperties true", func(b map[string]any) { contentSchema(b)["additionalProperties"] = true }},
		{"additionalProperties as a schema", func(b map[string]any) {
			at(contentSchema(b), "properties", "sms")["additionalProperties"] = map[string]any{"type": "string"}
		}},
		{"object without additionalProperties", func(b map[string]any) { delete(at(contentSchema(b), "properties", "email"), "additionalProperties") }},
	}
	for _, c := range cases {
		b := body(t, mockllm.Content, true, "<brief>b</brief>")
		c.mutate(b)
		resp := post(t, srv, b, apiHeaders)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", c.name, resp.StatusCode)
			continue
		}
		if typ := apiErrorType(t, resp); typ != "invalid_request_error" {
			t.Errorf("%s: error type %q, want invalid_request_error", c.name, typ)
		}
	}

	// What internal/llm sends must pass, for both stages.
	for _, stage := range []mockllm.Stage{mockllm.Segment, mockllm.Content} {
		if resp := post(t, srv, body(t, stage, true, "<brief>"+turkishBrief+"</brief>"), apiHeaders); resp.StatusCode != 200 {
			t.Errorf("valid %s request: status %d", stage, resp.StatusCode)
		}
	}

	noKey := map[string]string{"anthropic-version": "2023-06-01", "content-type": "application/json"}
	if resp := post(t, srv, body(t, mockllm.Segment, true, "<brief>b</brief>"), noKey); resp.StatusCode != http.StatusUnauthorized || apiErrorType(t, resp) != "authentication_error" {
		t.Errorf("no x-api-key: status %d, want 401 authentication_error", resp.StatusCode)
	}
	noVersion := map[string]string{"x-api-key": "mock-placeholder", "content-type": "application/json"}
	if resp := post(t, srv, body(t, mockllm.Segment, true, "<brief>b</brief>"), noVersion); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("no anthropic-version: status %d, want 400", resp.StatusCode)
	}
}

func brainAnswer(t *testing.T, srv *httptest.Server, stage mockllm.Stage, userTurns ...string) string {
	t.Helper()
	got, _ := text(readSSE(t, post(t, srv, body(t, stage, true, userTurns...), apiHeaders).Body))
	return got
}

// The demo brain turns the spec's brief into the documented rule, so
// docker compose up shows the real flow without a key.
func TestDemoBrain(t *testing.T) {
	set := schemas(t)
	example, err := os.ReadFile("../../docs/examples/istanbul_cart_abandoners.json")
	if err != nil {
		t.Fatal(err)
	}
	wantExample, res := segment.ValidateJSON(set, example)
	if !res.OK() {
		t.Fatal(res.Errors)
	}
	rule := func(city string, days int) segment.Rule {
		return segment.Rule{Match: "all", Conditions: []segment.Condition{
			{Kind: segment.KindEnum, Field: "city", Op: "eq", Value: city},
			{Kind: segment.KindEvent, Event: "cart_add", WithinDays: days},
			{Kind: segment.KindEvent, Event: "purchase", WithinDays: days, Negate: true},
		}}
	}
	cases := []struct {
		brief string
		want  segment.Rule
	}{
		{turkishBrief, wantExample},
		{"Son 7 günde sepete ürün ekleyip satın almayan İzmir kullanıcılarına hatırlatma gönder.", rule("izmir", 7)},
		{"Remind Ankara users who added items to their cart in the last 14 days but did not purchase.", rule("ankara", 14)},
	}
	_, srv := serve(t, mockllm.Options{})
	for _, c := range cases {
		seg := brainAnswer(t, srv, mockllm.Segment, "<brief>"+c.brief+"</brief>")
		got, res := segment.ValidateJSON(set, []byte(seg))
		if !res.OK() {
			t.Errorf("%q: invalid rule %s: %+v", c.brief, seg, res.Errors)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: rule %+v, want %+v", c.brief, got, c.want)
		}
		copyText := brainAnswer(t, srv, mockllm.Content, "<brief>"+c.brief+"</brief>\nSegment: "+segment.Describe(got))
		cont, res := content.ValidateJSON(set, []byte(copyText))
		if !res.OK() {
			t.Errorf("%q: invalid copy %s: %+v", c.brief, copyText, res.Errors)
			continue
		}
		for _, field := range []string{cont.SMSBody, cont.EmailBody} {
			if !strings.Contains(field, "{{first_name}}") || !strings.Contains(field, "{{campaign_url}}") {
				t.Errorf("%q: copy %q lacks {{first_name}} or {{campaign_url}}", c.brief, field)
			}
		}
	}
}

// The mock is used through the real SDK in internal/llm. This checks the SDK
// accumulates a mock stream into a full message and maps a mock error to its
// typed error.
func TestThroughRealSDK(t *testing.T) {
	m, srv := serve(t, mockllm.Options{})
	answer := `{"sms":{"body":"Merhaba {{first_name}} {{campaign_url}}"},"email":{"subject":"Sepet","body":"Selam"}}`
	m.Script(mockllm.Content, mockllm.Reply{Text: answer}, mockllm.Status(529))
	s, err := schemas(t).ForAPI(schema.Content)
	if err != nil {
		t.Fatal(err)
	}
	client := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("mock-placeholder"), option.WithMaxRetries(0))
	params := anthropic.MessageNewParams{
		Model:     "claude-opus-5-5",
		MaxTokens: 4096,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("<brief>b</brief>"))},
	}
	format := option.WithJSONSet("output_config", map[string]any{"effort": "medium", "format": map[string]any{"type": "json_schema", "schema": s}})
	ctx := context.Background()

	stream := client.Messages.NewStreaming(ctx, params, format)
	msg := anthropic.Message{}
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	var got string
	for _, b := range msg.Content {
		if tb, ok := b.AsAny().(anthropic.TextBlock); ok {
			got += tb.Text
		}
	}
	if got != answer || msg.StopReason != anthropic.StopReasonEndTurn {
		t.Errorf("accumulated %q (stop %q), want %q", got, msg.StopReason, answer)
	}

	_, err = client.Messages.New(ctx, params, format)
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 529 {
		t.Errorf("529 through the SDK: %v", err)
	}
	if n := len(m.Requests()); n != 2 {
		t.Errorf("%d requests; the SDK retried despite WithMaxRetries(0)", n)
	}
}
