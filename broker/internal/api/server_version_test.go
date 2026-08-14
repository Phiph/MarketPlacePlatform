package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"marketplace-broker/internal/bindingapi"
	"marketplace-broker/internal/catalog"
	"marketplace-broker/internal/k8sclient"
)

// serverWithFakeVersionClient is serverWithFakeClient's counterpart for
// this file's tests: the fake client also serves PromiseRevision and
// ResourceBinding, and is wired as *both* s.admin.Dynamic and the
// per-team impersonated client - fake mode never models per-team RBAC
// (see k8sclient.NewFake's doc comment), so one shared fake is correct
// here exactly like it is in production's NewFake.
func serverWithFakeVersionClient(objects ...runtime.Object) *Server {
	gvrToListKind := map[schema.GroupVersionResource]string{
		testDatabaseEntry.GVR():    "DatabaseList",
		catalog.PromiseRevisionGVR: "PromiseRevisionList",
		bindingapi.GVR:             "ResourceBindingList",
	}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objects...)
	return &Server{admin: &k8sclient.Clients{
		Dynamic: fake,
		Groups:  fakeGroupResolver{client: fake},
	}}
}

func revisionObject(name, promiseName, version string, latest bool) *unstructured.Unstructured {
	labels := map[string]interface{}{catalog.LabelPromiseName: promiseName}
	if latest {
		labels[catalog.LabelLatestRevision] = "true"
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "PromiseRevision",
		"metadata": map[string]interface{}{
			"name":              name,
			"labels":            labels,
			"creationTimestamp": "2026-01-01T00:00:00Z",
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

func testBindingObject(namespace, name, promiseName, resourceName, version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "ResourceBinding",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"kratix.io/promise-name":  promiseName,
				"kratix.io/resource-name": resourceName,
			},
		},
		"spec": map[string]interface{}{
			"version": version,
		},
	}}
}

func TestDoGetRequestVersion_Success(t *testing.T) {
	s := serverWithFakeVersionClient(
		testDatabaseObject("team-payments", "my-db", map[string]interface{}{"size": "10Gi"}),
		testBindingObject("team-payments", "my-db-binding", "database", "my-db", "v0.1.0"),
		revisionObject("database-v0.1.0", "database", "v0.1.0", false),
		revisionObject("database-v0.2.0", "database", "v0.2.0", true),
	)

	req := httptest.NewRequest(http.MethodGet, "/promises/database/requests/my-db/version", nil)
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doGetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got requestVersionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	want := requestVersionInfo{BoundVersion: "v0.1.0", LatestVersion: "v0.2.0", UpgradeAvailable: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDoGetRequestVersion_RequestNotFound(t *testing.T) {
	s := serverWithFakeVersionClient()

	req := httptest.NewRequest(http.MethodGet, "/promises/database/requests/missing-db/version", nil)
	req.SetPathValue("reqName", "missing-db")
	w := httptest.NewRecorder()

	s.doGetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestDoSetRequestVersion_Success(t *testing.T) {
	s := serverWithFakeVersionClient(
		testDatabaseObject("team-payments", "my-db", map[string]interface{}{"size": "10Gi"}),
		testBindingObject("team-payments", "my-db-binding", "database", "my-db", "v0.1.0"),
		revisionObject("database-v0.1.0", "database", "v0.1.0", false),
		revisionObject("database-v0.2.0", "database", "v0.2.0", true),
	)

	req := httptest.NewRequest(http.MethodPost, "/promises/database/requests/my-db/version", strings.NewReader(`{"version":"v0.2.0"}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doSetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got requestVersionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshalling response body: %v", err)
	}
	want := requestVersionInfo{BoundVersion: "v0.2.0", LatestVersion: "v0.2.0", UpgradeAvailable: false}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDoSetRequestVersion_UnknownVersion(t *testing.T) {
	s := serverWithFakeVersionClient(
		testDatabaseObject("team-payments", "my-db", map[string]interface{}{"size": "10Gi"}),
		testBindingObject("team-payments", "my-db-binding", "database", "my-db", "v0.1.0"),
		revisionObject("database-v0.1.0", "database", "v0.1.0", true),
	)

	req := httptest.NewRequest(http.MethodPost, "/promises/database/requests/my-db/version", strings.NewReader(`{"version":"v9.9.9"}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doSetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestDoSetRequestVersion_InvalidForTargetSchema(t *testing.T) {
	// The target revision requires "size"; this request's stored spec
	// doesn't have it, so the move must be rejected before the binding is
	// ever touched.
	s := serverWithFakeVersionClient(
		testDatabaseObject("team-payments", "my-db", map[string]interface{}{}),
		testBindingObject("team-payments", "my-db-binding", "database", "my-db", "v0.1.0"),
		revisionObject("database-v0.2.0", "database", "v0.2.0", true),
	)

	req := httptest.NewRequest(http.MethodPost, "/promises/database/requests/my-db/version", strings.NewReader(`{"version":"v0.2.0"}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doSetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestDoSetRequestVersion_MissingVersionField(t *testing.T) {
	s := serverWithFakeVersionClient()

	req := httptest.NewRequest(http.MethodPost, "/promises/database/requests/my-db/version", strings.NewReader(`{}`))
	req.SetPathValue("reqName", "my-db")
	w := httptest.NewRecorder()

	s.doSetRequestVersion(w, req, testDatabaseEntry, "payments", "team-payments")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}
