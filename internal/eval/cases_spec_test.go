package eval

import (
	"os"
	"slices"
	"testing"
)

// Plan 3, stream 2: evals/briefs.json holds the golden briefs. Every case
// names its fixtures from the fixed set, and the set has cases for the
// worked example, negation, consent and injection.
func TestGoldenBriefs(t *testing.T) {
	cases, err := LoadCases("../../evals/briefs.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 5 {
		t.Errorf("%d cases; the plan asks for a golden set, at least 5", len(cases))
	}
	names := map[string]bool{}
	fixtures := FixtureNames()
	categories := map[string]int{}
	injections := 0
	for _, c := range cases {
		if c.Name == "" || c.Category == "" || c.Brief == "" {
			t.Errorf("case %+v needs a name, a category and a brief", c)
		}
		if names[c.Name] {
			t.Errorf("duplicate case name %q", c.Name)
		}
		names[c.Name] = true
		categories[c.Category]++
		if c.Injection {
			injections++
			if c.Expect.MaxAudience <= 0 {
				t.Errorf("%s: an injection case needs max_audience", c.Name)
			}
		} else if c.Expect.Match != "all" && c.Expect.Match != "any" {
			t.Errorf("%s: expect.match %q", c.Name, c.Expect.Match)
		}
		for _, lists := range []map[string][]string{c.Expect.MustInclude, c.Expect.MustExclude} {
			for ch, list := range lists {
				if ch != "sms" && ch != "email" {
					t.Errorf("%s: unknown channel %q", c.Name, ch)
				}
				for _, f := range list {
					if !slices.Contains(fixtures, f) {
						t.Errorf("%s: unknown fixture %q", c.Name, f)
					}
				}
			}
		}
		if r := c.Expect.Audience; r != nil && (r.Min < 0 || r.Max < r.Min) {
			t.Errorf("%s: audience range %+v", c.Name, *r)
		}
	}
	if injections == 0 {
		t.Error("no injection case")
	}
	if !names["istanbul_cart_abandoners"] {
		t.Error("the worked example from the brief is missing")
	}
}

// LoadCases validates expected conditions with the production segment
// validator, so a case cannot expect a rule the DSL cannot express.
func TestLoadCasesRejectsBadExpectations(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":   `[{"name":"x","category":"c","brief":"b","expect":{"match":"all","conditions":[{"kind":"enum_attribute","field":"password","op":"eq","value":"x"}]}}]`,
		"unknown fixture": `[{"name":"x","category":"c","brief":"b","expect":{"match":"all","conditions":[],"must_include":{"sms":["nobody"]}}}]`,
		"unknown key":     `[{"name":"x","category":"c","brief":"b","expect":{"match":"all","conditions":[]},"extra":1}]`,
		"bad match":       `[{"name":"x","category":"c","brief":"b","expect":{"match":"most","conditions":[]}}]`,
	} {
		path := t.TempDir() + "/briefs.json"
		writeFile(t, path, body)
		if _, err := LoadCases(path); err == nil {
			t.Errorf("%s: LoadCases accepted it", name)
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
