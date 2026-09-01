from pipeline import build_manifest


def test_build_manifest_uses_given_namespace():
    manifest = build_manifest("example-database", "project-team-a-checkout-dev", "1Gi")

    assert manifest["apiVersion"] == "acid.zalan.do/v1"
    assert manifest["kind"] == "postgresql"
    assert manifest["metadata"] == {
        "name": "example-database",
        "namespace": "project-team-a-checkout-dev",
    }
    assert manifest["spec"]["volume"] == {"size": "1Gi"}


def test_build_manifest_defaults():
    manifest = build_manifest("db1", "project-team-b-billing-staging", "2Gi")

    assert manifest["spec"]["teamId"] == "kratix"
    assert manifest["spec"]["enableLogicalBackup"] is True
    assert manifest["spec"]["numberOfInstances"] == 2
    assert manifest["spec"]["users"] == {"team-a": ["superuser", "createdb"]}
    assert manifest["spec"]["postgresql"] == {"version": "16"}
