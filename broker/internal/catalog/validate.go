package catalog

import (
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ValidateAgainstSchema checks spec (a request's .spec, e.g. from
// resourceapi.Get) against schemaObj (an Entry.Schema-shaped OpenAPI v3
// schema for the full custom resource - schemaObj.properties.spec holds
// the actual field-level schema). Returns one human-readable problem per
// violation, empty when spec is valid.
//
// Checks properties/type/enum/required, plus rejecting fields the schema
// doesn't declare (unless it opts into x-kubernetes-preserve-unknown-fields)
// - the subset every Promise in this repo's schemas actually uses (see
// promises/*/promise.yaml). Deliberately not a full JSON Schema validator,
// to avoid pulling in k8s.io/apiextensions-apiserver's
// structural-schema/CEL machinery for schemas this simple.
func ValidateAgainstSchema(schemaObj map[string]interface{}, spec map[string]interface{}) []string {
	specSchema, _, _ := unstructured.NestedMap(schemaObj, "properties", "spec")
	if specSchema == nil {
		return nil
	}
	return validateObject(specSchema, spec, "spec")
}

func validateObject(fieldSchema map[string]interface{}, value map[string]interface{}, path string) []string {
	var problems []string

	required, _, _ := unstructured.NestedStringSlice(fieldSchema, "required")
	for _, name := range required {
		if _, ok := value[name]; !ok {
			problems = append(problems, fmt.Sprintf("missing required field %q", path+"."+name))
		}
	}

	properties, _, _ := unstructured.NestedMap(fieldSchema, "properties")
	for name, rawPropSchema := range properties {
		propValue, present := value[name]
		if !present {
			continue
		}
		propSchema, ok := rawPropSchema.(map[string]interface{})
		if !ok {
			continue
		}
		problems = append(problems, validateValue(propSchema, propValue, path+"."+name)...)
	}

	// A field present in the spec but not declared in this schema's
	// properties won't fit the target revision - flag it, unless the
	// schema explicitly opts into accepting undeclared fields (mirrors
	// apiextensions' x-kubernetes-preserve-unknown-fields).
	preserveUnknown, _, _ := unstructured.NestedBool(fieldSchema, "x-kubernetes-preserve-unknown-fields")
	if !preserveUnknown {
		for name := range value {
			if _, declared := properties[name]; !declared {
				problems = append(problems, fmt.Sprintf("unknown field %q for this version", path+"."+name))
			}
		}
	}

	return problems
}

func validateValue(fieldSchema map[string]interface{}, value interface{}, path string) []string {
	wantType, _, _ := unstructured.NestedString(fieldSchema, "type")

	if wantType != "" && !typeMatches(wantType, value) {
		return []string{fmt.Sprintf("field %q: want type %q, got %T", path, wantType, value)}
	}

	if enum, found, _ := unstructured.NestedSlice(fieldSchema, "enum"); found && !enumContains(enum, value) {
		return []string{fmt.Sprintf("field %q: value %v is not one of the allowed values %v", path, value, enum)}
	}

	if wantType == "object" {
		if obj, ok := value.(map[string]interface{}); ok {
			return validateObject(fieldSchema, obj, path)
		}
	}

	return nil
}

func typeMatches(wantType string, value interface{}) bool {
	switch wantType {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == float64(int64(f))
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	default:
		return true
	}
}

func enumContains(enum []interface{}, value interface{}) bool {
	for _, allowed := range enum {
		if reflect.DeepEqual(allowed, value) {
			return true
		}
	}
	return false
}
