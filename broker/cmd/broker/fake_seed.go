package main

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"marketplace-broker/internal/catalog"
)

// fakeGVRToListKind and fakeSeedObjects describe the in-memory catalog
// BROKER_FAKE_K8S mode serves: a single Database Promise, mirroring
// promises/database/promise.yaml's real shape, which is enough for a
// client to exercise a full submit/get/edit/delete request lifecycle
// without a cluster.
var fakeGVRToListKind = map[schema.GroupVersionResource]string{
	catalog.PromiseGVR: "PromiseList",
	{Group: "demo.kratix.io", Version: "v1alpha1", Resource: "databases"}: "DatabaseList",
}

func fakeSeedObjects() []runtime.Object {
	return []runtime.Object{fakeDatabasePromise()}
}

func fakeDatabasePromise() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.kratix.io/v1alpha1",
		"kind":       "Promise",
		"metadata": map[string]interface{}{
			"name": "database",
			"labels": map[string]interface{}{
				catalog.LabelVisible: "true",
			},
			"annotations": map[string]interface{}{
				catalog.AnnotationDisplayName: "Postgres Database",
				catalog.AnnotationDescription: "A sized, managed Postgres database, provisioned on request.",
			},
		},
		"spec": map[string]interface{}{
			"api": map[string]interface{}{
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
			},
		},
	}}
}
