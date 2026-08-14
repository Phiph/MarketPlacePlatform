package catalog

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func revisionFixture(name, promiseName, version string, latest bool, createdAt string) *unstructured.Unstructured {
	labels := map[string]interface{}{LabelPromiseName: promiseName}
	if latest {
		labels[LabelLatestRevision] = "true"
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "PromiseRevision",
		"metadata": map[string]interface{}{
			"name":              name,
			"labels":            labels,
			"creationTimestamp": createdAt,
		},
		"spec": map[string]interface{}{
			"version": version,
			"promiseSpec": map[string]interface{}{
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
												"type":     "object",
												"required": []interface{}{"size"},
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
		},
	}}
}

func fakeRevisionsClient(objects ...runtime.Object) dynamic.Interface {
	gvrToListKind := map[schema.GroupVersionResource]string{PromiseRevisionGVR: "PromiseRevisionList"}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objects...)
}

func TestListRevisions_NewestFirstWithLatestFlag(t *testing.T) {
	client := fakeRevisionsClient(
		revisionFixture("database-v0.1.0", "database", "v0.1.0", false, "2026-01-01T00:00:00Z"),
		revisionFixture("database-v0.2.0", "database", "v0.2.0", true, "2026-02-01T00:00:00Z"),
	)

	revisions, err := ListRevisions(context.Background(), client, "database")
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("len(revisions) = %d, want 2", len(revisions))
	}
	if revisions[0].Version != "v0.2.0" || !revisions[0].Latest {
		t.Errorf("revisions[0] = %+v, want v0.2.0 marked latest, listed first (newest)", revisions[0])
	}
	if revisions[1].Version != "v0.1.0" || revisions[1].Latest {
		t.Errorf("revisions[1] = %+v, want v0.1.0, not latest", revisions[1])
	}
}

func TestListRevisions_FiltersByPromiseName(t *testing.T) {
	client := fakeRevisionsClient(
		revisionFixture("database-v0.1.0", "database", "v0.1.0", true, "2026-01-01T00:00:00Z"),
		revisionFixture("redis-v0.1.0", "redis", "v0.1.0", true, "2026-01-01T00:00:00Z"),
	)

	revisions, err := ListRevisions(context.Background(), client, "database")
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != 1 || revisions[0].Version != "v0.1.0" {
		t.Errorf("ListRevisions(\"database\") = %+v, want exactly the database revision", revisions)
	}
}

func TestRevisionSchema_Found(t *testing.T) {
	client := fakeRevisionsClient(revisionFixture("database-v0.2.0", "database", "v0.2.0", true, "2026-02-01T00:00:00Z"))

	schemaObj, ok, err := RevisionSchema(context.Background(), client, "database", "v0.2.0")
	if err != nil {
		t.Fatalf("RevisionSchema: %v", err)
	}
	if !ok {
		t.Fatal("RevisionSchema: ok = false, want true")
	}
	specSchema, _, _ := unstructured.NestedMap(schemaObj, "properties", "spec")
	if specSchema == nil {
		t.Errorf("RevisionSchema: expected a properties.spec schema, got %v", schemaObj)
	}
}

func TestRevisionSchema_UnknownVersion(t *testing.T) {
	client := fakeRevisionsClient(revisionFixture("database-v0.1.0", "database", "v0.1.0", true, "2026-01-01T00:00:00Z"))

	_, ok, err := RevisionSchema(context.Background(), client, "database", "v9.9.9")
	if err != nil {
		t.Fatalf("RevisionSchema: got err %v, want nil", err)
	}
	if ok {
		t.Error("RevisionSchema: ok = true, want false for an unknown version")
	}
}
