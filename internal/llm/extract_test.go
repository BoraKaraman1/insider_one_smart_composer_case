package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// ExtractFields reads the three copy fields from an unfinished JSON answer,
// so the reviewer sees the copy as it streams. It must never show text that
// the final answer does not start with.
func TestExtractFields(t *testing.T) {
	cases := []struct {
		name, in string
		want     Fields
	}{
		{"empty", ``, Fields{}},
		{"before any value", `{"sms":{"bo`, Fields{}},
		{"key without a value yet", `{"sms":{"body":`, Fields{}},
		{"opening quote only", `{"sms":{"body":"`, Fields{}},
		{"unfinished value", `{"sms":{"body":"Mer`, Fields{SMSBody: "Mer"}},
		{"second field streaming", `{"sms":{"body":"Merhaba"},"email":{"subject":"Sep`, Fields{SMSBody: "Merhaba", EmailSubject: "Sep"}},
		{"complete", `{"sms":{"body":"a"},"email":{"subject":"b","body":"c"}}`, Fields{SMSBody: "a", EmailSubject: "b", EmailBody: "c"}},
		{"email first", `{"email":{"body":"c","subject":"b"},"sms":{"body":"a`, Fields{SMSBody: "a", EmailSubject: "b", EmailBody: "c"}},
		{"white space", "{\n  \"sms\" : {\n    \"body\" :  \"x y", Fields{SMSBody: "x y"}},
		{"escapes", `{"sms":{"body":"a\"b\\c\nd\/e\tf`, Fields{SMSBody: "a\"b\\c\nd/e\tf"}},
		{"escape cut after the backslash", `{"sms":{"body":"ab\`, Fields{SMSBody: "ab"}},
		{"unicode escape cut", `{"sms":{"body":"ab\u00`, Fields{SMSBody: "ab"}},
		{"unicode escape", `{"sms":{"body":"abç`, Fields{SMSBody: "abç"}},
		{"surrogate pair cut in half", `{"sms":{"body":"ab\ud83d`, Fields{SMSBody: "ab"}},
		{"surrogate pair cut in the second escape", `{"sms":{"body":"ab\ud83d\ude`, Fields{SMSBody: "ab"}},
		{"surrogate pair", `{"sms":{"body":"ab😀`, Fields{SMSBody: "ab😀"}},
		{"raw Turkish text", `{"sms":{"body":"Değerli müşterimiz, şimdi İstanbul`, Fields{SMSBody: "Değerli müşterimiz, şimdi İstanbul"}},
		{"key-like text inside a value", `{"sms":{"body":"say \"email\":{\"body\":\"x"},"email":{"subject":"s`,
			Fields{SMSBody: `say "email":{"body":"x`, EmailSubject: "s"}},
		{"body outside sms or email", `{"body":"x","subject":"y"`, Fields{}},
		{"unknown key first", `{"note":"hi","sms":{"body":"x`, Fields{SMSBody: "x"}},
		{"nested object before sms", `{"meta":{"sms":{"body":"no"}},"sms":{"body":"yes`, Fields{SMSBody: "yes"}},
		{"plain text", `Tabii! İşte kampanya metni: Merhaba`, Fields{}},
		{"value is not a string", `{"sms":{"body":42,"x":"y"`, Fields{}},
	}
	for _, c := range cases {
		if got := ExtractFields(c.in); got != c.want {
			t.Errorf("%s: ExtractFields(%q) = %+v, want %+v", c.name, c.in, got, c.want)
		}
	}
}

// FuzzExtractFieldsNeverPanics feeds arbitrary model output. Hostile output
// must never crash the stream.
func FuzzExtractFieldsNeverPanics(f *testing.F) {
	for _, s := range []string{``, `{`, `{"sms":{"body":"\`, `{"sms":{"body":"\u`, `{"sms":{"body":"\ud800\u`, `"}}}]]]`, `{"email":{"subject":"\uZZZZ`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		_ = ExtractFields(in)
	})
}

// FuzzExtractFieldsPrefix encodes three values in several ways and checks
// every prefix of the encoding: each field read from a prefix is a prefix of
// the final value, and the full text gives exactly the final value.
func FuzzExtractFieldsPrefix(f *testing.F) {
	f.Add("Merhaba {{first_name}}", "Sepetin seni bekliyor", "Satır 1\nSatır 2 \"tırnak\" \\ 😀", uint8(0))
	f.Add("", "", "", uint8(7))
	f.Add("\x00\x1f ", "<>&", "\xff\xfe", uint8(3))
	f.Fuzz(func(t *testing.T, sms, subject, body string, mode uint8) {
		text := encodeContent(sms, subject, body, mode)
		var final struct {
			SMS   struct{ Body string }          `json:"sms"`
			Email struct{ Subject, Body string } `json:"email"`
		}
		if err := json.Unmarshal([]byte(text), &final); err != nil {
			t.Fatalf("test encoder produced invalid JSON %q: %v", text, err)
		}
		want := Fields{SMSBody: final.SMS.Body, EmailSubject: final.Email.Subject, EmailBody: final.Email.Body}
		if got := ExtractFields(text); got != want {
			t.Fatalf("full text %q: got %+v, want %+v", text, got, want)
		}
		for i := range len(text) {
			got := ExtractFields(text[:i])
			if !strings.HasPrefix(want.SMSBody, got.SMSBody) ||
				!strings.HasPrefix(want.EmailSubject, got.EmailSubject) ||
				!strings.HasPrefix(want.EmailBody, got.EmailBody) {
				t.Fatalf("prefix %q gave %+v, which is not a prefix of %+v", text[:i], got, want)
			}
		}
	})
}

// encodeContent writes a content answer. The mode bits pick key order,
// indentation and whether every non-ASCII rune is written as a \u escape
// (with surrogate pairs), so the extractor sees the encodings a model may
// produce.
func encodeContent(sms, subject, body string, mode uint8) string {
	str := func(s string) string {
		if mode&4 == 0 {
			b, _ := json.Marshal(s)
			return string(b)
		}
		var sb strings.Builder
		sb.WriteByte('"')
		for _, r := range strings.ToValidUTF8(s, string(utf8.RuneError)) {
			switch {
			case r == '"' || r == '\\':
				sb.WriteByte('\\')
				sb.WriteRune(r)
			case r < 0x20 || r > 0x7e:
				if r > 0xffff {
					hi, lo := utf16.EncodeRune(r)
					fmt.Fprintf(&sb, `\u%04x\u%04x`, hi, lo)
				} else {
					fmt.Fprintf(&sb, `\u%04x`, r)
				}
			default:
				sb.WriteRune(r)
			}
		}
		sb.WriteByte('"')
		return sb.String()
	}
	sep := ":"
	if mode&8 != 0 {
		sep = " : "
	}
	smsObj := `{"body"` + sep + str(sms) + `}`
	email := `{"subject"` + sep + str(subject) + `,"body"` + sep + str(body) + `}`
	if mode&2 != 0 {
		email = `{"body"` + sep + str(body) + `,` + "\n  " + `"subject"` + sep + str(subject) + `}`
	}
	if mode&1 != 0 {
		return `{"email"` + sep + email + `,"sms"` + sep + smsObj + `}`
	}
	return `{"sms"` + sep + smsObj + `,"email"` + sep + email + `}`
}
