package segment

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

func mustSet(t *testing.T) *schema.Set {
	t.Helper()
	s, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rule(conds ...string) string {
	return `{"version":1,"match":"all","conditions":[` + strings.Join(conds, ",") + `]}`
}

const (
	cityEq  = `{"kind":"enum_attribute","field":"city","op":"eq","value":"istanbul"}`
	cartAdd = `{"kind":"event","event":"cart_add","within_days":30,"negate":false}`
)

func issues(res validation.Result) []string {
	out := make([]string, len(res.Errors))
	for i, is := range res.Errors {
		out[i] = is.Path + " " + is.Code
	}
	return out
}

func TestValidateExample(t *testing.T) {
	data, err := os.ReadFile("../../docs/examples/istanbul_cart_abandoners.json")
	if err != nil {
		t.Fatal(err)
	}
	got, res := ValidateJSON(mustSet(t), data)
	if !res.OK() {
		t.Fatalf("example rejected: %+v", res.Errors)
	}
	want := Rule{Match: "all", Conditions: []Condition{
		{Kind: KindEnum, Field: "city", Op: "eq", Value: "istanbul"},
		{Kind: KindEvent, Event: "cart_add", WithinDays: 30},
		{Kind: KindEvent, Event: "purchase", WithinDays: 30, Negate: true},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestValidateTypedValues(t *testing.T) {
	got, res := ValidateJSON(mustSet(t), []byte(`{"version":1,"match":"any","conditions":[
		{"kind":"enum_attribute","field":"city","op":"in","value":["istanbul","ankara"]},
		{"kind":"number_attribute","field":"days_since_signup","op":"gte","value":30.0}]}`))
	if !res.OK() {
		t.Fatalf("rejected: %+v", res.Errors)
	}
	want := Rule{Match: "any", Conditions: []Condition{
		{Kind: KindEnum, Field: "city", Op: "in", Values: []string{"istanbul", "ankara"}},
		{Kind: KindNumber, Field: "days_since_signup", Op: "gte", Number: 30},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestValidateNormalizesASCIICase(t *testing.T) {
	got, res := ValidateJSON(mustSet(t), []byte(`{"version":1,"match":"ALL","conditions":[
		{"kind":"Enum_Attribute","field":"City","op":"IN","value":["ISTANBUL","Ankara"]},
		{"kind":"EVENT","event":"Cart_Add","within_days":30,"negate":false}]}`))
	if !res.OK() {
		t.Fatalf("rejected: %+v", res.Errors)
	}
	if got.Match != "all" || !slices.Equal(got.Conditions[0].Values, []string{"istanbul", "ankara"}) || got.Conditions[1].Event != "cart_add" {
		t.Fatalf("not normalized: %+v", got)
	}
}

func TestValidateErrors(t *testing.T) {
	set := mustSet(t)
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = cartAdd
	}
	cases := []struct {
		name, json string
		want       []string // "path code", exactly, in path order
	}{
		{"missing version", `{"match":"all","conditions":[` + cityEq + `]}`, []string{"/version required"}},
		{"version 2", `{"version":2,"match":"all","conditions":[` + cityEq + `]}`, []string{"/version unknown_value"}},
		{"unknown match", `{"version":1,"match":"some","conditions":[` + cityEq + `]}`, []string{"/match unknown_value"}},
		{"no conditions", rule(), []string{"/conditions too_few"}},
		{"eleven conditions", rule(eleven...), []string{"/conditions too_many"}},
		{"extra top-level key", `{"version":1,"match":"all","conditions":[` + cityEq + `],"sql":"1=1"}`, []string{"/sql unknown_property"}},
		{"top level is an array", `[]`, []string{" invalid_type"}},
		{"condition is a string", rule(`"city = istanbul"`), []string{"/conditions/0 invalid_type"}},
		{"extra condition key", rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":"istanbul","raw":"OR 1=1"}`), []string{"/conditions/0/raw unknown_property"}},
		{"missing kind", rule(`{"field":"city","op":"eq","value":"istanbul"}`), []string{"/conditions/0/kind required"}},
		{"unknown kind", rule(`{"kind":"sql","field":"city","op":"eq","value":"istanbul"}`), []string{"/conditions/0/kind unknown_value"}},
		{"field outside catalog", rule(`{"kind":"enum_attribute","field":"email","op":"eq","value":"a@example.com"}`), []string{"/conditions/0/field unknown_value"}},
		{"consent is not a field", rule(`{"kind":"enum_attribute","field":"consent_sms","op":"eq","value":"false"}`), []string{"/conditions/0/field unknown_value"}},
		{"SQL in a value", rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":"x'; DROP TABLE users;--"}`), []string{"/conditions/0/value unknown_value"}},
		{"Turkish capital stays invalid", rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":"İSTANBUL"}`), []string{"/conditions/0/value unknown_value"}},
		{"gt on a city", rule(`{"kind":"enum_attribute","field":"city","op":"gt","value":"istanbul"}`), []string{"/conditions/0/op op_not_allowed"}},
		{"unknown operator", rule(`{"kind":"enum_attribute","field":"city","op":"like","value":"istanbul"}`), []string{"/conditions/0/op unknown_value"}},
		{"missing operator", rule(`{"kind":"enum_attribute","field":"city","value":"istanbul"}`), []string{"/conditions/0/op required"}},
		{"in with one string", rule(`{"kind":"enum_attribute","field":"city","op":"in","value":"istanbul"}`), []string{"/conditions/0/value op_value_mismatch"}},
		{"eq with a list", rule(`{"kind":"enum_attribute","field":"city","op":"eq","value":["istanbul"]}`), []string{"/conditions/0/value op_value_mismatch"}},
		{"in without a value", rule(`{"kind":"enum_attribute","field":"city","op":"in"}`), []string{"/conditions/0/value required"}},
		{"empty list", rule(`{"kind":"enum_attribute","field":"city","op":"in","value":[]}`), []string{"/conditions/0/value too_few"}},
		{"duplicate list values", rule(`{"kind":"enum_attribute","field":"city","op":"in","value":["istanbul","istanbul"]}`), []string{"/conditions/0/value duplicate_value"}},
		{"one bad list item", rule(`{"kind":"enum_attribute","field":"city","op":"in","value":["istanbul","paris"]}`), []string{"/conditions/0/value/1 unknown_value"}},
		{"city value on language", rule(`{"kind":"enum_attribute","field":"language","op":"eq","value":"istanbul"}`), []string{"/conditions/0/value unknown_value"}},
		{"number field outside catalog", rule(`{"kind":"number_attribute","field":"age","op":"gt","value":18}`), []string{"/conditions/0/field unknown_value"}},
		{"in on a number", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"in","value":30}`), []string{"/conditions/0/op op_not_allowed"}},
		{"negative days", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":-1}`), []string{"/conditions/0/value out_of_range"}},
		{"days over max", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":3651}`), []string{"/conditions/0/value out_of_range"}},
		{"fractional days", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":30.5}`), []string{"/conditions/0/value invalid_type"}},
		{"days as a string", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":"30"}`), []string{"/conditions/0/value invalid_type"}},
		{"days as a list", rule(`{"kind":"number_attribute","field":"days_since_signup","op":"eq","value":[30]}`), []string{"/conditions/0/value op_value_mismatch"}},
		{"within_days 0", rule(`{"kind":"event","event":"cart_add","within_days":0,"negate":false}`), []string{"/conditions/0/within_days out_of_range"}},
		{"within_days 366", rule(`{"kind":"event","event":"cart_add","within_days":366,"negate":false}`), []string{"/conditions/0/within_days out_of_range"}},
		{"missing negate", rule(`{"kind":"event","event":"cart_add","within_days":30}`), []string{"/conditions/0/negate required"}},
		{"negate as a string", rule(`{"kind":"event","event":"cart_add","within_days":30,"negate":"false"}`), []string{"/conditions/0/negate invalid_type"}},
		{"unknown event", rule(`{"kind":"event","event":"signup","within_days":30,"negate":false}`), []string{"/conditions/0/event unknown_value"}},
		{"only the bad condition is reported", rule(cityEq, `{"kind":"event","event":"cart_add","within_days":0,"negate":false}`), []string{"/conditions/1/within_days out_of_range"}},
		{"every problem is reported", `{"version":1,"match":"some","conditions":[` +
			`{"kind":"enum_attribute","field":"city","op":"gt","value":"istanbul"},` +
			`{"kind":"event","event":"cart_add","within_days":30}]}`,
			[]string{"/conditions/0/op op_not_allowed", "/conditions/1/negate required", "/match unknown_value"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, res := ValidateJSON(set, []byte(c.json))
			if got := issues(res); !slices.Equal(got, c.want) {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
			for _, is := range res.Errors {
				if is.Message == "" {
					t.Errorf("%s: empty message", is.Path)
				}
			}
		})
	}
}

func TestValidateInvalidJSON(t *testing.T) {
	for _, in := range []string{``, `{`, `{"version":1} {"version":1}`, `not json`} {
		_, res := ValidateJSON(mustSet(t), []byte(in))
		if got := issues(res); !slices.Equal(got, []string{" invalid_json"}) {
			t.Errorf("%q: got %q", in, got)
		}
	}
}
