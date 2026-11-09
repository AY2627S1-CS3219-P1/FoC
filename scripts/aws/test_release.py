"""CLI seams for pinned config, upload recovery, isolation, and fail-closed releases.

AWS, Docker, Git, Make, and cluster access are explicit offline fixtures. The real
wrappers and overlay renderer execute; kubectl kustomize checks actual manifests.
No AWS credentials or live cluster are used.
"""

import base64
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import unittest

import yaml

from release_config import ROOT, checksum

ACCOUNT = "123456789012"
REGION = "ap-southeast-1"
SOURCE = "a" * 40
CA = b"-----BEGIN CERTIFICATE-----\nOFFLINE RELEASE FIXTURE\n-----END CERTIFICATE-----\n"


class ReleaseCLITest(unittest.TestCase):
    def setUp(self):
        (ROOT / ".local").mkdir(exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix="release-test-", dir=ROOT / ".local")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.private = self.root / ".local"
        self.private.mkdir()
        shutil.copytree(ROOT / "scripts/aws", self.root / "scripts/aws", ignore=shutil.ignore_patterns("__pycache__"))
        shutil.copytree(ROOT / "infra/k8s", self.root / "infra/k8s")
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for name in ("aws", "terraform", "git", "docker", "make", "kubectl", "gh"):
            path = self.bin / name
            shutil.copy(ROOT / "scripts/aws/_release_fixture.py", path)
            path.chmod(0o700)
        self.state_file = self.private / "fixture.json"
        self.outputs = {
            "region": REGION, "cluster_name": "fixture-demo", "vpc_id": "vpc-0123456789abcdef0",
            "public_hostname": "fixture.execute-api.ap-southeast-1.amazonaws.com",
            "public_origin": "https://fixture.execute-api.ap-southeast-1.amazonaws.com",
            "image_prefix": ACCOUNT + ".dkr.ecr." + REGION + ".amazonaws.com/foc-demo",
            "order_bucket": "fixture-order-images", "order_role_arn": "arn:aws:iam::" + ACCOUNT + ":role/order",
            "ingress_role_arn": "arn:aws:iam::" + ACCOUNT + ":role/ingress",
            "gateway_target_arn": "arn:aws:elasticloadbalancing:" + REGION + ":" + ACCOUNT + ":targetgroup/fixture/0123456789abcdef",
            "ses_sender_email": "sender@example.test", "user_email_role_arn": "arn:aws:iam::" + ACCOUNT + ":role/email",
            "database_admin": "DO-NOT-PUBLISH-ADMIN", "database_secret_arn": "DO-NOT-PUBLISH-SECRET-REFERENCE",
            "future_secret_field": "DO-NOT-PUBLISH-FUTURE-OUTPUT",
        }
        self.write_state({"source": SOURCE, "account": ACCOUNT, "outputs": self.outputs})
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        RELEASE_FIXTURE_STATE=str(self.state_file), PYTHONDONTWRITEBYTECODE="1")
        self.plan = self.private / "reviewed.tfplan"
        self.plan.write_bytes(b"explicitly reviewed offline saved plan")
        self.plan.chmod(0o600)
        self.ca = self.private / "rds-ca.pem"
        self.ca.write_bytes(CA)
        self.document = self.private / "export.json"
        self.common = ["--account-id", ACCOUNT, "--region", REGION, "--environment", "demo",
                       "--bucket", "fixture-config", "--prefix", "releases"]

    def read_state(self):
        return json.loads(self.state_file.read_text())

    def write_state(self, value):
        self.state_file.write_text(json.dumps(value))

    def change(self, **values):
        state = self.read_state()
        state.update(values)
        self.write_state(state)

    def cli(self, script, args, success=True):
        result = subprocess.run([sys.executable, str(self.root / "scripts/aws" / script), *args],
                                cwd=self.root, env=self.env, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        for marker in ("fixture-ecr-password", "NOT-FOR-LOGS", "DO-NOT-PUBLISH"):
            self.assertNotIn(marker, result.stdout + result.stderr)
        return result

    def publish(self, success=True, extra=None):
        args = self.common + ["--document", str(self.document), "--plan", str(self.plan),
                              "--plan-sha256", checksum(self.plan.read_bytes()), "--rds-ca", str(self.ca)]
        return self.cli("apply_and_publish.py", args + (extra or []), success)

    def deploy(self, attempt="attempt1", success=True, extra=None):
        receipt = json.loads(self.document.with_suffix(".receipt.json").read_text())
        args = self.common + ["--role-arn", "arn:aws:iam::" + ACCOUNT + ":role/release",
                              "--config-version", receipt["version_id"], "--config-sha256", receipt["sha256"],
                              "--release-id", attempt, "--rds-ca", str(self.ca)]
        return self.cli("deploy_release.py", args + (extra or []), success)

    def test_failed_publication_recovers_exact_document_without_reapplying(self):
        # Publication retries must upload saved bytes without repeating Terraform apply.
        self.change(fail_upload=True)
        self.publish(success=False)
        self.assertEqual(self.read_state()["applies"], 1)
        original = self.document.read_bytes()
        self.change(fail_upload=False)
        recovery = self.common + ["--publish-only", "--document", str(self.document), "--config-sha256", checksum(original)]
        self.cli("apply_and_publish.py", recovery)
        first = self.document.with_suffix(".receipt.json").read_bytes()
        self.cli("apply_and_publish.py", recovery)
        self.assertEqual(self.document.read_bytes(), original)
        self.assertEqual(self.document.with_suffix(".receipt.json").read_bytes(), first)
        self.assertEqual(self.read_state()["applies"], 1)
        self.assertEqual(self.read_state()["version_sequence"], 1)
        self.assertEqual(self.document.stat().st_mode & 0o777, 0o600)
        for field in ("database_admin", "database_secret_arn", "future_secret_field"):
            self.assertNotIn(field, original.decode())
        self.cli("apply_and_publish.py", recovery + ["--config-sha256", "0" * 64], success=False)

    def test_pinned_release_regenerates_real_overlay_and_delegates_existing_operator(self):
        self.publish()
        receipt = json.loads(self.document.with_suffix(".receipt.json").read_text())
        state = self.read_state()
        state["objects"][receipt["key"]].append({"id": "unselected-latest", "data": base64.b64encode(b"not the selected configuration").decode()})
        self.write_state(state)
        # A developer's inherited overrides must not skip full migration/deploy.
        self.env.update(K8S_SKIP_BUILD="1", K8S_CONTEXT="kind-wrong", K8S_OVERLAY="wrong", service="frontend")
        self.deploy()
        state = self.read_state()
        self.assertEqual(state["applies"], 1)
        self.assertEqual(state["stages"], ["dependencies", "migrations", "applications"])
        self.assertTrue(state["smoke_called"] and state["password_stdin_ok"] and state["logged_out"])
        env = state["deploy_environment"]
        self.assertEqual((env["K8S_SKIP_BUILD"], env["K8S_PUSH_IMAGES"], env["DOCKER_DEFAULT_PLATFORM"]), ("0", "1", "linux/amd64"))
        self.assertEqual(env["K8S_IMAGE_TAG"], SOURCE + "-attempt1")
        self.assertEqual(env["K8S_CONTEXT"], "foc-release")
        self.assertFalse(Path(env["DOCKER_CONFIG"]).exists())
        self.assertTrue(Path(env["KUBECONFIG"]).is_relative_to(self.private))
        self.assertEqual(Path(env["KUBECONFIG"]).stat().st_mode & 0o777, 0o600)
        release = self.private / "aws/releases/demo/attempt1/release-receipt.json"
        record = json.loads(release.read_text())
        self.assertEqual(record["configuration"]["version_id"], receipt["version_id"])
        self.assertEqual(len(record["images"]), 8)
        self.assertEqual(record["public_smoke"], "passed")
        self.assertTrue(all(not versions for key, versions in state["objects"].items() if "/locks/" in key))
        kubectl = shutil.which("kubectl")
        self.assertIsNotNone(kubectl, "Real kubectl is required for offline rendering")
        rendered = subprocess.check_output([kubectl, "kustomize", env["K8S_OVERLAY"]], text=True)
        objects = list(yaml.safe_load_all(rendered))
        self.assertFalse(any(obj["kind"] == "Secret" for obj in objects))
        user = next(obj for obj in objects if obj["kind"] == "Deployment" and obj["metadata"]["name"] == "user-service")
        self.assertNotIn({"secretRef": {"name": "smtp"}}, user["spec"]["template"]["spec"]["containers"][0]["envFrom"])
        self.assertNotIn("CreditReservationService", json.dumps([obj for obj in objects if obj["kind"] == "HTTPRoute"]))
        calls = state["calls"]
        self.assertEqual(sum(call[0] == "terraform" and "apply" in call for call in calls), 1)
        self.assertIn(["make", "k8s-deploy", "service="], calls)
        print("PASS: fixture upload recovery/version pin -> actual renderer -> full operator delegation -> eight digest receipts -> smoke gate")

    def test_preflight_rejects_unreviewed_plan_or_public_unversioned_storage(self):
        for index, flags in enumerate(({"versioning": "Suspended"}, {"public_bucket": True}, {})):
            with self.subTest(flags=flags):
                self.change(**flags)
                self.publish(success=False, extra=["--plan-sha256", "0" * 64] if index == 2 else None)
                self.assertEqual(self.read_state().get("applies", 0), 0)
                self.change(versioning="Enabled", public_bucket=False)

    def test_wrong_identity_ca_immutable_repositories_and_existing_secrets_fail_before_build(self):
        self.publish()
        for index, flags in enumerate(({"caller_role": "infrastructure-admin"}, {"caller_account": "999999999999"},
                                       {"mutability": "MUTABLE"}, {"missing_secret": True}, {"lock_taken": True})):
            with self.subTest(flags=flags):
                self.change(**flags)
                self.deploy("negative" + str(index), success=False)
                self.assertNotIn("stages", self.read_state())
                self.change(caller_role="release", caller_account=ACCOUNT, mutability="IMMUTABLE", missing_secret=False, lock_taken=False)
        self.ca.write_bytes(b"changed CA")
        self.deploy("badca", success=False)
        self.assertNotIn("stages", self.read_state())

    def test_migration_or_smoke_failure_cannot_report_a_successful_release(self):
        self.publish()
        self.change(fail_migration=True)
        self.deploy("migrationfail", success=False)
        state = self.read_state()
        self.assertNotIn("applications", state["stages"])
        self.assertFalse(state.get("smoke_called"))
        self.assertTrue(state["logged_out"])
        self.assertFalse((self.private / "aws/releases/demo/migrationfail/release-receipt.json").exists())
        self.assertTrue(all(not versions for key, versions in state["objects"].items() if "/locks/" in key))
        self.change(fail_migration=False, fail_smoke=True)
        self.deploy("smokefail", success=False)
        self.assertFalse((self.private / "aws/releases/demo/smokefail/release-receipt.json").exists())

    def test_interrupt_retains_lock_while_deployment_descendant_is_alive(self):
        # Serialization must hold even when only the wrapper receives SIGINT.
        self.publish()
        self.change(interruptible_deploy=True)
        result = self.deploy("interrupted", success=False)
        self.assertEqual(result.returncode, 130)
        state = self.read_state()
        pid = state["descendant_pid"]
        started = subprocess.check_output(["ps", "-p", str(pid), "-o", "lstart="]).strip()
        try:
            os.kill(pid, 0)
            command = subprocess.check_output(["ps", "-ww", "-p", str(pid), "-o", "command="], text=True)
            self.assertIn(str(self.state_file.with_suffix(".descendant-ready")), command)
            self.assertIn("environment lock and private runtime files retained", result.stderr)
            self.assertFalse(state.get("logged_out", False))
            self.assertTrue(Path(state["deploy_environment"]["DOCKER_CONFIG"]).exists())
            locks = [versions for key, versions in state["objects"].items() if "/locks/" in key]
            self.assertEqual(len(locks), 1)
            self.assertEqual(len(locks[0]), 1)
            self.change(interruptible_deploy=False)
            self.deploy("blocked-by-interrupted-owner", success=False)
            os.kill(pid, 0)
            calls = self.read_state()["calls"]
            self.assertEqual(sum(call[:2] == ["make", "k8s-deploy"] for call in calls), 1)
        finally:
            observed = subprocess.run(["ps", "-p", str(pid), "-o", "lstart="], capture_output=True).stdout.strip()
            if observed == started:
                os.kill(pid, signal.SIGTERM)
        print("PASS: interrupted wrapper retains its environment lock while its deployment descendant remains alive; second release rejected")

    def launch(self, success=True, extra=None):
        config = self.private / "ci.json"
        config.write_text(json.dumps({"account_id": ACCOUNT, "region": REGION,
            "runner_name": "fixture-runner", "registration_parameter": "/fixture/ci/runner-jit",
            "launch_template_id": "lt-pinned", "launch_template_version": 3,
            "bucket": "fixture-config", "prefix": "releases"}))
        return self.cli("launch_runner.py", ["--configuration", str(config), "--runner-group", "3"] + (extra or []), success)

    def test_runner_probe_cleans_up_without_dispatching_and_uses_encrypted_expiring_registration(self):
        # Requested boundary: private one-job registration, no application release,
        # no standing runner, and no stored/static GitHub credential.
        self.launch()
        state = self.read_state()
        self.assertEqual(state["parameter_tier"], "Advanced")
        self.assertTrue(state["parameter_has_expiry"])
        self.assertTrue(state["instance_terminated"] and state["registration_removed"] and state["parameter_removed"])
        self.assertTrue(state["profile_detached"])
        locks = [v for k, v in state["objects"].items() if "/locks/runner-bootstrap.json" in k]
        self.assertEqual(locks, [[]])
        self.assertFalse(any("k8s-deploy" in c or "workflow" in c for c in state["calls"]))
        self.assertFalse(list(self.private.glob("aws/runner-launches/*/*.PRIVATE.input.json")))

    def test_runner_group_existing_instance_and_bootstrap_failure_fail_closed(self):
        for flags in ({"workflows": ["untrusted-pr-workflow"]},
                      {"workflows": ["AY2627S1-CS3219-P1/FoC/.github/workflows/aws-release.yml@refs/heads/main"]},
                      {"existing_runner": True}, {"lock_taken": True}):
            with self.subTest(flags=flags):
                self.change(**flags)
                self.launch(success=False)
                self.assertFalse(self.read_state().get("instance_terminated", False))
                self.change(workflows=[], existing_runner=False, lock_taken=False)
        self.launch(success=False, extra=["--keep-running"])
        self.assertNotIn("parameter_tier", self.read_state())
        self.change(bootstrap_failure=True)
        self.launch(success=False)
        state = self.read_state()
        self.assertTrue(state["instance_terminated"] and state["registration_removed"] and state["parameter_removed"])
        # Reproduced failure: cleanup removed the disk without retaining boot evidence.
        consoles = list(self.private.glob("aws/runner-launches/*/console.PRIVATE.json"))
        self.assertEqual(len(consoles), 1)
        self.assertIn("Output", json.loads(consoles[0].read_text()))
        operations = [call for call in state["calls"] if "ec2" in call]
        self.assertLess(next(i for i, call in enumerate(operations) if "get-console-output" in call),
                        next(i for i, call in enumerate(operations) if "terminate-instances" in call))

    def test_runner_diagnostic_failures_preserve_bootstrap_error_and_cleanup(self):
        # The reproduced boot failure must survive unavailable or unwritable diagnostics.
        for failure in ("console_failure", "console_save_failure"):
            with self.subTest(failure=failure):
                self.change(bootstrap_failure=True, console_failure=failure == "console_failure",
                            console_save_failure=failure == "console_save_failure")
                result = self.launch(success=False)
                self.assertIn("Runner bootstrap stopped", result.stderr)
                state = self.read_state()
                self.assertTrue(state["instance_terminated"] and state["registration_removed"] and state["parameter_removed"])

    def test_bootstrap_console_failure_still_cleans_and_preserves_exit_status(self):
        # Fault injection for the reviewed EXIT-handler failure; no privileged host commands run.
        cleanup = self.private / "cleanup-called"
        shutdown = self.private / "shutdown-called"
        scripts = {"systemd-run": "exit 0", "dnf": "exit 73",
                   "tee": "cat > /dev/null; exit 1",
                   "rm": "touch '" + str(cleanup) + "'",
                   "shutdown": "touch '" + str(shutdown) + "'"}
        for name, body in scripts.items():
            path = self.bin / name
            path.write_text("#!/bin/bash\n" + body + "\n")
            path.chmod(0o700)
        source = (ROOT / "infra/aws/ci/runner.sh.tftpl").read_text()
        source = source.replace("/var/log/foc-runner-packages.log", str(self.private / "packages.log"))
        result = subprocess.run(["bash"], input=source, env=self.env, text=True,
                                capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 73, result.stdout + result.stderr)
        self.assertTrue(cleanup.exists() and shutdown.exists())

    def test_checksum_and_environment_mismatches_never_deploy(self):
        self.publish()
        self.deploy("wrongsha", success=False, extra=["--config-sha256", "0" * 64])
        receipt = json.loads(self.document.with_suffix(".receipt.json").read_text())
        document = json.loads(self.document.read_text())
        document["environment"] = "another-environment"
        data = (json.dumps(document, sort_keys=True, indent=2) + "\n").encode()
        sha = hashlib.sha256(data).hexdigest()
        state = self.read_state()
        state["objects"]["releases/config/demo/" + sha + ".json"] = [{"id": receipt["version_id"], "data": base64.b64encode(data).decode()}]
        self.write_state(state)
        self.deploy("wrongenvironment", success=False, extra=["--config-sha256", sha])
        self.assertNotIn("stages", self.read_state())


if __name__ == "__main__":
    unittest.main(verbosity=2)
