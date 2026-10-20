package notice

import "strings"

// TemplateField retains the source dictionary's iteration order. Assignment is
// deliberately not NewEnvelope validation: the installed model does not enable
// Pydantic validate_assignment. An error retains earlier field assignments.
type TemplateField struct {
	Name  string
	Value any
}

func (e *Envelope) ApplyTemplateFields(fields []TemplateField) error {
	if e == nil || e.fields == nil {
		return ErrInvalidEnvelope
	}
	for _, item := range fields {
		known := false
		for _, name := range fieldOrder {
			if item.Name == name {
				known = true
				break
			}
		}
		if known {
			v, err := jsonCopy(item.Value)
			if err != nil {
				return ErrTemplateDependency
			}
			e.fields[item.Name] = v
			if _, enum := enumValues[item.Name]; enum {
				if e.assignedEnums == nil {
					e.assignedEnums = make(map[string]bool)
				}
				e.assignedEnums[item.Name] = true
			}
			continue
		}
		// These source BaseModel properties have no setter. Other public
		// BaseModel members are class attributes and reject instance assignment.
		switch item.Name {
		case "model_extra", "model_fields_set":
			return SourceError{Class: "AttributeError"}
		case "construct", "copy", "dict", "from_orm", "json", "model_computed_fields", "model_config", "model_construct", "model_copy", "model_dump", "model_dump_json", "model_fields", "model_json_schema", "model_parametrized_name", "model_post_init", "model_rebuild", "model_validate", "model_validate_json", "model_validate_strings", "parse_file", "parse_obj", "parse_raw", "schema", "schema_json", "to_dict", "update_forward_refs", "validate":
			return SourceError{Class: "ValueError"}
		}
		// A template may access actual Python model internals. Those are not
		// represented by the native envelope; never silently claim equivalence.
		if strings.HasPrefix(item.Name, "__") {
			return ErrTemplateDependency
		}
		// hasattr is false for an unknown ordinary field, so source ignores it.
	}
	return nil
}

// TemplateEnumValue is the original enum .value access, not a string coercion.
func (e *Envelope) TemplateEnumValue(field string) (string, error) {
	if e == nil || e.fields == nil {
		return "", ErrInvalidEnvelope
	}
	if _, known := enumValues[field]; !known {
		return "", ErrTemplateDependency
	}
	if e.assignedEnums[field] {
		return "", SourceError{Class: "AttributeError"}
	}
	value, ok := e.fields[field].(string)
	if !ok {
		return "", SourceError{Class: "AttributeError"}
	}
	return value, nil
}

// RequiresNativeEnumRouting marks values whose Python enum-vs-value semantics
// cannot be reconstructed from a plain JSON notification queue envelope.
func (e *Envelope) RequiresNativeEnumRouting() bool {
	if e == nil {
		return false
	}
	for _, field := range []string{"channel", "mtype"} {
		if e.assignedEnums[field] && e.fields[field] != nil {
			return true
		}
	}
	return false
}

// EnvelopeSnapshot is a host-private persistence format. It must be bound to a
// trusted sealed route; it is not an alternative user input constructor.
type EnvelopeSnapshot struct {
	Version       int      `json:"version"`
	Fields        Object   `json:"fields"`
	AssignedEnums []string `json:"assigned_enums"`
}

func (e *Envelope) Snapshot() (EnvelopeSnapshot, error) {
	if e == nil || e.fields == nil {
		return EnvelopeSnapshot{}, ErrInvalidEnvelope
	}
	value, err := jsonCopy(e.fields)
	if err != nil {
		return EnvelopeSnapshot{}, ErrInvalidEnvelope
	}
	out := EnvelopeSnapshot{Version: 1, Fields: value.(map[string]any), AssignedEnums: []string{}}
	for _, field := range fieldOrder {
		if e.assignedEnums[field] {
			out.AssignedEnums = append(out.AssignedEnums, field)
		}
	}
	return out, nil
}

// RestoreSnapshot preserves source assignments, including invalid field types.
// Revalidating Fields with NewEnvelope would change the source object.
func RestoreSnapshot(snapshot EnvelopeSnapshot) (*Envelope, error) {
	if snapshot.Version != 1 || len(snapshot.Fields) != len(fieldOrder) {
		return nil, ErrInvalidEnvelope
	}
	for _, field := range fieldOrder {
		if _, ok := snapshot.Fields[field]; !ok {
			return nil, ErrInvalidEnvelope
		}
	}
	value, err := jsonCopy(snapshot.Fields)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	assigned := make(map[string]bool, len(snapshot.AssignedEnums))
	for _, field := range snapshot.AssignedEnums {
		if _, known := enumValues[field]; !known || assigned[field] {
			return nil, ErrInvalidEnvelope
		}
		assigned[field] = true
	}
	fields := value.(map[string]any)
	for field, allowed := range enumValues {
		if assigned[field] || fields[field] == nil {
			continue
		}
		text, ok := fields[field].(string)
		valid := false
		if ok {
			for _, choice := range allowed {
				if text == choice {
					valid = true
					break
				}
			}
		}
		if !valid {
			return nil, ErrInvalidEnvelope
		}
	}
	return &Envelope{fields: fields, assignedEnums: assigned}, nil
}
