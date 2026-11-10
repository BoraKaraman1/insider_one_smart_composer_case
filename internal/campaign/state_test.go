package campaign

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

var allowed = map[State][]State{
	Draft:            {Generating},
	Generating:       {InReview, GenerationFailed, Cancelled},
	GenerationFailed: {Generating, Cancelled},
	InReview:         {InReview, Generating, Approved, Rejected, Cancelled},
	Approved:         {Sending, Cancelled},
	Sending:          {Completed, CompletedWithFailures, Cancelled},
}

func TestStates(t *testing.T) {
	want := []string{"draft", "generating", "in_review", "generation_failed", "approved",
		"sending", "completed", "completed_with_failures", "rejected", "cancelled"}
	var got []string
	for _, s := range States() {
		got = append(got, string(s))
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("States() = %v, want %v", got, want)
	}
}

func TestTransitionMatrix(t *testing.T) {
	for _, from := range States() {
		for _, to := range States() {
			want := slices.Contains(allowed[from], to)
			if got := CanTransition(from, to); got != want {
				t.Errorf("%s -> %s: allowed = %v, want %v", from, to, got, want)
			}
		}
	}
	for _, terminal := range []State{Completed, CompletedWithFailures, Rejected, Cancelled} {
		for _, to := range States() {
			if CanTransition(terminal, to) {
				t.Errorf("terminal state %s can move to %s", terminal, to)
			}
		}
	}
	if CanTransition("paused", Generating) || CanTransition(Draft, "paused") || CanTransition("", "") {
		t.Error("an unknown state is accepted")
	}
}

func schemas(t *testing.T) *schema.Set {
	t.Helper()
	set, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func decode(t *testing.T, s string) any {
	t.Helper()
	doc, err := schema.Decode(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func pathCodes(list []validation.Issue) []string {
	var out []string
	for _, is := range list {
		out = append(out, is.Path+" "+is.Code)
	}
	slices.Sort(out)
	return out
}

const (
	validSegment = `{"version":1,"match":"all","conditions":[{"kind":"enum_attribute","field":"city","op":"eq","value":"istanbul"}]}`
	validContent = `{"sms":{"body":"Selam {{first_name}}, indirim burada: {{campaign_url}}"},"email":{"subject":"Sepetin seni bekliyor","body":"Selam {{first_name}}, indirim burada: {{campaign_url}}"}}`
)

func TestValidateRevisionValid(t *testing.T) {
	rev, res := ValidateRevision(schemas(t), decode(t, `{"segment":`+validSegment+`,"content":`+validContent+`}`))
	if !res.OK() {
		t.Fatalf("valid revision rejected: %+v", res.Errors)
	}
	if rev.Segment.Match != "all" || len(rev.Segment.Conditions) != 1 || rev.Segment.Conditions[0].Value != "istanbul" {
		t.Errorf("segment %+v", rev.Segment)
	}
	if rev.Content.EmailSubject != "Sepetin seni bekliyor" || !strings.Contains(rev.Content.SMSBody, "{{campaign_url}}") {
		t.Errorf("content %+v", rev.Content)
	}
	if got := pathCodes(res.Warnings); !slices.Equal(got, []string{"/content/sms/body sms_multi_segment"}) {
		t.Errorf("warnings %v", got)
	}
}

func TestValidateRevisionNormalizesInPlace(t *testing.T) {
	doc := decode(t, `{"segment":{"version":1,"match":"ALL","conditions":[
		{"kind":"ENUM_ATTRIBUTE","field":"CITY","op":"IN","value":["Istanbul","ANKARA"]},
		{"kind":"Event","event":"CART_ADD","within_days":30,"negate":false}]},"content":`+validContent+`}`)
	rev, res := ValidateRevision(schemas(t), doc)
	if !res.OK() {
		t.Fatalf("uppercase enums rejected: %+v", res.Errors)
	}
	if rev.Segment.Match != "all" || !slices.Equal(rev.Segment.Conditions[0].Values, []string{"istanbul", "ankara"}) {
		t.Errorf("segment %+v", rev.Segment)
	}
	stored, err := json.Marshal(doc.(map[string]any)["segment"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.ToLower(string(stored)) != string(stored) {
		t.Errorf("the document to store still has upper case: %s", stored)
	}
}

func TestValidateRevisionReportsEachErrorOnce(t *testing.T) {
	doc := decode(t, `{
		"segment":{"version":1,"match":"all","conditions":[{"kind":"enum_attribute","field":"city","op":"eq","value":"paris"}]},
		"content":{"sms":{"body":"Hi {{password}}"},"email":{"subject":"s","body":"go to https://evil.example now"}}}`)
	_, res := ValidateRevision(schemas(t), doc)
	want := []string{
		"/content/email/body url_not_allowed",
		"/content/sms/body unknown_variable",
		"/segment/conditions/0/value unknown_value",
	}
	if got := pathCodes(res.Errors); !slices.Equal(got, want) {
		t.Errorf("errors %v, want %v", got, want)
	}
}

func TestValidateRevisionTopLevel(t *testing.T) {
	cases := []struct {
		name, doc string
		want      string
	}{
		{"not an object", `[]`, " invalid_type"},
		{"missing segment", `{"content":` + validContent + `}`, "/segment required"},
		{"missing content", `{"segment":` + validSegment + `}`, "/content required"},
		{"extra key", `{"segment":` + validSegment + `,"content":` + validContent + `,"sql":"1=1"}`, "/sql unknown_property"},
		{"consent as an extra key", `{"segment":` + validSegment + `,"content":` + validContent + `,"consent_sms":false}`, "/consent_sms unknown_property"},
	}
	for _, c := range cases {
		_, res := ValidateRevision(schemas(t), decode(t, c.doc))
		got := pathCodes(res.Errors)
		if !slices.Contains(got, c.want) {
			t.Errorf("%s: errors %v, want %q among them", c.name, got, c.want)
		}
		if len(slices.Compact(slices.Clone(got))) != len(got) {
			t.Errorf("%s: an error is reported twice: %v", c.name, got)
		}
	}
}
