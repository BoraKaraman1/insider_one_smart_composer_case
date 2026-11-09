package seed

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
)

var fixedAsOf = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func mustGenerate(t *testing.T, seed int64, asOf time.Time, n int) Dataset {
	t.Helper()
	ds, err := Generate(seed, asOf, n)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestDeterministic(t *testing.T) {
	a := mustGenerate(t, 42, fixedAsOf, 5000)
	b := mustGenerate(t, 42, fixedAsOf, 5000)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed, different data")
	}
	c := mustGenerate(t, 43, fixedAsOf, 5000)
	if reflect.DeepEqual(a.Users, c.Users) {
		t.Fatal("different seeds, same users")
	}
}

// TestAnchorIndependent: the same seed at another anchor gives the same data
// shifted in time, so counts measured from asOf never drift.
func TestAnchorIndependent(t *testing.T) {
	a := mustGenerate(t, 42, fixedAsOf, 2000)
	later := fixedAsOf.Add(97*24*time.Hour + 13*time.Hour + 1234*time.Microsecond)
	b := mustGenerate(t, 42, later, 2000)
	if len(a.Events) != len(b.Events) {
		t.Fatalf("event counts differ: %d vs %d", len(a.Events), len(b.Events))
	}
	for i := range a.Users {
		if a.AsOf.Sub(a.Users[i].SignupAt) != b.AsOf.Sub(b.Users[i].SignupAt) {
			t.Fatalf("user %d signup offset differs", i)
		}
	}
	for i := range a.Events {
		if a.AsOf.Sub(a.Events[i].OccurredAt) != b.AsOf.Sub(b.Events[i].OccurredAt) || a.Events[i].Type != b.Events[i].Type {
			t.Fatalf("event %d differs", i)
		}
	}
	if ta, sa := exampleCounts(a); true {
		if tb, sb := exampleCounts(b); ta != tb || sa != sb {
			t.Fatalf("example counts drift with the anchor: %d/%d vs %d/%d", ta, sa, tb, sb)
		}
	}
}

func TestShape(t *testing.T) {
	ds := mustGenerate(t, 42, time.Date(2026, 3, 4, 5, 6, 7, 891234567, time.UTC), 5000)
	if ds.AsOf.Nanosecond()%1000 != 0 {
		t.Fatalf("asOf %v not truncated to microseconds", ds.AsOf)
	}
	ids, emails, phones := map[[16]byte]bool{}, map[string]bool{}, map[string]bool{}
	for _, u := range ds.Users {
		if ids[u.ID] || emails[u.Email] || phones[u.Phone] {
			t.Fatalf("duplicate id, email or phone: %+v", u)
		}
		ids[u.ID], emails[u.Email], phones[u.Phone] = true, true, true
		if u.ID[6]>>4 != 4 || u.ID[8]>>6 != 2 {
			t.Fatalf("not a version 4 UUID: %x", u.ID)
		}
		if !slices.Contains(segment.EnumValues("city"), u.City) ||
			!slices.Contains(segment.EnumValues("language"), u.Language) ||
			!slices.Contains(segment.EnumValues("loyalty_tier"), u.LoyaltyTier) {
			t.Fatalf("value outside the catalog: %+v", u)
		}
		if u.SignupAt.After(ds.AsOf) || ds.AsOf.Sub(u.SignupAt) >= 730*24*time.Hour {
			t.Fatalf("signup outside the 730-day window: %v", u.SignupAt)
		}
	}
	for _, e := range ds.Events {
		if !ids[e.UserID] || !slices.Contains(segment.EventTypes(), e.Type) {
			t.Fatalf("bad event %+v", e)
		}
		if e.OccurredAt.After(ds.AsOf) || ds.AsOf.Sub(e.OccurredAt) >= 90*24*time.Hour {
			t.Fatalf("event outside the 90-day window: %v", e.OccurredAt)
		}
		if e.OccurredAt.Sub(ds.AsOf)%time.Second != 0 {
			t.Fatalf("event offset is not whole seconds: %v", e.OccurredAt)
		}
	}
}

func TestDistributions(t *testing.T) {
	ds := mustGenerate(t, 42, fixedAsOf, 5000)
	share := func(pred func(User) bool) float64 {
		n := 0
		for _, u := range ds.Users {
			if pred(u) {
				n++
			}
		}
		return 100 * float64(n) / float64(len(ds.Users))
	}
	recentCart := map[[16]byte]bool{}
	for _, e := range ds.Events {
		if e.Type == "cart_add" && !e.OccurredAt.Before(ds.AsOf.Add(-30*24*time.Hour)) {
			recentCart[e.UserID] = true
		}
	}
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"istanbul", share(func(u User) bool { return u.City == "istanbul" }), 30},
		{"eskisehir", share(func(u User) bool { return u.City == "eskisehir" }), 6},
		{"language tr", share(func(u User) bool { return u.Language == "tr" }), 85},
		{"bronze", share(func(u User) bool { return u.LoyaltyTier == "bronze" }), 60},
		{"gold", share(func(u User) bool { return u.LoyaltyTier == "gold" }), 10},
		{"consent sms", share(func(u User) bool { return u.ConsentSMS }), 65},
		{"consent email", share(func(u User) bool { return u.ConsentEmail }), 80},
		{"cart add in last 30 days", share(func(u User) bool { return recentCart[u.ID] }), 35},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > 3 {
			t.Errorf("%s: %.1f%%, want about %.0f%%", c.name, c.got, c.want)
		}
	}
}

func TestEdgeRows(t *testing.T) {
	ds := mustGenerate(t, 42, fixedAsOf, 5000)
	u := ds.Users
	if utf8.RuneCountInString(u[0].FirstName) <= 20 || u[0].FirstName != EdgeLongName {
		t.Errorf("user 1: %q", u[0].FirstName)
	}
	if u[1].FirstName != EdgeTemplateName || u[2].FirstName != EdgeInjectionName || u[3].FirstName != "" {
		t.Errorf("users 2-4: %q %q %q", u[1].FirstName, u[2].FirstName, u[3].FirstName)
	}
	if u[4].ConsentSMS || u[4].ConsentEmail {
		t.Error("user 5 has consent")
	}
	for _, e := range ds.Events {
		if e.UserID == u[5].ID {
			t.Fatal("user 6 has events")
		}
	}
	if _, err := Generate(42, fixedAsOf, EdgeUsers-1); err == nil {
		t.Error("fewer users than edge rows accepted")
	}
}

// exampleCounts evaluates docs/examples/istanbul_cart_abandoners.json with the
// v1 semantics: city = istanbul, a cart_add with asOf-30d <= t <= asOf, and no
// purchase in that window. sms counts those with SMS consent.
func exampleCounts(ds Dataset) (total, sms int) {
	from := ds.AsOf.Add(-30 * 24 * time.Hour)
	in := func(t time.Time) bool { return !t.Before(from) && !t.After(ds.AsOf) }
	cart, bought := map[[16]byte]bool{}, map[[16]byte]bool{}
	for _, e := range ds.Events {
		if in(e.OccurredAt) && e.Type == "cart_add" {
			cart[e.UserID] = true
		}
		if in(e.OccurredAt) && e.Type == "purchase" {
			bought[e.UserID] = true
		}
	}
	for _, u := range ds.Users {
		if u.City == "istanbul" && cart[u.ID] && !bought[u.ID] {
			total++
			if u.ConsentSMS {
				sms++
			}
		}
	}
	return total, sms
}

// missedAbandoners counts Istanbul users whose last cart add in the window
// has no purchase after it, but who bought earlier in the window. They are
// real abandoners that the v1 rule excludes (docs/segment-rules.md).
func missedAbandoners(ds Dataset) int {
	from := ds.AsOf.Add(-30 * 24 * time.Hour)
	lastCart := map[[16]byte]time.Time{}
	var purchases []Event
	for _, e := range ds.Events {
		if e.OccurredAt.Before(from) || e.OccurredAt.After(ds.AsOf) {
			continue
		}
		switch e.Type {
		case "cart_add":
			if e.OccurredAt.After(lastCart[e.UserID]) {
				lastCart[e.UserID] = e.OccurredAt
			}
		case "purchase":
			purchases = append(purchases, e)
		}
	}
	boughtBefore, boughtAfter := map[[16]byte]bool{}, map[[16]byte]bool{}
	for _, p := range purchases {
		last, ok := lastCart[p.UserID]
		switch {
		case !ok:
		case p.OccurredAt.After(last):
			boughtAfter[p.UserID] = true
		default:
			boughtBefore[p.UserID] = true
		}
	}
	n := 0
	for _, u := range ds.Users {
		if u.City == "istanbul" && boughtBefore[u.ID] && !boughtAfter[u.ID] {
			n++
		}
	}
	return n
}

// The numbers in docs/seed-data.md. If the generator changes, update both.
const (
	docUsers        = 5000
	docEvents       = 56765
	docExampleTotal = 315
	docExampleSMS   = 210
	docMissed       = 53
)

func TestDocumentedNumbers(t *testing.T) {
	ds := mustGenerate(t, 42, fixedAsOf, docUsers)
	total, sms := exampleCounts(ds)
	missed := missedAbandoners(ds)
	if len(ds.Events) != docEvents || total != docExampleTotal || sms != docExampleSMS || missed != docMissed {
		t.Errorf("seed 42 gives events=%d example=%d sms=%d missed=%d; update docs/seed-data.md and these constants",
			len(ds.Events), total, sms, missed)
	}
}
