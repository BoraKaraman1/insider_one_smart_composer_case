package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

// DB is the app database. *pgxpool.Pool satisfies it.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ManyInserter inserts River jobs in a transaction. *river.Client[pgx.Tx]
// satisfies it.
type ManyInserter interface {
	InsertManyTx(ctx context.Context, tx pgx.Tx, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

// FanoutWorker turns an approved campaign into messages and send jobs.
type FanoutWorker struct {
	river.WorkerDefaults[jobs.FanoutArgs]
	DB   DB
	Jobs ManyInserter
	// Now is the asOf of the recipient query: the audience is counted when
	// the fan-out runs, not at approval.
	Now func() time.Time

	once    sync.Once
	schemas *schema.Set
	err     error
}

// Work runs the fan-out in one transaction: a message per consenting
// recipient per channel, a send job per message on the channel's queue,
// and approved -> sending. With no message at all (consent revoked after
// approval), the campaign completes at once, since no send job will run
// the completion check. A second run finds the campaign sending and
// changes nothing.
func (w *FanoutWorker) Work(ctx context.Context, job *river.Job[jobs.FanoutArgs]) error {
	w.once.Do(func() { w.schemas, w.err = schema.Load() })
	if w.err != nil {
		return w.err
	}
	id := job.Args.CampaignID
	asOf := w.Now().UTC().Truncate(time.Microsecond)
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	c, err := store.LockCampaign(ctx, tx, id)
	if errors.Is(err, store.ErrNotFound) {
		return river.JobCancel(fmt.Errorf("fanout: campaign %s does not exist", id))
	}
	if err != nil {
		return err
	}
	switch campaign.State(c.State) {
	case campaign.Approved:
	case campaign.Sending, campaign.Completed, campaign.CompletedWithFailures:
		return nil // already fanned out
	default:
		return river.JobCancel(fmt.Errorf("fanout: campaign is %s, not approved", c.State))
	}
	rev, err := store.GetRevision(ctx, tx, id, *c.ApprovedRevisionID)
	if err != nil {
		return err
	}
	doc, err := schema.Decode(bytes.NewReader(rev.Segment))
	if err != nil {
		return river.JobCancel(fmt.Errorf("fanout: stored rule: %w", err))
	}
	rule, res := segment.Validate(w.schemas, doc)
	if !res.OK() {
		return river.JobCancel(fmt.Errorf("fanout: stored rule is invalid: %v", res.Errors))
	}

	var params []river.InsertManyParams
	for _, ch := range Channels {
		q, err := segment.AudienceQuery(rule, ch, asOf)
		if err != nil {
			return err
		}
		ids, err := store.InsertMessagesFrom(ctx, tx, id, rev.ID, ch, q.SQL, q.Args)
		if err != nil {
			return err
		}
		for _, mid := range ids {
			params = append(params, river.InsertManyParams{Args: jobs.SendArgs{MessageID: mid}, InsertOpts: &river.InsertOpts{Queue: ch}})
		}
	}
	if len(params) > 0 {
		if _, err := w.Jobs.InsertManyTx(ctx, tx, params); err != nil {
			return err
		}
	}
	if err := campaign.Transition(ctx, tx, campaign.Move{ID: id, From: campaign.Approved, To: campaign.Sending,
		Version: c.Version, Actor: campaign.WorkerActor, Reason: fmt.Sprintf("%d messages", len(params))}); err != nil {
		return err
	}
	n, err := store.CountMessages(ctx, tx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		if err := campaign.Transition(ctx, tx, campaign.Move{ID: id, From: campaign.Sending, To: campaign.Completed,
			Version: c.Version + 1, Actor: campaign.WorkerActor, Reason: "no recipients at send time"}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
