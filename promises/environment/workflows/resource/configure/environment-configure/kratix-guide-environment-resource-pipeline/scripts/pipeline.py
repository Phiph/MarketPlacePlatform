def namespace_name(team: str, project: str, environment: str) -> str:
    return f"project-{team}-{project}-{environment}"


# Read by the shared GlobalTenantResource (see promises/business-unit's
# team-rbac.yaml) to derive this namespace's owning team's Kubernetes Group -
# unchanged from how promises/team's Namespace output uses the same label.
LABEL_TEAM = "marketplace.kratix.io/team"

# Traceability only, not read by any RBAC mechanism.
LABEL_PROJECT = "marketplace.kratix.io/project"
LABEL_ENVIRONMENT = "marketplace.kratix.io/environment"


# Only ever builds a Namespace - never a Tenant, same separation
# promises/team's pipeline relies on (see that Promise's README,
# "Provisioning order matters" in promises/business-unit/README.md) to
# stay safe to ship declaratively via Flux: the referenced business
# unit's Tenant is expected to already exist.
def build_namespace(team: str, project: str, environment: str, business_unit: str) -> dict:
    return {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {
            "name": namespace_name(team, project, environment),
            "labels": {
                "capsule.clastix.io/tenant": business_unit,
                LABEL_TEAM: team,
                LABEL_PROJECT: project,
                LABEL_ENVIRONMENT: environment,
            },
        },
    }


def main():
    import kratix_sdk as ks
    import yaml

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

    namespace = build_namespace(team, project, environment, business_unit)
    ns = namespace["metadata"]["name"]
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
