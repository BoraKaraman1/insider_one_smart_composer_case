package store

import (
	"context"
	"testing"
)

func TestLatestStartedEventID(t *testing.T) {
	p, _ := seededPool(t)
	ctx := context.Background()
	a, b := newCampaignID(t, p), newCampaignID(t, p)

	if _, ok, err := LatestStartedEventID(ctx, p, a); err != nil || ok {
		t.Fatalf("no events: ok=%v err=%v, want ok=false", ok, err)
	}

	add := func(id, typ string) int64 {
		t.Helper()
		n, err := InsertGenerationEvent(ctx, p, id, typ, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	first := add(a, "started")
	add(a, "segment_ready")
	add(a, "done")
	if got, ok, err := LatestStartedEventID(ctx, p, a); err != nil || !ok || got != first {
		t.Fatalf("one generation: %d ok=%v err=%v, want %d", got, ok, err, first)
	}

	add(b, "started")
	if got, _, _ := LatestStartedEventID(ctx, p, a); got != first {
		t.Errorf("another campaign's started moved the cursor to %d", got)
	}

	second := add(a, "started")
	add(a, "delta")
	if got, ok, err := LatestStartedEventID(ctx, p, a); err != nil || !ok || got != second {
		t.Errorf("second generation: %d ok=%v err=%v, want %d", got, ok, err, second)
	}

	if _, ok, _ := LatestStartedEventID(ctx, p, b); !ok {
		t.Error("campaign b has a started event")
	}
	c := newCampaignID(t, p)
	add(c, "delta")
	if _, ok, _ := LatestStartedEventID(ctx, p, c); ok {
		t.Error("a campaign with events but no started has no cursor")
	}
}
