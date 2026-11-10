package segment

import (
	"encoding/json"
	"math/rand/v2"
)

func RandomRule(rng *rand.Rand) Rule {
	r := Rule{Match: []string{"all", "any"}[rng.IntN(2)]}
	for range 1 + rng.IntN(4) {
		r.Conditions = append(r.Conditions, randomCondition(rng))
	}
	return r
}

var (
	boundaryDays    = []int{0, 1, 29, 30, 31, 89, 90, 91, 180, 364, 365, 366, 729, 730}
	boundaryWindows = []int{1, 7, 29, 30, 31, 60, 89, 90, 120, 365}
)

func randomCondition(rng *rand.Rand) Condition {
	pick := func(list []int, max int) int {
		if rng.IntN(2) == 0 {
			return list[rng.IntN(len(list))]
		}
		return rng.IntN(max + 1)
	}
	switch rng.IntN(3) {
	case 0:
		field := enumFields[rng.IntN(len(enumFields))]
		vals := enumValues[field]
		if rng.IntN(2) == 0 {
			return Condition{Kind: KindEnum, Field: field, Op: singleOps[rng.IntN(len(singleOps))], Value: vals[rng.IntN(len(vals))]}
		}
		var list []string
		for _, i := range rng.Perm(len(vals))[:1+rng.IntN(len(vals))] {
			list = append(list, vals[i])
		}
		return Condition{Kind: KindEnum, Field: field, Op: listOps[rng.IntN(len(listOps))], Values: list}
	case 1:
		return Condition{Kind: KindNumber, Field: "days_since_signup", Op: numberOps[rng.IntN(len(numberOps))], Number: pick(boundaryDays, 800)}
	default:
		w := pick(boundaryWindows, WithinDaysMax-1)
		if w < WithinDaysMin {
			w = WithinDaysMin
		}
		return Condition{Kind: KindEvent, Event: eventTypes[rng.IntN(len(eventTypes))], WithinDays: w, Negate: rng.IntN(2) == 0}
	}
}

func RuleJSON(r Rule) []byte {
	conds := make([]map[string]any, len(r.Conditions))
	for i, c := range r.Conditions {
		switch c.Kind {
		case KindEnum:
			var v any = c.Value
			if c.Values != nil {
				v = c.Values
			}
			conds[i] = map[string]any{"kind": c.Kind, "field": c.Field, "op": c.Op, "value": v}
		case KindNumber:
			conds[i] = map[string]any{"kind": c.Kind, "field": c.Field, "op": c.Op, "value": c.Number}
		case KindEvent:
			conds[i] = map[string]any{"kind": c.Kind, "event": c.Event, "within_days": c.WithinDays, "negate": c.Negate}
		}
	}
	b, err := json.Marshal(map[string]any{"version": 1, "match": r.Match, "conditions": conds})
	if err != nil {
		panic(err)
	}
	return b
}
