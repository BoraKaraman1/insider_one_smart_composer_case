// Package campaign is the approval state machine (rule 7): the transition
// table, guarded transitions with an audit row each, and the validation of
// new campaigns and revisions. SQL lives in internal/store.
package campaign

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

// State is a campaign state. The names are the campaign_state domain in
// migration 00002.
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

// transitions is the whole state machine. A state that is not a key is
// terminal.
var transitions = map[State][]State{
	Draft:            {Generating},
	Generating:       {InReview, GenerationFailed, Cancelled},
	GenerationFailed: {Generating, Cancelled},
	InReview:         {InReview, Generating, Approved, Rejected, Cancelled},
	Approved:         {Sending, Cancelled},
	Sending:          {Completed, CompletedWithFailures, Cancelled},
}

// States returns every state.
func States() []State {
	return []State{Draft, Generating, InReview, GenerationFailed, Approved,
		Sending, Completed, CompletedWithFailures, Rejected, Cancelled}
}

// CanTransition reports whether the table allows from -> to.
func CanTransition(from, to State) bool {
	return slices.Contains(transitions[from], to)
}

var (
	// ErrInvalidTransition: the table does not allow the move.
	ErrInvalidTransition = errors.New("campaign: transition not allowed")
	// ErrConflict: the campaign is not in the expected state at the expected
	// version (someone else moved it first), or does not exist.
	ErrConflict = errors.New("campaign: state or version changed")
)

// Move is one requested transition. Version is the version the caller saw.
type Move struct {
	ID       string
	From, To State
	Version  int
	Actor    string
	Reason   string
}

// Transition applies m with a guarded update and writes its audit row, both
// in the caller's transaction, so callers can add their own writes
// atomically. On error the caller must roll back.
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
