package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

// Defaults of the event stream.
const (
	DefaultHeartbeat    = 15 * time.Second
	DefaultPollInterval = time.Second // covers a missed notification
)

// Hub fans out generation event notifications. One LISTEN connection per
// api process feeds it, so streams do not each hold a database connection.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewHub starts listening on pool until ctx ends. A lost connection is
// reopened after a pause; streams also poll, so nothing is lost meanwhile.
func NewHub(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) *Hub {
	h := &Hub{subs: map[string]map[chan struct{}]struct{}{}}
	go h.listen(ctx, pool, log)
	return h
}

func (h *Hub) listen(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	for ctx.Err() == nil {
		err := h.listenOnce(ctx, pool)
		if ctx.Err() != nil {
			return
		}
		log.Warn("event hub: listen failed; retrying", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (h *Hub) listenOnce(ctx context.Context, pool *pgxpool.Pool) error {
	pc, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// The connection is in LISTEN state; never hand it back to the pool.
	conn := pc.Hijack()
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "LISTEN "+store.GenerationEventsChannel); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		h.notify(n.Payload)
	}
}

func (h *Hub) notify(campaignID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[campaignID] {
		select {
		case ch <- struct{}{}:
		default: // a wake-up is already pending
		}
	}
}

// Subscribe returns a channel that is signalled when the campaign may have
// new events, and a function to unsubscribe.
func (h *Hub) Subscribe(campaignID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[campaignID] == nil {
		h.subs[campaignID] = map[chan struct{}]struct{}{}
	}
	h.subs[campaignID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs[campaignID], ch)
		if len(h.subs[campaignID]) == 0 {
			delete(h.subs, campaignID)
		}
	}
}

var errBadEventID = errors.New("expected a non-negative integer event id")

func parseEventID(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, errBadEventID
	}
	return n, nil
}

// events streams a campaign's generation events as SSE: the stored backlog
// after Last-Event-ID (or ?after=), then live events, with heartbeat
// comments. It ends after a done or failed event. The token comes only from
// the Authorization header, so the browser reads this with fetch, not
// EventSource.
func (s *Server) events(w http.ResponseWriter, r *http.Request, id string) {
	var after int64
	for _, raw := range []string{r.URL.Query().Get("after"), r.Header.Get("Last-Event-ID")} {
		if raw == "" {
			continue
		}
		n, err := parseEventID(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_event_id", err.Error())
			return
		}
		after = max(after, n)
	}
	{
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		_, err := store.GetCampaign(ctx, s.Pool, id)
		cancel()
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}

	var wake <-chan struct{}
	if s.Hub != nil {
		ch, unsubscribe := s.Hub.Subscribe(id)
		defer unsubscribe()
		wake = ch
	}
	rc := http.NewResponseController(w)
	// The server's WriteTimeout would cut a long stream.
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if rc.Flush() != nil {
		return
	}

	heartbeat := s.Heartbeat
	if heartbeat <= 0 {
		heartbeat = DefaultHeartbeat
	}
	poll := s.PollInterval
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	hb, pt := time.NewTicker(heartbeat), time.NewTicker(poll)
	defer hb.Stop()
	defer pt.Stop()

	// send writes every new event and reports whether the stream is over.
	send := func() (bool, error) {
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()
		list, err := store.ListGenerationEvents(ctx, s.Pool, id, after)
		if err != nil {
			return false, err
		}
		for _, e := range list {
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, e.Payload); err != nil {
				return false, err
			}
			after = e.ID
			if e.Type == "done" || e.Type == "failed" {
				return true, rc.Flush()
			}
		}
		return false, rc.Flush()
	}
	for {
		ended, err := send()
		if err != nil {
			if r.Context().Err() == nil {
				s.Log.Warn("event stream failed", "error", err, "request_id", requestIDFrom(r.Context()))
			}
			return
		}
		if ended {
			return
		}
	wait:
		select {
		case <-r.Context().Done():
			return
		case <-hb.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			goto wait
		case <-wake:
		case <-pt.C:
		}
	}
}
