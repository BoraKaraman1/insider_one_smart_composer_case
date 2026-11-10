package content

import (
	"errors"
	"html/template"
	"strings"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
)

const FirstNameFallback = "Değerli müşterimiz"

type RenderContext struct {
	FirstName   string
	LastName    string
	City        string
	CampaignURL string
}

var ErrInvalidTemplate = errors.New("content: invalid template")

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

// Değer yeniden ayrıştırılmaz ki süslü parantezli ad olduğu gibi basılsın.
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

func RenderEmailHTML(tmpl string, rc RenderContext) (string, error) {
	parts, err := parse(tmpl)
	if err != nil {
		return "", err
	}
	v := rc.values()
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
