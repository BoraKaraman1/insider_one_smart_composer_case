package segment

import (
	"fmt"
	"strings"
)

func Describe(r Rule) string {
	parts := make([]string, len(r.Conditions))
	for i, c := range r.Conditions {
		parts[i] = describeCondition(c)
	}
	joiner, lead := "; and ", "Users who match all of: "
	if r.Match == "any" {
		joiner, lead = "; or ", "Users who match any of: "
	}
	if len(parts) == 1 {
		lead = "Users whose "
		if r.Conditions[0].Kind == KindEvent {
			lead = "Users who "
		}
	}
	return lead + strings.Join(parts, joiner) + ". Only users who consented to a channel receive it."
}

var opWords = map[string]string{
	"eq": "is", "neq": "is not", "in": "is one of", "not_in": "is none of",
	"gt": "is more than", "gte": "is at least", "lt": "is less than", "lte": "is at most",
}

var fieldWords = map[string]string{
	"city": "city", "language": "language", "loyalty_tier": "loyalty tier", "days_since_signup": "days since signup",
}

var eventWords = map[string]string{
	"cart_add": "added to cart", "purchase": "made a purchase", "page_view": "viewed a page",
}

func describeCondition(c Condition) string {
	switch c.Kind {
	case KindEnum:
		v := c.Value
		if c.Values != nil {
			v = strings.Join(c.Values, ", ")
		}
		return fmt.Sprintf("%s %s %s", fieldWords[c.Field], opWords[c.Op], v)
	case KindNumber:
		return fmt.Sprintf("%s %s %d", fieldWords[c.Field], opWords[c.Op], c.Number)
	case KindEvent:
		did := "have " + eventWords[c.Event]
		if c.Negate {
			did = "have not " + eventWords[c.Event]
		}
		return fmt.Sprintf("%s (%s) in the last %d days", did, c.Event, c.WithinDays)
	}
	return c.Kind
}
