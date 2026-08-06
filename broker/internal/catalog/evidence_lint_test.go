package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// TestPromiseYAMLFilesHaveOperationalEvidence guards against a checked-in
// Promise regressing on the marketplace.kratix.io/{owner,lifecycle,support,
// policy} convention (see the root README.md, "Marketplace metadata
// convention"). Every installed Promise needs this metadata queryable from
// the running platform, independent of marketplace.kratix.io/visible - see
// docs/superpowers/specs/2026-08-07-operational-evidence-design.md for why.
//
// Unlike catalog_test.go's synthetic fixtures, this reads the real checked-in
// files, three directories up from this package to the repo root.
func TestPromiseYAMLFilesHaveOperationalEvidence(t *testing.T) {
	matches, err := filepath.Glob("../../../promises/*/promise.yaml")
	if err != nil {
		t.Fatalf("globbing promise.yaml files: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no promises/*/promise.yaml files found - is the glob path wrong?")
	}

	for _, path := range matches {
		path := path
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}

			var obj unstructured.Unstructured
			if err := yaml.Unmarshal(raw, &obj.Object); err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}

			entry, ok := parseEntry(&obj)
			if !ok {
				t.Fatalf("%s doesn't parse as a catalog entry (missing spec.api?)", path)
			}
			if len(entry.MissingEvidence) > 0 {
				t.Errorf("%s (Promise %q) is missing operational evidence: %v - add the corresponding marketplace.kratix.io/* annotation(s)",
					path, entry.Name, entry.MissingEvidence)
			}
		})
	}
}
