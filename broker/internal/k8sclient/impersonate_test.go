package k8sclient

import (
	"reflect"
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestForGroup_SetsImpersonation(t *testing.T) {
	orig := newDynamicClient
	defer func() { newDynamicClient = orig }()

	var captured *rest.Config
	newDynamicClient = func(c *rest.Config) (dynamic.Interface, error) {
		captured = c
		return dynamic.NewForConfig(c)
	}

	base := &rest.Config{Host: "https://example.invalid"}
	gc := NewGroupClients(base)

	if _, err := gc.ForGroup("marketplace:team-payments"); err != nil {
		t.Fatalf("ForGroup: %v", err)
	}

	if captured.Impersonate.UserName != ImpersonatedUser {
		t.Errorf("UserName = %q, want %q", captured.Impersonate.UserName, ImpersonatedUser)
	}
	wantGroups := []string{CapsuleUserGroup, "marketplace:team-payments"}
	if !reflect.DeepEqual(captured.Impersonate.Groups, wantGroups) {
		t.Errorf("Groups = %v, want %v", captured.Impersonate.Groups, wantGroups)
	}

	// The base config passed to NewGroupClients must never be mutated -
	// it's shared across every team's client.
	if base.Impersonate.UserName != "" || len(base.Impersonate.Groups) != 0 {
		t.Errorf("base config was mutated: %+v", base.Impersonate)
	}
}

func TestForGroup_CachesPerGroup(t *testing.T) {
	orig := newDynamicClient
	defer func() { newDynamicClient = orig }()

	var calls int
	newDynamicClient = func(c *rest.Config) (dynamic.Interface, error) {
		calls++
		return dynamic.NewForConfig(c)
	}

	gc := NewGroupClients(&rest.Config{Host: "https://example.invalid"})

	if _, err := gc.ForGroup("marketplace:team-payments"); err != nil {
		t.Fatalf("ForGroup: %v", err)
	}
	if _, err := gc.ForGroup("marketplace:team-payments"); err != nil {
		t.Fatalf("ForGroup: %v", err)
	}
	if calls != 1 {
		t.Errorf("repeated ForGroup for the same group built %d clients, want 1", calls)
	}

	if _, err := gc.ForGroup("marketplace:team-checkout"); err != nil {
		t.Fatalf("ForGroup: %v", err)
	}
	if calls != 2 {
		t.Errorf("ForGroup for a different group built %d clients total, want 2", calls)
	}
}
