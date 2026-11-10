// Paket mesaj arayüzünün sahte sunucusudur.
package mockllm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Stage string

const (
	Segment Stage = "segment"
	Content Stage = "content"
)

type Options struct {
	InvalidFirst bool
}

type Reply struct {
	Text       string
	StopReason string
	Status     int
	RetryAfter string
	Stall      bool
}

func Status(code int) Reply { return Reply{Status: code} }

func RateLimited(retryAfter string) Reply { return Reply{Status: 429, RetryAfter: retryAfter} }

func Refusal() Reply { return Reply{StopReason: "refusal"} }

func MaxTokens(text string) Reply { return Reply{Text: text, StopReason: "max_tokens"} }

func Stall(text string) Reply { return Reply{Text: text, Stall: true} }

type Request struct {
	Stage  Stage
	Header http.Header
	Body   []byte
}

type Server struct {
	opts Options

	mu       sync.Mutex
	scripts  map[Stage][]Reply
	requests []Request
	seq      int
}

func New(o Options) *Server {
	return &Server{opts: o, scripts: map[Stage][]Reply{}}
}

func (s *Server) Script(stage Stage, replies ...Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts[stage] = append(s.scripts[stage], replies...)
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

const maxRequestBody = 4 << 20

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
		apiError(w, http.StatusNotFound, "not_found_error", "unknown route "+r.Method+" "+r.URL.Path)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request_error", "could not read the body")
		return
	}
	var body map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	decodeErr := dec.Decode(&body)
	stage := stageOf(body)

	s.mu.Lock()
	s.requests = append(s.requests, Request{Stage: stage, Header: r.Header.Clone(), Body: raw})
	s.mu.Unlock()

	switch {
	case r.Header.Get("X-Api-Key") == "" && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
		apiError(w, http.StatusUnauthorized, "authentication_error", "x-api-key header is required")
		return
	case r.Header.Get("Anthropic-Version") == "":
		apiError(w, http.StatusBadRequest, "invalid_request_error", "anthropic-version header is required")
		return
	case decodeErr != nil:
		apiError(w, http.StatusBadRequest, "invalid_request_error", "body is not JSON: "+decodeErr.Error())
		return
	}
	if msg := checkRequest(body); msg != "" {
		apiError(w, http.StatusBadRequest, "invalid_request_error", msg)
		return
	}

	reply := s.next(stage, body)
	if reply.Status != 0 {
		if reply.RetryAfter != "" {
			w.Header().Set("Retry-After", reply.RetryAfter)
		}
		apiError(w, reply.Status, errorType(reply.Status), fmt.Sprintf("scripted %d", reply.Status))
		return
	}
	if reply.StopReason == "" {
		reply.StopReason = "end_turn"
	}
	model, _ := body["model"].(string)
	s.mu.Lock()
	s.seq++
	id := fmt.Sprintf("msg_mock_%06d", s.seq)
	s.mu.Unlock()
	in := len(raw)/4 + 1
	if stream, _ := body["stream"].(bool); stream {
		streamReply(w, r, id, model, in, reply)
		return
	}
	writeJSON(w, http.StatusOK, message(id, model, in, reply))
}

func (s *Server) next(stage Stage, body map[string]any) Reply {
	s.mu.Lock()
	if q := s.scripts[stage]; len(q) > 0 {
		s.scripts[stage] = q[1:]
		s.mu.Unlock()
		return q[0]
	}
	s.mu.Unlock()
	return brain(stage, body, s.opts)
}

func stageOf(body map[string]any) Stage {
	oc, _ := body["output_config"].(map[string]any)
	f, _ := oc["format"].(map[string]any)
	sch, _ := f["schema"].(map[string]any)
	props, _ := sch["properties"].(map[string]any)
	if _, ok := props["sms"]; ok {
		return Content
	}
	return Segment
}

var unsupported = []string{"minLength", "maxLength", "minimum", "maximum", "exclusiveMinimum",
	"exclusiveMaximum", "multipleOf", "maxItems", "uniqueItems"}

func checkRequest(body map[string]any) string {
	for _, k := range []string{"temperature", "top_p", "top_k"} {
		if _, ok := body[k]; ok {
			return k + " is not supported with this model"
		}
	}
	if tc, ok := body["tool_choice"].(map[string]any); ok {
		if t := tc["type"]; t == "any" || t == "tool" {
			return "forced tool_choice is not supported with thinking"
		}
	}
	if th, ok := body["thinking"].(map[string]any); ok && th["type"] != "adaptive" {
		return fmt.Sprintf("thinking.type %v is not supported with this model; use adaptive", th["type"])
	}
	if _, ok := body["max_tokens"]; !ok {
		return "max_tokens: field required"
	}
	if msgs, _ := body["messages"].([]any); len(msgs) == 0 {
		return "messages: at least one message is required"
	}
	if oc, ok := body["output_config"].(map[string]any); ok {
		if f, ok := oc["format"].(map[string]any); ok {
			if f["type"] != "json_schema" {
				return "output_config.format.type must be json_schema"
			}
			if msg := checkSchema(f["schema"], "output_config.format.schema"); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func checkSchema(node any, path string) string {
	switch n := node.(type) {
	case []any:
		for i, v := range n {
			if msg := checkSchema(v, path+"/"+strconv.Itoa(i)); msg != "" {
				return msg
			}
		}
	case map[string]any:
		for _, k := range unsupported {
			if _, ok := n[k]; ok {
				return fmt.Sprintf("%s: keyword %q is not supported", path, k)
			}
		}
		if mi, ok := n["minItems"]; ok {
			if n, ok := mi.(json.Number); ok && n.String() != "0" && n.String() != "1" {
				return path + ": minItems above 1 is not supported"
			}
		}
		if ref, ok := n["$ref"].(string); ok && !strings.HasPrefix(ref, "#") {
			return path + ": external $ref is not supported"
		}
		_, hasProps := n["properties"]
		if n["type"] == "object" || hasProps {
			if ap, ok := n["additionalProperties"]; !ok || ap != false {
				return path + ": additionalProperties must be false for every object"
			}
		}
		for k, v := range n {
			// Alan adları anahtar kelime değil, yalnız içleri gezilir.
			if k == "properties" || k == "$defs" {
				if m, ok := v.(map[string]any); ok {
					for name, sub := range m {
						if msg := checkSchema(sub, path+"/"+k+"/"+name); msg != "" {
							return msg
						}
					}
				}
				continue
			}
			if msg := checkSchema(v, path+"/"+k); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func errorType(status int) string {
	switch {
	case status == 400:
		return "invalid_request_error"
	case status == 401:
		return "authentication_error"
	case status == 403:
		return "permission_error"
	case status == 404:
		return "not_found_error"
	case status == 429:
		return "rate_limit_error"
	case status == 529:
		return "overloaded_error"
	}
	return "api_error"
}

func apiError(w http.ResponseWriter, status int, typ, msg string) {
	writeJSON(w, status, map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const signature = "mock-signature"

func usage(in, out int) map[string]any {
	return map[string]any{"input_tokens": in, "output_tokens": out}
}

func outTokens(text string) int { return utf8.RuneCountInString(text)/4 + 1 }

func message(id, model string, in int, r Reply) map[string]any {
	return map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": model,
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "", "signature": signature},
			map[string]any{"type": "text", "text": r.Text},
		},
		"stop_reason": r.StopReason, "stop_sequence": nil,
		"usage": usage(in, outTokens(r.Text)),
	}
}

const chunkRunes = 8

func streamReply(w http.ResponseWriter, r *http.Request, id, model string, in int, rep Reply) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	send := func(event string, data map[string]any) {
		data["type"] = event
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if fl != nil {
			fl.Flush()
		}
	}
	send("message_start", map[string]any{"message": map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": usage(in, 1),
	}})
	send("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}})
	send("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "signature_delta", "signature": signature}})
	send("content_block_stop", map[string]any{"index": 0})
	send("content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "text", "text": ""}})
	text := rep.Text
	for text != "" {
		n, i := 0, 0
		for i < len(text) && n < chunkRunes {
			_, size := utf8.DecodeRuneInString(text[i:])
			i += size
			n++
		}
		send("content_block_delta", map[string]any{"index": 1, "delta": map[string]any{"type": "text_delta", "text": text[:i]}})
		text = text[i:]
	}
	if rep.Stall {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Minute):
		}
		return
	}
	send("content_block_stop", map[string]any{"index": 1})
	send("message_delta", map[string]any{
		"delta": map[string]any{"stop_reason": rep.StopReason, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": outTokens(rep.Text)},
	})
	send("message_stop", map[string]any{})
}
