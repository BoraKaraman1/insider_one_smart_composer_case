// Paket model çağrılarını iki üretim aşaması için sarar.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/retry"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

const (
	DefaultModel     = "claude-opus-5-5"
	FallbackBeta     = "server-side-fallback-2026-07-01"
	MockBaseURL      = "http://mockllm:8090"
	AnthropicBaseURL = "https://api.anthropic.com"
	MockAPIKey       = "mock-placeholder"

	DefaultStallTimeout = 20 * time.Second
	DefaultTotalTimeout = 120 * time.Second

	MaxTokens = 16000
)

var effort = map[Stage]anthropic.OutputConfigEffort{
	Segment: anthropic.OutputConfigEffortLow,
	Content: anthropic.OutputConfigEffortMedium,
}

var schemaNames = map[Stage]schema.Name{
	Segment: schema.SegmentRule,
	Content: schema.Content,
}

var (
	ErrNoAPIKey     = errors.New("LLM_MODE=anthropic needs ANTHROPIC_API_KEY; set it, or use LLM_MODE=mock")
	ErrUnknownMode  = errors.New(`LLM_MODE must be "mock" or "anthropic"`)
	ErrStalled      = errors.New("llm: no stream event within the stall timeout")
	ErrTotalTimeout = errors.New("llm: the call took longer than the total timeout")
)

type Config struct {
	BaseURL      string
	APIKey       string
	Model        string
	Schemas      *schema.Set
	StallTimeout time.Duration
	TotalTimeout time.Duration
}

func ConfigFromEnv(getenv func(string) string) (Config, error) {
	var c Config
	switch getenv("LLM_MODE") {
	case "mock":
		c.BaseURL, c.APIKey = MockBaseURL, MockAPIKey
		if u := getenv("MOCKLLM_URL"); u != "" {
			c.BaseURL = u
		}
	case "anthropic":
		c.BaseURL, c.APIKey = AnthropicBaseURL, getenv("ANTHROPIC_API_KEY")
		if c.APIKey == "" {
			return Config{}, ErrNoAPIKey
		}
	default:
		return Config{}, ErrUnknownMode
	}
	c.Model = getenv("ANTHROPIC_MODEL")
	return c, nil
}

type Client struct {
	cfg     Config
	sdk     anthropic.Client
	schemas map[Stage]map[string]any
}

func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Schemas == nil {
		return nil, errors.New("llm: Config needs BaseURL, APIKey and Schemas")
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.StallTimeout <= 0 {
		cfg.StallTimeout = DefaultStallTimeout
	}
	if cfg.TotalTimeout <= 0 {
		cfg.TotalTimeout = DefaultTotalTimeout
	}
	c := &Client{cfg: cfg, schemas: map[Stage]map[string]any{}}
	for st, name := range schemaNames {
		s, err := cfg.Schemas.ForAPI(name)
		if err != nil {
			return nil, err
		}
		// Sayılar önce metin gibi duruyor, bir turdan sonra gerçek sayı oluyor.
		b, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		var plain map[string]any
		if err := json.Unmarshal(b, &plain); err != nil {
			return nil, err
		}
		c.schemas[st] = plain
	}
	c.sdk = anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(cfg.BaseURL),
		// Anahtar elle konuyor çünkü hazır yardımcı onu yanlış başlıkla yolluyor.
		option.WithHeader("X-Api-Key", cfg.APIKey),
		option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{}),
	)
	return c, nil
}

func (c *Client) Model() string { return c.cfg.Model }

type Conversation struct {
	Stage    Stage
	messages []anthropic.MessageParam
}

func NewConversation(stage Stage, firstTurn string) *Conversation {
	return &Conversation{Stage: stage, messages: []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(firstTurn)),
	}}
}

func (c *Conversation) Repair(r Response, issues []validation.Issue) {
	b, _ := json.Marshal(issues)
	c.messages = append(c.messages, r.message.ToParam(),
		anthropic.NewUserMessage(anthropic.NewTextBlock(
			"Your previous answer failed validation. Fix every error below and answer again with the whole JSON object. Errors: "+string(b))))
}

type Response struct {
	Text         string
	StopReason   string
	Model        string
	InputTokens  int
	OutputTokens int
	Latency      time.Duration
	message      anthropic.Message
}

type CallError struct {
	Status     int
	Class      retry.Class
	RetryAfter time.Duration
	Err        error
}

func (e *CallError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("llm: %s (HTTP %d): %v", e.Class, e.Status, e.Err)
	}
	return fmt.Sprintf("llm: %s: %v", e.Class, e.Err)
}

func (e *CallError) Unwrap() error { return e.Err }

func (c *Client) Call(parent context.Context, conv *Conversation, onText func(string)) (Response, error) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	total := time.AfterFunc(c.cfg.TotalTimeout, func() { cancel(ErrTotalTimeout) })
	defer total.Stop()
	stall := time.AfterFunc(c.cfg.StallTimeout, func() { cancel(ErrStalled) })
	defer stall.Stop()

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.cfg.Model),
		MaxTokens: MaxTokens,
		System: []anthropic.TextBlockParam{{
			Text:         SystemPrompt(conv.Stage),
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: conv.messages,
		Thinking: anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: effort[conv.Stage],
			Format: anthropic.JSONOutputFormatParam{Schema: c.schemas[conv.Stage]},
		},
	}
	start := time.Now()
	stream := c.sdk.Messages.NewStreaming(ctx, params,
		option.WithJSONSet("fallbacks", "default"),
		option.WithHeaderAdd("anthropic-beta", FallbackBeta),
	)
	defer stream.Close()
	var msg anthropic.Message
	for stream.Next() {
		stall.Reset(c.cfg.StallTimeout)
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return Response{}, &CallError{Class: retry.ConnReset, Err: fmt.Errorf("bad stream event: %w", err)}
		}
		if d, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent); ok && onText != nil {
			if td, ok := d.Delta.AsAny().(anthropic.TextDelta); ok {
				onText(td.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return Response{}, c.callError(parent, ctx, err)
	}
	// Akış ara olay olmadan biterse bağlantı kopmuş sayılır.
	if msg.StopReason == "" {
		if cause := context.Cause(ctx); parent.Err() == nil && cause != nil {
			return Response{}, c.callError(parent, ctx, cause)
		}
		return Response{}, &CallError{Class: retry.ConnReset, Err: errors.New("stream ended before the message was complete")}
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if tb, ok := b.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(tb.Text)
		}
	}
	return Response{
		Text:         text.String(),
		StopReason:   string(msg.StopReason),
		Model:        string(msg.Model),
		InputTokens:  int(msg.Usage.InputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
		Latency:      time.Since(start),
		message:      msg,
	}, nil
}

func (c *Client) callError(parent, ctx context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if cause := context.Cause(ctx); errors.Is(cause, ErrStalled) || errors.Is(cause, ErrTotalTimeout) {
		return &CallError{Class: retry.Timeout, Err: cause}
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		resp := apiErr.Response
		if resp == nil {
			resp = &http.Response{StatusCode: apiErr.StatusCode, Header: http.Header{}}
		}
		r := retry.Classify(resp, nil, time.Now())
		return &CallError{Status: apiErr.StatusCode, Class: r.Class, RetryAfter: r.RetryAfter, Err: err}
	}
	return &CallError{Class: retry.Classify(nil, err, time.Now()).Class, Err: err}
}
