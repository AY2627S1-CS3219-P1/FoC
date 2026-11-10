"""Offline integration checks for the agreed AWS deployment and existing secret contracts.

Expected outcomes: eight image names (deployment operator), five service DBs
(runbook), private Credit RPC boundary (landmine), and AWS S3/RDS contracts.
"""

import contextlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import yaml

from prepare_database import prepare
from render_overlay import ROOT, SERVICES, render


class AWSOverlayTest(unittest.TestCase):
    def setUp(self):
        private = ROOT / ".local/aws"
        private.mkdir(parents=True, exist_ok=True)
        self.directory = tempfile.TemporaryDirectory(prefix="offline-test-", dir=private)
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.config = {
            "region": "ap-southeast-1", "cluster_name": "foc-demo", "vpc_id": "vpc-example",
            "public_hostname": "a1b2c3d4.execute-api.ap-southeast-1.amazonaws.com",
            "image_prefix": "123456789012.dkr.ecr.ap-southeast-1.amazonaws.com/foc-demo",
            "order_bucket": "foc-demo-orders-example",
            "order_role_arn": "arn:aws:iam::123456789012:role/demo-order",
            "ingress_role_arn": "arn:aws:iam::123456789012:role/demo-ingress",
            "gateway_target_arn": "arn:aws:elasticloadbalancing:ap-southeast-1:123456789012:targetgroup/foc-demo-gw/0123456789abcdef",
            "database_host": "demo.database.invalid",
            "loki_bucket": "foc-demo-loki-example",
            "loki_role_arn": "arn:aws:iam::123456789012:role/demo-loki",
        }
        self.overlay = self.root / "production"
        self.output = io.StringIO()
        with contextlib.redirect_stdout(self.output):
            render(self.config, self.overlay, "-----BEGIN CERTIFICATE-----\nOFFLINE FIXTURE\n-----END CERTIFICATE-----\n")

    def test_rendered_stages_preserve_security_storage_and_database_contracts(self):
        self.assertIsNotNone(shutil.which("kubectl"), "kubectl is required for offline manifest rendering")
        # checkOverlay() in scripts/k8s/config.go requires this exact filename.
        for stage in ("gateway", "dependencies", "provisioning", "observability", "initialization", "applications"):
            self.assertTrue((self.overlay / stage / "kustomization.yaml").is_file())
        rendered = subprocess.check_output(["kubectl", "kustomize", str(self.overlay)], text=True)
        objects = list(yaml.safe_load_all(rendered))
        by_name = {(obj["kind"], obj["metadata"]["name"]): obj for obj in objects}
        self.assertNotIn("example.invalid", rendered)
        self.assertNotIn("foc-local-tls", rendered)
        self.assertNotIn("localhost", rendered)
        gateway = by_name["Gateway", "foc-public"]
        listener = gateway["spec"]["listeners"][0]
        self.assertEqual(listener["protocol"], "HTTP")
        self.assertEqual(listener["port"], 80)
        self.assertNotIn("hostname", listener)
        self.assertNotIn("tls", listener)
        self.assertEqual(listener["name"], "https")  # Existing parentRef section name.
        binding = by_name["TargetGroupBinding", "foc-public"]["spec"]
        self.assertEqual(binding["serviceRef"], {"name": "foc-public-istio", "port": 80})
        self.assertEqual(binding["targetGroupARN"], self.config["gateway_target_arn"])
        self.assertEqual(binding["targetType"], "ip")
        service = json.loads(by_name["ConfigMap", "foc-public-infrastructure"]["data"]["service"])
        self.assertEqual(service["spec"]["type"], "ClusterIP")
        self.assertNotIn("metadata", service)
        self.assertEqual(by_name["ServiceAccount", "order-service"]["metadata"]["annotations"]["eks.amazonaws.com/role-arn"], self.config["order_role_arn"])
        config = by_name["ConfigMap", "foc-config"]["data"]
        for key in ("ORIGIN", "FRONTEND_BASE_URL", "PUBLIC_USER_SERVICE_URL", "PUBLIC_ORDER_SERVICE_URL"):
            self.assertEqual(config[key], "https://a1b2c3d4.execute-api.ap-southeast-1.amazonaws.com")
        self.assertEqual(config["ORDER_S3_BUCKET"], "foc-demo-orders-example")
        self.assertEqual(config["ORDER_S3_PUBLIC_ENDPOINT"], "https://s3.ap-southeast-1.amazonaws.com")
        self.assertEqual(by_name["StorageClass", "foc-gp3"]["reclaimPolicy"], "Retain")
        self.assertEqual(by_name["StatefulSet", "kafka"]["spec"]["volumeClaimTemplates"][0]["spec"]["storageClassName"], "foc-gp3")
        for service in SERVICES:
            for kind, name in (("Deployment", f"{service}-service"), ("Job", f"{service}-migrate")):
                pod = by_name[kind, name]["spec"]["template"]["spec"]
                self.assertEqual(len(pod["containers"]), 1, f"Certificate patch added an unintended container to {name}")
                self.assertTrue(any(mount["mountPath"] == "/var/run/foc/rds" for mount in pod["containers"][0]["volumeMounts"]))
        self.assertIn({"secretRef": {"name": "smtp"}}, by_name["Deployment", "user-service"]["spec"]["template"]["spec"]["containers"][0]["envFrom"])
        user_mounts = by_name["Deployment", "user-service"]["spec"]["template"]["spec"]["containers"][0]["volumeMounts"]
        self.assertTrue(any(mount["name"] == "jwt-signing" for mount in user_mounts))
        routes = [obj for obj in objects if obj["kind"] == "HTTPRoute"]
        self.assertTrue(all("hostnames" not in route["spec"] for route in routes))
        self.assertNotIn("CreditReservationService", json.dumps(routes))
        self.assertNotIn("PublicKeyService", json.dumps(routes))
        images = {container["image"].split(":")[0] for obj in objects if obj["kind"] in ("Deployment", "Job") for container in obj["spec"]["template"]["spec"]["containers"] if container["image"].startswith("foc/")}
        self.assertEqual(len(images), 8)
        print(f"PASS: rendered {len(objects)} resources, eight image names, five database mounts, private RPC boundary, NLB and S3 configuration")

    def test_migrations_keep_database_credentials_out_of_process_arguments(self):
        # Credentials must stay out of argv; Goose v3.28.0 supports these environment inputs.
        rendered = subprocess.check_output(["kubectl", "kustomize", str(self.overlay / "initialization")], text=True)
        jobs = {obj["metadata"]["name"]: obj for obj in yaml.safe_load_all(rendered) if obj["kind"] == "Job"}
        for service in ("user", "supplier", "order", "credit", "communication"):
            container = jobs[f"{service}-migrate"]["spec"]["template"]["spec"]["containers"][0]
            self.assertEqual(container["args"], ["-dir", f"/migrations/{service}", "up"])
            env = {entry["name"]: entry for entry in container["env"]}
            self.assertEqual(env["GOOSE_DRIVER"]["value"], "postgres")
            self.assertEqual(env["GOOSE_DBSTRING"]["valueFrom"]["secretKeyRef"], {
                "name": f"{service}-db", "key": "DATABASE_URL",
            })

    def test_generated_overlay_is_accepted_by_actual_operator(self):
        operator = self.root / "k8s"
        subprocess.run(["go", "build", "-o", str(operator), "./k8s"], cwd=ROOT / "scripts",
                       env=dict(os.environ, GOWORK="off"), check=True, capture_output=True, text=True)
        environment = {key: value for key, value in os.environ.items() if not key.startswith("K8S_")}
        environment.update(K8S_OVERLAY=str(self.overlay), K8S_STATE_DIR=str(self.root / "operator"))
        result = subprocess.run([str(operator), "render", "gateway"], cwd=ROOT, env=environment,
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("kind: Gateway", result.stdout)
        settings = dict(line.split("=", 1) for line in self.output.getvalue().splitlines()
                        if line.startswith("K8S_"))
        self.assertEqual({key: settings[key] for key in (
            "K8S_LOKI_BUCKET", "K8S_LOKI_REGION", "K8S_LOKI_IRSA_ROLE", "K8S_LOKI_STORAGE_CLASS",
        )}, {
            "K8S_LOKI_BUCKET": self.config["loki_bucket"], "K8S_LOKI_REGION": self.config["region"],
            "K8S_LOKI_IRSA_ROLE": self.config["loki_role_arn"], "K8S_LOKI_STORAGE_CLASS": "foc-gp3",
        })
        rendered = subprocess.check_output(["kubectl", "kustomize", str(self.overlay / "observability")], text=True)
        objects = list(yaml.safe_load_all(rendered))
        claims = [obj for obj in objects if obj["kind"] == "PersistentVolumeClaim"]
        self.assertEqual({obj["metadata"]["name"] for obj in claims}, {"grafana-data", "alloy-data"})
        self.assertTrue(all(obj["spec"]["storageClassName"] == "foc-gp3" for obj in claims))
        self.assertFalse(any(obj["kind"] in ("Role", "RoleBinding", "Secret", "StatefulSet") for obj in objects))
        grafana = next(obj for obj in objects if obj["kind"] == "Deployment" and obj["metadata"]["name"] == "grafana")
        self.assertEqual({item["valueFrom"]["secretKeyRef"]["name"] for item in
                          grafana["spec"]["template"]["spec"]["containers"][0]["env"] if "valueFrom" in item}, {"grafana-admin"})
        if os.environ.get("K8S_RENDER_TESTS") == "1":
            # Exercise the real pinned Helm chart as well when it is cached.
            environment.update(settings)
            result = subprocess.run([str(operator), "render", "observability"], cwd=ROOT, env=environment,
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            objects = [obj for obj in yaml.safe_load_all(result.stdout) if obj]
            account = next(obj for obj in objects if obj["kind"] == "ServiceAccount" and obj["metadata"]["name"] == "loki")
            self.assertEqual(account["metadata"]["annotations"]["eks.amazonaws.com/role-arn"], self.config["loki_role_arn"])
            loki = next(obj for obj in objects if obj["kind"] == "StatefulSet")
            self.assertEqual(loki["spec"]["volumeClaimTemplates"][0]["spec"]["storageClassName"], "foc-gp3")
            self.assertIn(self.config["loki_bucket"], result.stdout)

    def test_logging_requires_its_own_bucket_and_identity(self):
        for changes in ({"loki_bucket": None}, {"loki_role_arn": None},
                        {"loki_bucket": self.config["order_bucket"]},
                        {"loki_role_arn": self.config["order_role_arn"]}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                render(dict(self.config, **changes), self.overlay, "-----BEGIN CERTIFICATE-----")

    def test_ses_overlay_uses_only_user_role_and_removes_smtp_dependency(self):
        config = dict(self.config, ses_sender_email="sender@example.test",
                      user_email_role_arn="arn:aws:iam::123456789012:role/demo-user-email")
        with contextlib.redirect_stdout(self.output):
            render(config, self.overlay, "-----BEGIN CERTIFICATE-----\nFIXTURE\n-----END CERTIFICATE-----\n")
        objects = list(yaml.safe_load_all(subprocess.check_output(["kubectl", "kustomize", str(self.overlay)], text=True)))
        by_name = {(obj["kind"], obj["metadata"]["name"]): obj for obj in objects}
        values = by_name["ConfigMap", "foc-config"]["data"]
        self.assertEqual(values["EMAIL_PROVIDER"], "ses")
        self.assertEqual(values["EMAIL_FROM"], "sender@example.test")
        user = by_name["Deployment", "user-service"]["spec"]["template"]["spec"]["containers"][0]
        self.assertEqual(user["envFrom"], [{"configMapRef": {"name": "foc-config"}}, {"secretRef": {"name": "user-db"}}])
        self.assertEqual(by_name["ServiceAccount", "user-service"]["metadata"]["annotations"]["eks.amazonaws.com/role-arn"], config["user_email_role_arn"])
        self.assertNotIn("eks.amazonaws.com/role-arn", by_name["ServiceAccount", "communication-service"]["metadata"].get("annotations", {}))
        self.assertEqual(by_name["ServiceAccount", "order-service"]["metadata"]["annotations"]["eks.amazonaws.com/role-arn"], self.config["order_role_arn"])

    def test_ses_overlay_rejects_incomplete_or_invalid_configuration(self):
        for extra in ({"ses_sender_email": "sender@example.test"},
                      {"user_email_role_arn": "arn:aws:iam::123456789012:role/demo-user-email"},
                      {"ses_sender_email": "FoC <sender@example.test>", "user_email_role_arn": "role"}):
            with self.subTest(extra=extra), self.assertRaises(ValueError):
                render(dict(self.config, **extra), self.overlay, "-----BEGIN CERTIFICATE-----")

    def test_database_preparation_preserves_credentials_and_verifies_rds_tls(self):
        database = self.root / "db"
        admin = {"username": "foc_admin", "password": "offline-test-only"}
        with contextlib.redirect_stdout(self.output):
            prepare(self.config, admin, database)
        first = (database / "order.json").read_text()
        with contextlib.redirect_stdout(self.output):
            prepare(self.config, admin, database)
        self.assertEqual(first, (database / "order.json").read_text())
        secrets = json.loads((database / "secrets.json").read_text())["items"]
        db_secrets = [obj for obj in secrets if obj["kind"] == "Secret" and obj["metadata"]["name"].endswith("-db")]
        self.assertEqual(len(db_secrets), 5)
        for obj in db_secrets:
            self.assertIn("sslmode=verify-full", obj["stringData"]["DATABASE_URL"])
            self.assertNotIn(obj["stringData"]["DB_PASSWORD"], self.output.getvalue())
        self.assertEqual((database / "secrets.json").stat().st_mode & 0o777, 0o600)
        job = json.loads((database / "job.json").read_text())
        self.assertEqual(job["spec"]["backoffLimit"], 0)
        print("PASS: five private database credentials retained across retries, verify-full TLS, no credentials in output")

    def test_rerender_removes_old_generated_json_filenames(self):
        obsolete = [self.overlay / "kustomization.json", self.overlay / "gateway/kustomization.json"]
        for path in obsolete:
            path.write_text("{}")
        with contextlib.redirect_stdout(self.output):
            render(self.config, self.overlay, "-----BEGIN CERTIFICATE-----\nOFFLINE FIXTURE\n-----END CERTIFICATE-----\n")
        for path in obsolete:
            self.assertFalse(path.exists())
        subprocess.check_output(["kubectl", "kustomize", str(self.overlay)], text=True)

    def test_reject_rendering_into_tracked_sources(self):
        with self.assertRaisesRegex(ValueError, "ignored .local"):
            render(self.config, ROOT / "infra/k8s/overlays/production", "-----BEGIN CERTIFICATE-----")


if __name__ == "__main__":
    unittest.main(verbosity=2)
