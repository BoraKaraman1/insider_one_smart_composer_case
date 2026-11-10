package content

import (
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/seed"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
)

const testURL = "https://shop.example/k"

func TestRenderText(t *testing.T) {
	cases := []struct {
		name, tmpl string
		rc         RenderContext
		want       string
	}{
		{"plain name", "Merhaba {{first_name}}!", RenderContext{FirstName: "Ayşe"}, "Merhaba Ayşe!"},
		{"spaces inside braces", "Merhaba {{ first_name }}", RenderContext{FirstName: "Ayşe"}, "Merhaba Ayşe"},
		{"every variable", "{{first_name}} {{last_name}}, {{city}}: {{campaign_url}}",
			RenderContext{FirstName: "Ali", LastName: "Kaya", City: "ankara", CampaignURL: testURL},
			"Ali Kaya, Ankara: " + testURL},
		{"no variables", "Sepetin seni bekliyor", RenderContext{}, "Sepetin seni bekliyor"},
		// Seed edge row 4: an empty first name gets the fallback.
		{"empty first name", "Merhaba {{first_name}}", RenderContext{}, "Merhaba " + FirstNameFallback},
		{"blank first name", "Merhaba {{first_name}}", RenderContext{FirstName: "   "}, "Merhaba " + FirstNameFallback},
		// Seed edge row 1: truncated to 20 characters, the render limit.
		{"long name", "{{first_name}}", RenderContext{FirstName: seed.EdgeLongName}, "Muhammed Mustafa Abd"},
		{"long Turkish name keeps whole letters", "{{first_name}}", RenderContext{FirstName: strings.Repeat("ş", 25)}, strings.Repeat("ş", 20)},
		{"long last name", "{{last_name}}", RenderContext{LastName: strings.Repeat("y", 30)}, strings.Repeat("y", 20)},
		// Seed edge row 2: a value is never parsed again.
		{"template in a name", "Merhaba {{first_name}}", RenderContext{FirstName: seed.EdgeTemplateName}, "Merhaba {{first_name}}"},
		{"other variable in a name", "{{first_name}}", RenderContext{FirstName: "{{city}}", City: "izmir"}, "{{city}}"},
		{"url variable in a name", "{{first_name}}", RenderContext{FirstName: "{{campaign_url}}", CampaignURL: testURL}, "{{campaign_url}}"},
		// Seed edge row 3: printed as is, it is just a name.
		{"injection text", "{{first_name}}", RenderContext{FirstName: seed.EdgeInjectionName}, seed.EdgeInjectionName[:20]},
		// Plain text is not HTML: an SMS shows the characters.
		{"markup in an SMS", "{{first_name}}", RenderContext{FirstName: "<b>Tom & Jerry</b>"}, "<b>Tom & Jerry</b>"},
		{"city slug to display name", "{{city}}", RenderContext{City: "istanbul"}, "İstanbul"},
		{"city with Turkish letters", "{{city}}", RenderContext{City: "eskisehir"}, "Eskişehir"},
	}
	for _, c := range cases {
		got, err := RenderText(c.tmpl, c.rc)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFallbackFitsTheLimit(t *testing.T) {
	if FirstNameFallback != "Değerli müşterimiz" {
		t.Errorf("fallback %q", FirstNameFallback)
	}
	if n := utf8.RuneCountInString(FirstNameFallback); n > maxLen["first_name"] {
		t.Errorf("fallback has %d characters, over the first_name limit %d", n, maxLen["first_name"])
	}
}

// Every catalog city has a display name that fits the city limit, so the
// validator's worst case holds.
func TestEveryCityHasADisplayName(t *testing.T) {
	for _, slug := range segment.EnumValues("city") {
		got, err := RenderText("{{city}}", RenderContext{City: slug})
		if err != nil {
			t.Fatal(err)
		}
		if r, _ := utf8.DecodeRuneInString(got); !unicode.IsUpper(r) {
			t.Errorf("city %q renders as %q; want a capitalized display name", slug, got)
		}
		if n := utf8.RuneCountInString(got); n > maxLen["city"] {
			t.Errorf("city %q renders as %q: %d characters, limit %d", slug, got, n, maxLen["city"])
		}
	}
}

// A template the parser rejects never renders. This is a second line behind
// the validator: {{email}} cannot reach a recipient even if validation broke.
func TestRenderRejectsInvalidTemplates(t *testing.T) {
	for _, tmpl := range []string{"{{email}}", "{{password}}", "{{phone}}", "{{first_name", "a }} b", "{{first_name|upper}}"} {
		if got, err := RenderText(tmpl, RenderContext{FirstName: "Ali"}); err == nil {
			t.Errorf("%q rendered as %q", tmpl, got)
		}
		if got, err := RenderEmailHTML(tmpl, RenderContext{FirstName: "Ali"}); err == nil {
			t.Errorf("%q rendered as HTML %q", tmpl, got)
		}
	}
}

// Email HTML comes from the plain text through html/template, so markup in a
// name is escaped, never run.
func TestRenderEmailHTMLEscapes(t *testing.T) {
	rc := RenderContext{FirstName: "<script>alert(1)</script>", LastName: "O'Brien & Co", CampaignURL: testURL}
	got, err := RenderEmailHTML("Merhaba {{first_name}} {{last_name}}, indirim: {{campaign_url}}", rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<script") {
		t.Errorf("script tag survived: %s", got)
	}
	for _, want := range []string{"&lt;script&gt;", "&amp;", testURL} {
		if !strings.Contains(got, want) {
			t.Errorf("HTML %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "O'Brien") {
		t.Errorf("quote not escaped: %s", got)
	}
}

func TestRenderEmailHTMLKeepsLiteralTemplateNames(t *testing.T) {
	got, err := RenderEmailHTML("Merhaba {{first_name}}", RenderContext{FirstName: seed.EdgeTemplateName})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Merhaba {{first_name}}") {
		t.Errorf("HTML %q does not print the literal name", got)
	}
}

// The validator checks the worst case; truncation makes it hold at send
// time. Render a validated, long SMS for every seed user and check the
// limit, edge rows included.
func TestRenderedSMSFitsForEverySeedUser(t *testing.T) {
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	doc := `{"sms":{"body":"{{first_name}} {{last_name}}, {{city}} için sepetindeki ürünlerde bugüne özel indirim var. Bak: {{campaign_url}}"},` +
		`"email":{"subject":"{{first_name}}, sepetin seni bekliyor","body":"Merhaba {{first_name}}"}}`
	c, res := ValidateJSON(set, []byte(doc))
	if !res.OK() {
		t.Fatalf("fixture content is invalid: %v", res.Errors)
	}
	ds, err := seed.Generate(42, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 5000)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://shop.example/" + strings.Repeat("k", 40-len("https://shop.example/"))
	for i, u := range ds.Users {
		rc := RenderContext{FirstName: u.FirstName, LastName: u.LastName, City: u.City, CampaignURL: url}
		sms, err := RenderText(c.SMSBody, rc)
		if err != nil {
			t.Fatal(err)
		}
		if n := utf8.RuneCountInString(sms); n > SMSMaxChars {
			t.Fatalf("user %d: rendered SMS has %d characters: %q", i+1, n, sms)
		}
		subject, err := RenderText(c.EmailSubject, rc)
		if err != nil {
			t.Fatal(err)
		}
		if n := utf8.RuneCountInString(subject); n > SubjectMaxChars {
			t.Fatalf("user %d: rendered subject has %d characters: %q", i+1, n, subject)
		}
	}
}
