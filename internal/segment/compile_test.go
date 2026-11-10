package segment

import (
	"math/rand/v2"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var asOf = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func mustCompile(t *testing.T, r Rule) fragment {
	t.Helper()
	f, err := compile(r, asOf)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCompileExample(t *testing.T) {
	r := Rule{Match: "all", Conditions: []Condition{
		{Kind: KindEnum, Field: "city", Op: "eq", Value: "istanbul"},
		{Kind: KindEvent, Event: "cart_add", WithinDays: 30},
		{Kind: KindEvent, Event: "purchase", WithinDays: 30, Negate: true},
	}}
	f := mustCompile(t, r)
	wantSQL := "(u.city = $1" +
		" AND EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = $2 AND e.occurred_at >= $3 AND e.occurred_at <= $4)" +
		" AND NOT EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = $5 AND e.occurred_at >= $6 AND e.occurred_at <= $7))"
	from := asOf.Add(-30 * day)
	wantArgs := []any{"istanbul", "cart_add", from, asOf, "purchase", from, asOf}
	if f.sql != wantSQL {
		t.Errorf("sql\n got %s\nwant %s", f.sql, wantSQL)
	}
	if !reflect.DeepEqual(f.args, wantArgs) {
		t.Errorf("args\n got %v\nwant %v", f.args, wantArgs)
	}
}

func TestCompileEachOperator(t *testing.T) {
	d := func(n int) time.Time { return asOf.Add(-time.Duration(n) * day) }
	cases := []struct {
		c    Condition
		sql  string
		args []any
	}{
		{Condition{Kind: KindEnum, Field: "city", Op: "eq", Value: "izmir"}, "u.city = $1", []any{"izmir"}},
		{Condition{Kind: KindEnum, Field: "language", Op: "neq", Value: "en"}, "u.language <> $1", []any{"en"}},
		{Condition{Kind: KindEnum, Field: "loyalty_tier", Op: "in", Values: []string{"gold", "silver"}}, "u.loyalty_tier = ANY($1::text[])", []any{[]string{"gold", "silver"}}},
		{Condition{Kind: KindEnum, Field: "city", Op: "not_in", Values: []string{"konya"}}, "u.city <> ALL($1::text[])", []any{[]string{"konya"}}},
		{Condition{Kind: KindNumber, Field: "days_since_signup", Op: "eq", Number: 30}, "(u.signup_at > $1 AND u.signup_at <= $2)", []any{d(31), d(30)}},
		{Condition{Kind: KindNumber, Field: "days_since_signup", Op: "neq", Number: 30}, "NOT (u.signup_at > $1 AND u.signup_at <= $2)", []any{d(31), d(30)}},
		{Condition{Kind: KindNumber, Field: "days_since_signup", Op: "gte", Number: 30}, "u.signup_at <= $1", []any{d(30)}},
		{Condition{Kind: KindNumber, Field: "days_since_signup", Op: "gt", Number: 30}, "u.signup_at <= $1", []any{d(31)}},
		{Condition{Kind: KindNumber, Field: "days_since_signup", Op: "lte", Number: 30}, "u.signup_at > $1", []any{d(31)}},
		{Condition{Kind: KindNumber, Field: "days_since_signup", Op: "lt", Number: 30}, "u.signup_at > $1", []any{d(30)}},
		{Condition{Kind: KindEvent, Event: "page_view", WithinDays: 7}, "EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = $1 AND e.occurred_at >= $2 AND e.occurred_at <= $3)", []any{"page_view", d(7), asOf}},
	}
	for _, c := range cases {
		f := mustCompile(t, Rule{Match: "all", Conditions: []Condition{c.c}})
		if f.sql != "("+c.sql+")" || !reflect.DeepEqual(f.args, c.args) {
			t.Errorf("%+v\n got %s %v\nwant (%s) %v", c.c, f.sql, f.args, c.sql, c.args)
		}
	}
}

func TestCompileMatchAnyAndPrecision(t *testing.T) {
	r := Rule{Match: "any", Conditions: []Condition{
		{Kind: KindEnum, Field: "city", Op: "eq", Value: "izmir"},
		{Kind: KindEnum, Field: "city", Op: "eq", Value: "konya"},
	}}
	if f := mustCompile(t, r); f.sql != "(u.city = $1 OR u.city = $2)" {
		t.Errorf("got %s", f.sql)
	}
	ev := Rule{Match: "all", Conditions: []Condition{{Kind: KindEvent, Event: "cart_add", WithinDays: 1}}}
	f, err := compile(ev, asOf.Add(1234567*time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.args[2].(time.Time); got.Nanosecond()%1000 != 0 {
		t.Errorf("asOf not truncated to microseconds: %v", got)
	}
}

func TestCompileRejectsStructureOutsideCatalog(t *testing.T) {
	bad := []Rule{
		{Match: "some", Conditions: []Condition{{Kind: KindEvent, Event: "cart_add", WithinDays: 1}}},
		{Match: "all"},
		{Match: "all", Conditions: []Condition{{Kind: "sql"}}},
		{Match: "all", Conditions: []Condition{{Kind: KindEnum, Field: "email", Op: "eq", Value: "x"}}},
		{Match: "all", Conditions: []Condition{{Kind: KindEnum, Field: "city", Op: "like", Value: "x"}}},
		{Match: "all", Conditions: []Condition{{Kind: KindNumber, Field: "age", Op: "eq"}}},
		{Match: "all", Conditions: []Condition{{Kind: KindNumber, Field: "days_since_signup", Op: "in"}}},
		{Match: "all", Conditions: []Condition{{Kind: KindEvent, Event: "signup", WithinDays: 1}}},
	}
	for _, r := range bad {
		if _, err := compile(r, asOf); err == nil {
			t.Errorf("accepted %+v", r)
		}
	}
}

func TestAudienceQueryAlwaysFiltersConsent(t *testing.T) {
	r := Rule{Match: "any", Conditions: []Condition{{Kind: KindEnum, Field: "city", Op: "eq", Value: "izmir"}}}
	for channel, col := range map[string]string{"sms": "u.consent_sms", "email": "u.consent_email"} {
		q, err := AudienceQuery(r, channel, asOf)
		if err != nil {
			t.Fatal(err)
		}
		if want := "SELECT u.id FROM users u WHERE (u.city = $1) AND " + col; q.SQL != want {
			t.Errorf("%s:\n got %s\nwant %s", channel, q.SQL, want)
		}
	}
	if _, err := AudienceQuery(r, "push", asOf); err == nil {
		t.Error("unknown channel accepted")
	}
}

func TestRandomRulesAreValid(t *testing.T) {
	set := mustSet(t)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 1000 {
		r := RandomRule(rng)
		got, res := ValidateJSON(set, RuleJSON(r))
		if !res.OK() {
			t.Fatalf("rule %d rejected: %s\n%+v", i, RuleJSON(r), res.Errors)
		}
		if !reflect.DeepEqual(got, r) {
			t.Fatalf("rule %d round trip:\n got %+v\nwant %+v", i, got, r)
		}
	}
}

func withNewValues(rng *rand.Rand, r Rule) Rule {
	out := Rule{Match: r.Match}
	for _, c := range r.Conditions {
		n := c
		switch c.Kind {
		case KindEnum:
			fresh := randomCondition(rng)
			for fresh.Kind != KindEnum || fresh.Field != c.Field || (fresh.Values == nil) != (c.Values == nil) {
				fresh = randomCondition(rng)
			}
			n.Value, n.Values = fresh.Value, fresh.Values
		case KindNumber:
			n.Number = rng.IntN(DaysSinceSignupMax + 1)
		case KindEvent:
			n.WithinDays = WithinDaysMin + rng.IntN(WithinDaysMax)
		}
		out.Conditions = append(out.Conditions, n)
	}
	return out
}

func mustAppear(c Condition) []any {
	switch c.Kind {
	case KindEnum:
		if c.Values != nil {
			return []any{c.Values}
		}
		return []any{c.Value}
	case KindNumber:
		return nil
	default:
		return []any{c.Event, asOf.Add(-time.Duration(c.WithinDays) * day)}
	}
}

var placeholder = regexp.MustCompile(`\$(\d+)`)

func checkArgs(t *testing.T, f fragment, r Rule) {
	t.Helper()
	seen := map[int]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(f.sql, -1) {
		n, _ := strconv.Atoi(m[1])
		seen[n] = true
	}
	if len(seen) != len(f.args) {
		t.Fatalf("%d placeholders, %d args: %s", len(seen), len(f.args), f.sql)
	}
	for i := 1; i <= len(f.args); i++ {
		if !seen[i] {
			t.Fatalf("$%d missing in %s", i, f.sql)
		}
	}
	for _, c := range r.Conditions {
		for _, v := range mustAppear(c) {
			if !containsArg(f.args, v) {
				t.Fatalf("value %v of %+v not bound in %v", v, c, f.args)
			}
		}
		if c.Kind == KindNumber && !containsArg(f.args, asOf.Add(-time.Duration(c.Number)*day)) &&
			!containsArg(f.args, asOf.Add(-time.Duration(c.Number+1)*day)) {
			t.Fatalf("no cutoff for %+v in %v", c, f.args)
		}
	}
}

func containsArg(args []any, v any) bool {
	for _, a := range args {
		if reflect.DeepEqual(a, v) {
			return true
		}
	}
	return false
}

func TestShapeProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 2000 {
		a := RandomRule(rng)
		b := withNewValues(rng, a)
		fa, fb := mustCompile(t, a), mustCompile(t, b)
		if fa.sql != fb.sql {
			t.Fatalf("same shape, different SQL:\n%s\n%s", fa.sql, fb.sql)
		}
		checkArgs(t, fa, a)
		checkArgs(t, fb, b)
	}
}

func FuzzCompileShape(f *testing.F) {
	for _, s := range []string{"istanbul", "x'; DROP TABLE users;--", "$1", "a", "in", "", "\x00", "' OR '1'='1"} {
		f.Add(s, "ankara")
	}
	shape := func(a, b string) Rule {
		return Rule{Match: "any", Conditions: []Condition{
			{Kind: KindEnum, Field: "city", Op: "eq", Value: a},
			{Kind: KindEnum, Field: "language", Op: "in", Values: []string{a, b}},
			{Kind: KindEnum, Field: "loyalty_tier", Op: "not_in", Values: []string{b}},
		}}
	}
	ref, err := compile(shape("istanbul", "ankara"), asOf)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		got, err := compile(shape(a, b), asOf)
		if err != nil {
			t.Fatal(err)
		}
		if got.sql != ref.sql {
			t.Fatalf("values changed the SQL:\n%s", got.sql)
		}
		if !reflect.DeepEqual(got.args, []any{a, []string{a, b}, []string{b}}) {
			t.Fatalf("args %#v", got.args)
		}
		if strings.Count(got.sql, "$") != 3 {
			t.Fatalf("placeholder count changed: %s", got.sql)
		}
	})
}
