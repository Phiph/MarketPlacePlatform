package catalog

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// databasePromise mirrors promises/database/promise.yaml's shape, plus the
// marketplace visibility/display-name/description annotations documented
// in README.md.
func databasePromise() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "Promise",
		"metadata": map[string]interface{}{
			"name": "database",
			"labels": map[string]interface{}{
				LabelVisible: "true",
			},
			"annotations": map[string]interface{}{
				AnnotationDisplayName: "Postgres Database",
				AnnotationDescription: "A sized, managed Postgres database.",
			},
		},
		"spec": map[string]interface{}{
			"api": map[string]interface{}{
				"apiVersion": "apiextensions.k8s.io/v1",
				"kind":       "CustomResourceDefinition",
				"spec": map[string]interface{}{
					"group": "demo.kratix.io",
					"names": map[string]interface{}{
						"kind":   "Database",
						"plural": "databases",
					},
					"scope": "Namespaced",
					"versions": []interface{}{
						map[string]interface{}{
							"name":    "v1alpha1",
							"served":  true,
							"storage": true,
							"schema": map[string]interface{}{
								"openAPIV3Schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"spec": map[string]interface{}{
											"type": "object",
											"properties": map[string]interface{}{
												"size": map[string]interface{}{"type": "string"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}}
}

func TestParseEntry(t *testing.T) {
	entry, ok := parseEntry(databasePromise())
	if !ok {
		t.Fatalf("expected the database Promise fixture to parse")
	}

	if entry.Name != "database" {
		t.Errorf("Name = %q, want %q", entry.Name, "database")
	}
	if entry.DisplayName != "Postgres Database" {
		t.Errorf("DisplayName = %q, want %q", entry.DisplayName, "Postgres Database")
	}
	if entry.Description != "A sized, managed Postgres database." {
		t.Errorf("Description = %q, want the fixture's description", entry.Description)
	}
	if !entry.Visible {
		t.Errorf("Visible = false, want true")
	}
	if entry.Group != "demo.kratix.io" || entry.Version != "v1alpha1" {
		t.Errorf("Group/Version = %q/%q, want demo.kratix.io/v1alpha1", entry.Group, entry.Version)
	}
	if entry.Kind != "Database" || entry.Plural != "databases" {
		t.Errorf("Kind/Plural = %q/%q, want Database/databases", entry.Kind, entry.Plural)
	}
	if !entry.Namespaced() {
		t.Errorf("Namespaced() = false, want true for scope %q", entry.Scope)
	}
	if entry.Schema == nil {
		t.Errorf("Schema is nil, want the openAPIV3Schema to be parsed through")
	}
}

func TestParseEntryDefaultsHiddenAndDisplayName(t *testing.T) {
	obj := databasePromise()
	// Strip labels/annotations entirely - a Promise author who hasn't opted
	// into the marketplace convention at all.
	delete(obj.Object["metadata"].(map[string]interface{}), "labels")
	delete(obj.Object["metadata"].(map[string]interface{}), "annotations")

	entry, ok := parseEntry(obj)
	if !ok {
		t.Fatalf("expected the fixture to still parse without marketplace metadata")
	}
	if entry.Visible {
		t.Errorf("Visible = true, want false when the visible label is absent")
	}
	if entry.DisplayName != entry.Name {
		t.Errorf("DisplayName = %q, want it to fall back to Name %q", entry.DisplayName, entry.Name)
	}
	if entry.Description != "" {
		t.Errorf("Description = %q, want empty when the annotation is absent", entry.Description)
	}
}

func TestParseEntryMissingAPIIsSkipped(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": "broken"},
		"spec":     map[string]interface{}{},
	}}

	if _, ok := parseEntry(obj); ok {
		t.Errorf("expected a Promise with no spec.api to fail to parse, got ok=true")
	}
}

func TestPickVersionPrefersStorage(t *testing.T) {
	versions := []interface{}{
		map[string]interface{}{"name": "v1alpha1", "served": true, "storage": false},
		map[string]interface{}{"name": "v1beta1", "served": true, "storage": true},
	}

	version, _ := pickVersion(versions)
	if version != "v1beta1" {
		t.Errorf("pickVersion() = %q, want the storage version %q", version, "v1beta1")
	}
}

func TestPickVersionFallsBackToServed(t *testing.T) {
	versions := []interface{}{
		map[string]interface{}{"name": "v1alpha1", "served": false, "storage": false},
		map[string]interface{}{"name": "v1beta1", "served": true, "storage": false},
	}

	version, _ := pickVersion(versions)
	if version != "v1beta1" {
		t.Errorf("pickVersion() = %q, want the served version %q", version, "v1beta1")
	}
}

func TestParseEntryOperationalEvidenceFields(t *testing.T) {
	obj := databasePromise()
	annotations := obj.Object["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
	annotations[AnnotationOwner] = "platform-team"
	annotations[AnnotationLifecycle] = "stable"
	annotations[AnnotationSupport] = "#platform-eng"
	annotations[AnnotationPolicy] = "confidential"

	entry, ok := parseEntry(obj)
	if !ok {
		t.Fatalf("expected the database Promise fixture to parse")
	}
	if entry.Owner != "platform-team" {
		t.Errorf("Owner = %q, want %q", entry.Owner, "platform-team")
	}
	if entry.Lifecycle != "stable" {
		t.Errorf("Lifecycle = %q, want %q", entry.Lifecycle, "stable")
	}
	if entry.Support != "#platform-eng" {
		t.Errorf("Support = %q, want %q", entry.Support, "#platform-eng")
	}
	if entry.Policy != "confidential" {
		t.Errorf("Policy = %q, want %q", entry.Policy, "confidential")
	}
	if len(entry.MissingEvidence) != 0 {
		t.Errorf("MissingEvidence = %v, want empty", entry.MissingEvidence)
	}
}

func TestParseEntryOperationalEvidenceMissing(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(annotations map[string]interface{})
		wantMissing []string
	}{
		{
			name:        "all evidence annotations absent",
			mutate:      func(annotations map[string]interface{}) {},
			wantMissing: []string{"owner", "lifecycle", "support", "policy"},
		},
		{
			name: "owner present, rest absent",
			mutate: func(annotations map[string]interface{}) {
				annotations[AnnotationOwner] = "platform-team"
			},
			wantMissing: []string{"lifecycle", "support", "policy"},
		},
		{
			name: "invalid lifecycle and policy values",
			mutate: func(annotations map[string]interface{}) {
				annotations[AnnotationOwner] = "platform-team"
				annotations[AnnotationLifecycle] = "made-up-stage"
				annotations[AnnotationSupport] = "#platform-eng"
				annotations[AnnotationPolicy] = "made-up-class"
			},
			wantMissing: []string{"lifecycle", "policy"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := databasePromise()
			annotations := obj.Object["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
			tt.mutate(annotations)

			entry, ok := parseEntry(obj)
			if !ok {
				t.Fatalf("expected the fixture to parse")
			}
			if !reflect.DeepEqual(entry.MissingEvidence, tt.wantMissing) {
				t.Errorf("MissingEvidence = %v, want %v", entry.MissingEvidence, tt.wantMissing)
			}
		})
	}
}
