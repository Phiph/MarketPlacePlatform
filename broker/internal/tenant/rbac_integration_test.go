//go:build integration

package tenant

import (
	"context"
	"os"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"marketplace-broker/internal/k8sclient"
)

var configMapsResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
var resourceBindingsResource = schema.GroupVersionResource{Group: "platform.kratix.io", Version: "v1alpha1", Resource: "resourcebindings"}

// TestRBACBoundary is the real proof this whole design exists for: it
// builds impersonated clients for two teams *in the same business unit*
// and confirms Kubernetes itself - not application code, and not even
// Capsule's own Tenant-owner model (business-unit Tenants deliberately have
// no owners; see promises/business-unit's pipeline) - is what stops one
// team's client from touching the other's namespace. That boundary comes
// entirely from the shared GlobalTenantResource (see
// promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/team-rbac.yaml).
//
// Run against a live kind-platform cluster with the business-unit + team
// Promises and Capsule installed, and `make broker-provision-teams` already
// applied - that submits both a BusinessUnit and a Team request per entry in
// teams.yaml, so by the time this runs, teamA and teamB's namespaces should
// already exist (BROKER_KUBE_CONTEXT, defaulting to "kind-platform"):
//
//	go test -tags=integration ./internal/tenant/...
//
// Uses core v1/configmaps rather than a Promise CRD so it doesn't also
// depend on `make promise-demo` for the database Promise.
func TestRBACBoundary(t *testing.T) {
	kubeContext := os.Getenv("BROKER_KUBE_CONTEXT")
	if kubeContext == "" {
		kubeContext = "kind-platform"
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		t.Fatalf("loading kubeconfig (context %q): %v", kubeContext, err)
	}

	groups := k8sclient.NewGroupClients(config)

	const teamA, teamB = "payments", "checkout"
	nsA, nsB := Namespace(teamA), Namespace(teamB)

	clientA, err := groups.ForGroup(Group(teamA))
	if err != nil {
		t.Fatalf("ForGroup(%s): %v", teamA, err)
	}
	clientB, err := groups.ForGroup(Group(teamB))
	if err != nil {
		t.Fatalf("ForGroup(%s): %v", teamB, err)
	}

	ctx := context.Background()

	// The shared GlobalTenantResource reconciles on its own schedule (see
	// its resyncPeriod), so there's a window after a team's namespace first
	// appears where even the owning team's own calls still 403 - poll
	// rather than asserting instantly. If this never succeeds, the
	// namespaces likely don't exist yet at all - re-run
	// `make broker-provision-teams` and confirm with
	// `kubectl get teams.demo.kratix.io`.
	for _, tc := range []struct {
		team   string
		client dynamic.Interface
		ns     string
	}{
		{teamA, clientA, nsA},
		{teamB, clientB, nsB},
	} {
		var lastErr error
		err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 15*time.Second, true, func(ctx context.Context) (bool, error) {
			lastErr = createConfigMap(ctx, tc.client, tc.ns, "rbac-boundary-test")
			return lastErr == nil, nil
		})
		if err != nil {
			t.Fatalf("%s: create ConfigMap in own namespace %q never succeeded: %v", tc.team, tc.ns, lastErr)
		}
		if _, err := tc.client.Resource(configMapsResource).Namespace(tc.ns).List(ctx, metav1.ListOptions{}); err != nil {
			t.Fatalf("%s: list ConfigMaps in own namespace %q: %v", tc.team, tc.ns, err)
		}
	}

	if _, err := clientA.Resource(configMapsResource).Namespace(nsB).List(ctx, metav1.ListOptions{}); !apierrors.IsForbidden(err) {
		t.Errorf("team A listing team B's namespace %q: got err=%v, want Forbidden", nsB, err)
	}
	if err := createConfigMap(ctx, clientA, nsB, "rbac-boundary-attack"); !apierrors.IsForbidden(err) {
		t.Errorf("team A creating in team B's namespace %q: got err=%v, want Forbidden", nsB, err)
	}
	if _, err := clientB.Resource(configMapsResource).Namespace(nsA).List(ctx, metav1.ListOptions{}); !apierrors.IsForbidden(err) {
		t.Errorf("team B listing team A's namespace %q: got err=%v, want Forbidden", nsA, err)
	}
	if err := createConfigMap(ctx, clientB, nsA, "rbac-boundary-attack"); !apierrors.IsForbidden(err) {
		t.Errorf("team B creating in team A's namespace %q: got err=%v, want Forbidden", nsA, err)
	}
}

func createConfigMap(ctx context.Context, client dynamic.Interface, namespace, name string) error {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
	}}
	_, err := client.Resource(configMapsResource).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// TestRBACBoundary_ResourceBindings is TestRBACBoundary's counterpart for
// platform.kratix.io/ResourceBinding - the object the promise
// version-upgrade feature reads/patches (see
// docs/superpowers/specs/2026-08-14-promise-version-upgrades-design.md).
// Confirms the RBAC change in marketplace-rbac.yaml actually grants teams
// get/list/patch on their own namespace's bindings, and nothing on
// another team's - same boundary, same mechanism, different API group
// than TestRBACBoundary already proves.
func TestRBACBoundary_ResourceBindings(t *testing.T) {
	kubeContext := os.Getenv("BROKER_KUBE_CONTEXT")
	if kubeContext == "" {
		kubeContext = "kind-platform"
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		t.Fatalf("loading kubeconfig (context %q): %v", kubeContext, err)
	}

	groups := k8sclient.NewGroupClients(config)

	const teamA, teamB = "payments", "checkout"
	nsA, nsB := Namespace(teamA), Namespace(teamB)

	clientA, err := groups.ForGroup(Group(teamA))
	if err != nil {
		t.Fatalf("ForGroup(%s): %v", teamA, err)
	}
	clientB, err := groups.ForGroup(Group(teamB))
	if err != nil {
		t.Fatalf("ForGroup(%s): %v", teamB, err)
	}

	ctx := context.Background()

	for _, tc := range []struct {
		team   string
		client dynamic.Interface
		ns     string
	}{
		{teamA, clientA, nsA},
		{teamB, clientB, nsB},
	} {
		var lastErr error
		err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 15*time.Second, true, func(ctx context.Context) (bool, error) {
			lastErr = createResourceBinding(ctx, tc.client, tc.ns, "rbac-boundary-test")
			return lastErr == nil, nil
		})
		if err != nil {
			t.Fatalf("%s: create ResourceBinding in own namespace %q never succeeded: %v", tc.team, tc.ns, lastErr)
		}
		if _, err := tc.client.Resource(resourceBindingsResource).Namespace(tc.ns).List(ctx, metav1.ListOptions{}); err != nil {
			t.Fatalf("%s: list ResourceBindings in own namespace %q: %v", tc.team, tc.ns, err)
		}
	}

	if _, err := clientA.Resource(resourceBindingsResource).Namespace(nsB).List(ctx, metav1.ListOptions{}); !apierrors.IsForbidden(err) {
		t.Errorf("team A listing team B's namespace %q: got err=%v, want Forbidden", nsB, err)
	}
	if err := createResourceBinding(ctx, clientA, nsB, "rbac-boundary-attack"); !apierrors.IsForbidden(err) {
		t.Errorf("team A creating in team B's namespace %q: got err=%v, want Forbidden", nsB, err)
	}
}

func createResourceBinding(ctx context.Context, client dynamic.Interface, namespace, name string) error {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "ResourceBinding",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"promiseRef": map[string]interface{}{"name": "database"},
			"resourceRef": map[string]interface{}{
				"name":      "rbac-boundary-test-resource",
				"namespace": namespace,
			},
			"version": "latest",
		},
	}}
	_, err := client.Resource(resourceBindingsResource).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}
