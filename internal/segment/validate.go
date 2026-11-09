package segment

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

// Condition kinds.
const (
	KindEnum   = "enum_attribute"
	KindNumber = "number_attribute"
	KindEvent  = "event"
)

// Rule is a validated segment rule.
type Rule struct {
	Match      string // "all" or "any"
	Conditions []Condition
}

// Condition is one validated condition. Which fields are set depends on Kind.
type Condition struct {
	Kind string

	// Attribute conditions (KindEnum, KindNumber).
	Field  string
	Op     string
	Value  string   // KindEnum with eq or neq
	Values []string // KindEnum with in or not_in
	Number int      // KindNumber

	// Event conditions (KindEvent).
	Event      string
	WithinDays int
	Negate     bool
}

// ValidateJSON parses and validates a rule given as raw JSON.
func ValidateJSON(set *schema.Set, data []byte) (Rule, validation.Result) {
	doc, err := schema.Decode(bytes.NewReader(data))
	if err != nil {
		var res validation.Result
		res.Error("", validation.InvalidJSON, err.Error())
		return Rule{}, res
	}
	return Validate(set, doc)
}

// Validate checks a decoded rule (from schema.Decode) and reports every
// problem with a precise path. It first lowercases ASCII letters in enum
// strings (docs/segment-rules.md, Normalization), changing doc in place.
func Validate(set *schema.Set, doc any) (Rule, validation.Result) {
	var res validation.Result
	normalize(doc)
	err := set.Validate(schema.SegmentRule, doc)
	if err == nil {
		return toRule(doc), res
	}

	// The condition union reports a failure for each of its branches, which
	// is noise. Keep errors outside the conditions as they are, and explain
	// each failing condition against the one branch it was meant to be.
	failing := map[int]bool{}
	for _, is := range schema.Issues(err, "") {
		if i, ok := conditionIndex(is.Path); ok {
			failing[i] = true
			continue
		}
		res.Errors = append(res.Errors, is)
	}
	m, _ := doc.(map[string]any)
	conds, _ := m["conditions"].([]any)
	for i := range failing {
		path := fmt.Sprintf("/conditions/%d", i)
		issues := explain(set, conds[i], path)
		if len(issues) == 0 {
			// Never report "invalid" without saying why.
			issues = []validation.Issue{{Path: path, Code: validation.SchemaViolation, Message: "condition matches no allowed form"}}
		}
		res.Errors = append(res.Errors, issues...)
	}
	if len(res.Errors) == 0 {
		res.Error("", validation.SchemaViolation, err.Error())
	}
	slices.SortStableFunc(res.Errors, func(a, b validation.Issue) int { return cmp.Compare(a.Path, b.Path) })
	return Rule{}, res
}

// explain validates one condition against the schema branch its kind, field
// and operator point to.
func explain(set *schema.Set, c any, path string) []validation.Issue {
	m, ok := c.(map[string]any)
	if !ok {
		return []validation.Issue{{Path: path, Code: validation.InvalidType, Message: "expected object"}}
	}
	kind, _ := m["kind"].(string)
	field, _ := m["field"].(string)
	op, _ := m["op"].(string)

	switch kind {
	case KindEnum:
		if _, known := enumValues[field]; !known {
			return keyProblem(m, path, "field", strings.Join(enumFields, ", "))
		}
		switch {
		case slices.Contains(singleOps, op):
			return branch(set, m, path, field+"_single", false)
		case slices.Contains(listOps, op):
			return branch(set, m, path, field+"_list", true)
		case slices.Contains(numberOps, op):
			return []validation.Issue{{Path: path + "/op", Code: validation.OpNotAllowed,
				Message: fmt.Sprintf("operator %q is not allowed for %s; use one of: %s", op, field, strings.Join(append(SingleOps(), listOps...), ", "))}}
		default:
			return keyProblem(m, path, "op", strings.Join(append(SingleOps(), listOps...), ", "))
		}
	case KindNumber:
		if field != "days_since_signup" {
			return keyProblem(m, path, "field", "days_since_signup")
		}
		switch {
		case slices.Contains(numberOps, op):
			return branch(set, m, path, "days_since_signup", false)
		case slices.Contains(listOps, op):
			return []validation.Issue{{Path: path + "/op", Code: validation.OpNotAllowed,
				Message: fmt.Sprintf("operator %q is not allowed for days_since_signup; use one of: %s", op, strings.Join(numberOps, ", "))}}
		default:
			return keyProblem(m, path, "op", strings.Join(numberOps, ", "))
		}
	case KindEvent:
		return branch(set, m, path, "event", false)
	default:
		return keyProblem(m, path, "kind", strings.Join([]string{KindEnum, KindNumber, KindEvent}, ", "))
	}
}

// keyProblem reports a missing key, or a value that is not one of allowed.
func keyProblem(m map[string]any, path, key, allowed string) []validation.Issue {
	if _, present := m[key]; !present {
		return []validation.Issue{{Path: path + "/" + key, Code: validation.Required, Message: fmt.Sprintf("missing required key %q", key)}}
	}
	got, _ := json.Marshal(m[key])
	return []validation.Issue{{Path: path + "/" + key, Code: validation.UnknownValue,
		Message: fmt.Sprintf("%s is not allowed; use one of: %s", got, allowed)}}
}

// branch validates against one $defs entry. A single value where a list is
// needed, or the reverse, becomes op_value_mismatch. The library reports that
// as a type error or an enum error depending on the branch, so the rewrite
// looks at the shape of the value, not the error code.
func branch(set *schema.Set, m map[string]any, path, def string, wantList bool) []validation.Issue {
	err := set.ValidateDef(schema.SegmentRule, def, m)
	if err == nil {
		return nil
	}
	issues := schema.Issues(err, path)
	value, present := m["value"]
	_, isList := value.([]any)
	for i, is := range issues {
		if is.Path == path+"/value" && present && isList != wantList {
			want := "a single value"
			if wantList {
				want = "a list of values"
			}
			issues[i] = validation.Issue{Path: is.Path, Code: validation.OpValueMismatch,
				Message: fmt.Sprintf("operator %q needs %s", m["op"], want)}
		}
	}
	return issues
}

// conditionIndex returns i for paths at or under /conditions/i.
func conditionIndex(path string) (int, bool) {
	rest, ok := strings.CutPrefix(path, "/conditions/")
	if !ok {
		return 0, false
	}
	num, _, _ := strings.Cut(rest, "/")
	i, err := strconv.Atoi(num)
	return i, err == nil
}

// normalize lowercases ASCII letters in the enum strings of a rule. Other
// characters are left alone, so "İSTANBUL" still fails, loudly.
func normalize(doc any) {
	m, ok := doc.(map[string]any)
	if !ok {
		return
	}
	lowerKey(m, "match")
	conds, _ := m["conditions"].([]any)
	for _, c := range conds {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		for _, k := range []string{"kind", "field", "op", "event"} {
			lowerKey(cm, k)
		}
		if cm["kind"] != KindEnum {
			continue
		}
		switch v := cm["value"].(type) {
		case string:
			cm["value"] = asciiLower(v)
		case []any:
			for i, item := range v {
				if s, ok := item.(string); ok {
					v[i] = asciiLower(s)
				}
			}
		}
	}
}

func lowerKey(m map[string]any, key string) {
	if s, ok := m[key].(string); ok {
		m[key] = asciiLower(s)
	}
}

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// toRule converts a document that passed the schema. The type assertions are
// safe because the schema fixes every shape.
func toRule(doc any) Rule {
	m := doc.(map[string]any)
	r := Rule{Match: m["match"].(string)}
	for _, c := range m["conditions"].([]any) {
		cm := c.(map[string]any)
		cond := Condition{Kind: cm["kind"].(string)}
		switch cond.Kind {
		case KindEnum:
			cond.Field, cond.Op = cm["field"].(string), cm["op"].(string)
			if list, ok := cm["value"].([]any); ok {
				for _, item := range list {
					cond.Values = append(cond.Values, item.(string))
				}
			} else {
				cond.Value = cm["value"].(string)
			}
		case KindNumber:
			cond.Field, cond.Op = cm["field"].(string), cm["op"].(string)
			cond.Number = integer(cm["value"])
		case KindEvent:
			cond.Event = cm["event"].(string)
			cond.WithinDays = integer(cm["within_days"])
			cond.Negate = cm["negate"].(bool)
		}
		r.Conditions = append(r.Conditions, cond)
	}
	return r
}

// integer reads a json.Number the schema has checked is an integer in range.
// JSON Schema counts 30.0 as an integer, so it goes through float64, which
// is exact for these small ranges.
func integer(v any) int {
	f, _ := v.(json.Number).Float64()
	return int(f)
}
