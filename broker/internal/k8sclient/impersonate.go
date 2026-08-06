package k8sclient

import (
	"fmt"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// ImpersonatedUser is the fixed username every impersonated call carries.
// The Kubernetes API server requires Impersonate-User whenever
// Impersonate-Group is set; its value doesn't matter to Capsule (which
// recognises callers by Group, via CapsuleUserGroup), so one constant
// covers every team.
const ImpersonatedUser = "marketplace-broker"

// CapsuleUserGroup is the Kubernetes Group Capsule's CapsuleConfiguration
// recognises as a Capsule user at all (spec.users, chart default
// `manager.options.users`). Every impersonated call must carry this
// alongside the team-specific Group, or Capsule ignores the request
// entirely rather than applying its RBAC. Re-verify against the live
// cluster's `kubectl get capsuleconfiguration default -o
// jsonpath='{.spec.users}'` if promises/business-unit's Capsule dependency
// step is ever rendered with a values override that changes this.
const CapsuleUserGroup = "projectcapsule.dev"

// newDynamicClient is a seam over dynamic.NewForConfig so tests can stub it
// out without a real cluster. Wrapped (rather than assigned directly) since
// dynamic.NewForConfig returns the concrete *dynamic.DynamicClient, and Go
// won't implicitly convert that to the dynamic.Interface-returning func
// type a test stub needs to assign here.
var newDynamicClient = func(config *rest.Config) (dynamic.Interface, error) {
	return dynamic.NewForConfig(config)
}

// GroupClients builds dynamic.Interface clients impersonating a Kubernetes
// Group, caching one per group so repeated calls for the same team reuse
// the same client.
type GroupClients struct {
	base *rest.Config

	mu      sync.Mutex
	clients map[string]dynamic.Interface
}

// NewGroupClients builds a GroupClients from a base REST config (the
// broker's own, typically cluster-admin in local dev). base is never
// mutated; each call to ForGroup copies it.
func NewGroupClients(base *rest.Config) *GroupClients {
	return &GroupClients{base: base, clients: make(map[string]dynamic.Interface)}
}

// ForGroup returns a dynamic.Interface impersonating group, alongside the
// fixed CapsuleUserGroup every call needs to be recognised by Capsule at
// all. Callers must only ever pass tenant.Group(...) output here, never a
// caller-controlled string - impersonation is exactly the boundary that
// keeps one team from touching another's resources, so an unvalidated
// group would defeat it.
func (g *GroupClients) ForGroup(group string) (dynamic.Interface, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if client, ok := g.clients[group]; ok {
		return client, nil
	}

	config := rest.CopyConfig(g.base)
	config.Impersonate = rest.ImpersonationConfig{
		UserName: ImpersonatedUser,
		Groups:   []string{CapsuleUserGroup, group},
	}

	client, err := newDynamicClient(config)
	if err != nil {
		return nil, fmt.Errorf("building impersonated client for group %q: %w", group, err)
	}

	g.clients[group] = client
	return client, nil
}
