// Paket doğrulayıcıların ortak sonuç tipini tutar ve kod adları değişmez.
package validation

const (
	InvalidJSON     = "invalid_json"
	Required        = "required"
	UnknownProperty = "unknown_property"
	InvalidType     = "invalid_type"
	UnknownValue    = "unknown_value"
	OutOfRange      = "out_of_range"
	TooFew          = "too_few"
	TooMany         = "too_many"
	DuplicateValue  = "duplicate_value"
	TooShort        = "too_short"
	TooLong         = "too_long"
	SchemaViolation = "schema_violation"
)

const (
	OpNotAllowed    = "op_not_allowed"
	OpValueMismatch = "op_value_mismatch"
)

const (
	UnknownVariable   = "unknown_variable"
	MalformedTemplate = "malformed_template"
	URLNotAllowed     = "url_not_allowed"
	EmailNotAllowed   = "email_not_allowed"
	HTMLNotAllowed    = "html_not_allowed"
	RenderedTooLong   = "rendered_too_long"
)

const (
	SMSMultiSegment = "sms_multi_segment"
)

type Issue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Result struct {
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
}

func (r Result) OK() bool { return len(r.Errors) == 0 }

func (r *Result) Error(path, code, message string) {
	r.Errors = append(r.Errors, Issue{Path: path, Code: code, Message: message})
}

func (r *Result) Warn(path, code, message string) {
	r.Warnings = append(r.Warnings, Issue{Path: path, Code: code, Message: message})
}
