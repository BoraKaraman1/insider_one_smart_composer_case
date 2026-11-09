// Package content validates campaign copy: the SMS body and the email
// subject and body. Copy is plain text with {{ name }} variables from a fixed
// allowlist; links come only from {{campaign_url}}.
package content

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

// maxLen is the allowlist of template variables, with the most characters
// (runes) each may render to. The renderer truncates values to this length,
// so a worst-case length check made at validation time holds at send time.
// Nothing security-critical or contact-related (email, phone) is here.
var maxLen = map[string]int{
	"first_name":   20,
	"last_name":    20,
	"city":         15,
	"campaign_url": 40,
}

// mayBeNonGSM marks variables whose values can contain letters outside GSM-7,
// such as ş, ğ and ı in Turkish names and city names.
var mayBeNonGSM = map[string]bool{"first_name": true, "last_name": true, "city": true}

// Variables returns the allowlisted variable names, sorted.
func Variables() []string {
	names := make([]string, 0, len(maxLen))
	for n := range maxLen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Part is one piece of a parsed template: literal text, or one variable.
type Part struct {
	Text string // literal text, when Var is empty
	Var  string // variable name
}

// Parse splits a template into parts. Only {{ name }} with an allowlisted
// name is a variable. Unknown names, unclosed or stray braces, filters and
// nesting are errors. The returned issues have an empty Path; the caller
// knows which field it parsed.
func Parse(tmpl string) ([]Part, []validation.Issue) {
	var parts []Part
	var issues []validation.Issue
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			parts = append(parts, Part{Text: text.String()})
			text.Reset()
		}
	}
	at := func(i int) int { return utf8.RuneCountInString(tmpl[:i]) + 1 }

	for i := 0; i < len(tmpl); {
		switch {
		case strings.HasPrefix(tmpl[i:], "{{"):
			end := strings.Index(tmpl[i+2:], "}}")
			if end < 0 {
				issues = append(issues, validation.Issue{Code: validation.MalformedTemplate,
					Message: fmt.Sprintf("unclosed {{ at character %d", at(i))})
				text.WriteString(tmpl[i:])
				i = len(tmpl)
				continue
			}
			inner := tmpl[i+2 : i+2+end]
			name := strings.Trim(inner, " ")
			switch {
			case !isIdent(name):
				issues = append(issues, validation.Issue{Code: validation.MalformedTemplate,
					Message: fmt.Sprintf("{{%s}} at character %d is not a variable; write {{ name }} with no filters", inner, at(i))})
			case maxLen[name] == 0:
				issues = append(issues, validation.Issue{Code: validation.UnknownVariable,
					Message: fmt.Sprintf("{{%s}} is not an allowed variable; use one of: %s", name, strings.Join(Variables(), ", "))})
			default:
				flush()
				parts = append(parts, Part{Var: name})
			}
			i += 2 + end + 2
		case strings.HasPrefix(tmpl[i:], "}}"):
			issues = append(issues, validation.Issue{Code: validation.MalformedTemplate,
				Message: fmt.Sprintf("stray }} at character %d", at(i))})
			i += 2
		default:
			text.WriteByte(tmpl[i])
			i++
		}
	}
	flush()
	return parts, issues
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		letter := r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// worstCase renders parts with every variable at its maximum length. With
// turkish set, variables that may hold non-GSM letters are filled with ş.
func worstCase(parts []Part, turkish bool) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Var == "" {
			b.WriteString(p.Text)
			continue
		}
		fill := "x"
		if turkish && mayBeNonGSM[p.Var] {
			fill = "ş"
		}
		b.WriteString(strings.Repeat(fill, maxLen[p.Var]))
	}
	return b.String()
}
