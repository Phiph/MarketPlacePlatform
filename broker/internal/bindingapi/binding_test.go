package bindingapi

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func bindingObject(namespace, name, promiseName, resourceName, version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "ResourceBinding",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				labelPromiseName:  promiseName,
				labelResourceName: resourceName,
			},
		},
		"spec": map[string]interface{}{
			"version": version,
		},
	}}
}

func fakeClient(objects ...runtime.Object) dynamic.Interface {
	gvrToListKind := map[schema.GroupVersionResource]string{GVR: "ResourceBindingList"}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objects...)
}

func TestGet_Found(t *testing.T) {
	client := fakeClient(bindingObject("team-payments", "database-my-db", "database", "my-db", "v0.1.0"))

	obj, ok, err := Get(context.Background(), client, "team-payments", "database", "my-db")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: ok = false, want true")
	}
	version, _, _ := unstructured.NestedString(obj.Object, "spec", "version")
	if version != "v0.1.0" {
		t.Errorf("spec.version = %q, want %q", version, "v0.1.0")
	}
}

func TestGet_NotFound(t *testing.T) {
	client := fakeClient()

	obj, ok, err := Get(context.Background(), client, "team-payments", "database", "missing-db")
	if err != nil {
		t.Fatalf("Get: got err %v, want nil", err)
	}
	if ok {
		t.Error("Get: ok = true, want false")
	}
	if obj != nil {
		t.Errorf("Get: obj = %v, want nil", obj)
	}
}

func TestVersion_ResolvesLatest(t *testing.T) {
	binding := bindingObject("team-payments", "database-my-db", "database", "my-db", "latest")
	if got := Version(binding, "v0.2.0"); got != "v0.2.0" {
		t.Errorf("Version() = %q, want %q", got, "v0.2.0")
	}
}

func TestVersion_ResolvesConcrete(t *testing.T) {
	binding := bindingObject("team-payments", "database-my-db", "database", "my-db", "v0.1.0")
	if got := Version(binding, "v0.2.0"); got != "v0.1.0" {
		t.Errorf("Version() = %q, want %q", got, "v0.1.0")
	}
}

func TestSetVersion_Success(t *testing.T) {
	client := fakeClient(bindingObject("team-payments", "database-my-db", "database", "my-db", "v0.1.0"))

	updated, ok, err := SetVersion(context.Background(), client, "team-payments", "database", "my-db", "v0.2.0")
	if err != nil {
		t.Fatalf("SetVersion: %v", err)
	}
	if !ok {
		t.Fatal("SetVersion: ok = false, want true")
	}
	version, _, _ := unstructured.NestedString(updated.Object, "spec", "version")
	if version != "v0.2.0" {
		t.Errorf("spec.version = %q, want %q", version, "v0.2.0")
	}
}

func TestSetVersion_NotFound(t *testing.T) {
	client := fakeClient()

	_, ok, err := SetVersion(context.Background(), client, "team-payments", "database", "missing-db", "v0.2.0")
	if err != nil {
		t.Fatalf("SetVersion: got err %v, want nil", err)
	}
	if ok {
		t.Error("SetVersion: ok = true, want false")
	}
}

func TestSetVersion_PropagatesConflict(t *testing.T) {
	existing := bindingObject("team-payments", "database-my-db", "database", "my-db", "v0.1.0")
	gvrToListKind := map[schema.GroupVersionResource]string{GVR: "ResourceBindingList"}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, existing)
	fake.PrependReactor("update", "resourcebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "platform.kratix.io", Resource: "resourcebindings"}, "database-my-db", nil)
	})

	_, ok, err := SetVersion(context.Background(), fake, "team-payments", "database", "my-db", "v0.2.0")
	if err == nil {
		t.Fatal("SetVersion: err = nil, want a conflict error")
	}
	if !apierrors.IsConflict(err) {
		t.Errorf("SetVersion: err = %v, want a conflict error", err)
	}
	if ok {
		t.Error("SetVersion: ok = true, want false on error")
	}
}
