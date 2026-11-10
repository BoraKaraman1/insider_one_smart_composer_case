package campaign

import (
	"fmt"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

// Modelin yer tutucu döndürmesini yakalar ("x" şemadan geçer). Yalnız model çıktısına uygulanır.
const (
	MinSMSChars     = 10
	MinSubjectChars = 5
	MinBodyChars    = 20
)

// checkSubstance, geçerli sayılmış metin çok kısaysa tekrar denemeye yol açan hata ekler.
func checkSubstance(c content.Content, res *validation.Result) {
	for _, f := range []struct {
		path, text string
		min        int
	}{
		{"/sms/body", c.SMSBody, MinSMSChars},
		{"/email/subject", c.EmailSubject, MinSubjectChars},
		{"/email/body", c.EmailBody, MinBodyChars},
	} {
		if n := utf8.RuneCountInString(f.text); n < f.min {
			res.Error(f.path, validation.TooShort, fmt.Sprintf(
				"has %d characters; write real copy of at least %d, not a placeholder", n, f.min))
		}
	}
}
