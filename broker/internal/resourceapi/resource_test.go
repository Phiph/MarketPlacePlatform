package resourceapi

import (
	"context"
	"reflect"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"marketplace-broker/internal/catalog"
)

var databaseEntry = catalog.Entry{
	Name:    "database",
	Group:   "demo.kratix.io",
	Version: "v1alpha1",
	Kind:    "Database",
	Plural:  "databases",
	Scope:   "Namespaced",
}

func databaseObject(namespace, name string, spec map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "demo.kratix.io/v1alpha1",
		"kind":       "Database",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": spec,
	}}
}

func fakeClient(objects ...runtime.Object) dynamic.Interface {
	gvrToListKind := map[schema.GroupVersionResource]string{
		databaseEntry.GVR(): "DatabaseList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objects...)
}

// Update replaces .spec wholesale rather than merging it - a field present
// in the old spec but omitted from the new one must not survive, since that
// omission is how a user clears a field via the edit form (see resource.go's
// doc comment on Update).
func TestUpdate_ReplacesSpecWholesale(t *testing.T) {
	existing := databaseObject("team-payments", "my-db", map[string]interface{}{
		"size":             "10Gi",
		"highAvailability": true,
	})
	client := fakeClient(existing)

	newSpec := map[string]interface{}{"size": "50Gi"}
	updated, ok, err := Update(context.Background(), client, databaseEntry, "team-payments", "my-db", newSpec)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !ok {
		t.Fatal("Update: ok = false, want true")
	}

	gotSpec, _, _ := unstructured.NestedMap(updated.Object, "spec")
	if !reflect.DeepEqual(gotSpec, newSpec) {
		t.Errorf("updated spec = %v, want %v (highAvailability should not survive - not a merge patch)", gotSpec, newSpec)
	}

	// Confirm the replace actually landed in the backing store, not just the
	// returned object.
	stored, err := client.Resource(databaseEntry.GVR()).Namespace("team-payments").Get(context.Background(), "my-db", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	storedSpec, _, _ := unstructured.NestedMap(stored.Object, "spec")
	if !reflect.DeepEqual(storedSpec, newSpec) {
		t.Errorf("stored spec = %v, want %v", storedSpec, newSpec)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	client := fakeClient()

	updated, ok, err := Update(context.Background(), client, databaseEntry, "team-payments", "missing-db", map[string]interface{}{"size": "10Gi"})
	if err != nil {
		t.Fatalf("Update: got err %v, want nil", err)
	}
	if ok {
		t.Error("Update: ok = true, want false for a nonexistent request")
	}
	if updated != nil {
		t.Errorf("Update: obj = %v, want nil", updated)
	}
}

// A caller-facing conflict (409) must propagate as a plain error rather than
// being swallowed as not-found - doUpdateRequest depends on apierrors.IsConflict
// being able to see it.
func TestUpdate_PropagatesConflict(t *testing.T) {
	existing := databaseObject("team-payments", "my-db", map[string]interface{}{"size": "10Gi"})
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{databaseEntry.GVR(): "DatabaseList"}, existing)

	fake.PrependReactor("update", "databases", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: databaseEntry.Group, Resource: databaseEntry.Plural}, "my-db", nil)
	})

	_, ok, err := Update(context.Background(), fake, databaseEntry, "team-payments", "my-db", map[string]interface{}{"size": "50Gi"})
	if err == nil {
		t.Fatal("Update: err = nil, want a conflict error")
	}
	if !apierrors.IsConflict(err) {
		t.Errorf("Update: err = %v, want a conflict error apierrors.IsConflict recognises", err)
	}
	if ok {
		t.Error("Update: ok = true, want false on error")
	}
}
