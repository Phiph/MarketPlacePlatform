package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"marketplace-broker/internal/catalog"
	"marketplace-broker/internal/k8sclient"
)

// These tests pick up where TestDoUpdateRequest_* in server_test.go leaves
// off: those only exercise the pre-client-call guard/validation paths with
// a nil k8s client, so doUpdateRequest's actual success/error-mapping
// behaviour against resourceapi.Update - and thus a real client.Resource(...)
// call - had zero coverage. A fake dynamic.Interface (not a real cluster)
// gets us the full handler path cheaply.

var testDatabaseEntry = catalog.Entry{
	Name:    "database",
	Group:   "demo.kratix.io",
	Version: "v1alpha1",
	Kind:    "Database",
	Plural:  "databases",
	Scope:   "Namespaced",
}

func testDatabaseObject(namespace, name string, spec map[string]interface{}) *unstructured.Unstructured {
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

// fakeGroupResolver hands back a fixed client for every team - these tests
// aren't exercising per-team RBAC (rbac_integration_test.go covers that
// against a real cluster), just doUpdateRequest's own logic.
type fakeGroupResolver struct {
	client dynamic.Interface
	err    error
}

func (f fakeGroupResolver) ForGroup(string) (dynamic.Interface, error) {
	return f.client, f.err
}

func serverWithFakeClient(objects ...runtime.Object) *Server {
	gvrToListKind := map[schema.GroupVersionResource]string{testDatabaseEntry.GVR(): "DatabaseList"}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objects...)
	return &Server{admin: &k8sclient.Clients{Groups: fakeGroupResolver{client: fake}}}
}

func TestDoUpdateRequest_Success(t *testing.T) {
	s := serverWithFakeClient(testDatabaseObject("team-payments", "my-db", map[string]interface{}{"size": "10Gi"}))

	req := httptest.NewRequest(http.MethodPut, "/promises/database/requests/my-db", strings.NewReader(`{"spec":{"size":"50Gi"}}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	gotSpec, _, _ := unstructured.NestedMap(body, "spec")
	wantSpec := map[string]interface{}{"size": "50Gi"}
	if !reflect.DeepEqual(gotSpec, wantSpec) {
		t.Errorf("response spec = %v, want %v (highAvailability from the old spec should not survive)", gotSpec, wantSpec)
	}
}

func TestDoUpdateRequest_NotFound(t *testing.T) {
	s := serverWithFakeClient()

	req := httptest.NewRequest(http.MethodPut, "/promises/database/requests/missing-db", strings.NewReader(`{"spec":{"size":"50Gi"}}`))
	req.SetPathValue("reqName", "missing-db")
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestDoUpdateRequest_Conflict(t *testing.T) {
	existing := testDatabaseObject("team-payments", "my-db", map[string]interface{}{"size": "10Gi"})
	gvrToListKind := map[schema.GroupVersionResource]string{testDatabaseEntry.GVR(): "DatabaseList"}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, existing)
	fake.PrependReactor("update", "databases", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: testDatabaseEntry.Group, Resource: testDatabaseEntry.Plural}, "my-db", nil)
	})
	s := &Server{admin: &k8sclient.Clients{Groups: fakeGroupResolver{client: fake}}}

	req := httptest.NewRequest(http.MethodPut, "/promises/database/requests/my-db", strings.NewReader(`{"spec":{"size":"50Gi"}}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestDoUpdateRequest_GroupResolutionFailure(t *testing.T) {
	s := &Server{admin: &k8sclient.Clients{Groups: fakeGroupResolver{err: fmt.Errorf("boom")}}}

	req := httptest.NewRequest(http.MethodPut, "/promises/database/requests/my-db", strings.NewReader(`{"spec":{"size":"50Gi"}}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doUpdateRequest(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadGateway, w.Body.String())
	}
}
