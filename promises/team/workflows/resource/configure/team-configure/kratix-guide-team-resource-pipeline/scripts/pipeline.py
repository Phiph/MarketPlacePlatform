import kratix_sdk as ks
import yaml


# Naming source of truth: broker/internal/tenant/tenant.go's Namespace()
# computes this identical string - the two can't share code across
# languages, so both sides carry a comment pointing at the other.
def namespace_name(team: str) -> str:
    return f"team-{team}"


# Read by the shared GlobalTenantResource (see promises/business-unit's
# team-rbac.yaml) to derive this namespace's owning team's Kubernetes Group.
LABEL_TEAM = "marketplace.kratix.io/team"

# Where Argo CD itself runs - AppProject is namespace-scoped and must live
# in Argo's own control-plane namespace, unlike the Namespace this pipeline
# also writes (which lives wherever the team's own workloads do). See
# docs/superpowers/specs/2026-08-14-container-workload-logs-design.md,
# "RBAC".
#
# Naming source of truth: the Makefile's ARGO_WORKER_CLUSTER_NAME and
# ARGO_ROLE variables (argo-provision-teams target) must compute/hold the
# identical strings - the two can't share code across languages, so both
# sides carry a comment pointing at the other, same convention as
# namespace_name()'s cross-reference to tenant.go's Namespace() below.
ARGO_NAMESPACE = "argocd"
ARGO_WORKER_CLUSTER_NAME = "worker-1"
ARGO_ROLE = "viewer"


def build_app_project(team: str) -> dict:
    ns = namespace_name(team)
    return {
        "apiVersion": "argoproj.io/v1alpha1",
        "kind": "AppProject",
        "metadata": {"name": team, "namespace": ARGO_NAMESPACE},
        "spec": {
            # Wildcarded rather than pinned to this repo's URL: which
            # source(s) an Application in this project may reference is a
            # per-Application concern (see the next plan), not something
            # this team-scoping layer needs to constrain.
            "sourceRepos": ["*"],
            "destinations": [{"name": ARGO_WORKER_CLUSTER_NAME, "namespace": ns}],
            "sourceNamespaces": [ns],
            # Argo never applies anything in this design (Flux is the sole
            # applier - see the design doc's "Architecture"). Empty
            # whitelists are a second, independent guarantee of that: even
            # a misconfigured Application in this project has nothing it's
            # allowed to sync.
            "namespaceResourceWhitelist": [],
            "clusterResourceWhitelist": [],
            "roles": [
                {
                    "name": ARGO_ROLE,
                    "description": f"Read-only ({team}): application status and pod logs only, no sync/delete.",
                    "policies": [
                        f"p, proj:{team}:{ARGO_ROLE}, applications, get, {team}/*, allow",
                        f"p, proj:{team}:{ARGO_ROLE}, logs, get, {team}/*, allow",
                    ],
                }
            ],
        },
    }


def main():
    sdk = ks.KratixSDK()
    resource = sdk.read_resource_input()
    team = resource.get_name()
    business_unit = resource.get_value("spec.businessUnit")

    ns = namespace_name(team)

    # Only ever writes a Namespace - never a Tenant. The referenced
    # business_unit's Tenant is expected to already exist (a separate,
    # earlier BusinessUnit request); this pipeline doesn't create or manage
    # it. That separation is what keeps this safe to ship declaratively:
    # unlike the earlier "one Tenant per team" design (see
    # promises/business-unit/README.md's "Why no Namespace output" section
    # for that history), there's no risk of this Namespace and its Tenant
    # racing to apply in the same Flux batch, because they're never in the
    # same batch - the Tenant landed in a prior, separate resource request.
    namespace = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {
            "name": ns,
            "labels": {
                "capsule.clastix.io/tenant": business_unit,
                LABEL_TEAM: team,
            },
        },
    }
    sdk.write_output("namespace.yaml", yaml.safe_dump(namespace).encode("utf-8"))

    app_project = build_app_project(team)
    sdk.write_output("app-project.yaml", yaml.safe_dump(app_project).encode("utf-8"))

    status = ks.Status()
    status.set("namespace", ns)
    status.set("businessUnit", business_unit)
    status.set("argoProject", team)
    sdk.write_status(status)


if __name__ == "__main__":
    main()
