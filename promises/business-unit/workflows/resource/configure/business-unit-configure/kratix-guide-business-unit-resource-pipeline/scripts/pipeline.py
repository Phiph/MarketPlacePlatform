import kratix_sdk as ks
import yaml


DEFAULT_NAMESPACE_QUOTA = 5

# Matched by the shared GlobalTenantResource's tenantSelector (see
# promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/marketplace-rbac.yaml)
# - that's the mechanism that grants each team access to only its own
# namespace within this Tenant, so every business unit's Tenant needs this
# label to be picked up by it.
LABEL_MANAGED = "marketplace.kratix.io/managed"


def main():
    sdk = ks.KratixSDK()
    resource = sdk.read_resource_input()
    bu = resource.get_name()
    namespace_quota = resource.get_value("spec.namespaceQuota", default=None) or DEFAULT_NAMESPACE_QUOTA
    resource_quotas = resource.get_value("spec.resourceQuotas", default=None)

    # Deliberately no `owners` here. Capsule's default owner model grants
    # every owner equal access to every namespace in the Tenant - fine for a
    # flat "team == tenant" design, but wrong once multiple teams share one
    # business unit's Tenant (that would let any team in the BU see every
    # other team's namespace). Team-level access instead comes entirely from
    # the shared GlobalTenantResource, which grants a team's Group access to
    # only its own namespace. A BU-admin persona (someone who legitimately
    # needs to see every namespace in the BU) isn't modelled yet - add an
    # owners entry here when that's needed.
    tenant = {
        "apiVersion": "capsule.clastix.io/v1beta2",
        "kind": "Tenant",
        "metadata": {
            "name": bu,
            "labels": {LABEL_MANAGED: "true"},
        },
        "spec": {
            "owners": [],
            "namespaceOptions": {"quota": namespace_quota},
        },
    }
    if resource_quotas:
        tenant["spec"]["resourceQuotas"] = {"items": [{"hard": resource_quotas}]}

    sdk.write_output("tenant.yaml", yaml.safe_dump(tenant).encode("utf-8"))

    status = ks.Status()
    status.set("tenant", bu)
    sdk.write_status(status)


if __name__ == "__main__":
    main()
