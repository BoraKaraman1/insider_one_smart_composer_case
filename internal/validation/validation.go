// Package validation holds the result type every validator returns. The
// codes are stable: API clients, the LLM retry prompt and metric labels all
// depend on them, so add codes but never rename one.
package validation

// Codes for structural problems, mapped from JSON Schema keywords.
const (
	InvalidJSON     = "invalid_json"     // not exactly one JSON value
	Required        = "required"         // a required key is missing
	UnknownProperty = "unknown_property" // a key the schema does not define
	InvalidType     = "invalid_type"     // wrong JSON type
	UnknownValue    = "unknown_value"    // value outside an enum or const
	OutOfRange      = "out_of_range"     // number outside its minimum/maximum
	TooFew          = "too_few"          // array below minItems
	TooMany         = "too_many"         // array above maxItems
	DuplicateValue  = "duplicate_value"  // repeated array item
	TooShort        = "too_short"        // string below minLength
	TooLong         = "too_long"         // string above maxLength
	SchemaViolation = "schema_violation" // any other schema failure
)

// Codes for segment rules (docs/segment-rules.md).
const (
	OpNotAllowed    = "op_not_allowed"    // operator exists, but not for this field
	OpValueMismatch = "op_value_mismatch" // list operator with one value, or the reverse
)

// Codes for content (SMS and email templates).
const (
	UnknownVariable   = "unknown_variable"   // {{name}} outside the allowlist
	MalformedTemplate = "malformed_template" // broken braces, filters, nesting
	URLNotAllowed     = "url_not_allowed"    // links come only from {{campaign_url}}
	EmailNotAllowed   = "email_not_allowed"  // no email addresses in copy
	HTMLNotAllowed    = "html_not_allowed"   // copy is plain text
	RenderedTooLong   = "rendered_too_long"  // worst-case rendered text over the limit
)

// Warning codes. Warnings never block approval.
const (
	SMSMultiSegment = "sms_multi_segment" // the SMS may need more than one segment
)

// Issue is one problem. Path is a JSON Pointer into the validated document
// ("" is the document itself).
type Issue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Result collects every problem found, not just the first.
type Result struct {
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
}

// OK reports whether there are no errors. Warnings do not count.
func (r Result) OK() bool { return len(r.Errors) == 0 }

// Error records an error.
func (r *Result) Error(path, code, message string) {
	r.Errors = append(r.Errors, Issue{Path: path, Code: code, Message: message})
}

// Warn records a warning.
func (r *Result) Warn(path, code, message string) {
	r.Warnings = append(r.Warnings, Issue{Path: path, Code: code, Message: message})
}
