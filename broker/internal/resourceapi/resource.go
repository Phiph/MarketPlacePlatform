// Package resourceapi submits and reads requests against a Promise: it
// creates/lists/gets/deletes instances of whatever custom resource a
// catalog.Entry describes, scoped to a single namespace (a team's).
package resourceapi

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"marketplace-broker/internal/catalog"
)

// ErrAlreadyExists is returned by Submit when a request with the given name
// already exists in the namespace.
var ErrAlreadyExists = fmt.Errorf("request already exists")

// Submit creates a new request: an instance of entry's custom resource,
// named name, in namespace, with spec as its .spec.
func Submit(ctx context.Context, client dynamic.Interface, entry catalog.Entry, namespace, name string, spec map[string]interface{}) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": entry.Group + "/" + entry.Version,
			"kind":       entry.Kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}

	created, err := client.Resource(entry.GVR()).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("creating %s %q in namespace %q: %w", entry.Kind, name, namespace, err)
	}
	return created, nil
}

// List returns every request against entry in namespace.
func List(ctx context.Context, client dynamic.Interface, entry catalog.Entry, namespace string) ([]unstructured.Unstructured, error) {
	list, err := client.Resource(entry.GVR()).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing %s in namespace %q: %w", entry.Kind, namespace, err)
	}
	return list.Items, nil
}

// Get fetches a single request by name. ok is false if it doesn't exist.
func Get(ctx context.Context, client dynamic.Interface, entry catalog.Entry, namespace, name string) (obj *unstructured.Unstructured, ok bool, err error) {
	obj, err = client.Resource(entry.GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("getting %s %q in namespace %q: %w", entry.Kind, name, namespace, err)
	}
	return obj, true, nil
}

// Delete removes a request by name. ok is false if it didn't exist.
func Delete(ctx context.Context, client dynamic.Interface, entry catalog.Entry, namespace, name string) (ok bool, err error) {
	err = client.Resource(entry.GVR()).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("deleting %s %q in namespace %q: %w", entry.Kind, name, namespace, err)
	}
	return true, nil
}

// Update replaces an existing request's .spec wholesale - not a merge
// patch. Callers (the broker's update handlers) always submit the complete
// desired spec, matching how Submit's create semantics already work; a
// merge patch would silently leave behind any field the caller omitted
// because the user cleared it in the form. ok is false if no such request
// exists.
func Update(ctx context.Context, client dynamic.Interface, entry catalog.Entry, namespace, name string, spec map[string]interface{}) (obj *unstructured.Unstructured, ok bool, err error) {
	existing, err := client.Resource(entry.GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("getting %s %q in namespace %q: %w", entry.Kind, name, namespace, err)
	}

	existing.Object["spec"] = spec

	updated, err := client.Resource(entry.GVR()).Namespace(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("updating %s %q in namespace %q: %w", entry.Kind, name, namespace, err)
	}
	return updated, true, nil
}
