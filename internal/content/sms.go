package content

import (
	"strings"
	"unicode/utf16"
)

// GSM 03.38 default alphabet (3GPP TS 23.038), without the escape code.
// The order follows the standard table, row by row.
const gsmBasic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"

// GSM 03.38 extension table. Each of these costs two septets (escape + char).
const gsmExtension = "\f^{}\\[~]|€"

// Segment sizes from the spec: a single SMS, and each part of a split SMS.
const (
	gsmSingle  = 160
	gsmPart    = 153
	ucs2Single = 70
	ucs2Part   = 67
)

// SMSInfo describes how an SMS body would be sent.
type SMSInfo struct {
	Encoding string `json:"encoding"` // "GSM-7" or "UCS-2"
	Units    int    `json:"units"`    // septets for GSM-7, UTF-16 code units for UCS-2
	Segments int    `json:"segments"`
}

// MeasureSMS picks the encoding and counts segments. One character outside
// GSM-7 switches the whole message to UCS-2. This is an estimate: real
// encoders never split an escape pair or a surrogate pair across parts,
// which can add a segment in rare cases.
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
