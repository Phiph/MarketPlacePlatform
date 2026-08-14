package main

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"marketplace-broker/internal/bindingapi"
	"marketplace-broker/internal/catalog"
)

// fakeGVRToListKind and fakeSeedObjects describe the in-memory catalog
// BROKER_FAKE_K8S mode serves: a single Database Promise, now at
// kratix.io/promise-version v0.2.0 with both its v0.1.0 and v0.2.0
// revisions on record, plus one example request still pinned to the
// older v0.1.0 revision via its ResourceBinding - enough for a client to
// exercise a full submit/get/edit/delete/version-move request lifecycle,
// including an upgrade-available scenario, without a cluster.
var fakeGVRToListKind = map[schema.GroupVersionResource]string{
	catalog.PromiseGVR:         "PromiseList",
	catalog.PromiseRevisionGVR: "PromiseRevisionList",
	bindingapi.GVR:             "ResourceBindingList",
	{Group: "demo.kratix.io", Version: "v1alpha1", Resource: "databases"}: "DatabaseList",
}

func fakeSeedObjects() []runtime.Object {
	return []runtime.Object{
		fakeDatabasePromise(),
		fakeDatabaseRevision("v0.1.0", false),
		fakeDatabaseRevision("v0.2.0", true),
		fakeExampleDatabaseRequest(),
		fakeExampleDatabaseBinding(),
	}
}

func fakeDatabasePromise() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "Promise",
		"metadata": map[string]interface{}{
			"name": "database",
			"labels": map[string]interface{}{
				catalog.LabelVisible:        "true",
				catalog.LabelPromiseVersion: "v0.2.0",
			},
			"annotations": map[string]interface{}{
				catalog.AnnotationDisplayName: "Postgres Database",
				catalog.AnnotationDescription: "A sized, managed Postgres database, provisioned on request.",
			},
		},
		"spec": map[string]interface{}{
			"api": fakeDatabaseCRDv2(),
		},
	}}
}

// fakeDatabaseCRDv1 is the CustomResourceDefinition manifest for the
// database Promise's v0.1.0 revision - deliberately identical to the real
// Promise's current schema (promises/database/promise.yaml: size only,
// required, enum-constrained), so the "old" revision fixture matches
// production rather than an invented shape.
func fakeDatabaseCRDv1() map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"spec": map[string]interface{}{
			"group": "demo.kratix.io",
			"names": map[string]interface{}{
				"kind":   "Database",
				"plural": "databases",
			},
			"scope": "Namespaced",
			"versions": []interface{}{
				map[string]interface{}{
					"name":    "v1alpha1",
					"served":  true,
					"storage": true,
					"schema": map[string]interface{}{
						"openAPIV3Schema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"spec": map[string]interface{}{
									"type":     "object",
									"required": []interface{}{"size"},
									"properties": map[string]interface{}{
										"size": map[string]interface{}{
											"type": "string",
											"enum": []interface{}{"1Gi", "5Gi", "10Gi", "50Gi"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// fakeDatabaseCRDv2 is the database Promise's v0.2.0 revision: the same
// shape as v1, plus one new optional field, highAvailability - a Promise
// author shipping HA support as a new capability without breaking any
// request still on v0.1.0 (the field is optional, not required, so a v0.1.0
// spec with no highAvailability key remains valid under this schema too).
// This is also the live Promise's current schema (fakeDatabasePromise's
// spec.api, above) - v0.2.0 is "latest".
func fakeDatabaseCRDv2() map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"spec": map[string]interface{}{
			"group": "demo.kratix.io",
			"names": map[string]interface{}{
				"kind":   "Database",
				"plural": "databases",
			},
			"scope": "Namespaced",
			"versions": []interface{}{
				map[string]interface{}{
					"name":    "v1alpha1",
					"served":  true,
					"storage": true,
					"schema": map[string]interface{}{
						"openAPIV3Schema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"spec": map[string]interface{}{
									"type":     "object",
									"required": []interface{}{"size"},
									"properties": map[string]interface{}{
										"size": map[string]interface{}{
											"type": "string",
											"enum": []interface{}{"1Gi", "5Gi", "10Gi", "50Gi"},
										},
										"highAvailability": map[string]interface{}{"type": "boolean"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// fakeDatabaseRevisionSchemas maps each simulated revision to its schema
// builder, so fakeDatabaseRevision can look up the right one per version -
// v0.1.0 and v0.2.0 genuinely differ (see fakeDatabaseCRDv1/v2 above),
// unlike a single shared schema wrapped in two differently-labeled
// revisions.
var fakeDatabaseRevisionSchemas = map[string]func() map[string]interface{}{
	"v0.1.0": fakeDatabaseCRDv1,
	"v0.2.0": fakeDatabaseCRDv2,
}

func fakeDatabaseRevision(version string, latest bool) *unstructured.Unstructured {
	labels := map[string]interface{}{catalog.LabelPromiseName: "database"}
	if latest {
		labels[catalog.LabelLatestRevision] = "true"
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "PromiseRevision",
		"metadata": map[string]interface{}{
			"name":   "database-" + version,
			"labels": labels,
		},
		"spec": map[string]interface{}{
			"version": version,
			"promiseSpec": map[string]interface{}{
				"api": fakeDatabaseRevisionSchemas[version](),
			},
		},
	}}
}

func fakeExampleDatabaseRequest() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "demo.kratix.io/v1alpha1",
		"kind":       "Database",
		"metadata": map[string]interface{}{
			"name":      "example-database",
			"namespace": "team-payments",
		},
		"spec": map[string]interface{}{
			"size": "1Gi",
		},
	}}
}

func fakeExampleDatabaseBinding() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "ResourceBinding",
		"metadata": map[string]interface{}{
			"name":      "example-database-binding",
			"namespace": "team-payments",
			"labels": map[string]interface{}{
				"kratix.io/promise-name":  "database",
				"kratix.io/resource-name": "example-database",
			},
		},
		"spec": map[string]interface{}{
			"version": "v0.1.0",
		},
	}}
}
