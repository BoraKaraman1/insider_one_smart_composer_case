package campaign

import (
	"strings"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

func TestCheckSubstance(t *testing.T) {
	good := content.Content{
		SMSBody:      "Merhaba {{first_name}}, sepetin seni bekliyor",
		EmailSubject: "Sepetin seni bekliyor",
		EmailBody:    "Merhaba {{first_name}}, ürünler hâlâ seni bekliyor.",
	}
	cases := []struct {
		name string
		c    content.Content
		want []string
	}{
		{"real copy", good, nil},
		{"placeholder x everywhere", content.Content{SMSBody: "x", EmailSubject: "x", EmailBody: "x"},
			[]string{"/sms/body", "/email/subject", "/email/body"}},
		{"only the subject is a placeholder", content.Content{SMSBody: good.SMSBody, EmailSubject: "x", EmailBody: good.EmailBody},
			[]string{"/email/subject"}},
		{"Turkish letters count as one character each", content.Content{SMSBody: strings.Repeat("ş", MinSMSChars),
			EmailSubject: strings.Repeat("ğ", MinSubjectChars), EmailBody: strings.Repeat("ı", MinBodyChars)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var res validation.Result
			checkSubstance(c.c, &res)
			var got []string
			for _, e := range res.Errors {
				if e.Code != validation.TooShort {
					t.Errorf("code %q, want too_short", e.Code)
				}
				got = append(got, e.Path)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("paths %v, want %v", got, c.want)
			}
		})
	}
}
