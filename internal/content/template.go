// Paket kısa ileti ve e-posta metnini doğrular.
package content

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

var maxLen = map[string]int{
	"first_name":   20,
	"last_name":    20,
	"city":         15,
	"campaign_url": 40,
}

// Bu alanlar yedi bitlik alfabede olmayan harf taşıyabilir.
var mayBeNonGSM = map[string]bool{"first_name": true, "last_name": true, "city": true}

func Variables() []string {
	names := make([]string, 0, len(maxLen))
	for n := range maxLen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

type Part struct {
	Text string
	Var  string
}

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

// Türkçe seçilince bu alanlar en geniş harfle doldurulur.
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
