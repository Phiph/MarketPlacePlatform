// Package tenant maps API callers to teams, and teams to their namespace and
// Kubernetes Group naming convention. Auth here is a static API-key lookup -
// a stand-in for real authn (OIDC/SSO) appropriate for a local demo, not
// production.
//
// Provisioning is two separate Kratix Promises, submitted in order:
//   - business-unit (promises/business-unit/) - a Capsule
//     (https://projectcapsule.dev) Tenant with resource quotas. Its owners
//     are deliberately left empty: Capsule's owner model grants every owner
//     equal access to every namespace in the Tenant, which would let one
//     team see every other team sharing its business unit.
//   - team (promises/team/) - a Namespace inside an existing business
//     unit's Tenant, labeled with the team's name. A single, always-installed
//     GlobalTenantResource (see
//     promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/team-rbac.yaml)
//     is what actually grants access: it watches every business-unit Tenant
//     and, per namespace, binds that one namespace's own team Group to it -
//     never to its business-unit-mates'.
//
// A team can further self-serve two more levels underneath its own
// namespace: project (promises/project/) and environment
// (promises/environment/). An environment's Namespace carries the exact
// same marketplace.kratix.io/team label a team's own namespace does, so the
// GlobalTenantResource above grants access to it identically - no RBAC
// changes were needed to add this layer, only namespace-naming and
// request-routing (see internal/api's project/environment-scoped routes).
//
// Both this package's Namespace() and Group() (and ProjectEnvironmentNamespace,
// its scoped-routing equivalent) must stay byte-for-byte in sync with the
// naming those pipelines compute - see the comments on each.
package tenant

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// Directory resolves API keys to teams, loaded once at startup from a
// teams.yaml file shaped like:
//
//	businessUnits:
//	  platform-org:
//	    teams:
//	      payments: "demo-key-payments"
//	      checkout: "demo-key-checkout"
//
// The business unit a team belongs to matters twice: at provisioning time
// (which business-unit's Tenant a team's namespace request should
// reference) and again whenever the broker composes an Environment request
// on a team's behalf (spec.businessUnit - see BusinessUnit below and
// internal/api's POST /environments handler), so unlike before the
// project/environment layer existed, Directory keeps it around after Load
// rather than discarding it.
type Directory struct {
	teamByAPIKey       map[string]string
	businessUnitByTeam map[string]string
}

type directoryFile struct {
	BusinessUnits map[string]struct {
		Teams map[string]string `json:"teams"`
	} `json:"businessUnits"`
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

	teamByAPIKey := make(map[string]string)
	businessUnitByTeam := make(map[string]string)
	for bu, entry := range file.BusinessUnits {
		for team, apiKey := range entry.Teams {
			if team == "" || apiKey == "" {
				continue
			}
			teamByAPIKey[apiKey] = team
			businessUnitByTeam[team] = bu
		}
	}

	return &Directory{teamByAPIKey: teamByAPIKey, businessUnitByTeam: businessUnitByTeam}, nil
}

// Resolve looks up which team an API key belongs to.
func (d *Directory) Resolve(apiKey string) (team string, ok bool) {
	team, ok = d.teamByAPIKey[apiKey]
	return team, ok
}

// BusinessUnit looks up which business unit a team belongs to. Used to
// broker-compose an Environment request's spec.businessUnit from the
// authenticated caller, rather than trusting a client-supplied value (see
// promises/environment/README.md, "Why team/businessUnit are broker-owned
// fields").
func (d *Directory) BusinessUnit(team string) (bu string, ok bool) {
	bu, ok = d.businessUnitByTeam[team]
	return bu, ok
}

// Namespace returns the Kubernetes namespace a team's resource requests live
// in. Namespace names are DNS labels, so team names must already be valid
// ones (teams.yaml is operator-controlled, not user input). Provisioned by
// the team Promise (promises/team/), not this package - by the time a team
// can authenticate at all, its namespace should already exist.
func Namespace(team string) string {
	return "team-" + team
}

// ProjectEnvironmentNamespace returns the Kubernetes namespace a project's
// environment's resource requests live in - the scoped-routing equivalent
// of Namespace() above. This is the naming convention's single source of
// truth on the Go side - promises/environment's pipeline.py's
// namespace_name() must compute the identical string; see the comments on
// both.
//
// team is part of the name, not just project/environment: Namespaces are
// cluster-scoped in Kubernetes, but a Project's name is only unique within
// its owning team's own namespace (two different teams can both create a
// project called "checkout-service"). Without team in the string, two
// teams picking the same project+environment name would collide on one
// real Namespace - and since it's applied declaratively (kubectl
// apply/Flux), whichever team's Environment request reconciled second
// would silently overwrite the first's marketplace.kratix.io/team label,
// handing the namespace's RBAC to the wrong team. team names are already
// assumed globally unique across the whole directory (see Namespace()
// above), so prefixing with team makes this collision-proof the same way.
func ProjectEnvironmentNamespace(team, project, environment string) string {
	return "project-" + team + "-" + project + "-" + environment
}

// GroupPrefix namespaces the Kubernetes Group every impersonated broker
// call carries, so it can't collide with unrelated Groups a real IdP might
// hand out.
const GroupPrefix = "marketplace:team-"

// Group returns the Kubernetes Group that the shared GlobalTenantResource
// binds to a team's namespace (see the package doc), and that the broker
// impersonates for every call it makes on that team's behalf. This is the
// naming convention's single source of truth on the Go side -
// promises/business-unit's team-rbac.yaml template must compute the
// identical string from the same namespace's marketplace.kratix.io/team
// label.
func Group(team string) string {
	return GroupPrefix + team
}
