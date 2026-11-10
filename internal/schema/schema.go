// Paket gömülü şemaları yükler ve doğrular.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/schemas"
)

const BaseURL = "https://schemas.composer.local/"

type Name string

const (
	SegmentRule Name = "segment_rule.schema.json"
	Content     Name = "content.schema.json"
	Revision    Name = "revision.schema.json"
)

type Set struct {
	raw      map[Name]any
	compiled map[Name]*jsonschema.Schema
	defs     map[string]*jsonschema.Schema
}

func Load() (*Set, error) {
	files, err := fs.Glob(schemas.FS, "*.schema.json")
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	set := &Set{raw: map[Name]any{}, compiled: map[Name]*jsonschema.Schema{}, defs: map[string]*jsonschema.Schema{}}
	for _, f := range files {
		b, err := schemas.FS.ReadFile(f)
		if err != nil {
			return nil, err
		}
		doc, err := Decode(bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", f, err)
		}
		m, ok := doc.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema %s: not a JSON object", f)
		}
		if id, _ := m["$id"].(string); id != BaseURL+f {
			return nil, fmt.Errorf("schema %s: $id is %q, want %q", f, id, BaseURL+f)
		}
		if err := c.AddResource(BaseURL+f, doc); err != nil {
			return nil, fmt.Errorf("schema %s: %w", f, err)
		}
		set.raw[Name(f)] = doc
	}
	for name := range set.raw {
		s, err := c.Compile(BaseURL + string(name))
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", name, err)
		}
		set.compiled[name] = s

		defs, _ := set.raw[name].(map[string]any)["$defs"].(map[string]any)
		for def := range defs {
			key := string(name) + "#/$defs/" + def
			ds, err := c.Compile(BaseURL + key)
			if err != nil {
				return nil, fmt.Errorf("schema %s: %w", key, err)
			}
			set.defs[key] = ds
		}
	}
	return set, nil
}

func Decode(r io.Reader) (any, error) {
	return jsonschema.UnmarshalJSON(r)
}

func (s *Set) Validate(name Name, instance any) error {
	sch, ok := s.compiled[name]
	if !ok {
		return fmt.Errorf("schema: unknown schema %q", name)
	}
	return sch.Validate(instance)
}

func (s *Set) ValidateDef(name Name, def string, instance any) error {
	sch, ok := s.defs[string(name)+"#/$defs/"+def]
	if !ok {
		return fmt.Errorf("schema: unknown definition %s#/$defs/%s", name, def)
	}
	return sch.Validate(instance)
}

var unsupportedForAPI = map[string]bool{
	"$schema":          true,
	"$id":              true,
	"minLength":        true,
	"maxLength":        true,
	"minimum":          true,
	"maximum":          true,
	"exclusiveMinimum": true,
	"exclusiveMaximum": true,
	"multipleOf":       true,
	"maxItems":         true,
	"uniqueItems":      true,
}

func (s *Set) ForAPI(name Name) (map[string]any, error) {
	doc, ok := s.raw[name]
	if !ok {
		return nil, fmt.Errorf("schema: unknown schema %q", name)
	}
	out, err := reduce(doc, "")
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", name, err)
	}
	return out.(map[string]any), nil
}

func reduce(node any, path string) (any, error) {
	m, ok := node.(map[string]any)
	if !ok {
		return node, nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		p := path + "/" + k
		switch {
		case unsupportedForAPI[k]:
			// Modelin reddettiği sözcükler burada düşer.
			continue
		case k == "minItems":
			n, err := v.(json.Number).Int64()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p, err)
			}
			if n > 1 {
				continue
			}
			out[k] = v
		case k == "$ref":
			ref, _ := v.(string)
			if !strings.HasPrefix(ref, "#/") {
				return nil, fmt.Errorf("%s: external $ref %q is not supported by the API", p, ref)
			}
			out[k] = v
		case k == "additionalProperties":
			if b, isBool := v.(bool); !isBool || b {
				return nil, fmt.Errorf("%s: must be false for the API", p)
			}
			out[k] = v
		case k == "properties" || k == "$defs":
			children := map[string]any{}
			for name, child := range v.(map[string]any) {
				r, err := reduce(child, p+"/"+name)
				if err != nil {
					return nil, err
				}
				children[name] = r
			}
			out[k] = children
		case k == "items":
			r, err := reduce(v, p)
			if err != nil {
				return nil, err
			}
			out[k] = r
		case k == "anyOf" || k == "allOf":
			list := v.([]any)
			reduced := make([]any, len(list))
			for i, child := range list {
				r, err := reduce(child, fmt.Sprintf("%s/%d", p, i))
				if err != nil {
					return nil, err
				}
				reduced[i] = r
			}
			out[k] = reduced
		default:
			out[k] = clone(v)
		}
	}
	return out, nil
}

func clone(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = clone(val)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, val := range t {
			l[i] = clone(val)
		}
		return l
	default:
		return v
	}
}
