//go:build integration

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"marketplace-broker/internal/k8sclient"
	"marketplace-broker/internal/tenant"
)

// TestUserJourney_SubmitEditVerifyDelete drives the real broker binary's
// HTTP surface - the same routing, auth, and handler code a browser talks
// to - against a live kind-platform cluster with the database Promise and
// broker teams already provisioned. It's the platform-engineer-level check
// server_update_test.go's fake-client tests can't give: that a request
// submitted through the broker actually becomes a real Kubernetes object,
// and that editing it through the broker's PUT route actually replaces
// that live object's .spec, end to end.
//
// Needs (see Makefile's broker-test-integration target):
//   - `make up` (kind-platform + kind-worker running)
//   - `make promise-demo` (installs the database Promise used here)
//   - `make broker-provision-teams` (so demo-key-payments' namespace,
//     team-payments, exists)
//
// Run with (BROKER_KUBE_CONTEXT defaults to "kind-platform", same
// convention as rbac_integration_test.go):
//
//	go test -tags=integration ./internal/api/...
func TestUserJourney_SubmitEditVerifyDelete(t *testing.T) {
	kubeContext := os.Getenv("BROKER_KUBE_CONTEXT")
	if kubeContext == "" {
		kubeContext = "kind-platform"
	}
	t.Setenv("BROKER_KUBE_CONTEXT", kubeContext)

	clients, err := k8sclient.New()
	if err != nil {
		t.Fatalf("building Kubernetes clients: %v", err)
	}

	dir, err := tenant.Load("../../config/teams.yaml")
	if err != nil {
		t.Fatalf("loading team directory: %v", err)
	}

	srv := httptest.NewServer(New(clients, dir, "").Handler())
	defer srv.Close()

	const apiKey = "demo-key-payments" // team "payments", per config/teams.yaml
	reqName := fmt.Sprintf("journey-test-%d", time.Now().UnixNano())

	client := srv.Client()
	do := func(method, path, body string) (*http.Response, map[string]interface{}) {
		req, err := http.NewRequest(method, srv.URL+"/api"+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("building %s %s: %v", method, path, err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var parsed map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&parsed) // best-effort - DELETE has no body
		return resp, parsed
	}

	// Cleanup first, so a failed assertion mid-journey still deletes what
	// was submitted.
	defer func() {
		resp, _ := do(http.MethodDelete, "/promises/database/requests/"+reqName, "")
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
			t.Logf("cleanup DELETE %s: unexpected status %d", reqName, resp.StatusCode)
		}
	}()

	// 1. Submit - a platform user requesting a new database.
	resp, submitted := do(http.MethodPost, "/promises/database/requests",
		fmt.Sprintf(`{"name":%q,"spec":{"size":"1Gi"}}`, reqName))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit: status = %d, want %d; body: %v", resp.StatusCode, http.StatusCreated, submitted)
	}

	// 2. Get - confirm it's readable back immediately.
	resp, got := do(http.MethodGet, "/promises/database/requests/"+reqName, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get after submit: status = %d, want %d; body: %v", resp.StatusCode, http.StatusOK, got)
	}

	// 3. Edit - the request-editing feature under test: change size.
	resp, updated := do(http.MethodPut, "/promises/database/requests/"+reqName, `{"spec":{"size":"5Gi"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit: status = %d, want %d; body: %v", resp.StatusCode, http.StatusOK, updated)
	}
	if spec, _, _ := unstructured.NestedMap(updated, "spec"); spec["size"] != "5Gi" {
		t.Errorf("edit response spec = %v, want size=5Gi", spec)
	}

	// 4. Verify against the live cluster object directly (not just the
	// broker's response) - the actual proof this journey landed for real.
	databaseGVR := schema.GroupVersionResource{Group: "demo.kratix.io", Version: "v1alpha1", Resource: "databases"}
	live, err := clients.Dynamic.Resource(databaseGVR).Namespace(tenant.Namespace("payments")).Get(t.Context(), reqName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting live Database object: %v", err)
	}
	if size, _, _ := unstructured.NestedString(live.Object, "spec", "size"); size != "5Gi" {
		t.Errorf("live object's spec.size = %q, want %q - the edit didn't reach the real cluster", size, "5Gi")
	}

	// 5. Delete - explicit check here too, ahead of the deferred cleanup,
	// so a failure here is attributed to the delete step itself.
	resp, _ = do(http.MethodDelete, "/promises/database/requests/"+reqName, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	resp, _ = do(http.MethodGet, "/promises/database/requests/"+reqName, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete: status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
