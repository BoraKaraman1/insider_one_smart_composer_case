package mockllm

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

var cities = []string{"istanbul", "ankara", "izmir", "bursa", "antalya", "adana", "konya", "gaziantep", "kocaeli", "eskisehir"}

var (
	briefRe    = regexp.MustCompile(`(?s)<brief>(.*?)</brief>`)
	daysRe     = regexp.MustCompile(`(?:son|last)\s+(\d{1,3})\s*(?:gun|day)`)
	discountRe = regexp.MustCompile(`%\s?(\d{1,2})|(\d{1,2})\s?%`)
)

var (
	cartWords        = []string{"sepet", "cart"}
	notPurchaseWords = []string{"almayan", "almadi", "yapmayan", "satin alma", "not purchase", "not buy",
		"didn't buy", "didn't purchase", "without purchas", "no purchase"}
)

var fold = strings.NewReplacer("İ", "i", "I", "i", "ı", "i", "Ş", "s", "ş", "s", "Ğ", "g", "ğ", "g",
	"Ü", "u", "ü", "u", "Ö", "o", "ö", "o", "Ç", "c", "ç", "c")

func brain(stage Stage, body map[string]any, o Options) Reply {
	msgs, _ := body["messages"].([]any)
	first := ""
	if len(msgs) > 0 {
		first = turnText(msgs[0])
	}
	brief := first
	if m := briefRe.FindStringSubmatch(first); m != nil {
		brief = m[1]
	}
	brief = strings.ToLower(fold.Replace(brief))
	if o.InvalidFirst && len(msgs) == 1 {
		if stage == Content {
			return Reply{Text: `{"sms":{"body":"Merhaba {{email}}"},"email":{"subject":"Sepet"}}`}
		}
		return Reply{Text: `{"version":1,"match":"all","conditions":[{"kind":"event","event":"cart_add","within_days":0,"negate":false}]}`}
	}
	if stage == Content {
		return Reply{Text: copyFor(brief)}
	}
	return Reply{Text: ruleFor(brief)}
}

func turnText(m any) string {
	mm, _ := m.(map[string]any)
	switch c := mm["content"].(type) {
	case string:
		return c
	case []any:
		var sb strings.Builder
		for _, b := range c {
			bm, _ := b.(map[string]any)
			if s, ok := bm["text"].(string); ok {
				sb.WriteString(s)
			}
		}
		return sb.String()
	}
	return ""
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func ruleFor(brief string) string {
	days := 30
	if m := daysRe.FindStringSubmatch(brief); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= 365 {
			days = n
		}
	}
	var conds []map[string]any
	for _, c := range cities {
		if strings.Contains(brief, c) {
			conds = append(conds, map[string]any{"kind": "enum_attribute", "field": "city", "op": "eq", "value": c})
			break
		}
	}
	if containsAny(brief, cartWords) {
		conds = append(conds, map[string]any{"kind": "event", "event": "cart_add", "within_days": days, "negate": false})
	}
	if containsAny(brief, notPurchaseWords) {
		conds = append(conds, map[string]any{"kind": "event", "event": "purchase", "within_days": days, "negate": true})
	}
	if len(conds) == 0 {
		conds = append(conds, map[string]any{"kind": "event", "event": "page_view", "within_days": days, "negate": false})
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "match": "all", "conditions": conds})
	return string(b)
}

func copyFor(brief string) string {
	offer := "Sana özel bir fırsat var"
	if m := discountRe.FindStringSubmatch(brief); m != nil {
		offer = "Sana özel %" + m[1] + m[2] + " indirim var"
	}
	sms := "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! " + offer + ": {{campaign_url}}"
	subject := "Sepetin seni bekliyor"
	body := "Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. " + offer +
		".\n\nAlışverişini tamamla: {{campaign_url}}\n\nSevgiler"
	b, _ := json.Marshal(map[string]any{"sms": map[string]any{"body": sms}, "email": map[string]any{"subject": subject, "body": body}})
	return string(b)
}
