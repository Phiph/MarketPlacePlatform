def build_manifest(name: str, namespace: str, size: str) -> dict:
    return {
        "apiVersion": "acid.zalan.do/v1",
        "kind": "postgresql",
        "metadata": {"name": name, "namespace": namespace},
        "spec": {
            "teamId": "kratix",
            "enableLogicalBackup": True,
            "volume": {"size": size},
            "numberOfInstances": 2,
            "users": {"team-a": ["superuser", "createdb"]},
            "postgresql": {"version": "16"},
        },
    }


def main():
    import kratix_sdk as ks
    import yaml

    sdk = ks.KratixSDK()
    resource = sdk.read_resource_input()
    name = resource.get_name()
    size = resource.get_value("spec.size")
    namespace = resource.get_namespace()

    manifest = build_manifest(name, namespace, size)
    data = yaml.safe_dump(manifest).encode("utf-8")
    sdk.write_output("database.yaml", data)

    status = ks.Status()
    status.set("teamId", "kratix")
    sdk.write_status(status)


if __name__ == "__main__":
    main()
