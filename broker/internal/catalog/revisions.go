package catalog

import (
	"context"
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// PromiseRevisionGVR is where Kratix stores each snapshot of a Promise's
// spec at a specific version - see
// docs.kratix.io/main/reference/promises/promise-upgrade/promise-revisions.
var PromiseRevisionGVR = schema.GroupVersionResource{
	Group:    "platform.kratix.io",
	Version:  "v1alpha1",
	Resource: "promiserevisions",
}

// LabelPromiseName is the label Kratix sets on every PromiseRevision and
// ResourceBinding it creates, identifying which Promise they belong to.
const LabelPromiseName = "kratix.io/promise-name"

// LabelLatestRevision marks the one PromiseRevision Kratix currently
// considers latest for a given Promise.
const LabelLatestRevision = "kratix.io/latest-revision"

// Revision is one version of a Promise, as Kratix snapshotted it.
type Revision struct {
	Version   string    `json:"version"`
	Latest    bool      `json:"latest"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListRevisions returns every PromiseRevision Kratix has created for
// promiseName, newest first.
func ListRevisions(ctx context.Context, client dynamic.Interface, promiseName string) ([]Revision, error) {
	list, err := client.Resource(PromiseRevisionGVR).List(ctx, metav1.ListOptions{
		LabelSelector: LabelPromiseName + "=" + promiseName,
	})
	if err != nil {
		return nil, fmt.Errorf("listing promise revisions for %q: %w", promiseName, err)
	}

	revisions := make([]Revision, 0, len(list.Items))
	for _, item := range list.Items {
		version, _, _ := unstructured.NestedString(item.Object, "spec", "version")
		if version == "" {
			continue
		}
		revisions = append(revisions, Revision{
			Version:   version,
			Latest:    item.GetLabels()[LabelLatestRevision] == "true",
			CreatedAt: item.GetCreationTimestamp().Time,
		})
	}

	sort.Slice(revisions, func(i, j int) bool { return revisions[i].CreatedAt.After(revisions[j].CreatedAt) })
	return revisions, nil
}

// RevisionSchema returns the request schema embedded in promiseName's
// PromiseRevision at version - the same shape Entry.Schema holds, sourced
// from spec.promiseSpec.api instead of a live Promise's spec.api. ok is
// false if no such version exists for this Promise.
func RevisionSchema(ctx context.Context, client dynamic.Interface, promiseName, version string) (schemaObj map[string]interface{}, ok bool, err error) {
	list, err := client.Resource(PromiseRevisionGVR).List(ctx, metav1.ListOptions{
		LabelSelector: LabelPromiseName + "=" + promiseName,
	})
	if err != nil {
		return nil, false, fmt.Errorf("listing promise revisions for %q: %w", promiseName, err)
	}

	for _, item := range list.Items {
		itemVersion, _, _ := unstructured.NestedString(item.Object, "spec", "version")
		if itemVersion != version {
			continue
		}
		apiObj, _, _ := unstructured.NestedMap(item.Object, "spec", "promiseSpec", "api")
		_, _, _, _, _, schemaObj, ok = parseCRD(apiObj)
		return schemaObj, ok, nil
	}
	return nil, false, nil
}
