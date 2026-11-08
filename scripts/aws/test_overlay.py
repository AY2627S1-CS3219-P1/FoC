"""Offline integration checks for the agreed AWS deployment and existing secret contracts.

Expected outcomes: eight image names (deployment operator), five service DBs
(runbook), private Credit RPC boundary (landmine), and AWS S3/RDS contracts.
"""

import contextlib
import io
import json
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
            "public_hostname": "campus.demo.example.org",
            "image_prefix": "123456789012.dkr.ecr.ap-southeast-1.amazonaws.com/foc-demo",
            "order_bucket": "foc-demo-orders-example",
            "order_role_arn": "arn:aws:iam::123456789012:role/demo-order",
            "ingress_role_arn": "arn:aws:iam::123456789012:role/demo-ingress",
            "public_subnet_ids": ["subnet-first", "subnet-second"],
            "database_host": "demo.database.invalid",
        }
        self.overlay = self.root / "production"
        self.output = io.StringIO()
        with contextlib.redirect_stdout(self.output):
            render(self.config, self.overlay, "-----BEGIN CERTIFICATE-----\nOFFLINE FIXTURE\n-----END CERTIFICATE-----\n")

    def test_rendered_stages_preserve_security_storage_and_database_contracts(self):
        self.assertIsNotNone(shutil.which("kubectl"), "kubectl is required for offline manifest rendering")
        # checkOverlay() in scripts/k8s/config.go requires this exact filename.
        for stage in ("gateway", "dependencies", "provisioning", "initialization", "applications"):
            self.assertTrue((self.overlay / stage / "kustomization.yaml").is_file())
        rendered = subprocess.check_output(["kubectl", "kustomize", str(self.overlay)], text=True)
        objects = list(yaml.safe_load_all(rendered))
        by_name = {(obj["kind"], obj["metadata"]["name"]): obj for obj in objects}
        self.assertNotIn("example.invalid", rendered)
        self.assertNotIn("foc-local-tls", rendered)
        self.assertNotIn("localhost", rendered)
        gateway = by_name["Gateway", "foc-public"]
        listener = gateway["spec"]["listeners"][0]
        self.assertEqual(listener["hostname"], "campus.demo.example.org")
        self.assertEqual(listener["tls"]["certificateRefs"][0]["name"], "foc-public-tls")
        service = json.loads(by_name["ConfigMap", "foc-public-infrastructure"]["data"]["service"])
        self.assertEqual(service["spec"]["type"], "LoadBalancer")
        self.assertIn({"port": 15021, "$patch": "delete"}, service["spec"]["ports"])
        self.assertEqual(service["metadata"]["annotations"]["service.beta.kubernetes.io/aws-load-balancer-nlb-target-type"], "ip")
        self.assertEqual(by_name["ServiceAccount", "order-service"]["metadata"]["annotations"]["eks.amazonaws.com/role-arn"], self.config["order_role_arn"])
        config = by_name["ConfigMap", "foc-config"]["data"]
        self.assertEqual(config["ORDER_S3_BUCKET"], "foc-demo-orders-example")
        self.assertEqual(config["ORDER_S3_PUBLIC_ENDPOINT"], "https://s3.ap-southeast-1.amazonaws.com")
        self.assertEqual(by_name["StorageClass", "foc-gp3"]["reclaimPolicy"], "Retain")
        self.assertEqual(by_name["StatefulSet", "kafka"]["spec"]["volumeClaimTemplates"][0]["spec"]["storageClassName"], "foc-gp3")
        for service in SERVICES:
            for kind, name in (("Deployment", f"{service}-service"), ("Job", f"{service}-migrate")):
                pod = by_name[kind, name]["spec"]["template"]["spec"]
                self.assertEqual(len(pod["containers"]), 1, f"Certificate patch added an unintended container to {name}")
                self.assertTrue(any(mount["mountPath"] == "/var/run/foc/rds" for mount in pod["containers"][0]["volumeMounts"]))
        user_mounts = by_name["Deployment", "user-service"]["spec"]["template"]["spec"]["containers"][0]["volumeMounts"]
        self.assertTrue(any(mount["name"] == "jwt-signing" for mount in user_mounts))
        routes = [obj for obj in objects if obj["kind"] == "HTTPRoute"]
        self.assertNotIn("CreditReservationService", json.dumps(routes))
        self.assertNotIn("PublicKeyService", json.dumps(routes))
        images = {container["image"].split(":")[0] for obj in objects if obj["kind"] in ("Deployment", "Job") for container in obj["spec"]["template"]["spec"]["containers"] if container["image"].startswith("foc/")}
        self.assertEqual(len(images), 8)
        print(f"PASS: rendered {len(objects)} resources, eight image names, five database mounts, private RPC boundary, NLB and S3 configuration")

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
