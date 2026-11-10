package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

// approveAPI builds the API over a seeded clone with an insert-only River
// client, as the api process holds one. "now" is the seed anchor.
func approveAPI(t *testing.T) (http.Handler, *pgxpool.Pool, time.Time) {
	t.Helper()
	url, meta := testdb.Seeded(t)
	p := pool(t, url)
	rc, err := river.NewClient(riverpgxv5.New(p), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{AdminToken: token, CampaignURLHosts: []string{"shop.example"}}
	return handler(t, &Server{Config: cfg, Pool: p, River: rc, Now: func() time.Time { return meta.AsOf }}), p, meta.AsOf
}

type eligible struct{ Total, SMS, Email int }

// reviewCampaign is a campaign in review with no approval yet. Each counts
// entry adds a revision with those audience counts; the last one is current.
func reviewCampaign(t *testing.T, p *pgxpool.Pool, version int, counts ...eligible) (id string, revisions []string) {
	t.Helper()
	ctx := context.Background()
	if err := p.QueryRow(ctx, `INSERT INTO campaigns (brief, campaign_url) VALUES ('brief', $1) RETURNING id::text`, okURL).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for i, c := range counts {
		var rev string
		if err := p.QueryRow(ctx, `INSERT INTO campaign_revisions (campaign_id, n, source, segment, sms_body, email_subject, email_body,
			audience_total, sms_eligible, email_eligible) VALUES ($1, $2, 'llm', $3, 'sms', 'subject', 'body', $4, $5, $6) RETURNING id::text`,
			id, i+1, editSegment, c.Total, c.SMS, c.Email).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		revisions = append(revisions, rev)
	}
	if _, err := p.Exec(ctx, `UPDATE campaigns SET state = 'in_review', version = $2, current_revision_id = $3 WHERE id = $1`,
		id, version, revisions[len(revisions)-1]); err != nil {
		t.Fatal(err)
	}
	return id, revisions
}

func approveBody(revision string, version int) string {
	return fmt.Sprintf(`{"revision_id":%q,"version":%d}`, revision, version)
}

// fanoutJobs counts the fan-out jobs queued for a campaign.
func fanoutJobs(t *testing.T, p *pgxpool.Pool, campaignID string) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), `SELECT count(*) FROM river_job
		WHERE kind = 'fanout' AND queue = 'fanout' AND args->>'campaign_id' = $1`, campaignID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type approval struct {
	State      string
	Version    int
	RevisionID *string
	By         *string
	At         *time.Time
}

func loadApproval(t *testing.T, p *pgxpool.Pool, id string) approval {
	t.Helper()
	var a approval
	if err := p.QueryRow(context.Background(), `SELECT state::text, version, approved_revision_id::text, approved_by, approved_at
		FROM campaigns WHERE id = $1`, id).Scan(&a.State, &a.Version, &a.RevisionID, &a.By, &a.At); err != nil {
		t.Fatal(err)
	}
	return a
}

// Approve moves the campaign to approved, records who approved which
// revision, and queues exactly one fan-out job, in one transaction.
func TestApprove(t *testing.T) {
	h, p, _ := approveAPI(t)
	id, revs := reviewCampaign(t, p, 2, eligible{315, 210, 0})

	rec := do(h, "POST", "/api/campaigns/"+id+"/approve", approveBody(revs[0], 2), bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	v := decodeView(t, rec)
	if v.Campaign.State != "approved" || *v.Campaign.Version != 3 {
		t.Errorf("response campaign %+v; want approved at version 3", v.Campaign)
	}
	a := loadApproval(t, p, id)
	if a.State != "approved" || a.Version != 3 || a.RevisionID == nil || *a.RevisionID != revs[0] || a.By == nil || *a.By != "admin" || a.At == nil {
		t.Errorf("stored %+v; want approved by admin, revision %s", a, revs[0])
	}
	if n := fanoutJobs(t, p, id); n != 1 {
		t.Errorf("%d fan-out jobs, want 1", n)
	}
	if got := loadCampaign(t, p, id); !slices.Equal(got.Events, []string{"in_review>approved"}) {
		t.Errorf("audit trail %v", got.Events)
	}
}

// One channel with recipients is enough.
func TestApproveEmailOnly(t *testing.T) {
	h, p, _ := approveAPI(t)
	id, revs := reviewCampaign(t, p, 0, eligible{40, 0, 12})
	if rec := do(h, "POST", "/api/campaigns/"+id+"/approve", approveBody(revs[0], 0), bearer); rec.Code != http.StatusOK {
		t.Errorf("approve: %d %s", rec.Code, rec.Body)
	}
}

// The guards: a stale revision, a stale version, no consenting recipient
// (the total ignores consent, so matches alone are not enough), and a state
// that cannot be approved. Each gets 409 and changes nothing.
func TestApproveGuards(t *testing.T) {
	h, p, _ := approveAPI(t)
	cases := []struct {
		name   string
		setup  func() (id, body string)
		code   string // expected error code, "" for any 409
		before string // state the campaign must keep
	}{
		{"not the latest revision", func() (string, string) {
			id, revs := reviewCampaign(t, p, 1, eligible{10, 5, 5}, eligible{10, 5, 5})
			return id, approveBody(revs[0], 1)
		}, codeVersionConflict, "in_review"},
		{"stale version", func() (string, string) {
			id, revs := reviewCampaign(t, p, 4, eligible{10, 5, 5})
			return id, approveBody(revs[0], 3)
		}, codeVersionConflict, "in_review"},
		{"matches but no consent on either channel", func() (string, string) {
			id, revs := reviewCampaign(t, p, 0, eligible{315, 0, 0})
			return id, approveBody(revs[0], 0)
		}, "no_recipients", "in_review"},
		{"no matches at all", func() (string, string) {
			id, revs := reviewCampaign(t, p, 0, eligible{0, 0, 0})
			return id, approveBody(revs[0], 0)
		}, "no_recipients", "in_review"},
		{"revision of another campaign", func() (string, string) {
			id, _ := reviewCampaign(t, p, 0, eligible{10, 5, 5})
			_, other := reviewCampaign(t, p, 0, eligible{10, 5, 5})
			return id, approveBody(other[0], 0)
		}, "", "in_review"},
		{"already approved", func() (string, string) {
			f := insertCampaign(t, p, "approved", 2)
			return f.ID, approveBody(f.Revision, 2)
		}, codeInvalidTransition, "approved"},
		{"still generating", func() (string, string) {
			f := insertCampaign(t, p, "generating", 2)
			return f.ID, approveBody(f.Revision, 2)
		}, codeInvalidTransition, "generating"},
		{"rejected", func() (string, string) {
			f := insertCampaign(t, p, "rejected", 2)
			return f.ID, approveBody(f.Revision, 2)
		}, codeInvalidTransition, "rejected"},
	}
	for _, c := range cases {
		id, body := c.setup()
		before := loadApproval(t, p, id)
		rec := do(h, "POST", "/api/campaigns/"+id+"/approve", body, bearer)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s: %d %s; want 409", c.name, rec.Code, rec.Body)
			continue
		}
		if c.code != "" && !slices.Contains(errorCodes(t, rec), " "+c.code) {
			t.Errorf("%s: codes %v; want %q", c.name, errorCodes(t, rec), c.code)
		}
		after := loadApproval(t, p, id)
		if after.State != c.before || after.Version != before.Version {
			t.Errorf("%s: campaign moved to %s version %d", c.name, after.State, after.Version)
		}
		if n := fanoutJobs(t, p, id); n != 0 {
			t.Errorf("%s: %d fan-out jobs queued", c.name, n)
		}
	}
}

func TestApproveRequestErrors(t *testing.T) {
	h, p, _ := approveAPI(t)
	id, revs := reviewCampaign(t, p, 0, eligible{10, 5, 5})
	cases := []struct {
		name, body string
		want       []string
	}{
		{"no revision", `{"version":0}`, []string{"/revision_id required"}},
		{"no version", fmt.Sprintf(`{"revision_id":%q}`, revs[0]), []string{"/version required"}},
		{"revision not a string", `{"revision_id":7,"version":0}`, []string{"/revision_id invalid_type"}},
		{"extra key", fmt.Sprintf(`{"revision_id":%q,"version":0,"force":true}`, revs[0]), []string{"/force unknown_property"}},
	}
	for _, c := range cases {
		rec := do(h, "POST", "/api/campaigns/"+id+"/approve", c.body, bearer)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s; want 422", c.name, rec.Code, rec.Body)
			continue
		}
		if got := errorCodes(t, rec); !slices.Equal(got, c.want) {
			t.Errorf("%s: codes %v, want %v", c.name, got, c.want)
		}
	}
	if rec := do(h, "POST", "/api/campaigns/"+id+"/approve", approveBody(revs[0], 0), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/campaigns/"+unknown+"/approve", approveBody(revs[0], 0), bearer); rec.Code != http.StatusNotFound {
		t.Errorf("unknown campaign: %d", rec.Code)
	}
	if n := fanoutJobs(t, p, id); n != 0 {
		t.Errorf("%d fan-out jobs after rejected requests", n)
	}
}

// Two reviewers approve at the same moment: one wins, one fan-out job.
func TestConcurrentApprove(t *testing.T) {
	h, p, _ := approveAPI(t)
	id, revs := reviewCampaign(t, p, 0, eligible{10, 5, 5})
	codes := make([]int, 6)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			<-start
			codes[i] = do(h, "POST", "/api/campaigns/"+id+"/approve", approveBody(revs[0], 0), bearer).Code
		})
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok != 1 {
		t.Errorf("status codes %v; want one 200", codes)
	}
	if n := fanoutJobs(t, p, id); n != 1 {
		t.Errorf("%d fan-out jobs, want 1", n)
	}
}
