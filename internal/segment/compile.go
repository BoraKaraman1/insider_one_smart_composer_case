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

var consentColumns = map[string]string{
	"sms":   "u.consent_sms",
	"email": "u.consent_email",
}

const day = 24 * time.Hour

type fragment struct {
	sql  string
	args []any
}

func (f *fragment) arg(v any) string {
	f.args = append(f.args, v)
	return "$" + strconv.Itoa(len(f.args))
}

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
		d := func(n int) time.Time { return asOf.Add(-time.Duration(n) * day) }
		n := c.Number
		switch c.Op {
		case "eq":
			// Gün sayısı kayan nokta yerine zaman aralığıyla sayılır.
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

type Query struct {
	SQL  string
	Args []any
}

func AudienceQuery(r Rule, channel string, asOf time.Time) (Query, error) {
	consent, ok := consentColumns[channel]
	if !ok {
		return Query{}, fmt.Errorf("segment: unknown channel %q", channel)
	}
	f, err := compile(r, asOf)
	if err != nil {
		return Query{}, err
	}
	// Rıza süzgeci her zaman eklenir ve kural onu yazamaz.
	return Query{SQL: "SELECT u.id FROM users u WHERE " + f.sql + " AND " + consent, Args: f.args}, nil
}

type Counts struct {
	Total         int `json:"total"`
	SMSEligible   int `json:"sms_eligible"`
	EmailEligible int `json:"email_eligible"`
}

type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

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
