// Paket kampanya onayının durum makinesini ve üretim işini tutar.
package campaign

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

type State string

const (
	Draft                 State = "draft"
	Generating            State = "generating"
	InReview              State = "in_review"
	GenerationFailed      State = "generation_failed"
	Approved              State = "approved"
	Sending               State = "sending"
	Completed             State = "completed"
	CompletedWithFailures State = "completed_with_failures"
	Rejected              State = "rejected"
	Cancelled             State = "cancelled"
)

var transitions = map[State][]State{
	Draft:            {Generating},
	Generating:       {InReview, GenerationFailed, Cancelled},
	GenerationFailed: {Generating, Cancelled},
	InReview:         {InReview, Generating, Approved, Rejected, Cancelled},
	Approved:         {Sending, Cancelled},
	Sending:          {Completed, CompletedWithFailures, Cancelled},
}

func States() []State {
	return []State{Draft, Generating, InReview, GenerationFailed, Approved,
		Sending, Completed, CompletedWithFailures, Rejected, Cancelled}
}

func CanTransition(from, to State) bool {
	return slices.Contains(transitions[from], to)
}

var (
	ErrInvalidTransition = errors.New("campaign: transition not allowed")
	ErrConflict          = errors.New("campaign: state or version changed")
)

type Move struct {
	ID       string
	From, To State
	Version  int
	Actor    string
	Reason   string
}

func Transition(ctx context.Context, tx pgx.Tx, m Move) error {
	if !CanTransition(m.From, m.To) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, m.From, m.To)
	}
	ok, err := store.UpdateCampaignState(ctx, tx, m.ID, string(m.From), string(m.To), m.Version)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: expected %s at version %d", ErrConflict, m.From, m.Version)
	}
	return store.InsertCampaignEvent(ctx, tx, m.ID, string(m.From), string(m.To), m.Actor, m.Reason)
}
