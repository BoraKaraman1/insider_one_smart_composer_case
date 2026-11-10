package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/riverqueue/river"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

// HTTP-level error codes, next to the validation codes.
const (
	codeUnauthorized = "unauthorized"
	codeBodyTooLarge = "body_too_large"
	codeInternal     = "internal"
)

const (
	maxBody      = 64 << 10
	queryTimeout = 3 * time.Second
)

// Server holds the API's dependencies.
type Server struct {
	Config  Config
	Pool    *pgxpool.Pool
	Schemas *schema.Set
	Log     *slog.Logger
	Now     func() time.Time // defaults to time.Now; tests pin it
	// River is an insert-only client: generate and approve enqueue jobs in
	// the same transaction as their transition, and cancel cancels them.
	River *river.Client[pgx.Tx]
	// Hub wakes event streams on new events. Without one they only poll.
	Hub          *Hub
	Heartbeat    time.Duration // SSE heartbeat; DefaultHeartbeat if zero
	PollInterval time.Duration // SSE poll; DefaultPollInterval if zero
}

// Handler builds the routes. It refuses to build an open API: with no token,
// ALLOW_UNAUTHENTICATED must have been set explicitly.
func (s *Server) Handler() (http.Handler, error) {
	if s.Config.AdminToken == "" && !s.Config.AllowUnauthenticated {
		return nil, ErrNoToken
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Log == nil {
		s.Log = slog.New(slog.DiscardHandler)
	}
	api := http.NewServeMux()
	api.HandleFunc("POST /api/audience/count", s.count)
	s.campaignRoutes(api)
	s.generateRoutes(api)
	s.deliveryRoutes(api)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	// Everything under /api is behind auth, unknown paths included, so an
	// unauthenticated caller cannot map the routes.
	mux.Handle("/api/", s.auth(api))
	return s.requestID(s.logRequests(mux)), nil
}

type ctxKey struct{}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// requestID keeps a well-formed X-Request-ID and replaces anything else, so
// a caller cannot inject log lines or huge values.
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(id) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the connection (flush, write
// deadline) through this wrapper.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests logs method, path, status and duration. Never bodies, query
// strings or headers.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(), "request_id", requestIDFrom(r.Context()))
	})
}

// auth checks the bearer token. Both sides are hashed first, so the
// constant-time compare also hides the token's length.
func (s *Server) auth(next http.Handler) http.Handler {
	if s.Config.AdminToken == "" {
		return next // only reachable with AllowUnauthenticated; Handler checks
	}
	want := sha256.Sum256([]byte(s.Config.AdminToken))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(token))
		if !ok || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz checks the database is reachable and fully migrated. Liveness
// (healthz) deliberately checks nothing outside the process.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	notReady := func(reason string) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "reason": reason})
	}
	if err := s.Pool.Ping(ctx); err != nil {
		notReady("database unreachable")
		return
	}
	db := stdlib.OpenDBFromPool(s.Pool) // goose needs database/sql; closing it leaves the pool open
	defer db.Close()
	if pending, err := store.Pending(ctx, db); err != nil || pending {
		notReady("migrations pending")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type countResponse struct {
	segment.Counts
	AsOf     time.Time  `json:"as_of"`
	SeededAt *time.Time `json:"seeded_at"`
}

// count validates a segment rule and returns its audience size.
func (s *Server) count(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	doc, err := schema.Decode(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, codeBodyTooLarge, "body is larger than 64 KiB")
			return
		}
		writeError(w, http.StatusBadRequest, validation.InvalidJSON, err.Error())
		return
	}

	var res validation.Result
	body, isObject := doc.(map[string]any)
	switch {
	case !isObject:
		res.Error("", validation.InvalidType, `expected an object: {"rule": {...}}`)
	default:
		for k := range body {
			if k != "rule" {
				res.Error("/"+k, validation.UnknownProperty, `only "rule" is allowed`)
			}
		}
		if _, ok := body["rule"]; !ok {
			res.Error("/rule", validation.Required, `missing required key "rule"`)
		}
	}
	if !res.OK() {
		writeJSON(w, http.StatusUnprocessableEntity, issues(res.Errors))
		return
	}
	rule, res := segment.Validate(s.Schemas, body["rule"])
	if !res.OK() {
		for i := range res.Errors {
			res.Errors[i].Path = "/rule" + res.Errors[i].Path
		}
		writeJSON(w, http.StatusUnprocessableEntity, issues(res.Errors))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	asOf := s.Now().UTC().Truncate(time.Microsecond)
	counts, err := segment.CountAudience(ctx, s.Pool, rule, asOf)
	if err != nil {
		s.Log.Error("count failed", "error", err, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusInternalServerError, codeInternal, "counting the audience failed")
		return
	}
	seededAt, err := store.SeededAt(ctx, s.Pool)
	if err != nil {
		s.Log.Error("reading seed_meta failed", "error", err, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusInternalServerError, codeInternal, "counting the audience failed")
		return
	}
	writeJSON(w, http.StatusOK, countResponse{Counts: counts, AsOf: asOf, SeededAt: seededAt})
}

type errorBody struct {
	Errors []validation.Issue `json:"errors"`
}

func issues(list []validation.Issue) errorBody { return errorBody{Errors: list} }

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, issues([]validation.Issue{{Path: "", Code: code, Message: message}}))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
