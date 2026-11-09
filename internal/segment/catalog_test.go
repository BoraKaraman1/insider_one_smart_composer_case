package segment

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/schemas"
)

// TestCatalogMatchesSchema fails when the Go catalog and the JSON Schema
// disagree, so neither can change without the other.
func TestCatalogMatchesSchema(t *testing.T) {
	b, err := schemas.FS.ReadFile("segment_rule.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	defs := at(t, doc, "$defs")

	if got := num(t, at(t, doc, "properties", "conditions", "maxItems")); got != MaxConditions {
		t.Errorf("conditions maxItems = %d, catalog MaxConditions = %d", got, MaxConditions)
	}
	eqStrings(t, "single_op", strs(t, at(t, defs, "single_op", "enum")), SingleOps())
	eqStrings(t, "list_op", strs(t, at(t, defs, "list_op", "enum")), ListOps())

	wantBranches := []string{}
	for _, f := range EnumFields() {
		eqStrings(t, f+" values", strs(t, at(t, defs, f, "enum")), EnumValues(f))

		single, list := f+"_single", f+"_list"
		wantBranches = append(wantBranches, single, list)
		for _, branch := range []string{single, list} {
			if got := at(t, defs, branch, "properties", "kind", "const"); got != "enum_attribute" {
				t.Errorf("%s kind = %v", branch, got)
			}
			if got := at(t, defs, branch, "properties", "field", "const"); got != f {
				t.Errorf("%s field = %v, want %s", branch, got, f)
			}
		}
		if got := at(t, defs, single, "properties", "op", "$ref"); got != "#/$defs/single_op" {
			t.Errorf("%s op = %v", single, got)
		}
		if got := at(t, defs, single, "properties", "value", "$ref"); got != "#/$defs/"+f {
			t.Errorf("%s value = %v", single, got)
		}
		if got := at(t, defs, list, "properties", "op", "$ref"); got != "#/$defs/list_op" {
			t.Errorf("%s op = %v", list, got)
		}
		if got := at(t, defs, list, "properties", "value", "items", "$ref"); got != "#/$defs/"+f {
			t.Errorf("%s items = %v", list, got)
		}
		if got := num(t, at(t, defs, list, "properties", "value", "maxItems")); got != MaxListValues {
			t.Errorf("%s maxItems = %d, catalog MaxListValues = %d", list, got, MaxListValues)
		}
	}

	eqStrings(t, "days_since_signup ops", strs(t, at(t, defs, "days_since_signup", "properties", "op", "enum")), NumberOps())
	if got := num(t, at(t, defs, "days_since_signup", "properties", "value", "minimum")); got != 0 {
		t.Errorf("days_since_signup minimum = %d, want 0", got)
	}
	if got := num(t, at(t, defs, "days_since_signup", "properties", "value", "maximum")); got != DaysSinceSignupMax {
		t.Errorf("days_since_signup maximum = %d, catalog = %d", got, DaysSinceSignupMax)
	}

	eqStrings(t, "event types", strs(t, at(t, defs, "event", "properties", "event", "enum")), EventTypes())
	if got := num(t, at(t, defs, "event", "properties", "within_days", "minimum")); got != WithinDaysMin {
		t.Errorf("within_days minimum = %d, catalog = %d", got, WithinDaysMin)
	}
	if got := num(t, at(t, defs, "event", "properties", "within_days", "maximum")); got != WithinDaysMax {
		t.Errorf("within_days maximum = %d, catalog = %d", got, WithinDaysMax)
	}

	// The condition union lists exactly one branch per (enum field, op group),
	// plus days_since_signup and event. No branch is missing or extra.
	wantBranches = append(wantBranches, "days_since_signup", "event")
	var gotBranches []string
	for _, item := range at(t, defs, "condition", "anyOf").([]any) {
		ref, _ := item.(map[string]any)["$ref"].(string)
		gotBranches = append(gotBranches, ref[len("#/$defs/"):])
	}
	slices.Sort(gotBranches)
	slices.Sort(wantBranches)
	eqStrings(t, "condition branches", gotBranches, wantBranches)
}

func TestAccessorsReturnCopies(t *testing.T) {
	c := EnumValues("city")
	c[0] = "changed"
	if EnumValues("city")[0] == "changed" {
		t.Fatal("EnumValues exposes the catalog slice")
	}
	if EnumValues("email") != nil {
		t.Fatal("EnumValues returned values for a field outside the catalog")
	}
}

// at walks a decoded JSON document by object keys.
func at(t *testing.T, doc any, path ...string) any {
	t.Helper()
	cur := doc
	for i, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%v: element %d is not an object", path, i)
		}
		if cur, ok = m[k]; !ok {
			t.Fatalf("%v: missing key %q", path, k)
		}
	}
	return cur
}

func strs(t *testing.T, v any) []string {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("not an array: %v", v)
	}
	out := make([]string, len(list))
	for i, item := range list {
		if out[i], ok = item.(string); !ok {
			t.Fatalf("not a string: %v", item)
		}
	}
	return out
}

func num(t *testing.T, v any) int {
	t.Helper()
	f, ok := v.(float64)
	if !ok || f != float64(int(f)) {
		t.Fatalf("not an integer: %v", v)
	}
	return int(f)
}

func eqStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: schema has %v, catalog has %v", what, got, want)
	}
}
