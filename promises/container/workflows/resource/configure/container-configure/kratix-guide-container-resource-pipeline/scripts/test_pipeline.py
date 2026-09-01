from pipeline import build_deployment, build_service


def test_build_deployment_minimal():
    spec = {"image": "nginx:1.27", "cpu": "250m", "memory": "128Mi"}
    deployment = build_deployment("example-container", "project-team-a-checkout-dev", spec)

    assert deployment["apiVersion"] == "apps/v1"
    assert deployment["kind"] == "Deployment"
    assert deployment["metadata"] == {"name": "example-container", "namespace": "project-team-a-checkout-dev", "labels": {"app": "example-container"}}
    assert deployment["spec"]["replicas"] == 1
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    assert container["name"] == "example-container"
    assert container["image"] == "nginx:1.27"
    assert container["resources"] == {
        "requests": {"cpu": "250m", "memory": "128Mi"},
        "limits": {"cpu": "250m", "memory": "128Mi"},
    }
    assert "ports" not in container
    assert "env" not in container


def test_build_deployment_with_replicas_port_and_env():
    spec = {
        "image": "nginx:1.27",
        "cpu": "250m",
        "memory": "128Mi",
        "replicas": 2,
        "port": 80,
        "env": [{"name": "GREETING", "value": "hello"}],
    }
    deployment = build_deployment("example-container", "project-team-a-checkout-dev", spec)

    assert deployment["spec"]["replicas"] == 2
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    assert container["ports"] == [{"containerPort": 80}]
    assert container["env"] == [{"name": "GREETING", "value": "hello"}]


def test_build_service_returns_none_without_port():
    spec = {"image": "nginx:1.27", "cpu": "250m", "memory": "128Mi"}
    assert build_service("example-container", "project-team-a-checkout-dev", spec) is None


def test_build_service_with_port():
    spec = {"image": "nginx:1.27", "cpu": "250m", "memory": "128Mi", "port": 80}
    service = build_service("example-container", "project-team-a-checkout-dev", spec)

    assert service["apiVersion"] == "v1"
    assert service["kind"] == "Service"
    assert service["metadata"] == {"name": "example-container", "namespace": "project-team-a-checkout-dev", "labels": {"app": "example-container"}}
    assert service["spec"] == {
        "selector": {"app": "example-container"},
        "ports": [{"port": 80, "targetPort": 80}],
    }
