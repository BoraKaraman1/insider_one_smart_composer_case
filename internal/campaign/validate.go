package campaign

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/content"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/segment"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

const (
	MaxBriefChars = 2000
	MaxURLChars   = 40
)

type Revision struct {
	Segment    segment.Rule
	SegmentDoc any
	Content    content.Content
}

func ValidateRevision(set *schema.Set, doc any) (Revision, validation.Result) {
	var res validation.Result
	m, ok := doc.(map[string]any)
	if !ok {
		res.Error("", validation.InvalidType, `expected an object: {"segment": {...}, "content": {...}}`)
		return Revision{}, res
	}
	for k := range m {
		if k != "segment" && k != "content" {
			res.Error("/"+k, validation.UnknownProperty, fmt.Sprintf("key %q is not allowed", k))
		}
	}
	var rev Revision
	if seg, ok := m["segment"]; ok {
		rule, r := segment.Validate(set, seg)
		merge(&res, r, "/segment")
		rev.Segment, rev.SegmentDoc = rule, seg
	} else {
		res.Error("/segment", validation.Required, `missing required key "segment"`)
	}
	if c, ok := m["content"]; ok {
		cont, r := content.Validate(set, c)
		merge(&res, r, "/content")
		rev.Content = cont
	} else {
		res.Error("/content", validation.Required, `missing required key "content"`)
	}
	if !res.OK() {
		return Revision{}, res
	}
	return rev, res
}

func merge(dst *validation.Result, src validation.Result, prefix string) {
	for _, is := range src.Errors {
		dst.Error(prefix+is.Path, is.Code, is.Message)
	}
	for _, is := range src.Warnings {
		dst.Warn(prefix+is.Path, is.Code, is.Message)
	}
}

type New struct {
	Brief       string
	CampaignURL string
}

func ValidateNew(doc any, hosts []string) (New, validation.Result) {
	var res validation.Result
	m, ok := doc.(map[string]any)
	if !ok {
		res.Error("", validation.InvalidType, `expected an object: {"brief": "...", "campaign_url": "..."}`)
		return New{}, res
	}
	for k := range m {
		if k != "brief" && k != "campaign_url" {
			res.Error("/"+k, validation.UnknownProperty, fmt.Sprintf("key %q is not allowed", k))
		}
	}
	var n New
	switch v, ok := m["brief"]; {
	case !ok:
		res.Error("/brief", validation.Required, `missing required key "brief"`)
	default:
		s, isString := v.(string)
		switch chars := utf8.RuneCountInString(s); {
		case !isString:
			res.Error("/brief", validation.InvalidType, "expected a string")
		case chars < 1:
			res.Error("/brief", validation.TooShort, "must be at least 1 character")
		case chars > MaxBriefChars:
			res.Error("/brief", validation.TooLong, fmt.Sprintf("must be at most %d characters, has %d", MaxBriefChars, chars))
		default:
			n.Brief = s
		}
	}
	switch v, ok := m["campaign_url"]; {
	case !ok:
		res.Error("/campaign_url", validation.Required, `missing required key "campaign_url"`)
	default:
		s, isString := v.(string)
		if !isString {
			res.Error("/campaign_url", validation.InvalidType, "expected a string")
			break
		}
		if chars := utf8.RuneCountInString(s); chars > MaxURLChars {
			res.Error("/campaign_url", validation.TooLong, fmt.Sprintf("must be at most %d characters, has %d", MaxURLChars, chars))
		}
		if !allowedURL(s, hosts) {
			res.Error("/campaign_url", validation.URLNotAllowed, "must be an https URL on an allowed host: "+strings.Join(hosts, ", "))
		}
		n.CampaignURL = s
	}
	if !res.OK() {
		return New{}, res
	}
	return n, res
}

func allowedURL(s string, hosts []string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return false
	}
	return slices.Contains(hosts, strings.ToLower(u.Host)) // kapı numarası da yasak, adres olduğu gibi bakılır
}
