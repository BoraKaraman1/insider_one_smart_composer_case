package schema

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

// Issues turns an error from Validate or ValidateDef into stable issues,
// sorted by path. base is prepended to every path, so a check of one
// condition can report /conditions/2/... paths.
func Issues(err error, base string) []validation.Issue {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []validation.Issue{{Path: base, Code: validation.SchemaViolation, Message: err.Error()}}
	}
	var out []validation.Issue
	collect(ve, base, &out)
	slices.SortFunc(out, func(a, b validation.Issue) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Code, b.Code), cmp.Compare(a.Message, b.Message))
	})
	return slices.Compact(out)
}

// collect walks to the leaf errors; inner nodes only group their causes.
func collect(e *jsonschema.ValidationError, base string, out *[]validation.Issue) {
	if len(e.Causes) > 0 {
		for _, c := range e.Causes {
			collect(c, base, out)
		}
		return
	}
	path := base + Pointer(e.InstanceLocation)
	add := func(p, code, msg string) {
		*out = append(*out, validation.Issue{Path: p, Code: code, Message: msg})
	}
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		for _, m := range k.Missing {
			add(path+"/"+escape(m), validation.Required, fmt.Sprintf("missing required key %q", m))
		}
	case *kind.AdditionalProperties:
		for _, p := range k.Properties {
			add(path+"/"+escape(p), validation.UnknownProperty, fmt.Sprintf("key %q is not allowed", p))
		}
	case *kind.Type:
		add(path, validation.InvalidType, fmt.Sprintf("expected %s, got %s", strings.Join(k.Want, " or "), k.Got))
	case *kind.Enum:
		want := make([]string, len(k.Want))
		for i, w := range k.Want {
			want[i] = show(w)
		}
		add(path, validation.UnknownValue, fmt.Sprintf("%s is not allowed; use one of: %s", show(k.Got), strings.Join(want, ", ")))
	case *kind.Const:
		add(path, validation.UnknownValue, fmt.Sprintf("%s is not allowed; must be %s", show(k.Got), show(k.Want)))
	case *kind.Minimum:
		add(path, validation.OutOfRange, fmt.Sprintf("must be at least %s", k.Want.RatString()))
	case *kind.ExclusiveMinimum:
		add(path, validation.OutOfRange, fmt.Sprintf("must be greater than %s", k.Want.RatString()))
	case *kind.Maximum:
		add(path, validation.OutOfRange, fmt.Sprintf("must be at most %s", k.Want.RatString()))
	case *kind.ExclusiveMaximum:
		add(path, validation.OutOfRange, fmt.Sprintf("must be less than %s", k.Want.RatString()))
	case *kind.MinItems:
		add(path, validation.TooFew, fmt.Sprintf("needs at least %d items, has %d", k.Want, k.Got))
	case *kind.MaxItems:
		add(path, validation.TooMany, fmt.Sprintf("allows at most %d items, has %d", k.Want, k.Got))
	case *kind.UniqueItems:
		add(path, validation.DuplicateValue, fmt.Sprintf("items %d and %d are the same", k.Duplicates[0], k.Duplicates[1]))
	case *kind.MinLength:
		add(path, validation.TooShort, fmt.Sprintf("must be at least %d characters, has %d", k.Want, k.Got))
	case *kind.MaxLength:
		add(path, validation.TooLong, fmt.Sprintf("must be at most %d characters, has %d", k.Want, k.Got))
	default:
		add(path, validation.SchemaViolation, e.Error())
	}
}

// Pointer renders an instance location as a JSON Pointer ("" for the root).
func Pointer(loc []string) string {
	var b strings.Builder
	for _, tok := range loc {
		b.WriteString("/")
		b.WriteString(escape(tok))
	}
	return b.String()
}

func escape(tok string) string {
	return strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1")
}

func show(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprint(v)
}
