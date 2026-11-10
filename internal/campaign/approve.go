package campaign

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

// ErrNoRecipients: the revision has no consenting recipient on any channel.
// Its total ignores consent, so matches alone are not enough.
var ErrNoRecipients = errors.New("campaign: no recipient has consent on any channel")

// Approve approves the latest revision of a campaign in review and enqueues
// its fan-out job, in one transaction: an approval without its job, or a
// job without an approval, cannot exist.
func Approve(ctx context.Context, db Beginner, ins Inserter, id, revisionID string, version int, actor string) error {
	return inTx(ctx, db, func(tx pgx.Tx) error {
		c, err := store.LockCampaign(ctx, tx, id)
		if err != nil {
			return err
		}
		if !CanTransition(State(c.State), Approved) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, c.State, Approved)
		}
		if c.Version != version {
			return fmt.Errorf("%w: expected version %d", ErrConflict, version)
		}
		if c.CurrentRevisionID == nil || *c.CurrentRevisionID != revisionID {
			return fmt.Errorf("%w: revision %s is not the current one", ErrConflict, revisionID)
		}
		rev, err := store.GetRevision(ctx, tx, id, revisionID)
		if err != nil {
			return err
		}
		if deref(rev.SMSEligible)+deref(rev.EmailEligible) <= 0 {
			return ErrNoRecipients
		}
		ok, err := store.SetApproval(ctx, tx, id, revisionID, actor, version)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: the campaign moved on", ErrConflict)
		}
		if err := Transition(ctx, tx, Move{ID: id, From: InReview, To: Approved, Version: version, Actor: actor, Reason: "approved"}); err != nil {
			return err
		}
		_, err = ins.InsertTx(ctx, tx, jobs.FanoutArgs{CampaignID: id}, nil)
		return err
	})
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// CompleteIfDone moves a sending campaign to completed, or to
// completed_with_failures when a message failed or died, once no message is
// queued or sending. Callers run it after every terminal message update.
// It is a no-op when messages are left or the campaign is not sending, and
// when another run completed it first.
func CompleteIfDone(ctx context.Context, db Beginner, id string) error {
	err := inTx(ctx, db, func(tx pgx.Tx) error {
		c, err := store.LockCampaign(ctx, tx, id)
		if err != nil {
			return err
		}
		if State(c.State) != Sending {
			return nil
		}
		o, err := store.MessageOutcome(ctx, tx, id)
		if err != nil || o.Unfinished > 0 {
			return err
		}
		to := Completed
		if o.Failures > 0 {
			to = CompletedWithFailures
		}
		return Transition(ctx, tx, Move{ID: id, From: Sending, To: to, Version: c.Version, Actor: WorkerActor, Reason: "all messages finished"})
	})
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}
