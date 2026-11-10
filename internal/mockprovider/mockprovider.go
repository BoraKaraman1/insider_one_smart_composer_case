// Paket kendi veritabanında hata enjekte eden sahte sağlayıcıdır.
package mockprovider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Seed    uint64
	Latency time.Duration

	Rate500             float64
	Rate503             float64
	Rate429             float64
	Rate429NoRetryAfter float64
	RateHang            float64
	RateDrop            float64
	RateLate            float64
	RetryAfter          time.Duration

	ChaosHeader bool
}

var chaosModes = []string{"500", "503", "429", "429-no-retry-after", "400", "hang", "drop", "late"}

func ConfigFromEnv(getenv func(string) string) (Config, error) {
	var c Config
	var errs []error
	num := func(name string, dst *float64) {
		v := getenv("MOCKPROVIDER_" + name)
		if v == "" {
			return
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 || math.IsNaN(f) {
			errs = append(errs, fmt.Errorf("MOCKPROVIDER_%s: want a share between 0 and 1, got %q", name, v))
			return
		}
		*dst = f
	}
	dur := func(name string, dst *time.Duration) {
		v := getenv("MOCKPROVIDER_" + name)
		if v == "" {
			return
		}
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			errs = append(errs, fmt.Errorf("MOCKPROVIDER_%s: want a duration such as 50ms, got %q", name, v))
			return
		}
		*dst = d
	}
	if v := getenv("MOCKPROVIDER_SEED"); v != "" {
		s, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("MOCKPROVIDER_SEED: %q is not a number", v))
		}
		c.Seed = s
	}
	dur("LATENCY", &c.Latency)
	dur("RETRY_AFTER", &c.RetryAfter)
	num("RATE_500", &c.Rate500)
	num("RATE_503", &c.Rate503)
	num("RATE_429", &c.Rate429)
	num("RATE_429_NO_RETRY_AFTER", &c.Rate429NoRetryAfter)
	num("RATE_HANG", &c.RateHang)
	num("RATE_DROP", &c.RateDrop)
	num("RATE_LATE", &c.RateLate)
	c.ChaosHeader = getenv("MOCKPROVIDER_CHAOS_HEADER") == "true"
	if total := c.Rate500 + c.Rate503 + c.Rate429 + c.Rate429NoRetryAfter + c.RateHang + c.RateDrop + c.RateLate; total > 1 {
		errs = append(errs, fmt.Errorf("MOCKPROVIDER_RATE_*: the rates add up to %.2f, more than 1", total))
	}
	return c, errors.Join(errs...)
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS sends (
    idempotency_key text        PRIMARY KEY,
    channel         text        NOT NULL,
    payload_hash    text        NOT NULL,
    provider_id     text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
-- One row per real send. The idempotency logic allows one per key; this log
-- measures it instead of assuming it.
CREATE TABLE IF NOT EXISTS deliveries (
    id              bigserial   PRIMARY KEY,
    idempotency_key text        NOT NULL,
    channel         text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS requests (
    id              bigserial   PRIMARY KEY,
    channel         text        NOT NULL,
    idempotency_key text        NOT NULL,
    outcome         text        NOT NULL,
    received_at     timestamptz NOT NULL
);`)
	return err
}

type Server struct {
	pool *pgxpool.Pool
	cfg  Config

	mu  sync.Mutex
	rng *mrand.Rand
}

func New(pool *pgxpool.Pool, cfg Config) *Server {
	return &Server{pool: pool, cfg: cfg, rng: mrand.New(mrand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15))}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/stats":
		s.stats(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		s.send(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

type message struct {
	Channel string `json:"channel"`
	To      string `json:"to"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

func (s *Server) draw() (string, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x := s.rng.Float64()
	var latency time.Duration
	if s.cfg.Latency > 0 {
		latency = time.Duration(s.rng.Int64N(int64(s.cfg.Latency) + 1))
	}
	c := s.cfg
	for _, m := range []struct {
		name string
		rate float64
	}{
		{"500", c.Rate500}, {"503", c.Rate503}, {"429", c.Rate429}, {"429-no-retry-after", c.Rate429NoRetryAfter},
		{"hang", c.RateHang}, {"drop", c.RateDrop}, {"late", c.RateLate},
	} {
		if x < m.rate {
			return m.name, latency
		}
		x -= m.rate
	}
	return "ok", latency
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	received := time.Now()
	key := r.Header.Get("Idempotency-Key")
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var m message
	if err == nil {
		err = json.Unmarshal(raw, &m)
	}
	reject := func(msg string) {
		s.log(r.Context(), m.Channel, key, "rejected", received)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
	}
	switch {
	case key == "":
		reject("Idempotency-Key header is required")
		return
	case err != nil:
		reject("body is not JSON")
		return
	case m.Channel != "sms" && m.Channel != "email":
		reject(`channel must be "sms" or "email"`)
		return
	case m.To == "":
		reject("to is required")
		return
	case m.Body == "":
		reject("body is required")
		return
	}

	mode, latency := s.draw()
	if s.cfg.ChaosHeader {
		if forced := r.Header.Get("X-Chaos"); forced != "" {
			if forced != "ok" && !slices.Contains(chaosModes, forced) {
				reject("unknown X-Chaos value")
				return
			}
			mode = forced
		}
	}
	wait := func() bool {
		if latency <= 0 {
			return true
		}
		select {
		case <-r.Context().Done():
			return false
		case <-time.After(latency):
			return true
		}
	}

	switch mode {
	case "500", "503", "400", "429", "429-no-retry-after":
		if !wait() {
			s.log(r.Context(), m.Channel, key, "client_left", received)
			return
		}
	}
	switch mode {
	case "500", "503", "400":
		code, _ := strconv.Atoi(mode)
		s.log(r.Context(), m.Channel, key, mode, received)
		writeJSON(w, code, map[string]string{"error": "chaos " + mode})
		return
	case "429", "429-no-retry-after":
		if mode == "429" && s.cfg.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(s.cfg.RetryAfter.Seconds()))))
		}
		s.log(r.Context(), m.Channel, key, mode, received)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow down"})
		return
	case "hang":
		s.log(r.Context(), m.Channel, key, mode, received)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Minute):
		}
		return
	}

	status, id, outcome, err := s.process(r.Context(), key, m, wait)
	if err != nil {
		s.log(context.WithoutCancel(r.Context()), m.Channel, key, "error", received)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store failed"})
		return
	}
	if mode == "drop" || mode == "late" {
		outcome = mode
	}
	s.log(context.WithoutCancel(r.Context()), m.Channel, key, outcome, received)
	switch mode {
	case "drop":
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
				return
			}
		}
		panic(http.ErrAbortHandler)
	case "late":
		select {
		case <-r.Context().Done():
		case <-time.After(time.Minute):
		}
	}
	if status == http.StatusUnprocessableEntity {
		writeJSON(w, status, map[string]string{"error": "Idempotency-Key reused with a different payload"})
		return
	}
	writeJSON(w, status, map[string]string{"id": id})
}

func (s *Server) process(ctx context.Context, key string, m message, send func() bool) (status int, id, outcome string, err error) {
	canon, _ := json.Marshal(m)
	sum := sha256.Sum256(canon)
	hash := hex.EncodeToString(sum[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, "", "", err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return 0, "", "", err
	}
	var storedHash string
	err = tx.QueryRow(ctx, `SELECT payload_hash, provider_id FROM sends WHERE idempotency_key = $1`, key).Scan(&storedHash, &id)
	switch {
	case err == nil && storedHash == hash:
		return http.StatusAccepted, id, "replay", tx.Commit(ctx)
	case err == nil:
		return http.StatusUnprocessableEntity, "", "conflict", tx.Commit(ctx)
	case !errors.Is(err, pgx.ErrNoRows):
		return 0, "", "", err
	}
	if !send() {
		return 0, "", "", ctx.Err()
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id = "pm_" + hex.EncodeToString(b)
	if _, err := tx.Exec(ctx, `INSERT INTO sends (idempotency_key, channel, payload_hash, provider_id) VALUES ($1, $2, $3, $4)`,
		key, m.Channel, hash, id); err != nil {
		return 0, "", "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO deliveries (idempotency_key, channel) VALUES ($1, $2)`, key, m.Channel); err != nil {
		return 0, "", "", err
	}
	return http.StatusAccepted, id, "sent", tx.Commit(ctx)
}

func (s *Server) log(ctx context.Context, channel, key, outcome string, at time.Time) {
	_, _ = s.pool.Exec(context.WithoutCancel(ctx), `INSERT INTO requests (channel, idempotency_key, outcome, received_at) VALUES ($1, $2, $3, $4)`,
		channel, key, outcome, at)
}

type Stats struct {
	Requests       int                    `json:"requests"`
	UniqueSends    int                    `json:"unique_sends"`
	MaxSendsPerKey int                    `json:"max_sends_per_key"`
	Replays        int                    `json:"replays"`
	Outcomes       map[string]int         `json:"outcomes"`
	ReceivedAt     map[string][]time.Time `json:"received_at"`
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := Stats{Outcomes: map[string]int{}, ReceivedAt: map[string][]time.Time{}}
	err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM requests),
			(SELECT count(DISTINCT idempotency_key) FROM deliveries),
			(SELECT coalesce(max(n), 0) FROM (SELECT count(*) AS n FROM deliveries GROUP BY idempotency_key) d)`).
		Scan(&st.Requests, &st.UniqueSends, &st.MaxSendsPerKey)
	if err == nil {
		err = s.collect(ctx, `SELECT outcome, count(*) FROM requests GROUP BY outcome`, func(rows pgx.Rows) error {
			var o string
			var n int
			if err := rows.Scan(&o, &n); err != nil {
				return err
			}
			st.Outcomes[o] = n
			return nil
		})
	}
	if err == nil {
		err = s.collect(ctx, `SELECT channel, received_at FROM requests WHERE channel IN ('sms', 'email') ORDER BY id`, func(rows pgx.Rows) error {
			var ch string
			var at time.Time
			if err := rows.Scan(&ch, &at); err != nil {
				return err
			}
			st.ReceivedAt[ch] = append(st.ReceivedAt[ch], at)
			return nil
		})
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stats failed"})
		return
	}
	st.Replays = st.Outcomes["replay"]
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) collect(ctx context.Context, sql string, scan func(pgx.Rows) error) error {
	rows, err := s.pool.Query(ctx, sql)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
