package content

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/schemas"
)

func TestMeasureSMS(t *testing.T) {
	r := strings.Repeat
	cases := []struct {
		name, text string
		want       SMSInfo
	}{
		{"empty", "", SMSInfo{"GSM-7", 0, 1}},
		{"plain Turkish word", "Merhaba", SMSInfo{"GSM-7", 7, 1}},
		{"GSM letters Ç ö ü", "Çok güzel ödül", SMSInfo{"GSM-7", 14, 1}},
		{"160 GSM", r("a", 160), SMSInfo{"GSM-7", 160, 1}},
		{"161 GSM", r("a", 161), SMSInfo{"GSM-7", 161, 2}},
		{"306 GSM", r("a", 306), SMSInfo{"GSM-7", 306, 2}},
		{"307 GSM", r("a", 307), SMSInfo{"GSM-7", 307, 3}},
		{"extension chars count 2", "{}€", SMSInfo{"GSM-7", 6, 1}},
		{"80 euro signs", r("€", 80), SMSInfo{"GSM-7", 160, 1}},
		{"81 euro signs", r("€", 81), SMSInfo{"GSM-7", 162, 2}},
		{"lowercase ç", "ç", SMSInfo{"UCS-2", 1, 1}},
		{"ş", "ş", SMSInfo{"UCS-2", 1, 1}},
		{"ğ", "ğ", SMSInfo{"UCS-2", 1, 1}},
		{"dotless ı", "ı", SMSInfo{"UCS-2", 1, 1}},
		{"dotted İ", "İ", SMSInfo{"UCS-2", 1, 1}},
		{"70 UCS-2", r("ş", 70), SMSInfo{"UCS-2", 70, 1}},
		{"71 UCS-2", r("ş", 71), SMSInfo{"UCS-2", 71, 2}},
		{"134 UCS-2", r("ş", 134), SMSInfo{"UCS-2", 134, 2}},
		{"150-character Turkish SMS (spec answer)", r("ş", 150), SMSInfo{"UCS-2", 150, 3}},
		{"emoji is 2 UTF-16 units", "😀", SMSInfo{"UCS-2", 2, 1}},
		{"35 emoji", r("😀", 35), SMSInfo{"UCS-2", 70, 1}},
		{"36 emoji", r("😀", 36), SMSInfo{"UCS-2", 72, 2}},
	}
	for _, c := range cases {
		if got := MeasureSMS(c.text); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestGSMTables(t *testing.T) {
	if n := utf8.RuneCountInString(gsmBasic); n != 127 {
		t.Errorf("basic table has %d characters, want 127 (128 minus escape)", n)
	}
	if n := utf8.RuneCountInString(gsmExtension); n != 10 {
		t.Errorf("extension table has %d characters, want 10", n)
	}
	seen := map[rune]bool{}
	for _, r := range gsmBasic + gsmExtension {
		if seen[r] {
			t.Errorf("%q appears twice", r)
		}
		seen[r] = true
	}
}

func TestVariablesAllowlist(t *testing.T) {
	want := []string{"campaign_url", "city", "first_name", "last_name"}
	if got := Variables(); !slices.Equal(got, want) {
		t.Fatalf("allowlist is %v, want %v", got, want)
	}
	for _, v := range Variables() {
		for _, bad := range []string{"pass", "token", "secret", "hash", "email", "phone"} {
			if strings.Contains(v, bad) {
				t.Errorf("security-critical or contact variable %q in the allowlist", v)
			}
		}
	}
}

func TestLimitsMatchSchema(t *testing.T) {
	b, err := schemas.FS.ReadFile("content.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties struct {
			SMS struct {
				Properties struct {
					Body struct{ MaxLength int } `json:"body"`
				}
			} `json:"sms"`
			Email struct {
				Properties struct {
					Subject struct{ MaxLength int } `json:"subject"`
				}
			} `json:"email"`
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Properties.SMS.Properties.Body.MaxLength; got != SMSMaxChars {
		t.Errorf("schema sms maxLength %d, SMSMaxChars %d", got, SMSMaxChars)
	}
	if got := doc.Properties.Email.Properties.Subject.MaxLength; got != SubjectMaxChars {
		t.Errorf("schema subject maxLength %d, SubjectMaxChars %d", got, SubjectMaxChars)
	}
}

func TestParse(t *testing.T) {
	parts, issues := Parse("Merhaba {{ first_name }}, {{city}}'da indirim!")
	want := []Part{{Text: "Merhaba "}, {Var: "first_name"}, {Text: ", "}, {Var: "city"}, {Text: "'da indirim!"}}
	if len(issues) != 0 || !slices.Equal(parts, want) {
		t.Fatalf("got %+v %+v", parts, issues)
	}
	bad := map[string]string{
		"{{password}}":         validation.UnknownVariable,
		"{{email}}":            validation.UnknownVariable,
		"{{phone}}":            validation.UnknownVariable,
		"{{First_Name}}":       validation.UnknownVariable,
		"{{first_name}":        validation.MalformedTemplate,
		"{{ first name }}":     validation.MalformedTemplate,
		"{{first_name|upper}}": validation.MalformedTemplate,
		"{{{first_name}}}":     validation.MalformedTemplate,
		"{{}}":                 validation.MalformedTemplate,
		"{{1st}}":              validation.MalformedTemplate,
		"x }} y":               validation.MalformedTemplate,
	}
	for tmpl, code := range bad {
		_, issues := Parse(tmpl)
		if len(issues) != 1 || issues[0].Code != code {
			t.Errorf("%q: got %+v, want one %s", tmpl, issues, code)
		}
	}
}

func contentJSON(sms, subject, body string) []byte {
	b, _ := json.Marshal(map[string]any{
		"sms":   map[string]any{"body": sms},
		"email": map[string]any{"subject": subject, "body": body},
	})
	return b
}

func codes(issues []validation.Issue) []string {
	out := make([]string, len(issues))
	for i, is := range issues {
		out[i] = is.Path + " " + is.Code
	}
	return out
}

func TestValidate(t *testing.T) {
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	r := strings.Repeat
	cases := []struct {
		name               string
		sms, subject, body string
		want               []string
	}{
		{"Turkish copy with variables", "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! %30 indirim: {{campaign_url}}",
			"{{first_name}}, sepetin seni bekliyor", "Merhaba {{first_name}},\n\nSepetindeki ürünler hâlâ seni bekliyor.", nil},
		{"text that only looks like a link", "Saat 10.30'da %30 indirim, 1.000 TL üzeri. T.C. vb. Dr. Ali, e-posta ile değil SMS ile.",
			"İstanbul'da indirim", "10.30/11.30 arası. Detay ({{campaign_url}}). Hemen: \"{{campaign_url}}\"\n{{campaign_url}}, {{campaign_url}}", nil},
		{"dotted text that is not a domain", "v2.0 çıktı, saat 9.00-18.00. A.Ş. ve Ltd. Şti. No.5, bekle...gel. Bitti.",
			"T.C. onaylı", "Örn. 3.5 kat, a.m. ve p.m. Son.\nYeni satır.", nil},
		{"exactly 160 rendered", r("a", 140) + "{{first_name}}", "s", "b", nil},

		{"password variable", "Şifren: {{password}}", "s", "b", []string{"/sms/body unknown_variable"}},
		{"email variable", "{{email}}", "s", "b", []string{"/sms/body unknown_variable"}},
		{"phone variable in subject", "x", "{{phone}}", "b", []string{"/email/subject unknown_variable"}},
		{"filter", "{{first_name|upper}}", "s", "b", []string{"/sms/body malformed_template"}},
		{"unclosed braces", "Merhaba {{first_name", "s", "b", []string{"/sms/body malformed_template"}},

		{"https link", "Tıkla: https://evil.example/x", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"uppercase scheme", "HTTP://X.COM", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"www", "www.insider.com", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"bare .com.tr domain", "insider.com.tr/indirim adresine gel", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"shortener", "bit.ly/abc", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"Cyrillic lookalike domain", "іnsider.com", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"domain built around a variable", "{{city}}.com", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"IP address", "Gir: 192.168.1.1", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"link in subject", "x", "Fırsat: shop.example.com", "b", []string{"/email/subject url_not_allowed"}},
		{"domain with an unlisted TLD and a path", "evil.top/login", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"bare domain with an unlisted TLD", "Visit evil.click", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"bare .top domain", "Visit evil.top", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"unlisted domain with only a query", "Visit evil.zip?next=login", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"unlisted domain ending a sentence", "x", "s", "Gel: evil.click.", []string{"/email/body url_not_allowed"}},
		{"Turkish IDN", "örnek.şirket adresine gel", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"Cyrillic IDN", "x", "пример.рф", "b", []string{"/email/subject url_not_allowed"}},
		{"Japanese IDN", "例え.テスト", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"punycode domain", "xn--80akhbyknj4f.xn--p1ai", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"no space after a sentence period", "Kaçırma.Hemen gel", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"unlisted shortener", "Gel: is.gd/abc", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"userinfo after the link", "{{campaign_url}}@evil.top", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"subdomain after the link", "{{campaign_url}}.evil.com", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"hyphen after the link", "{{campaign_url}}-evil.com", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"path after the link", "x", "s", "{{campaign_url}}/kampanya", []string{"/email/body url_not_allowed"}},
		{"query after the link", "{{campaign_url}}?next=x", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"port after the link", "{{campaign_url}}:8080", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"dot then text after the link", "{{campaign_url}}.Hemen gel", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"variable after the link", "{{campaign_url}}{{city}}", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"zero-width space after the link", "{{campaign_url}}\u200b.evil.com", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"text before the link", "evil@{{campaign_url}}", "s", "b", []string{"/sms/body url_not_allowed"}},
		{"email address", "Yaz: info@insider.com", "s", "b", []string{"/sms/body email_not_allowed"}},
		{"HTML in email body", "x", "s", "<b>Son gün!</b>", []string{"/email/body html_not_allowed"}},
		{"script tag", "x", "s", "<script>alert(1)</script>", []string{"/email/body html_not_allowed"}},

		{"raw SMS over 160", r("a", 161), "s", "b", []string{"/sms/body too_long"}},
		{"rendered SMS over 160", r("a", 145) + "{{first_name}}", "s", "b", []string{"/sms/body rendered_too_long"}},
		{"rendered subject over 80", "x", r("a", 61) + "{{first_name}}", "b", []string{"/email/subject rendered_too_long"}},
		{"email body over 3000", "x", "s", r("a", 3001), []string{"/email/body too_long"}},
		{"empty SMS", "", "s", "b", []string{"/sms/body too_short"}},
		{"every problem is reported", "{{password}}", "www.x.com", "<i>x</i>",
			[]string{"/email/body html_not_allowed", "/email/subject url_not_allowed", "/sms/body unknown_variable"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, res := ValidateJSON(set, contentJSON(c.sms, c.subject, c.body))
			if got := codes(res.Errors); !slices.Equal(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestValidateStructure(t *testing.T) {
	set, _ := schema.Load()
	_, res := ValidateJSON(set, []byte(`{"sms":{"body":"x"},"html":"<b>x</b>"}`))
	if got := codes(res.Errors); !slices.Equal(got, []string{"/email required", "/html unknown_property"}) {
		t.Errorf("got %q", got)
	}
	_, res = ValidateJSON(set, []byte(`{"sms":`))
	if got := codes(res.Errors); !slices.Equal(got, []string{" invalid_json"}) {
		t.Errorf("got %q", got)
	}
}

func TestValidateSMSWarnings(t *testing.T) {
	set, _ := schema.Load()
	r := strings.Repeat
	cases := []struct {
		name, sms   string
		wantWarning bool
		contains    string
	}{
		{"short ASCII, no variables", "Sale today", false, ""},
		{"160 GSM, no variables", r("a", 160), false, ""},
		{"150-character Turkish SMS", r("ş", 150), true, "UCS-2, 3 segments"},
		{"names may switch to UCS-2", r("a", 100) + "{{first_name}}", true, "without them: GSM-7, 1 segment"},
	}
	for _, c := range cases {
		content, res := ValidateJSON(set, contentJSON(c.sms, "s", "b"))
		if !res.OK() {
			t.Fatalf("%s: unexpected errors %+v", c.name, res.Errors)
		}
		got := len(res.Warnings) == 1 && res.Warnings[0].Code == validation.SMSMultiSegment &&
			strings.Contains(res.Warnings[0].Message, c.contains)
		if got != c.wantWarning || (!c.wantWarning && len(res.Warnings) > 0) {
			t.Errorf("%s: warnings %+v", c.name, res.Warnings)
		}
		if c.name == "150-character Turkish SMS" && content.SMS != (SMSInfo{"UCS-2", 150, 3}) {
			t.Errorf("SMS info %+v", content.SMS)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"Merhaba {{first_name}}", "{{{x}}}", "}}{{", "{{ city }}{{campaign_url}}", "ş{{", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		parts, issues := Parse(s)
		if len(issues) > 0 {
			return
		}
		for _, p := range parts {
			if p.Var == "" && p.Text == "" {
				t.Fatal("empty part")
			}
			if p.Var != "" && maxLen[p.Var] == 0 {
				t.Fatalf("variable %q outside the allowlist", p.Var)
			}
		}
	})
}
