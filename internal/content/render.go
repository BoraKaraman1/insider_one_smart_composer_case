package content

import (
	"errors"
	"html/template"
	"strings"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
)

// FirstNameFallback replaces an empty first name.
const FirstNameFallback = "Değerli müşterimiz"

// RenderContext is everything a template can show about a recipient. It is
// a dedicated struct, never the users row, so a field that is not here
// (email, phone) cannot be rendered whatever a template says (rule 9).
type RenderContext struct {
	FirstName   string
	LastName    string
	City        string // the slug; rendered as its display name
	CampaignURL string // from the campaign, never from the model
}

// ErrInvalidTemplate is returned for a template the parser rejects. The
// validator rejects it first; this is the second line.
var ErrInvalidTemplate = errors.New("content: invalid template")

// values returns each variable's text: fallbacks applied, then cut to the
// variable's render limit, so the validator's worst case holds.
func (rc RenderContext) values() map[string]string {
	first := rc.FirstName
	if strings.TrimSpace(first) == "" {
		first = FirstNameFallback
	}
	city := segment.CityDisplayName(rc.City)
	if city == "" {
		city = rc.City
	}
	v := map[string]string{"first_name": first, "last_name": rc.LastName, "city": city, "campaign_url": rc.CampaignURL}
	for k, s := range v {
		v[k] = truncate(s, maxLen[k])
	}
	return v
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for range n {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s[:i]
}

func parse(tmpl string) ([]Part, error) {
	parts, issues := Parse(tmpl)
	if len(issues) > 0 {
		return nil, ErrInvalidTemplate
	}
	return parts, nil
}

// RenderText renders a template as plain text (SMS, email subject). It is
// one pass over the parsed parts: a value is written as is and never parsed
// again, so a name like "{{first_name}}" prints literally.
func RenderText(tmpl string, rc RenderContext) (string, error) {
	parts, err := parse(tmpl)
	if err != nil {
		return "", err
	}
	v := rc.values()
	var b strings.Builder
	for _, p := range parts {
		if p.Var == "" {
			b.WriteString(p.Text)
		} else {
			b.WriteString(v[p.Var])
		}
	}
	return b.String(), nil
}

// htmlPiece is text, or the campaign link.
type htmlPiece struct {
	Text string
	URL  string
}

var emailHTML = template.Must(template.New("email").Parse(`<!doctype html>
<html><body>
{{- range .}}
<p>{{range $i, $line := .}}{{if $i}}<br>{{end}}{{range $line}}{{if .URL}}<a href="{{.URL}}">{{.URL}}</a>{{else}}{{.Text}}{{end}}{{end}}{{end}}</p>
{{- end}}
</body></html>
`))

// RenderEmailHTML renders an email body as HTML. The copy is plain text;
// html/template escapes every piece, so markup in copy or in a name is
// shown, never run. Blank lines start paragraphs; {{campaign_url}} becomes a
// link.
func RenderEmailHTML(tmpl string, rc RenderContext) (string, error) {
	parts, err := parse(tmpl)
	if err != nil {
		return "", err
	}
	v := rc.values()
	// paragraphs > lines > pieces
	paras := [][][]htmlPiece{{{}}}
	add := func(p htmlPiece) {
		para := paras[len(paras)-1]
		para[len(para)-1] = append(para[len(para)-1], p)
	}
	for _, p := range parts {
		switch {
		case p.Var == "campaign_url":
			add(htmlPiece{URL: v[p.Var]})
		case p.Var != "":
			add(htmlPiece{Text: v[p.Var]})
		default:
			for i, line := range strings.Split(strings.ReplaceAll(p.Text, "\r\n", "\n"), "\n") {
				if i > 0 {
					para := paras[len(paras)-1]
					if len(para[len(para)-1]) == 0 && len(para) > 1 {
						// A blank line: drop it and start a paragraph.
						paras[len(paras)-1] = para[:len(para)-1]
						paras = append(paras, [][]htmlPiece{{}})
					} else {
						paras[len(paras)-1] = append(para, []htmlPiece{})
					}
				}
				if line != "" {
					add(htmlPiece{Text: line})
				}
			}
		}
	}
	var b strings.Builder
	if err := emailHTML.Execute(&b, paras); err != nil {
		return "", err
	}
	return b.String(), nil
}
