// Package segment implements the segment rule language described in
// docs/segment-rules.md.
//
// The catalog below is the Go source of truth for the language. The JSON
// Schema (schemas/segment_rule.schema.json) repeats it so Claude's structured
// outputs can enforce it; catalog_test.go fails if the two drift apart.
package segment

import "slices"

// Limits of the rule language.
const (
	MaxConditions      = 10
	MaxListValues      = 20
	WithinDaysMin      = 1
	WithinDaysMax      = 365
	DaysSinceSignupMax = 3650
)

// enumValues maps each enum attribute to its allowed values. City values are
// ASCII slugs, so Turkish casing (İ/i) can never change a comparison.
var enumValues = map[string][]string{
	"city":         {"istanbul", "ankara", "izmir", "bursa", "antalya", "adana", "konya", "gaziantep", "kocaeli", "eskisehir"},
	"language":     {"tr", "en"},
	"loyalty_tier": {"bronze", "silver", "gold"},
}

var (
	enumFields = []string{"city", "language", "loyalty_tier"}
	eventTypes = []string{"cart_add", "purchase", "page_view"}
	singleOps  = []string{"eq", "neq"}
	listOps    = []string{"in", "not_in"}
	numberOps  = []string{"eq", "neq", "gt", "gte", "lt", "lte"}
)

// The accessors return copies, so callers cannot change the catalog.

// EnumFields returns the enum attributes in a fixed order.
func EnumFields() []string { return slices.Clone(enumFields) }

// EnumValues returns the allowed values of an enum attribute, or nil if the
// field is not an enum attribute.
func EnumValues(field string) []string { return slices.Clone(enumValues[field]) }

// EventTypes returns the event types a rule can match on.
func EventTypes() []string { return slices.Clone(eventTypes) }

// SingleOps returns the operators that take one enum value.
func SingleOps() []string { return slices.Clone(singleOps) }

// ListOps returns the operators that take a list of enum values.
func ListOps() []string { return slices.Clone(listOps) }

// NumberOps returns the operators for days_since_signup.
func NumberOps() []string { return slices.Clone(numberOps) }
