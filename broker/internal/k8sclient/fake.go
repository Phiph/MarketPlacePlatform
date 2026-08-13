package k8sclient

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// NewFake builds Clients backed entirely in-memory - no kubeconfig, no real
// or even network-reachable API server - seeded with objects. Every team's
// Groups.ForGroup(...) call returns the same fake dynamic client: fake mode
// doesn't model per-team RBAC (that boundary is only meaningfully proven
// against a real cluster - see internal/tenant/rbac_integration_test.go).
// It exists so the real broker binary (same routing, handlers, and JSON
// shapes as production) can be driven over real HTTP for fast, cluster-free
// testing - see cmd/broker/main.go's BROKER_FAKE_K8S mode.
func NewFake(gvrToListKind map[schema.GroupVersionResource]string, objects ...runtime.Object) *Clients {
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objects...)
	return &Clients{
		Dynamic: dynamicClient,
		Typed:   k8sfake.NewSimpleClientset(),
		Groups:  fakeGroupResolver{dynamicClient},
	}
}

type fakeGroupResolver struct {
	client dynamic.Interface
}

func (f fakeGroupResolver) ForGroup(string) (dynamic.Interface, error) {
	return f.client, nil
}
