// Package bindingapi reads and moves the ResourceBinding tying a resource
// request to a specific PromiseRevision - see
// docs.kratix.io/main/reference/promises/promise-upgrade/resource-bindings.
package bindingapi

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// GVR is where Kratix stores ResourceBindings - namespaced, one per
// resource request.
var GVR = schema.GroupVersionResource{
	Group:    "platform.kratix.io",
	Version:  "v1alpha1",
	Resource: "resourcebindings",
}

const (
	labelPromiseName  = "kratix.io/promise-name"
	labelResourceName = "kratix.io/resource-name"
)

// Get finds the ResourceBinding for one resource request, by the two
// labels Kratix sets on every binding it creates. The binding's own object
// name is Kratix-owned and never constructed here - this is the same
// lookup the Kratix docs show via `kubectl get resourcebindings -l ...`.
// ok is false if no such binding exists yet (e.g. the narrow window
// between a request being created and Kratix's own controller creating
// its binding).
func Get(ctx context.Context, client dynamic.Interface, namespace, promiseName, resourceName string) (obj *unstructured.Unstructured, ok bool, err error) {
	list, err := client.Resource(GVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelPromiseName + "=" + promiseName + "," + labelResourceName + "=" + resourceName,
	})
	if err != nil {
		return nil, false, fmt.Errorf("listing resource bindings for %s/%s in namespace %q: %w", promiseName, resourceName, namespace, err)
	}
	if len(list.Items) == 0 {
		return nil, false, nil
	}
	return &list.Items[0], true, nil
}

// Version returns the resolved version a binding currently points at:
// binding.spec.version verbatim if it's already a concrete version, or
// latestVersion if the binding says "latest" (the field's default).
func Version(binding *unstructured.Unstructured, latestVersion string) string {
	version, _, _ := unstructured.NestedString(binding.Object, "spec", "version")
	if version == "" || version == "latest" {
		return latestVersion
	}
	return version
}

// SetVersion moves an existing binding to version - get-modify-write, same
// optimistic-concurrency shape as resourceapi.Update: the fetched
// resourceVersion rides along on the Update call, so a concurrent move
// surfaces as a conflict (apierrors.IsConflict). ok is false if no binding
// exists yet for this request.
func SetVersion(ctx context.Context, client dynamic.Interface, namespace, promiseName, resourceName, version string) (obj *unstructured.Unstructured, ok bool, err error) {
	existing, ok, err := Get(ctx, client, namespace, promiseName, resourceName)
	if err != nil || !ok {
		return nil, ok, err
	}

	if err := unstructured.SetNestedField(existing.Object, version, "spec", "version"); err != nil {
		return nil, false, fmt.Errorf("setting spec.version on binding for %s/%s: %w", promiseName, resourceName, err)
	}

	updated, err := client.Resource(GVR).Namespace(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("updating resource binding for %s/%s in namespace %q: %w", promiseName, resourceName, namespace, err)
	}
	return updated, true, nil
}
