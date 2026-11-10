package eval

import (
	"slices"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

type CallRow = store.LLMCallRow

type Numbers struct {
	Stages       map[string]StageNumbers `json:"stages"`
	Generations  int                     `json:"generations"`
	InputTokens  int                     `json:"input_tokens"`
	OutputTokens int                     `json:"output_tokens"`
}

type StageNumbers struct {
	FirstAttemptValid  int            `json:"first_attempt_valid"`
	ValidAfterRepair   int            `json:"valid_after_repair"`
	Failed             int            `json:"failed"`
	ValidationAttempts int            `json:"validation_attempts"`
	Refusals           int            `json:"refusals"`
	TransportRetries   map[string]int `json:"transport_retries"`
	FailureCodes       map[string]int `json:"failure_codes"`
	LatencyP50MS       int            `json:"latency_p50_ms"`
	LatencyP95MS       int            `json:"latency_p95_ms"`
}

const transportOutcome = "transport_error"

func Aggregate(rows []CallRow) Numbers {
	n := Numbers{Stages: map[string]StageNumbers{}}
	type key struct{ campaign, stage string }
	type run struct {
		attempts int
		last     string
	}
	runs := map[key]*run{}
	var order []key
	campaigns := map[string]bool{}
	latencies := map[string][]int{}

	for _, r := range rows {
		campaigns[r.CampaignID] = true
		n.InputTokens += r.InputTokens
		n.OutputTokens += r.OutputTokens
		latencies[r.Stage] = append(latencies[r.Stage], r.LatencyMS)
		s := stageOf(n, r.Stage)
		k := key{r.CampaignID, r.Stage}
		if runs[k] == nil {
			runs[k] = &run{}
			order = append(order, k)
		}
		if r.Outcome == transportOutcome {
			s.TransportRetries[r.ErrorClass]++
		} else {
			s.ValidationAttempts++
			runs[k].attempts++
			runs[k].last = r.Outcome
			if r.Outcome == "refusal" {
				s.Refusals++
			}
			for _, c := range r.Codes {
				s.FailureCodes[c]++
			}
		}
		n.Stages[r.Stage] = s
	}
	for _, k := range order {
		s := n.Stages[k.stage]
		switch rr := runs[k]; {
		case rr.last == "valid" && rr.attempts == 1:
			s.FirstAttemptValid++
		case rr.last == "valid":
			s.ValidAfterRepair++
		default:
			s.Failed++
		}
		n.Stages[k.stage] = s
	}
	for st, l := range latencies {
		s := n.Stages[st]
		s.LatencyP50MS, s.LatencyP95MS = percentile(l, 50), percentile(l, 95)
		n.Stages[st] = s
	}
	n.Generations = len(campaigns)
	return n
}

func stageOf(n Numbers, stage string) StageNumbers {
	s, ok := n.Stages[stage]
	if !ok {
		s = StageNumbers{TransportRetries: map[string]int{}, FailureCodes: map[string]int{}}
	}
	return s
}

func percentile(values []int, p int) int {
	if len(values) == 0 {
		return 0
	}
	s := slices.Sorted(slices.Values(values))
	rank := (p*len(s) + 99) / 100
	return s[max(rank, 1)-1]
}
