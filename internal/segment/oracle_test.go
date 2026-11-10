package segment_test

import (
	"context"
	"database/sql"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/seed"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

const day = 24 * time.Hour

type data struct {
	asOf   time.Time
	users  []seed.User
	events map[[16]byte][]seed.Event
}

func index(ds seed.Dataset) data {
	d := data{asOf: ds.AsOf, users: ds.Users, events: map[[16]byte][]seed.Event{}}
	for _, e := range ds.Events {
		d.events[e.UserID] = append(d.events[e.UserID], e)
	}
	return d
}

func floorDays(d time.Duration) int {
	q := d / day
	if d < 0 && d%day != 0 {
		q--
	}
	return int(q)
}

func holds(c segment.Condition, u seed.User, evs []seed.Event, asOf time.Time) bool {
	switch c.Kind {
	case segment.KindEnum:
		v := map[string]string{"city": u.City, "language": u.Language, "loyalty_tier": u.LoyaltyTier}[c.Field]
		switch c.Op {
		case "eq":
			return v == c.Value
		case "neq":
			return v != c.Value
		case "in":
			return slices.Contains(c.Values, v)
		case "not_in":
			return !slices.Contains(c.Values, v)
		}
	case segment.KindNumber:
		days := floorDays(asOf.Sub(u.SignupAt))
		switch c.Op {
		case "eq":
			return days == c.Number
		case "neq":
			return days != c.Number
		case "gt":
			return days > c.Number
		case "gte":
			return days >= c.Number
		case "lt":
			return days < c.Number
		case "lte":
			return days <= c.Number
		}
	case segment.KindEvent:
		from := asOf.Add(-time.Duration(c.WithinDays) * day)
		found := false
		for _, e := range evs {
			if e.Type == c.Event && !e.OccurredAt.Before(from) && !e.OccurredAt.After(asOf) {
				found = true
				break
			}
		}
		return found != c.Negate
	}
	panic("oracle: unknown condition")
}

func matches(r segment.Rule, u seed.User, evs []seed.Event, asOf time.Time) bool {
	for _, c := range r.Conditions {
		h := holds(c, u, evs, asOf)
		if r.Match == "any" && h {
			return true
		}
		if r.Match == "all" && !h {
			return false
		}
	}
	return r.Match == "all"
}

type expected struct {
	counts segment.Counts
	sms    map[[16]byte]bool
	email  map[[16]byte]bool
}

func evaluate(r segment.Rule, d data) expected {
	e := expected{sms: map[[16]byte]bool{}, email: map[[16]byte]bool{}}
	for _, u := range d.users {
		if !matches(r, u, d.events[u.ID], d.asOf) {
			continue
		}
		e.counts.Total++
		if u.ConsentSMS {
			e.counts.SMSEligible++
			e.sms[u.ID] = true
		}
		if u.ConsentEmail {
			e.counts.EmailEligible++
			e.email[u.ID] = true
		}
	}
	return e
}

func recipients(t *testing.T, pool *pgxpool.Pool, r segment.Rule, channel string, asOf time.Time) map[[16]byte]bool {
	t.Helper()
	q, err := segment.AudienceQuery(r, channel, asOf)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(context.Background(), q.SQL, q.Args...)
	if err != nil {
		t.Fatalf("%s: %v", q.SQL, err)
	}
	defer rows.Close()
	got := map[[16]byte]bool{}
	for rows.Next() {
		var id [16]byte
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func seededPool(t *testing.T) (*pgxpool.Pool, data) {
	t.Helper()
	url, meta := testdb.Seeded(t)
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ds, err := seed.Generate(meta.Seed, meta.AsOf, meta.Users)
	if err != nil {
		t.Fatal(err)
	}
	if !ds.AsOf.Equal(meta.AsOf) {
		t.Fatalf("regenerated anchor %v differs from seed_meta %v", ds.AsOf, meta.AsOf)
	}
	return pool, index(ds)
}

func TestOracle(t *testing.T) {
	pool, d := seededPool(t)
	rng := rand.New(rand.NewPCG(5, 6))
	nonTrivial := 0
	for i := range 500 {
		r := segment.RandomRule(rng)
		want := evaluate(r, d)
		got, err := segment.CountAudience(context.Background(), pool, r, d.asOf)
		if err != nil {
			t.Fatal(err)
		}
		if got != want.counts {
			t.Fatalf("rule %d %s\ncounts: sql %+v, oracle %+v", i, segment.RuleJSON(r), got, want.counts)
		}
		for channel, wantIDs := range map[string]map[[16]byte]bool{"sms": want.sms, "email": want.email} {
			if gotIDs := recipients(t, pool, r, channel, d.asOf); !mapsEqual(gotIDs, wantIDs) {
				t.Fatalf("rule %d %s\n%s recipients differ: sql %d, oracle %d", i, segment.RuleJSON(r), channel, len(gotIDs), len(wantIDs))
			}
		}
		if want.counts.Total > 0 && want.counts.Total < len(d.users) {
			nonTrivial++
		}
	}
	if nonTrivial < 250 {
		t.Fatalf("only %d of 500 rules select a proper subset of users", nonTrivial)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mapsEqual(a, b map[[16]byte]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func TestExampleCountsMatchDocs(t *testing.T) {
	pool, d := seededPool(t)
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, res := segment.ValidateJSON(set, mustRead(t, "../../docs/examples/istanbul_cart_abandoners.json"))
	if !res.OK() {
		t.Fatal(res.Errors)
	}
	got, err := segment.CountAudience(context.Background(), pool, r, d.asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.users) != 5000 || got.Total != 315 || got.SMSEligible != 210 {
		t.Fatalf("users %d, counts %+v; docs/seed-data.md says 315 total, 210 SMS for 5,000 users", len(d.users), got)
	}
}

func TestConsentIsAlwaysApplied(t *testing.T) {
	pool, d := seededPool(t)
	everyone := segment.Rule{Match: "all", Conditions: []segment.Condition{
		{Kind: segment.KindNumber, Field: "days_since_signup", Op: "gte", Number: 0},
	}}
	noConsent := d.users[4].ID
	for channel, consented := range map[string]func(seed.User) bool{
		"sms":   func(u seed.User) bool { return u.ConsentSMS },
		"email": func(u seed.User) bool { return u.ConsentEmail },
	} {
		got := recipients(t, pool, everyone, channel, d.asOf)
		want := 0
		for _, u := range d.users {
			if consented(u) {
				want++
				if !got[u.ID] {
					t.Fatalf("%s: consented user missing", channel)
				}
			} else if got[u.ID] {
				t.Fatalf("%s: user without consent returned", channel)
			}
		}
		if len(got) != want || got[noConsent] {
			t.Fatalf("%s: %d recipients, want %d; no-consent user present: %v", channel, len(got), want, got[noConsent])
		}
	}
}

func TestBoundaries(t *testing.T) {
	ctx := context.Background()
	url := testdb.Fresh(t)
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	asOf := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	us := time.Microsecond
	signupOffsets := map[string]time.Duration{
		"30d-1us": 30*day - us, "30d": 30 * day, "30d+1us": 30*day + us,
		"31d-1us": 31*day - us, "31d": 31 * day, "future": -us,
	}
	eventOffsets := map[string]time.Duration{
		"at asOf": 0, "30d-1us": 30*day - us, "30d": 30 * day, "30d+1us": 30*day + us, "future": -us,
	}
	ds := seed.Dataset{AsOf: asOf, Seed: 0}
	label := map[[16]byte]string{}
	n := 0
	newUser := func(name string, signupAgo time.Duration) seed.User {
		n++
		u := seed.User{ID: [16]byte{15: byte(n)}, LastName: "Test", Email: name + "@example.com", Phone: name,
			City: "istanbul", Language: "tr", LoyaltyTier: "gold", SignupAt: asOf.Add(-signupAgo), ConsentSMS: true, ConsentEmail: true}
		label[u.ID] = name
		ds.Users = append(ds.Users, u)
		return u
	}
	for name, off := range signupOffsets {
		newUser("signup "+name, off)
	}
	for name, off := range eventOffsets {
		u := newUser("cart "+name, 400*day)
		ds.Events = append(ds.Events, seed.Event{UserID: u.ID, Type: "cart_add", OccurredAt: asOf.Add(-off)})
	}
	if _, err := seed.Load(ctx, pool, ds, false); err != nil {
		t.Fatal(err)
	}

	days := func(op string, n int) segment.Rule {
		return segment.Rule{Match: "all", Conditions: []segment.Condition{{Kind: segment.KindNumber, Field: "days_since_signup", Op: op, Number: n}}}
	}
	cart := func(negate bool) segment.Rule {
		return segment.Rule{Match: "all", Conditions: []segment.Condition{{Kind: segment.KindEvent, Event: "cart_add", WithinDays: 30, Negate: negate}}}
	}
	cases := []struct {
		group string
		rule  segment.Rule
		want  []string
	}{
		{"signup ", days("eq", 30), []string{"signup 30d", "signup 30d+1us", "signup 31d-1us"}},
		{"signup ", days("gte", 31), []string{"signup 31d"}},
		{"signup ", days("lt", 30), []string{"signup 30d-1us", "signup future"}},
		{"signup ", days("lt", 0), []string{"signup future"}},
		{"cart ", cart(false), []string{"cart at asOf", "cart 30d-1us", "cart 30d"}},
	}
	d := index(ds)
	for _, c := range cases {
		got := recipients(t, pool, c.rule, "sms", asOf)
		var names []string
		for id := range got {
			if strings.HasPrefix(label[id], c.group) {
				names = append(names, label[id])
			}
		}
		slices.Sort(names)
		want := slices.Sorted(slices.Values(c.want))
		if !slices.Equal(names, want) {
			t.Errorf("%s: sql %v, want %v", segment.RuleJSON(c.rule), names, want)
		}
		if oracle := evaluate(c.rule, d).sms; !mapsEqual(got, oracle) {
			t.Errorf("%s: sql and oracle disagree", segment.RuleJSON(c.rule))
		}
	}
	for _, op := range []string{"eq", "neq", "gt", "gte", "lt", "lte"} {
		for _, n := range []int{0, 29, 30, 31} {
			if r := days(op, n); !mapsEqual(recipients(t, pool, r, "sms", asOf), evaluate(r, d).sms) {
				t.Errorf("%s: sql and oracle disagree", segment.RuleJSON(r))
			}
		}
	}
	if r := cart(true); !mapsEqual(recipients(t, pool, r, "sms", asOf), evaluate(r, d).sms) {
		t.Errorf("%s: sql and oracle disagree", segment.RuleJSON(r))
	}
}
