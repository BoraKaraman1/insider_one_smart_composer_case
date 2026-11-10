//go:build spec

package eval

import (
	"maps"
	"testing"
)

// Plan 3, 2.4: Aggregate turns llm_calls rows into the reported numbers.
// Contract assumed:
//
//	CallRow{CampaignID, Stage, Outcome, ErrorClass string; LatencyMS,
//	        InputTokens, OutputTokens int; Codes []string}
//	Aggregate(rows []CallRow) Numbers   rows in id order
//	Numbers{Stages map[string]StageNumbers; Generations int;
//	        InputTokens, OutputTokens int}           totals over all rows
//	StageNumbers{FirstAttemptValid, ValidAfterRepair, Failed,
//	        ValidationAttempts, Refusals int; TransportRetries map[string]int;
//	        FailureCodes map[string]int; LatencyP50MS, LatencyP95MS int}
//
// Each (campaign, stage) that ran ends as exactly one of first-attempt
// valid, valid after repair, or failed. A refusal row is a validation
// attempt that ends the stage as failed, and is also counted in Refusals.
// Transport rows are never validation attempts. Latency percentiles use the
// nearest rank over every call of the stage, transport calls included.

func row(campaign, stage, outcome string, latency int, codes ...string) CallRow {
	return CallRow{CampaignID: campaign, Stage: stage, Outcome: outcome, LatencyMS: latency,
		InputTokens: 100, OutputTokens: 10, Codes: codes}
}

func transport(campaign, stage, class string, latency int) CallRow {
	r := row(campaign, stage, "transport_error", latency)
	r.ErrorClass = class
	r.InputTokens, r.OutputTokens = 0, 0
	return r
}

func TestAggregate(t *testing.T) {
	rows := []CallRow{
		// A: valid segment; content valid after a transport retry.
		row("A", "segment", "valid", 100),
		transport("A", "content", "server_error", 50),
		row("A", "content", "valid", 60),
		// B: both stages repaired.
		row("B", "segment", "invalid", 200, "unknown_field"),
		row("B", "segment", "valid", 300),
		row("B", "content", "invalid", 70, "unknown_variable"),
		row("B", "content", "invalid", 80, "unknown_variable", "url_not_allowed"),
		row("B", "content", "valid", 90),
		// C: refused at the segment stage; content never ran.
		row("C", "segment", "refusal", 400),
		// D: content fails after three invalid answers and a 429.
		row("D", "segment", "valid", 500),
		row("D", "content", "invalid", 10, "sms_too_long"),
		transport("D", "content", "rate_limited", 15),
		row("D", "content", "invalid", 20, "sms_too_long"),
		row("D", "content", "invalid", 30, "sms_too_long"),
	}
	n := Aggregate(rows)

	if n.Generations != 4 {
		t.Errorf("generations %d, want 4", n.Generations)
	}
	if n.InputTokens != 1200 || n.OutputTokens != 120 {
		t.Errorf("tokens in %d out %d, want 1200 and 120 (transport rows carry none)", n.InputTokens, n.OutputTokens)
	}

	seg := n.Stages["segment"]
	checkStage(t, "segment", seg, StageNumbers{
		FirstAttemptValid: 2, ValidAfterRepair: 1, Failed: 1, ValidationAttempts: 5, Refusals: 1,
		TransportRetries: map[string]int{}, FailureCodes: map[string]int{"unknown_field": 1},
		LatencyP50MS: 300, LatencyP95MS: 500,
	})
	con := n.Stages["content"]
	checkStage(t, "content", con, StageNumbers{
		FirstAttemptValid: 1, ValidAfterRepair: 1, Failed: 1, ValidationAttempts: 7, Refusals: 0,
		TransportRetries: map[string]int{"server_error": 1, "rate_limited": 1},
		FailureCodes:     map[string]int{"unknown_variable": 2, "url_not_allowed": 1, "sms_too_long": 3},
		// Sorted: 10 15 20 30 50 60 70 80 90; rank ceil(.5*9)=5, ceil(.95*9)=9.
		LatencyP50MS: 50, LatencyP95MS: 90,
	})
}

func TestAggregateEmpty(t *testing.T) {
	n := Aggregate(nil)
	if n.Generations != 0 || len(n.Stages) != 0 {
		t.Errorf("no rows: %+v", n)
	}
}

func checkStage(t *testing.T, name string, got, want StageNumbers) {
	t.Helper()
	if got.FirstAttemptValid != want.FirstAttemptValid || got.ValidAfterRepair != want.ValidAfterRepair || got.Failed != want.Failed {
		t.Errorf("%s: first %d repaired %d failed %d, want %d %d %d", name,
			got.FirstAttemptValid, got.ValidAfterRepair, got.Failed, want.FirstAttemptValid, want.ValidAfterRepair, want.Failed)
	}
	if got.ValidationAttempts != want.ValidationAttempts {
		t.Errorf("%s: %d validation attempts, want %d (transport rows excluded)", name, got.ValidationAttempts, want.ValidationAttempts)
	}
	if got.Refusals != want.Refusals {
		t.Errorf("%s: %d refusals, want %d", name, got.Refusals, want.Refusals)
	}
	if !equalCounts(got.TransportRetries, want.TransportRetries) {
		t.Errorf("%s: transport retries %v, want %v", name, got.TransportRetries, want.TransportRetries)
	}
	if !equalCounts(got.FailureCodes, want.FailureCodes) {
		t.Errorf("%s: failure codes %v, want %v", name, got.FailureCodes, want.FailureCodes)
	}
	if got.LatencyP50MS != want.LatencyP50MS || got.LatencyP95MS != want.LatencyP95MS {
		t.Errorf("%s: latency p50 %d p95 %d, want %d %d", name, got.LatencyP50MS, got.LatencyP95MS, want.LatencyP50MS, want.LatencyP95MS)
	}
}

// equalCounts treats a nil map as empty.
func equalCounts(a, b map[string]int) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return maps.Equal(a, b)
}
