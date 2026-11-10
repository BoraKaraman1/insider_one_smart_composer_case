package eval

import (
	"context"
	"time"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

// Fixture is a named recipient the harness inserts (plan 3, 2.2).
type Fixture struct {
	ID           string
	ConsentSMS   bool
	ConsentEmail bool
}

// Fixtures maps a fixture name to the inserted row.
type Fixtures map[string]Fixture

type fixtureSpec struct {
	name       string
	city       string
	sms, email bool
	purchased  bool // bought after the cart add, inside 30 days
}

// Every fixture added an item to the cart two days before asOf.
var fixtureSpecs = []fixtureSpec{
	{name: "istanbul_cart_no_purchase", city: "istanbul", sms: true, email: true},
	{name: "istanbul_cart_purchased", city: "istanbul", sms: true, email: true, purchased: true},
	{name: "ankara_cart_no_purchase", city: "ankara", sms: true, email: true},
	{name: "istanbul_cart_no_purchase_no_sms_consent", city: "istanbul", sms: false, email: true},
	{name: "istanbul_cart_no_purchase_no_consent", city: "istanbul", sms: false, email: false},
}

// FixtureNames returns the fixture names, in insert order.
func FixtureNames() []string {
	out := make([]string, len(fixtureSpecs))
	for i, f := range fixtureSpecs {
		out[i] = f.name
	}
	return out
}

// InsertFixtures inserts the fixture users and their events, relative to
// asOf. Their contact fields are placeholders on a reserved domain.
func InsertFixtures(ctx context.Context, db store.DB, asOf time.Time) (Fixtures, error) {
	const day = 24 * time.Hour
	asOf = asOf.UTC().Truncate(time.Second)
	fx := Fixtures{}
	for _, f := range fixtureSpecs {
		id, err := store.InsertEvalUser(ctx, db, store.EvalUser{
			FirstName: "Fixture", LastName: "Eval", Email: f.name + "@fixture.invalid", Phone: "+900000000000",
			City: f.city, Language: "tr", LoyaltyTier: "bronze", SignupAt: asOf.Add(-100 * day),
			ConsentSMS: f.sms, ConsentEmail: f.email,
		})
		if err != nil {
			return nil, err
		}
		if err := store.InsertUserEvent(ctx, db, id, "cart_add", asOf.Add(-2*day)); err != nil {
			return nil, err
		}
		if f.purchased {
			if err := store.InsertUserEvent(ctx, db, id, "purchase", asOf.Add(-1*day)); err != nil {
				return nil, err
			}
		}
		fx[f.name] = Fixture{ID: id, ConsentSMS: f.sms, ConsentEmail: f.email}
	}
	return fx, nil
}

// Audience maps a channel to the set of user ids in its audience.
type Audience map[string]map[string]bool

// Audiences runs a rule through segment.AudienceQuery, the compiler and
// consent filter that fan-out uses, for both channels.
func Audiences(ctx context.Context, db store.DB, r segment.Rule, asOf time.Time) (Audience, error) {
	aud := Audience{}
	for _, ch := range []string{"sms", "email"} {
		q, err := segment.AudienceQuery(r, ch, asOf)
		if err != nil {
			return nil, err
		}
		ids, err := store.AudienceIDs(ctx, db, q.SQL, q.Args)
		if err != nil {
			return nil, err
		}
		aud[ch] = make(map[string]bool, len(ids))
		for _, id := range ids {
			aud[ch][id] = true
		}
	}
	return aud, nil
}
