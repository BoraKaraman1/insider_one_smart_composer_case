package campaign

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, _ := testdb.Seeded(t)
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// insertCampaign creates a campaign in the given state and version. It has a
// revision and a full approval in every state, so the table's CHECKs accept
// any state and the test exercises only the state machine.
func insertCampaign(t *testing.T, p *pgxpool.Pool, state State, version int) string {
	t.Helper()
	ctx := context.Background()
	var id, rev string
	if err := p.QueryRow(ctx, `INSERT INTO campaigns (brief, campaign_url) VALUES ('brief', 'https://shop.example/k') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO campaign_revisions (campaign_id, n, source, segment, sms_body, email_subject, email_body)
		VALUES ($1, 1, 'llm', '{}', 'sms', 'subject', 'body') RETURNING id::text`, id).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE campaigns SET state = $2, version = $3, current_revision_id = $4,
		approved_revision_id = $4, approved_by = 'fixture', approved_at = now() WHERE id = $1`, id, string(state), version, rev); err != nil {
		t.Fatal(err)
	}
	return id
}

type row struct {
	State   State
	Version int
	Events  []event
}

type event struct{ From, To, Actor, Reason string }

func load(t *testing.T, p *pgxpool.Pool, id string) row {
	t.Helper()
	ctx := context.Background()
	var r row
	var state string
	if err := p.QueryRow(ctx, `SELECT state::text, version FROM campaigns WHERE id = $1`, id).Scan(&state, &r.Version); err != nil {
		t.Fatal(err)
	}
	r.State = State(state)
	rows, err := p.Query(ctx, `SELECT coalesce(from_state::text, ''), to_state::text, actor, reason FROM campaign_events WHERE campaign_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.From, &e.To, &e.Actor, &e.Reason); err != nil {
			t.Fatal(err)
		}
		r.Events = append(r.Events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return r
}

// move runs one transition in its own transaction and commits on success.
func move(p *pgxpool.Pool, m Move) error {
	ctx := context.Background()
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := Transition(ctx, tx, m); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TestTransitionMatrixAgainstDatabase runs all 100 pairs through the guarded
// update. An allowed pair moves the row, bumps the version and writes one
// audit row. A pair outside the table changes nothing.
func TestTransitionMatrixAgainstDatabase(t *testing.T) {
	p := testPool(t)
	for _, from := range States() {
		for _, to := range States() {
			id := insertCampaign(t, p, from, 4)
			err := move(p, Move{ID: id, From: from, To: to, Version: 4, Actor: "marketer", Reason: "matrix"})
			got := load(t, p, id)
			if slices.Contains(allowed[from], to) {
				if err != nil {
					t.Errorf("%s -> %s: %v", from, to, err)
					continue
				}
				want := []event{{string(from), string(to), "marketer", "matrix"}}
				if got.State != to || got.Version != 5 || !slices.Equal(got.Events, want) {
					t.Errorf("%s -> %s: row %+v; want state %s, version 5, events %v", from, to, got, to, want)
				}
				continue
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("%s -> %s: error %v, want ErrInvalidTransition", from, to, err)
			}
			if got.State != from || got.Version != 4 || len(got.Events) != 0 {
				t.Errorf("%s -> %s was rejected but changed the row: %+v", from, to, got)
			}
		}
	}
}

func TestTransitionConflicts(t *testing.T) {
	p := testPool(t)
	cases := []struct {
		name   string
		actual State
		m      Move
	}{
		{"stale version", InReview, Move{From: InReview, To: Rejected, Version: 6}},
		{"version from the future", InReview, Move{From: InReview, To: Rejected, Version: 8}},
		{"the campaign already moved on", Approved, Move{From: InReview, To: Rejected, Version: 7}},
	}
	for _, c := range cases {
		id := insertCampaign(t, p, c.actual, 7)
		c.m.ID, c.m.Actor = id, "marketer"
		if err := move(p, c.m); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: error %v, want ErrConflict", c.name, err)
		}
		if got := load(t, p, id); got.State != c.actual || got.Version != 7 || len(got.Events) != 0 {
			t.Errorf("%s: a lost update changed the row: %+v", c.name, got)
		}
	}
	// A campaign that does not exist matches zero rows too.
	err := move(p, Move{ID: "00000000-0000-4000-8000-00000000dead", From: InReview, To: Rejected, Version: 0, Actor: "marketer"})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("unknown campaign: error %v, want ErrConflict", err)
	}
}

// Two people act on the same review screen at once. Exactly one wins.
func TestConcurrentTransitionsOneWins(t *testing.T) {
	p := testPool(t)
	id := insertCampaign(t, p, InReview, 2)
	targets := []State{Approved, Rejected, Cancelled, Generating, Approved, Rejected, Cancelled, Generating}
	errs := make([]error, len(targets))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, to := range targets {
		wg.Go(func() {
			<-start
			errs[i] = move(p, Move{ID: id, From: InReview, To: to, Version: 2, Actor: "marketer"})
		})
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, err := range errs {
		switch {
		case err == nil && winner == -1:
			winner = i
		case err == nil:
			t.Errorf("transitions %d and %d both won", winner, i)
		case !errors.Is(err, ErrConflict):
			t.Errorf("loser %d: error %v, want ErrConflict", i, err)
		}
	}
	if winner == -1 {
		t.Fatal("no transition won")
	}
	got := load(t, p, id)
	if got.State != targets[winner] || got.Version != 3 || len(got.Events) != 1 {
		t.Errorf("row %+v; want state %s, version 3, one audit row", got, targets[winner])
	}
}

// The transition and its audit row belong to the caller's transaction, so
// approve and cancel can add their own writes atomically.
func TestTransitionRollsBackWithCaller(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	id := insertCampaign(t, p, InReview, 0)
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Transition(ctx, tx, Move{ID: id, From: InReview, To: Rejected, Version: 0, Actor: "marketer"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got := load(t, p, id); got.State != InReview || got.Version != 0 || len(got.Events) != 0 {
		t.Errorf("after rollback: %+v", got)
	}
}
