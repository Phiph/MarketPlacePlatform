import kratix_sdk as ks


def main():
    sdk = ks.KratixSDK()
    resource = sdk.read_resource_input()
    name = resource.get_name()
    description = resource.get_value("spec.description", default=None)

    # No output manifest: a Project is a pure grouping record an Environment
    # (see promises/environment/) references by name - it doesn't provision
    # any infrastructure of its own, so there's nothing to write to
    # /kratix/output. Status is the only observable effect of this pipeline.
    status = ks.Status()
    status.set("name", name)
    if description:
        status.set("description", description)
    status.set("message", f"Project {name} registered - request Environments under it to provision namespaces.")
    sdk.write_status(status)


if __name__ == "__main__":
    main()
