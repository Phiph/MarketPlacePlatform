// Package k8sclient builds the Kubernetes clients the broker uses to talk to
// the platform cluster: a dynamic client for Promises and the resource
// requests they define, plus a typed clientset for tenant namespace
// management.
package k8sclient

import (
	"fmt"
	"os"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients bundles the Kubernetes clients the broker needs.
type Clients struct {
	Dynamic dynamic.Interface
	Typed   kubernetes.Interface
	// Groups builds per-team impersonated clients (see impersonate.go) -
	// how every resource-request call is scoped to the calling team's
	// namespace, with Capsule (not application code) enforcing the
	// boundary. Interface (rather than *GroupClients directly) so tests can
	// substitute a stub that hands back a fake dynamic.Interface without
	// going through real impersonation/REST plumbing - *GroupClients
	// satisfies it unchanged.
	Groups GroupResolver
}

// GroupResolver builds a dynamic.Interface scoped to a Kubernetes Group.
// See GroupClients.ForGroup for the real implementation.
type GroupResolver interface {
	ForGroup(group string) (dynamic.Interface, error)
}

// New builds Clients from a kubeconfig, honouring the standard KUBECONFIG
// env var (defaulting to ~/.kube/config) and overriding the context with
// BROKER_KUBE_CONTEXT (defaulting to "kind-platform", where Kratix and its
// Promises run in this repo's local dev environment).
func New() (*Clients, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()

	kubeContext := os.Getenv("BROKER_KUBE_CONTEXT")
	if kubeContext == "" {
		kubeContext = "kind-platform"
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig (context %q): %w", kubeContext, err)
	}

	return newFromRESTConfig(config)
}

func newFromRESTConfig(config *rest.Config) (*Clients, error) {
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("building dynamic client: %w", err)
	}

	typedClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("building typed client: %w", err)
	}

	return &Clients{Dynamic: dynamicClient, Typed: typedClient, Groups: NewGroupClients(config)}, nil
}
