// Paket kuralı doğrular ve sorguya derler.
package segment

import "slices"

const (
	MaxConditions      = 10
	MaxListValues      = 20
	WithinDaysMin      = 1
	WithinDaysMax      = 365
	DaysSinceSignupMax = 3650
)

// Şehir değerleri yalın harftir ki Türkçe büyük harf karşılaştırmayı değiştirmesin.
var enumValues = map[string][]string{
	"city":         {"istanbul", "ankara", "izmir", "bursa", "antalya", "adana", "konya", "gaziantep", "kocaeli", "eskisehir"},
	"language":     {"tr", "en"},
	"loyalty_tier": {"bronze", "silver", "gold"},
}

var (
	enumFields = []string{"city", "language", "loyalty_tier"}
	eventTypes = []string{"cart_add", "purchase", "page_view"}
	singleOps  = []string{"eq", "neq"}
	listOps    = []string{"in", "not_in"}
	numberOps  = []string{"eq", "neq", "gt", "gte", "lt", "lte"}
)

func EnumFields() []string { return slices.Clone(enumFields) }

func EnumValues(field string) []string { return slices.Clone(enumValues[field]) }

func EventTypes() []string { return slices.Clone(eventTypes) }

func SingleOps() []string { return slices.Clone(singleOps) }

func ListOps() []string { return slices.Clone(listOps) }

func NumberOps() []string { return slices.Clone(numberOps) }

var cityNames = map[string]string{
	"istanbul": "İstanbul", "ankara": "Ankara", "izmir": "İzmir", "bursa": "Bursa", "antalya": "Antalya",
	"adana": "Adana", "konya": "Konya", "gaziantep": "Gaziantep", "kocaeli": "Kocaeli", "eskisehir": "Eskişehir",
}

func CityDisplayName(slug string) string { return cityNames[slug] }
