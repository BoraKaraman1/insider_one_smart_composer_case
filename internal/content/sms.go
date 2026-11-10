package content

import (
	"strings"
	"unicode/utf16"
)

const gsmBasic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"

const gsmExtension = "\f^{}\\[~]|€"

const (
	gsmSingle  = 160
	gsmPart    = 153
	ucs2Single = 70
	ucs2Part   = 67
)

type SMSInfo struct {
	Encoding string `json:"encoding"`
	Units    int    `json:"units"`
	Segments int    `json:"segments"`
}

// Alfabede olmayan tek karakter tüm iletiyi geniş koda çevirir.
func MeasureSMS(text string) SMSInfo {
	septets, gsm := 0, true
	for _, r := range text {
		switch {
		case strings.ContainsRune(gsmBasic, r):
			septets++
		case strings.ContainsRune(gsmExtension, r):
			septets += 2
		default:
			gsm = false
		}
	}
	if gsm {
		return SMSInfo{Encoding: "GSM-7", Units: septets, Segments: segments(septets, gsmSingle, gsmPart)}
	}
	units := len(utf16.Encode([]rune(text)))
	return SMSInfo{Encoding: "UCS-2", Units: units, Segments: segments(units, ucs2Single, ucs2Part)}
}

func segments(units, single, part int) int {
	if units <= single {
		return 1
	}
	return (units + part - 1) / part
}
