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

    status = ks.Status()
    status.set("namespace", ns)
    status.set("businessUnit", business_unit)
    sdk.write_status(status)


if __name__ == "__main__":
    main()
