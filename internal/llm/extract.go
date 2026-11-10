package llm

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Fields are the three copy fields of a content answer.
type Fields struct {
	SMSBody      string
	EmailSubject string
	EmailBody    string
}

// ExtractFields reads sms.body, email.subject and email.body from a content
// answer that may still be streaming. An unfinished string gives the part
// received so far; an escape that is cut off is left out until it is whole,
// so a value read from a prefix is always a prefix of the final value. Text
// that is not a JSON object gives empty fields. It never panics.
func ExtractFields(text string) Fields {
	p := &partial{s: text}
	var f Fields
	p.object(func(key string) bool {
		var targets map[string]*string
		switch key {
		case "sms":
			targets = map[string]*string{"body": &f.SMSBody}
		case "email":
			targets = map[string]*string{"subject": &f.EmailSubject, "body": &f.EmailBody}
		default:
			return false
		}
		p.ws()
		if !p.peek('{') {
			return false
		}
		p.object(func(inner string) bool {
			dst := targets[inner]
			p.ws()
			if dst == nil || !p.peek('"') {
				return false
			}
			*dst, _ = p.str()
			return true
		})
		return true
	})
	return f
}

// partial is a reader over JSON that may end anywhere. Every method stops at
// the end of the input or at the first byte it does not expect, and sets done.
type partial struct {
	s    string
	i    int
	done bool
}

func (p *partial) ws() {
	for p.i < len(p.s) && strings.IndexByte(" \t\r\n", p.s[p.i]) >= 0 {
		p.i++
	}
}

func (p *partial) peek(c byte) bool { return !p.done && p.i < len(p.s) && p.s[p.i] == c }

// object reads {"key": value, ...}. value is called with the reader just
// after the colon; it reads the value and returns true, or returns false to
// have the value skipped.
func (p *partial) object(value func(key string) bool) {
	p.ws()
	if !p.peek('{') {
		p.done = true
		return
	}
	p.i++
	for !p.done {
		p.ws()
		if p.peek('}') {
			p.i++
			return
		}
		if !p.peek('"') {
			p.done = true
			return
		}
		key, ok := p.str()
		if !ok {
			p.done = true
			return
		}
		p.ws()
		if !p.peek(':') {
			p.done = true
			return
		}
		p.i++
		if !value(key) {
			p.skip()
		}
		if p.done {
			return
		}
		p.ws()
		switch {
		case p.peek(','):
			p.i++
		case p.peek('}'):
			p.i++
			return
		default:
			p.done = true
		}
	}
}

// skip reads past one value of any type.
func (p *partial) skip() {
	p.ws()
	if p.done || p.i >= len(p.s) {
		p.done = true
		return
	}
	switch c := p.s[p.i]; c {
	case '"':
		if _, ok := p.str(); !ok {
			p.done = true
		}
	case '{':
		p.object(func(string) bool { return false })
	case '[':
		p.i++
		for !p.done {
			p.ws()
			if p.peek(']') {
				p.i++
				return
			}
			p.skip()
			p.ws()
			switch {
			case p.peek(','):
				p.i++
			case p.peek(']'):
				p.i++
				return
			default:
				p.done = true
			}
		}
	default: // number, true, false, null
		for p.i < len(p.s) && strings.IndexByte(",}] \t\r\n", p.s[p.i]) < 0 {
			p.i++
		}
		if p.i >= len(p.s) {
			p.done = true
		}
	}
}

// str reads a string starting at its opening quote. It returns the decoded
// text and whether the closing quote was seen. On a cut-off escape or rune it
// stops before it and marks the reader done.
func (p *partial) str() (string, bool) {
	p.i++ // opening quote
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return b.String(), true
		case c == '\\':
			r, n := p.escape()
			if n == 0 {
				p.done = true
				return b.String(), false
			}
			b.WriteRune(r)
			p.i += n
		case c < utf8.RuneSelf:
			b.WriteByte(c)
			p.i++
		default:
			if !utf8.FullRuneInString(p.s[p.i:]) {
				p.done = true
				return b.String(), false
			}
			r, n := utf8.DecodeRuneInString(p.s[p.i:])
			b.WriteRune(r) // invalid bytes become U+FFFD, as encoding/json does
			p.i += n
		}
	}
	p.done = true
	return b.String(), false
}

// escape decodes the escape at p.i. It returns 0 bytes when the escape is
// incomplete or invalid.
func (p *partial) escape() (rune, int) {
	rest := p.s[p.i:]
	if len(rest) < 2 {
		return 0, 0
	}
	switch rest[1] {
	case '"', '\\', '/':
		return rune(rest[1]), 2
	case 'b':
		return '\b', 2
	case 'f':
		return '\f', 2
	case 'n':
		return '\n', 2
	case 'r':
		return '\r', 2
	case 't':
		return '\t', 2
	case 'u':
		r, ok := hex4(rest[2:])
		if !ok {
			return 0, 0
		}
		if !utf16.IsSurrogate(r) {
			return r, 6
		}
		// A high surrogate needs its pair; wait until the second escape is whole.
		if r < 0xdc00 && len(rest) >= 12 && rest[6] == '\\' && rest[7] == 'u' {
			if lo, ok := hex4(rest[8:]); ok {
				if dec := utf16.DecodeRune(r, lo); dec != utf8.RuneError {
					return dec, 12
				}
				return utf8.RuneError, 6
			}
			return 0, 0
		}
		if r < 0xdc00 && len(rest) < 12 && strings.HasPrefix(`\u`, rest[6:min(len(rest), 8)]) {
			return 0, 0 // the pair may still arrive
		}
		return utf8.RuneError, 6 // a lone surrogate, as encoding/json decodes it
	}
	return 0, 0
}

func hex4(s string) (rune, bool) {
	if len(s) < 4 {
		return 0, false
	}
	var r rune
	for _, c := range []byte(s[:4]) {
		r <<= 4
		switch {
		case '0' <= c && c <= '9':
			r |= rune(c - '0')
		case 'a' <= c && c <= 'f':
			r |= rune(c - 'a' + 10)
		case 'A' <= c && c <= 'F':
			r |= rune(c - 'A' + 10)
		default:
			return 0, false
		}
	}
	return r, true
}
