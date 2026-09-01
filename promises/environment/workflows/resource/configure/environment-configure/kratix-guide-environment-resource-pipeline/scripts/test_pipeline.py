from pipeline import build_namespace, namespace_name


def test_namespace_name():
    assert namespace_name("team-a", "checkout", "dev") == "project-team-a-checkout-dev"


def test_build_namespace():
    namespace = build_namespace("team-a", "checkout", "dev", "acme-corp")

    assert namespace["apiVersion"] == "v1"
    assert namespace["kind"] == "Namespace"
    assert namespace["metadata"] == {
        "name": "project-team-a-checkout-dev",
        "labels": {
            "capsule.clastix.io/tenant": "acme-corp",
            "marketplace.kratix.io/team": "team-a",
            "marketplace.kratix.io/project": "checkout",
            "marketplace.kratix.io/environment": "dev",
        },
    }
