package catalog

import "testing"

// databaseSpecSchema mirrors the openAPIV3Schema shape parseCRD/pickVersion
// produce for the real database Promise (see
// broker/cmd/broker/fake_seed.go and promises/database/promise.yaml).
func databaseSpecSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"spec": map[string]interface{}{
				"type":     "object",
				"required": []interface{}{"size"},
				"properties": map[string]interface{}{
					"size": map[string]interface{}{
						"type": "string",
						"enum": []interface{}{"1Gi", "5Gi", "10Gi", "50Gi"},
					},
					"highAvailability": map[string]interface{}{"type": "boolean"},
				},
			},
		},
	}
}

func TestValidateAgainstSchema(t *testing.T) {
	schemaObj := databaseSpecSchema()

	tests := []struct {
		name     string
		spec     map[string]interface{}
		wantErrs int
	}{
		{"valid spec", map[string]interface{}{"size": "10Gi"}, 0},
		{"valid spec with optional field", map[string]interface{}{"size": "10Gi", "highAvailability": true}, 0},
		{"missing required field", map[string]interface{}{"highAvailability": true}, 1},
		{"invalid enum value", map[string]interface{}{"size": "999Gi"}, 1},
		{"wrong type", map[string]interface{}{"size": "10Gi", "highAvailability": "yes"}, 1},
		// A field the target revision's schema doesn't declare (e.g. a spec
		// carrying highAvailability, then rolled back to a revision whose
		// schema predates that field) must be rejected - see the design
		// spec's third goal: reject before the move, not let Configure fail
		// asynchronously later.
		{"unknown field", map[string]interface{}{"size": "10Gi", "notARealField": true}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := ValidateAgainstSchema(schemaObj, tt.spec)
			if len(problems) != tt.wantErrs {
				t.Errorf("ValidateAgainstSchema(%v) = %v, want %d problem(s)", tt.spec, problems, tt.wantErrs)
			}
		})
	}
}

// databaseSpecSchemaPreserveUnknown is like databaseSpecSchema, but its
// spec schema opts into x-kubernetes-preserve-unknown-fields - mirroring a
// Promise author who deliberately allows callers to pass fields the schema
// doesn't enumerate.
func databaseSpecSchemaPreserveUnknown() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"spec": map[string]interface{}{
				"type":                                 "object",
				"required":                             []interface{}{"size"},
				"x-kubernetes-preserve-unknown-fields": true,
				"properties": map[string]interface{}{
					"size": map[string]interface{}{
						"type": "string",
						"enum": []interface{}{"1Gi", "5Gi", "10Gi", "50Gi"},
					},
				},
			},
		},
	}
}

func TestValidateAgainstSchema_PreserveUnknownFields(t *testing.T) {
	schemaObj := databaseSpecSchemaPreserveUnknown()

	problems := ValidateAgainstSchema(schemaObj, map[string]interface{}{"size": "10Gi", "notARealField": true})
	if len(problems) != 0 {
		t.Errorf("ValidateAgainstSchema with x-kubernetes-preserve-unknown-fields = %v, want no problems", problems)
	}
}

func TestValidateAgainstSchema_NoSpecSchema(t *testing.T) {
	problems := ValidateAgainstSchema(map[string]interface{}{}, map[string]interface{}{"anything": true})
	if problems != nil {
		t.Errorf("ValidateAgainstSchema with no spec schema = %v, want nil", problems)
	}
}
