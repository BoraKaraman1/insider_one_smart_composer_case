package campaign

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

// Beginner starts a transaction. *pgxpool.Pool satisfies it.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ErrNotFound: the campaign does not exist.
var ErrNotFound = store.ErrNotFound

func inTx(ctx context.Context, db Beginner, fn func(pgx.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Create stores a draft campaign and its first audit row.
func Create(ctx context.Context, db Beginner, n New, actor string) (store.Campaign, error) {
	var c store.Campaign
	err := inTx(ctx, db, func(tx pgx.Tx) error {
		var err error
		if c, err = store.CreateCampaign(ctx, tx, n.Brief, n.CampaignURL); err != nil {
			return err
		}
		return store.InsertCampaignEvent(ctx, tx, c.ID, "", string(Draft), actor, "created")
	})
	return c, err
}

// Reject moves a campaign in review to rejected.
func Reject(ctx context.Context, db Beginner, id string, version int, actor string) error {
	return inTx(ctx, db, func(tx pgx.Tx) error {
		if _, err := store.GetCampaign(ctx, tx, id); err != nil {
			return err
		}
		return Transition(ctx, tx, Move{ID: id, From: InReview, To: Rejected, Version: version, Actor: actor})
	})
}

// Cancel moves a campaign to cancelled from any state that allows it, and in
// the same transaction takes its queued messages out of the queue. A worker
// that already holds one of the other messages loses its guarded update.
func Cancel(ctx context.Context, db Beginner, id string, version int, actor string) error {
	return inTx(ctx, db, func(tx pgx.Tx) error {
		c, err := store.GetCampaign(ctx, tx, id)
		if err != nil {
			return err
		}
		// The state read here can be stale; the guarded update then loses.
		if err := Transition(ctx, tx, Move{ID: id, From: State(c.State), To: Cancelled, Version: version, Actor: actor}); err != nil {
			return err
		}
		_, err = store.SkipQueuedMessages(ctx, tx, id)
		return err
	})
}

// Edit is a human edit of a campaign in review.
type Edit struct {
	ID             string
	BaseRevisionID string // the revision the reviewer edited
	Version        int    // the campaign version the reviewer saw
	Revision       Revision
	Warnings       []validation.Issue
	Actor          string
	AsOf           time.Time // "now" for the audience count
}

// SaveEdit stores a validated human edit as a new revision (source human),
// counts its audience again and keeps the campaign in review. It conflicts
// when the campaign moved on or the base revision is no longer current.
func SaveEdit(ctx context.Context, db Beginner, e Edit) (store.Revision, error) {
	segmentJSON, err := json.Marshal(e.Revision.SegmentDoc)
	if err != nil {
		return store.Revision{}, err
	}
	warnings := e.Warnings
	if warnings == nil {
		warnings = []validation.Issue{}
	}
	warningsJSON, err := json.Marshal(warnings)
	if err != nil {
		return store.Revision{}, err
	}
	var rev store.Revision
	err = inTx(ctx, db, func(tx pgx.Tx) error {
		if _, err := store.GetCampaign(ctx, tx, e.ID); err != nil {
			return err
		}
		// The transition locks the row, so the base check below and the
		// revision number cannot race with another edit.
		if err := Transition(ctx, tx, Move{ID: e.ID, From: InReview, To: InReview, Version: e.Version, Actor: e.Actor, Reason: "edit"}); err != nil {
			return err
		}
		c, err := store.GetCampaign(ctx, tx, e.ID)
		if err != nil {
			return err
		}
		if c.CurrentRevisionID == nil || *c.CurrentRevisionID != e.BaseRevisionID {
			return fmt.Errorf("%w: revision %s is not the current one", ErrConflict, e.BaseRevisionID)
		}
		counts, err := segment.CountAudience(ctx, tx, e.Revision.Segment, e.AsOf)
		if err != nil {
			return err
		}
		rev, err = store.InsertRevision(ctx, tx, store.NewRevision{
			CampaignID:    e.ID,
			Source:        "human",
			Segment:       segmentJSON,
			SMSBody:       e.Revision.Content.SMSBody,
			EmailSubject:  e.Revision.Content.EmailSubject,
			EmailBody:     e.Revision.Content.EmailBody,
			Warnings:      warningsJSON,
			AudienceTotal: counts.Total,
			SMSEligible:   counts.SMSEligible,
			EmailEligible: counts.EmailEligible,
		})
		return err
	})
	return rev, err
}
