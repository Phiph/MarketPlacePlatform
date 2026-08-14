def build_deployment(name, namespace, spec):
    replicas = spec.get("replicas", 1)
    container = {
        "name": name,
        "image": spec["image"],
        "resources": {
            "requests": {"cpu": spec["cpu"], "memory": spec["memory"]},
            "limits": {"cpu": spec["cpu"], "memory": spec["memory"]},
        },
    }
    port = spec.get("port")
    if port:
        container["ports"] = [{"containerPort": port}]
    env = spec.get("env")
    if env:
        container["env"] = [{"name": e["name"], "value": e["value"]} for e in env]

    return {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": name, "namespace": namespace},
        "spec": {
            "replicas": replicas,
            "selector": {"matchLabels": {"app": name}},
            "template": {
                "metadata": {"labels": {"app": name}},
                "spec": {"containers": [container]},
            },
        },
    }


def build_service(name, namespace, spec):
    port = spec.get("port")
    if not port:
        return None
    return {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": name, "namespace": namespace},
        "spec": {
            "selector": {"app": name},
            "ports": [{"port": port, "targetPort": port}],
        },
    }


def main():
    import kratix_sdk as ks
    import yaml

    sdk = ks.KratixSDK()
    resource = sdk.read_resource_input()
    name = resource.get_name()
    # Hardcoded, not resource.get_namespace(): the worker cluster (where this
    # Deployment/Service land) has no per-team/environment namespaces yet -
    # see docs/superpowers/specs/2026-08-14-container-promise-design.md,
    # "Known limitation". Matches database's existing precedent.
    namespace = "default"
    spec = {
        "image": resource.get_value("spec.image"),
        "replicas": resource.get_value("spec.replicas", default=1),
        "cpu": resource.get_value("spec.cpu"),
        "memory": resource.get_value("spec.memory"),
        "port": resource.get_value("spec.port", default=None),
        "env": resource.get_value("spec.env", default=None),
    }

    deployment = build_deployment(name, namespace, spec)
    sdk.write_output("deployment.yaml", yaml.safe_dump(deployment).encode("utf-8"))

    service = build_service(name, namespace, spec)
    if service:
        sdk.write_output("service.yaml", yaml.safe_dump(service).encode("utf-8"))

    status = ks.Status()
    status.set("image", spec["image"])
    if service:
        status.set("service", name)
    sdk.write_status(status)


if __name__ == "__main__":
    main()
