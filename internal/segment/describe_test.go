package segment

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestDescribe(t *testing.T) {
	example := Rule{Match: "all", Conditions: []Condition{
		{Kind: KindEnum, Field: "city", Op: "eq", Value: "istanbul"},
		{Kind: KindEvent, Event: "cart_add", WithinDays: 30},
		{Kind: KindEvent, Event: "purchase", WithinDays: 30, Negate: true},
	}}
	want := "Users who match all of: city is istanbul; and have added to cart (cart_add) in the last 30 days; " +
		"and have not made a purchase (purchase) in the last 30 days. Only users who consented to a channel receive it."
	if got := Describe(example); got != want {
		t.Errorf("Describe:\n got %q\nwant %q", got, want)
	}
	cases := []struct {
		r    Rule
		want string
	}{
		{Rule{Match: "any", Conditions: []Condition{
			{Kind: KindEnum, Field: "loyalty_tier", Op: "in", Values: []string{"silver", "gold"}},
			{Kind: KindNumber, Field: "days_since_signup", Op: "lte", Number: 90},
		}}, "Users who match any of: loyalty tier is one of silver, gold; or days since signup is at most 90."},
		{Rule{Match: "all", Conditions: []Condition{{Kind: KindEnum, Field: "language", Op: "neq", Value: "en"}}},
			"Users whose language is not en."},
	}
	for _, c := range cases {
		if got := Describe(c.r); !strings.HasPrefix(got, c.want) {
			t.Errorf("Describe = %q, want prefix %q", got, c.want)
		}
	}
}

// Every value of a rule shows up in its description, so the reviewer sees
// the whole rule, and the same rule always gives the same text.
func TestDescribeCoversRandomRules(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 300 {
		r := RandomRule(rng)
		d := Describe(r)
		if d != Describe(r) {
			t.Fatal("Describe is not deterministic")
		}
		for _, c := range r.Conditions {
			for _, v := range append([]string{c.Value, c.Event}, c.Values...) {
				if !strings.Contains(d, v) {
					t.Errorf("%q lacks %q", d, v)
				}
			}
		}
	}
}
