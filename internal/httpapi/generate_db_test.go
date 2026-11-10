package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

// generateAPI builds the API with an insert-only River client, as the api
// process holds one. No worker runs, so jobs stay queued for inspection.
func generateAPI(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	url, meta := testdb.Seeded(t)
	p := pool(t, url)
	rc, err := river.NewClient(riverpgxv5.New(p), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{AdminToken: token, CampaignURLHosts: []string{"shop.example"}}
	return handler(t, &Server{Config: cfg, Pool: p, River: rc, Now: func() time.Time { return meta.AsOf }}), p
}

func generateJobArgs(t *testing.T, p *pgxpool.Pool, campaignID string) []jobs.GenerateArgs {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT args::text FROM river_job WHERE kind = 'generate' AND queue = $2
		AND args->>'campaign_id' = $1 ORDER BY id`, campaignID, jobs.QueueGenerate)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (jobs.GenerateArgs, error) {
		var raw string
		var a jobs.GenerateArgs
		err := r.Scan(&raw)
		if err == nil {
			err = json.Unmarshal([]byte(raw), &a)
		}
		return a, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// POST /generate moves the campaign to generating and enqueues the job in
// one transaction, then answers 202 with the URL of its event stream.
func TestGenerateEndpoint(t *testing.T) {
	h, p := generateAPI(t)
	for _, from := range []string{"draft", "in_review", "generation_failed"} {
		f := insertCampaign(t, p, from, 2)
		rec := do(h, "POST", "/api/campaigns/"+f.ID+"/generate", versionBody(2), bearer)
		if rec.Code != http.StatusAccepted {
			t.Errorf("generate from %s: %d %s", from, rec.Code, rec.Body)
			continue
		}
		var body struct {
			EventsURL string `json:"events_url"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !isEventsURL(body.EventsURL, f.ID) {
			t.Errorf("generate from %s: body %s, want events_url for /api/campaigns/%s/events", from, rec.Body, f.ID)
		}
		got := loadCampaign(t, p, f.ID)
		if got.State != "generating" || got.Version != 3 || !slices.Equal(got.Events, []string{from + ">generating"}) {
			t.Errorf("generate from %s: stored %+v", from, got)
		}
		if args := generateJobArgs(t, p, f.ID); !slices.Equal(args, []jobs.GenerateArgs{{CampaignID: f.ID, Version: 3}}) {
			t.Errorf("generate from %s: jobs %+v, want one at version 3", from, args)
		}
	}

	refused := []struct {
		name, state string
		version     int
		status      int
	}{
		{"stale version", "in_review", 1, http.StatusConflict},
		{"already generating", "generating", 2, http.StatusConflict},
		{"approved", "approved", 2, http.StatusConflict},
		{"sending", "sending", 2, http.StatusConflict},
		{"cancelled", "cancelled", 2, http.StatusConflict},
	}
	for _, c := range refused {
		f := insertCampaign(t, p, c.state, 2)
		if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/generate", versionBody(c.version), bearer); rec.Code != c.status {
			t.Errorf("%s: %d, want %d: %s", c.name, rec.Code, c.status, rec.Body)
		}
		if got := loadCampaign(t, p, f.ID); got.State != c.state || got.Version != 2 || len(got.Events) != 0 {
			t.Errorf("%s: campaign changed: %+v", c.name, got)
		}
		if n := len(generateJobArgs(t, p, f.ID)); n != 0 {
			t.Errorf("%s: %d jobs enqueued", c.name, n)
		}
	}

	f := insertCampaign(t, p, "draft", 0)
	for _, body := range []string{`{}`, `{"version":"0"}`, `{"version":0,"force":true}`} {
		if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/generate", body, bearer); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("body %s: %d, want 422", body, rec.Code)
		}
	}
	if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/generate", versionBody(0), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a token: %d, want 401", rec.Code)
	}
	if rec := do(h, "POST", "/api/campaigns/"+unknown+"/generate", versionBody(0), bearer); rec.Code != http.StatusNotFound {
		t.Errorf("unknown campaign: %d, want 404", rec.Code)
	}
	if n := len(generateJobArgs(t, p, f.ID)); n != 0 {
		t.Errorf("%d jobs enqueued by rejected requests", n)
	}
}
