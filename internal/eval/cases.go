// Package eval measures how often generation gives valid and correct output
// (plan 3, stream 2). It runs campaign.Generator.Run over golden briefs on a
// cloned, seeded database with named fixture recipients, scores each result
// and aggregates the llm_calls rows. It never touches the app database.
package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
)

// Case is one golden brief and what a correct answer looks like.
type Case struct {
	Name      string
	Category  string
	Brief     string
	Injection bool
	Expect    Expect
}

// Expect is what Score checks (plan 3, 2.3).
type Expect struct {
	Match      string // "all" or "any"; the rule's top-level match
	Conditions []segment.Condition
	// Fixture names per channel ("sms", "email").
	MustInclude, MustExclude map[string][]string
	Audience                 *Range // total audience on the fixed seed, if given
	MaxAudience              int    // injection cases: the widest safe total
	Language                 string // "tr" or "en", if given
	RequireVariable          string // a variable both bodies must use, if given
}

// Range is an inclusive range.
type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type fileCase struct {
	Name      string     `json:"name"`
	Category  string     `json:"category"`
	Brief     string     `json:"brief"`
	Injection bool       `json:"injection"`
	Expect    fileExpect `json:"expect"`
}

type fileExpect struct {
	Match           string              `json:"match"`
	Conditions      []json.RawMessage   `json:"conditions"`
	MustInclude     map[string][]string `json:"must_include"`
	MustExclude     map[string][]string `json:"must_exclude"`
	Audience        *Range              `json:"audience"`
	MaxAudience     int                 `json:"max_audience"`
	Language        string              `json:"language"`
	RequireVariable string              `json:"require_variable"`
}

// LoadCases reads a briefs file: a JSON array of cases. Unknown keys,
// unknown fixtures and channels, and expected conditions the production
// segment validator rejects are errors.
func LoadCases(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw []fileCase
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("eval: %s: %w", path, err)
	}
	set, err := schemas()
	if err != nil {
		return nil, err
	}
	cases := make([]Case, 0, len(raw))
	for i, fc := range raw {
		c, err := toCase(set, fc)
		if err != nil {
			return nil, fmt.Errorf("eval: %s: case %d (%s): %w", path, i, fc.Name, err)
		}
		cases = append(cases, c)
	}
	return cases, nil
}

func toCase(set *schema.Set, fc fileCase) (Case, error) {
	e := fc.Expect
	if fc.Name == "" || fc.Category == "" || fc.Brief == "" {
		return Case{}, fmt.Errorf("name, category and brief are required")
	}
	switch {
	case e.Match == "all" || e.Match == "any":
	case e.Match == "" && fc.Injection:
	default:
		return Case{}, fmt.Errorf("expect.match %q: want all or any", e.Match)
	}
	if fc.Injection && e.MaxAudience <= 0 {
		return Case{}, fmt.Errorf("an injection case needs expect.max_audience")
	}
	if r := e.Audience; r != nil && (r.Min < 0 || r.Max < r.Min) {
		return Case{}, fmt.Errorf("expect.audience %+v", *r)
	}
	if e.Language != "" && e.Language != "tr" && e.Language != "en" {
		return Case{}, fmt.Errorf("expect.language %q: want tr or en", e.Language)
	}
	if v := e.RequireVariable; v != "" && !slices.Contains(content.Variables(), v) {
		return Case{}, fmt.Errorf("expect.require_variable %q is not an allowlisted variable", v)
	}
	names := FixtureNames()
	for _, lists := range []map[string][]string{e.MustInclude, e.MustExclude} {
		for ch, list := range lists {
			if ch != "sms" && ch != "email" {
				return Case{}, fmt.Errorf("unknown channel %q", ch)
			}
			for _, f := range list {
				if !slices.Contains(names, f) {
					return Case{}, fmt.Errorf("unknown fixture %q", f)
				}
			}
		}
	}
	var conds []segment.Condition
	if len(e.Conditions) > 0 {
		match := e.Match
		if match == "" {
			match = "all"
		}
		doc, err := json.Marshal(map[string]any{"version": 1, "match": match, "conditions": e.Conditions})
		if err != nil {
			return Case{}, err
		}
		rule, res := segment.ValidateJSON(set, doc)
		if !res.OK() {
			return Case{}, fmt.Errorf("expected conditions are not a valid rule: %v", res.Errors)
		}
		conds = rule.Conditions
	}
	return Case{
		Name: fc.Name, Category: fc.Category, Brief: fc.Brief, Injection: fc.Injection,
		Expect: Expect{
			Match: e.Match, Conditions: conds, MustInclude: e.MustInclude, MustExclude: e.MustExclude,
			Audience: e.Audience, MaxAudience: e.MaxAudience, Language: e.Language, RequireVariable: e.RequireVariable,
		},
	}, nil
}
