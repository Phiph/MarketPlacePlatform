from pipeline import build_app_project


def test_build_app_project_scopes_destinations_to_one_team():
    project = build_app_project("checkout")

    assert project["apiVersion"] == "argoproj.io/v1alpha1"
    assert project["kind"] == "AppProject"
    assert project["metadata"] == {"name": "checkout", "namespace": "argocd"}
    assert project["spec"]["destinations"] == [{"name": "worker-1", "namespace": "team-checkout"}]
    assert project["spec"]["sourceNamespaces"] == ["team-checkout"]


def test_build_app_project_never_allows_sync():
    project = build_app_project("checkout")

    assert project["spec"]["namespaceResourceWhitelist"] == []
    assert project["spec"]["clusterResourceWhitelist"] == []


def test_build_app_project_role_is_read_only():
    project = build_app_project("checkout")
    roles = project["spec"]["roles"]

    assert len(roles) == 1
    assert roles[0]["name"] == "viewer"
    assert roles[0]["policies"] == [
        "p, proj:checkout:viewer, applications, get, checkout/*, allow",
        "p, proj:checkout:viewer, logs, get, checkout/*, allow",
    ]


def test_build_app_project_uses_team_name_not_namespace():
    project = build_app_project("payments")

    assert project["metadata"]["name"] == "payments"
    assert project["metadata"]["namespace"] == "argocd"
