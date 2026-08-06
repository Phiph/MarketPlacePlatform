import kratix_sdk as ks
import yaml


# Naming source of truth: broker/internal/tenant.ProjectEnvironmentNamespace()
# computes this identical string - the two can't share code across
# languages, so both sides carry a comment pointing at the other. Same
# convention promises/team/'s pipeline uses for its own Namespace().
#
# team is part of the name, not just project/environment: Namespaces are
# cluster-scoped, but a Project's name is only unique within its own
# team's namespace - two different teams can both create a project called
# "checkout-service". Without team in the string, two teams picking the
# same project+environment name would collide on one real Namespace, and
# whichever Environment request reconciled second would silently overwrite
# the first's marketplace.kratix.io/team label - handing that namespace's
# RBAC to the wrong team. See tenant.ProjectEnvironmentNamespace()'s
# comment for the full reasoning.
def namespace_name(team: str, project: str, environment: str) -> str:
    return f"project-{team}-{project}-{environment}"


# Read by the shared GlobalTenantResource (see promises/business-unit's
# team-rbac.yaml) to derive this namespace's owning team's Kubernetes Group -
# unchanged from how promises/team's Namespace output uses the same label.
LABEL_TEAM = "marketplace.kratix.io/team"

# Traceability only, not read by any RBAC mechanism.
LABEL_PROJECT = "marketplace.kratix.io/project"
LABEL_ENVIRONMENT = "marketplace.kratix.io/environment"


def main():
    sdk = ks.KratixSDK()
    resource = sdk.read_resource_input()
    environment = resource.get_name()
    project = resource.get_value("spec.project")
    # team/businessUnit are broker-owned fields (see promise.yaml) - this
    # pipeline just trusts whatever the request carries, the same way
    # promises/team's pipeline trusts spec.businessUnit: enforcing that
    # trust is the broker's job (POST /api/environments composes these
    # itself), not this pipeline's.
    team = resource.get_value("spec.team")
    business_unit = resource.get_value("spec.businessUnit")

    ns = namespace_name(team, project, environment)

    # Only ever writes a Namespace - never a Tenant, same separation
    # promises/team's pipeline relies on (see that Promise's README,
    # "Provisioning order matters" in promises/business-unit/README.md) to
    # stay safe to ship declaratively via Flux: the referenced business
    # unit's Tenant is expected to already exist.
    namespace = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {
            "name": ns,
            "labels": {
                "capsule.clastix.io/tenant": business_unit,
                LABEL_TEAM: team,
                LABEL_PROJECT: project,
                LABEL_ENVIRONMENT: environment,
            },
        },
    }
    sdk.write_output("namespace.yaml", yaml.safe_dump(namespace).encode("utf-8"))

    status = ks.Status()
    status.set("namespace", ns)
    status.set("project", project)
    status.set("team", team)
    status.set(
        "message",
        f"Environment {environment} provisioning - namespace {ns} for project {project} (team {team})",
    )
    sdk.write_status(status)


if __name__ == "__main__":
    main()
