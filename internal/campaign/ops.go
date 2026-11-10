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

type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

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

func Reject(ctx context.Context, db Beginner, id string, version int, actor string) error {
	return inTx(ctx, db, func(tx pgx.Tx) error {
		if _, err := store.GetCampaign(ctx, tx, id); err != nil {
			return err
		}
		return Transition(ctx, tx, Move{ID: id, From: InReview, To: Rejected, Version: version, Actor: actor})
	})
}

func Cancel(ctx context.Context, db Beginner, id string, version int, actor string) error {
	return CancelWithJobs(ctx, db, nil, id, version, actor)
}

func cancelTx(ctx context.Context, tx pgx.Tx, id string, version int, actor string) error {
	c, err := store.GetCampaign(ctx, tx, id)
	if err != nil {
		return err
	}
	from := State(c.State)
	if err := Transition(ctx, tx, Move{ID: id, From: from, To: Cancelled, Version: version, Actor: actor}); err != nil {
		return err
	}
	if from == Generating {
		if _, err := store.InsertGenerationEvent(ctx, tx, id, "failed", map[string]any{"reason": ReasonCancelled}); err != nil {
			return err
		}
	}
	_, err = store.SkipQueuedMessages(ctx, tx, id)
	return err
}

type Edit struct {
	ID             string
	BaseRevisionID string
	Version        int
	Revision       Revision
	Warnings       []validation.Issue
	Actor          string
	AsOf           time.Time
}

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
