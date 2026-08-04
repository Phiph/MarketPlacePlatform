// Package tenant maps API callers to teams, and teams to their own
// Kubernetes namespace. Auth here is a static API-key lookup - a stand-in
// for real authn (OIDC/SSO) appropriate for a local demo, not production.
package tenant

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// LabelTeam marks a namespace as belonging to a team, so EnsureNamespace
// can tell "already exists and is ours" apart from "name collides with
// something else."
const LabelTeam = "marketplace.kratix.io/team"

// Directory resolves API keys to teams, loaded once at startup from a
// teams.yaml file shaped like:
//
//	teams:
//	  team-payments: "demo-key-payments"
//	  team-checkout: "demo-key-checkout"
type Directory struct {
	teamByAPIKey map[string]string
}

type directoryFile struct {
	Teams map[string]string `json:"teams"`
}

// Load reads a Directory from a teams.yaml file at path.
func Load(path string) (*Directory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading teams file %q: %w", path, err)
	}

	var file directoryFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing teams file %q: %w", path, err)
	}

	teamByAPIKey := make(map[string]string, len(file.Teams))
	for team, apiKey := range file.Teams {
		if team == "" || apiKey == "" {
			continue
		}
		teamByAPIKey[apiKey] = team
	}

	return &Directory{teamByAPIKey: teamByAPIKey}, nil
}

// Resolve looks up which team an API key belongs to.
func (d *Directory) Resolve(apiKey string) (team string, ok bool) {
	team, ok = d.teamByAPIKey[apiKey]
	return team, ok
}

// Namespace returns the Kubernetes namespace a team's resource requests live
// in. Namespace names are DNS labels, so team names must already be valid
// ones (teams.yaml is operator-controlled, not user input).
func Namespace(team string) string {
	return "team-" + team
}

// EnsureNamespace gets or creates the namespace for a team, labeling it so
// it's identifiable as the broker's own.
func EnsureNamespace(ctx context.Context, client kubernetes.Interface, team string) (string, error) {
	ns := Namespace(team)

	_, err := client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		return ns, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("getting namespace %q: %w", ns, err)
	}

	_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   ns,
			Labels: map[string]string{LabelTeam: team},
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating namespace %q: %w", ns, err)
	}

	return ns, nil
}
