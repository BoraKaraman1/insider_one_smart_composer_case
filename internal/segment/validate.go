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

const (
	KindEnum   = "enum_attribute"
	KindNumber = "number_attribute"
	KindEvent  = "event"
)

type Rule struct {
	Match      string
	Conditions []Condition
}

type Condition struct {
	Kind string

	Field  string
	Op     string
	Value  string
	Values []string
	Number int

	Event      string
	WithinDays int
	Negate     bool
}

func ValidateJSON(set *schema.Set, data []byte) (Rule, validation.Result) {
	doc, err := schema.Decode(bytes.NewReader(data))
	if err != nil {
		var res validation.Result
		res.Error("", validation.InvalidJSON, err.Error())
		return Rule{}, res
	}
	return Validate(set, doc)
}

func Validate(set *schema.Set, doc any) (Rule, validation.Result) {
	var res validation.Result
	normalize(doc)
	err := set.Validate(schema.SegmentRule, doc)
	if err == nil {
		return toRule(doc), res
	}

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

func keyProblem(m map[string]any, path, key, allowed string) []validation.Issue {
	if _, present := m[key]; !present {
		return []validation.Issue{{Path: path + "/" + key, Code: validation.Required, Message: fmt.Sprintf("missing required key %q", key)}}
	}
	got, _ := json.Marshal(m[key])
	return []validation.Issue{{Path: path + "/" + key, Code: validation.UnknownValue,
		Message: fmt.Sprintf("%s is not allowed; use one of: %s", got, allowed)}}
}

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

func conditionIndex(path string) (int, bool) {
	rest, ok := strings.CutPrefix(path, "/conditions/")
	if !ok {
		return 0, false
	}
	num, _, _ := strings.Cut(rest, "/")
	i, err := strconv.Atoi(num)
	return i, err == nil
}

// Yalnızca yalın harfler küçülür ve noktalı büyük i yine elenir.
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

func integer(v any) int {
	f, _ := v.(json.Number).Float64()
	return int(f)
}
