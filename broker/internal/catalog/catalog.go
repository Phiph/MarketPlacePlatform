// Package catalog turns installed Kratix Promises into a marketplace
// catalog: for each Promise it surfaces the request schema (so a UI can
// render a form) and the GroupVersionResource needed to submit a request
// against it.
package catalog

import (
	"context"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// PromiseGVR is where Kratix stores installed Promises. It's stable across
// Promises - every catalog entry is derived from an object of this type.
var PromiseGVR = schema.GroupVersionResource{
	Group:    "platform.kratix.io",
	Version:  "v1alpha1",
	Resource: "promises",
}

// Marketplace metadata convention: Promise authors opt a Promise into the
// catalog, and describe it, via a small set of well-known labels/annotations
// on the Promise object itself (see README.md for the full writeup).
//
// Visibility is a label (not an annotation) specifically so List can filter
// with a LabelSelector at the API server rather than fetching every Promise
// and filtering client-side. Display name and description are annotations
// because label values are capped at 63 chars of a narrow charset - too
// restrictive for free text.
const (
	LabelVisible          = "marketplace.kratix.io/visible"
	AnnotationDisplayName = "marketplace.kratix.io/display-name"
	AnnotationDescription = "marketplace.kratix.io/description"
	AnnotationOwner       = "marketplace.kratix.io/owner"
	AnnotationLifecycle   = "marketplace.kratix.io/lifecycle"
	AnnotationSupport     = "marketplace.kratix.io/support"
	AnnotationPolicy      = "marketplace.kratix.io/policy"
)

// validLifecycles and validPolicies are the fixed, queryable value sets for
// the lifecycle/policy evidence annotations - an unrecognised value counts
// the same as an absent one (see parseEntry).
var validLifecycles = map[string]bool{"experimental": true, "stable": true, "deprecated": true}
var validPolicies = map[string]bool{"internal": true, "confidential": true, "regulated": true}

// Entry is one Promise as seen by the marketplace: enough to render it in a
// catalog and build a request against it.
type Entry struct {
	Name        string                 `json:"name"`
	DisplayName string                 `json:"displayName"`
	Description string                 `json:"description,omitempty"`
	Visible     bool                   `json:"visible"`
	Group       string                 `json:"group"`
	Version     string                 `json:"version"`
	Kind        string                 `json:"kind"`
	Plural      string                 `json:"plural"`
	Scope       string                 `json:"scope"`
	Schema      map[string]interface{} `json:"schema,omitempty"`
	Status      map[string]interface{} `json:"status,omitempty"`

	// Operational evidence: see README.md, "Marketplace metadata
	// convention". Owner/Lifecycle/Support/Policy are read straight off
	// the Promise's annotations with no fallback default - an empty
	// string means the annotation is absent. MissingEvidence is the
	// queryable answer to "does this Promise have complete operational
	// evidence": nil/empty when it does, otherwise the subset of
	// {"owner", "lifecycle", "support", "policy"} that's absent or (for
	// Lifecycle/Policy) not one of the fixed allowed values.
	Owner           string   `json:"owner,omitempty"`
	Lifecycle       string   `json:"lifecycle,omitempty"`
	Support         string   `json:"support,omitempty"`
	Policy          string   `json:"policy,omitempty"`
	MissingEvidence []string `json:"missingEvidence,omitempty"`
}

// GVR is the GroupVersionResource of the custom resource this Promise
// defines - i.e. what a request against this Promise creates.
func (e Entry) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: e.Group, Version: e.Version, Resource: e.Plural}
}

// Namespaced reports whether requests against this Promise live in a
// namespace (true for every Promise this broker can broker - cluster-scoped
// Promise APIs aren't supported since the whole point is per-team isolation).
func (e Entry) Namespaced() bool {
	return e.Scope == "Namespaced"
}

// List returns installed Promises as catalog Entries, sorted by name. By
// default only Promises labeled marketplace.kratix.io/visible=true are
// returned (filtered server-side via a LabelSelector); pass all=true to
// bypass that and see every installed Promise regardless of visibility. A
// Promise that doesn't parse as expected is skipped rather than failing the
// whole listing - one malformed Promise shouldn't take down the catalog.
func List(ctx context.Context, client dynamic.Interface, all bool) ([]Entry, error) {
	opts := metav1.ListOptions{}
	if !all {
		opts.LabelSelector = LabelVisible + "=true"
	}

	list, err := client.Resource(PromiseGVR).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing promises: %w", err)
	}

	entries := make([]Entry, 0, len(list.Items))
	for _, item := range list.Items {
		entry, ok := parseEntry(&item)
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// Get fetches a single Promise by name and parses it into a catalog Entry,
// regardless of its marketplace visibility (visibility only gates List).
func Get(ctx context.Context, client dynamic.Interface, name string) (*Entry, bool, error) {
	obj, err := client.Resource(PromiseGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("getting promise %q: %w", name, err)
	}

	entry, ok := parseEntry(obj)
	if !ok {
		return nil, false, fmt.Errorf("promise %q does not have a recognisable spec.api CRD", name)
	}
	return &entry, true, nil
}

// parseEntry reads a Promise's embedded spec.api (a CustomResourceDefinition
// manifest) and picks the storage version's schema to expose.
func parseEntry(obj *unstructured.Unstructured) (Entry, bool) {
	name := obj.GetName()

	group, _, _ := unstructured.NestedString(obj.Object, "spec", "api", "spec", "group")
	kind, _, _ := unstructured.NestedString(obj.Object, "spec", "api", "spec", "names", "kind")
	plural, _, _ := unstructured.NestedString(obj.Object, "spec", "api", "spec", "names", "plural")
	scope, _, _ := unstructured.NestedString(obj.Object, "spec", "api", "spec", "scope")
	versions, _, _ := unstructured.NestedSlice(obj.Object, "spec", "api", "spec", "versions")

	if group == "" || kind == "" || plural == "" || len(versions) == 0 {
		return Entry{}, false
	}

	version, schemaObj := pickVersion(versions)
	if version == "" {
		return Entry{}, false
	}

	status, _, _ := unstructured.NestedMap(obj.Object, "status")

	displayName := obj.GetAnnotations()[AnnotationDisplayName]
	if displayName == "" {
		displayName = name
	}

	owner := obj.GetAnnotations()[AnnotationOwner]
	lifecycle := obj.GetAnnotations()[AnnotationLifecycle]
	support := obj.GetAnnotations()[AnnotationSupport]
	policy := obj.GetAnnotations()[AnnotationPolicy]

	var missing []string
	if owner == "" {
		missing = append(missing, "owner")
	}
	if !validLifecycles[lifecycle] {
		missing = append(missing, "lifecycle")
	}
	if support == "" {
		missing = append(missing, "support")
	}
	if !validPolicies[policy] {
		missing = append(missing, "policy")
	}

	return Entry{
		Name:            name,
		DisplayName:     displayName,
		Description:     obj.GetAnnotations()[AnnotationDescription],
		Visible:         obj.GetLabels()[LabelVisible] == "true",
		Group:           group,
		Version:         version,
		Kind:            kind,
		Plural:          plural,
		Scope:           scope,
		Schema:          schemaObj,
		Status:          status,
		Owner:           owner,
		Lifecycle:       lifecycle,
		Support:         support,
		Policy:          policy,
		MissingEvidence: missing,
	}, true
}

// pickVersion prefers the storage version (there's exactly one per CRD
// convention), falling back to the first served version, then the first
// version present at all.
func pickVersion(versions []interface{}) (string, map[string]interface{}) {
	var fallbackServed, fallbackAny string
	var fallbackServedSchema, fallbackAnySchema map[string]interface{}

	for _, v := range versions {
		versionMap, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(versionMap, "name")
		if name == "" {
			continue
		}
		openAPISchema, _, _ := unstructured.NestedMap(versionMap, "schema", "openAPIV3Schema")

		if fallbackAny == "" {
			fallbackAny, fallbackAnySchema = name, openAPISchema
		}

		served, _, _ := unstructured.NestedBool(versionMap, "served")
		if served && fallbackServed == "" {
			fallbackServed, fallbackServedSchema = name, openAPISchema
		}

		storage, _, _ := unstructured.NestedBool(versionMap, "storage")
		if storage {
			return name, openAPISchema
		}
	}

	if fallbackServed != "" {
		return fallbackServed, fallbackServedSchema
	}
	return fallbackAny, fallbackAnySchema
}
