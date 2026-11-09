package content

import (
	"bytes"
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

// Rendered length limits (runes, worst case). They match the maxLength of the
// raw templates in schemas/content.schema.json; content_test.go checks that.
const (
	SMSMaxChars     = 160
	SubjectMaxChars = 80
)

// Content is validated copy.
type Content struct {
	SMSBody      string
	EmailSubject string
	EmailBody    string
	// SMS is the pessimistic estimate: longest variable values, and Turkish
	// letters in names and city.
	SMS SMSInfo
}

// Banned patterns. They are checked on the text with every variable filled
// in by a placeholder, so "{{city}}.com" is caught too. {{campaign_url}} is
// the one allowed link and is filled with a space.
var (
	schemeRe = regexp.MustCompile(`(?i)[a-z][a-z0-9+.\-]*://|www\.`)
	// A bare domain: dot-separated labels ending in a common TLD. Labels are
	// Unicode-aware, so a Cyrillic lookalike such as "іnsider.com" is caught.
	domainRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}@._-])((?:[\p{L}\p{N}](?:[\p{L}\p{N}-]*[\p{L}\p{N}])?\.)+` +
		`(?:com|net|org|info|biz|io|co|ai|app|dev|me|ly|gl|to|cc|tv|xyz|link|shop|store|site|online|tr|eu|uk|de|us))(?:$|[^\p{L}\p{N}_-])`)
	ipRe    = regexp.MustCompile(`(?:^|[^\p{N}.])(\d{1,3}(?:\.\d{1,3}){3})(?:$|[^\p{N}.])`)
	emailRe = regexp.MustCompile(`[\p{L}\p{N}._%+-]+@[\p{L}\p{N}-]+(?:\.[\p{L}\p{N}-]+)+`)
	htmlRe  = regexp.MustCompile(`<\s*/?\s*[A-Za-z][^>]*>`)
)

// ValidateJSON parses and validates content given as raw JSON.
func ValidateJSON(set *schema.Set, data []byte) (Content, validation.Result) {
	doc, err := schema.Decode(bytes.NewReader(data))
	if err != nil {
		var res validation.Result
		res.Error("", validation.InvalidJSON, err.Error())
		return Content{}, res
	}
	return Validate(set, doc)
}

// Validate checks decoded content (from schema.Decode): the schema first,
// then templates, banned links, emails and HTML, and the worst-case rendered
// length. Every problem is reported. SMS encoding and segment count are
// warnings only.
func Validate(set *schema.Set, doc any) (Content, validation.Result) {
	var res validation.Result
	rawTooLong := map[string]bool{}
	if err := set.Validate(schema.Content, doc); err != nil {
		for _, is := range schema.Issues(err, "") {
			res.Errors = append(res.Errors, is)
			if is.Code == validation.TooLong {
				rawTooLong[is.Path] = true
			}
		}
	}

	fields := []struct {
		path, a, b string
		limit      int
	}{
		{"/sms/body", "sms", "body", SMSMaxChars},
		{"/email/subject", "email", "subject", SubjectMaxChars},
		{"/email/body", "email", "body", 0},
	}
	var smsParts []Part
	values := map[string]string{}
	for _, f := range fields {
		s, ok := lookup(doc, f.a, f.b)
		if !ok {
			continue // the schema already reported it
		}
		values[f.path] = s
		parts, issues := Parse(s)
		for _, is := range issues {
			res.Error(f.path, is.Code, is.Message)
		}
		checkBans(&res, f.path, parts)
		if len(issues) > 0 {
			continue // lengths of a broken template mean nothing
		}
		if f.limit > 0 && !rawTooLong[f.path] {
			if n := utf8.RuneCountInString(worstCase(parts, false)); n > f.limit {
				res.Error(f.path, validation.RenderedTooLong, fmt.Sprintf(
					"can render up to %d characters with the longest variable values; the limit is %d", n, f.limit))
			}
		}
		if f.path == "/sms/body" {
			smsParts = parts
		}
	}

	var info SMSInfo
	if smsParts != nil {
		info = MeasureSMS(worstCase(smsParts, true))
		if info.Segments > 1 {
			msg := fmt.Sprintf("%s, %d segments in the worst case", info.Encoding, info.Segments)
			if plain := MeasureSMS(worstCase(smsParts, false)); plain.Encoding != info.Encoding {
				msg += fmt.Sprintf(" (names or city may contain Turkish letters such as ş, ğ, ı that switch the SMS to UCS-2; without them: %s, %d segment(s))", plain.Encoding, plain.Segments)
			}
			res.Warn("/sms/body", validation.SMSMultiSegment, msg)
		}
	}

	slices.SortStableFunc(res.Errors, func(a, b validation.Issue) int { return cmp.Compare(a.Path, b.Path) })
	if !res.OK() {
		return Content{}, res
	}
	return Content{
		SMSBody:      values["/sms/body"],
		EmailSubject: values["/email/subject"],
		EmailBody:    values["/email/body"],
		SMS:          info,
	}, res
}

// checkBans reports links, email addresses and HTML in the literal text.
func checkBans(res *validation.Result, path string, parts []Part) {
	var b strings.Builder
	for _, p := range parts {
		switch {
		case p.Var == "":
			b.WriteString(p.Text)
		case p.Var == "campaign_url":
			b.WriteString(" ")
		default:
			b.WriteString("x")
		}
	}
	text := b.String()
	if m := schemeRe.FindString(text); m != "" {
		res.Error(path, validation.URLNotAllowed, fmt.Sprintf("link %q is not allowed; use {{campaign_url}}", m))
	} else if m := domainRe.FindStringSubmatch(text); m != nil {
		res.Error(path, validation.URLNotAllowed, fmt.Sprintf("domain %q is not allowed; use {{campaign_url}}", m[1]))
	} else if m := ipRe.FindStringSubmatch(text); m != nil {
		res.Error(path, validation.URLNotAllowed, fmt.Sprintf("address %q is not allowed; use {{campaign_url}}", m[1]))
	}
	if m := emailRe.FindString(text); m != "" {
		res.Error(path, validation.EmailNotAllowed, fmt.Sprintf("email address %q is not allowed", m))
	}
	if m := htmlRe.FindString(text); m != "" {
		res.Error(path, validation.HTMLNotAllowed, fmt.Sprintf("HTML %q is not allowed; copy is plain text", m))
	}
}

func lookup(doc any, a, b string) (string, bool) {
	m, _ := doc.(map[string]any)
	inner, _ := m[a].(map[string]any)
	s, ok := inner[b].(string)
	return s, ok
}
