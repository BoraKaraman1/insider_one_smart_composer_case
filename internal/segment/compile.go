package segment

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The compiler turns a validated Rule into SQL over `users u`. Every piece of
// SQL text comes from this file; columns, operators and event types are
// looked up in the maps below, and every value, event type and time cutoff
// is a bound parameter. So the SQL text depends only on the rule's shape,
// never on its values (rule 2). Semantics: docs/segment-rules.md.

var enumColumns = map[string]string{
	"city":         "u.city",
	"language":     "u.language",
	"loyalty_tier": "u.loyalty_tier",
}

var enumOps = map[string]string{
	"eq":     "%s = %s",
	"neq":    "%s <> %s",
	"in":     "%s = ANY(%s::text[])",
	"not_in": "%s <> ALL(%s::text[])",
}

// consentColumns is the only way to name a channel. AudienceQuery always
// ANDs the channel's column, and nothing else returns recipients.
var consentColumns = map[string]string{
	"sms":   "u.consent_sms",
	"email": "u.consent_email",
}

const day = 24 * time.Hour

// fragment is compiled SQL plus its arguments, in $n order.
type fragment struct {
	sql  string
	args []any
}

func (f *fragment) arg(v any) string {
	f.args = append(f.args, v)
	return "$" + strconv.Itoa(len(f.args))
}

// compile builds the WHERE condition for a rule. It is unexported on
// purpose: callers get recipients only through AudienceQuery, which adds the
// consent filter, or counts through CountAudience. It checks structure
// against the catalog but not values, which are data and always bound.
func compile(r Rule, asOf time.Time) (fragment, error) {
	asOf = asOf.UTC().Truncate(time.Microsecond)
	var joiner string
	switch r.Match {
	case "all":
		joiner = " AND "
	case "any":
		joiner = " OR "
	default:
		return fragment{}, fmt.Errorf("segment: unknown match %q", r.Match)
	}
	if len(r.Conditions) == 0 {
		return fragment{}, fmt.Errorf("segment: rule has no conditions")
	}
	var f fragment
	parts := make([]string, len(r.Conditions))
	for i, c := range r.Conditions {
		s, err := f.condition(c, asOf)
		if err != nil {
			return fragment{}, fmt.Errorf("segment: condition %d: %w", i, err)
		}
		parts[i] = s
	}
	f.sql = "(" + strings.Join(parts, joiner) + ")"
	return f, nil
}

func (f *fragment) condition(c Condition, asOf time.Time) (string, error) {
	switch c.Kind {
	case KindEnum:
		col, ok := enumColumns[c.Field]
		if !ok {
			return "", fmt.Errorf("unknown enum field %q", c.Field)
		}
		tmpl, ok := enumOps[c.Op]
		if !ok {
			return "", fmt.Errorf("unknown operator %q", c.Op)
		}
		if slices.Contains(listOps, c.Op) {
			return fmt.Sprintf(tmpl, col, f.arg(c.Values)), nil
		}
		return fmt.Sprintf(tmpl, col, f.arg(c.Value)), nil

	case KindNumber:
		if c.Field != "days_since_signup" {
			return "", fmt.Errorf("unknown number field %q", c.Field)
		}
		// days = floor((asOf - signup_at) / 24h), as timestamp ranges with
		// d(n) = asOf - n*24h, so no float math happens in SQL.
		d := func(n int) time.Time { return asOf.Add(-time.Duration(n) * day) }
		n := c.Number
		switch c.Op {
		case "eq":
			return fmt.Sprintf("(u.signup_at > %s AND u.signup_at <= %s)", f.arg(d(n+1)), f.arg(d(n))), nil
		case "neq":
			return fmt.Sprintf("NOT (u.signup_at > %s AND u.signup_at <= %s)", f.arg(d(n+1)), f.arg(d(n))), nil
		case "gte":
			return "u.signup_at <= " + f.arg(d(n)), nil
		case "gt":
			return "u.signup_at <= " + f.arg(d(n+1)), nil
		case "lte":
			return "u.signup_at > " + f.arg(d(n+1)), nil
		case "lt":
			return "u.signup_at > " + f.arg(d(n)), nil
		default:
			return "", fmt.Errorf("unknown operator %q", c.Op)
		}

	case KindEvent:
		if !slices.Contains(eventTypes, c.Event) {
			return "", fmt.Errorf("unknown event %q", c.Event)
		}
		exists := "EXISTS"
		if c.Negate {
			exists = "NOT EXISTS"
		}
		from := asOf.Add(-time.Duration(c.WithinDays) * day)
		return fmt.Sprintf("%s (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = %s"+
			" AND e.occurred_at >= %s AND e.occurred_at <= %s)",
			exists, f.arg(c.Event), f.arg(from), f.arg(asOf)), nil

	default:
		return "", fmt.Errorf("unknown kind %q", c.Kind)
	}
}

// Query is a SQL statement with its arguments.
type Query struct {
	SQL  string
	Args []any
}

// AudienceQuery selects the ids of a rule's recipients on one channel: users
// who match the rule and consented to that channel. The consent filter is
// always part of the query; a rule cannot express or remove it.
func AudienceQuery(r Rule, channel string, asOf time.Time) (Query, error) {
	consent, ok := consentColumns[channel]
	if !ok {
		return Query{}, fmt.Errorf("segment: unknown channel %q", channel)
	}
	f, err := compile(r, asOf)
	if err != nil {
		return Query{}, err
	}
	return Query{SQL: "SELECT u.id FROM users u WHERE " + f.sql + " AND " + consent, Args: f.args}, nil
}

// Counts is the audience size of a rule. Total ignores consent; the eligible
// counts are what would actually be sent per channel.
type Counts struct {
	Total         int `json:"total"`
	SMSEligible   int `json:"sms_eligible"`
	EmailEligible int `json:"email_eligible"`
}

// Querier runs one query. *pgxpool.Pool, *pgx.Conn and pgx.Tx satisfy it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CountAudience counts the users a rule matches, in total and per channel
// with consent applied.
func CountAudience(ctx context.Context, q Querier, r Rule, asOf time.Time) (Counts, error) {
	f, err := compile(r, asOf)
	if err != nil {
		return Counts{}, err
	}
	sql := "SELECT count(*), count(*) FILTER (WHERE " + consentColumns["sms"] + "), count(*) FILTER (WHERE " +
		consentColumns["email"] + ") FROM users u WHERE " + f.sql
	var c Counts
	if err := q.QueryRow(ctx, sql, f.args...).Scan(&c.Total, &c.SMSEligible, &c.EmailEligible); err != nil {
		return Counts{}, fmt.Errorf("segment: count: %w", err)
	}
	return c, nil
}
