#!/usr/bin/env python3
"""Offline external-command fixture for release CLI contract tests. Never contacts AWS."""

import base64
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

state_file = Path(os.environ["RELEASE_FIXTURE_STATE"])
state = json.loads(state_file.read_text())
command = Path(sys.argv[0]).name
args = sys.argv[1:]
state.setdefault("calls", []).append([command, *args])


def option(name):
    return args[args.index(name) + 1]


def finish(value=None, error=None):
    state_file.write_text(json.dumps(state))
    if error:
        print(error, file=sys.stderr)
        sys.exit(1)
    if value is not None:
        print(value if isinstance(value, str) else json.dumps(value))
    sys.exit(0)


if command == "git":
    finish(state["source"] if args[:2] == ["rev-parse", "HEAD"] else "")
if command == "terraform":
    if "apply" in args:
        state["applies"] = state.get("applies", 0) + 1
        finish("fixture-private-provider-output-NOT-FOR-LOGS")
    if "output" in args:
        finish(state["outputs"])
    finish(error="unexpected Terraform command")
if command == "gh":
    endpoint = next(a for a in args if a.startswith("orgs/"))
    method = option("--method")
    body = json.load(sys.stdin) if "--input" in args else None
    if "/runner-groups/" in endpoint:
        if endpoint.endswith("/repositories"):
            finish({"total_count": 1, "repositories": [{"full_name": "AY2627S1-CS3219-P1/FoC"}]})
        finish({"name": "foc-release", "visibility": "selected", "restricted_to_workflows": True,
                "selected_workflows": state.get("workflows", [])})
    if endpoint.endswith("generate-jitconfig"):
        finish({"runner": {"id": 42}, "encoded_jit_config": "fixture-private-JIT-NOT-FOR-LOGS"})
    if method == "DELETE":
        state["registration_removed"] = True
        finish()
    finish({"status": "offline" if state.get("bootstrap_failure") else "online"})
if command == "aws":
    service = next(a for a in args if a in ("sts", "s3api", "eks", "ecr", "ssm", "ec2"))
    operation = args[args.index(service) + 1]
    if service == "sts":
        finish({"Account": state.get("caller_account", state["account"]),
                "Arn": "arn:aws:sts::" + state["account"] + ":assumed-role/" + state.get("caller_role", "release") + "/fixture"})
    if service == "ssm":
        if operation == "put-parameter":
            body = json.loads(Path(option("--cli-input-json").removeprefix("file://")).read_text())
            state["parameter_tier"] = body["Tier"]
            state["parameter_has_expiry"] = bool(json.loads(body["Policies"])[0]["Attributes"]["Timestamp"])
            finish({"Version": 1})
        state["parameter_removed"] = True
        finish()
    if service == "ec2":
        if operation == "describe-instances":
            if "--instance-ids" in args:
                finish({"Reservations": [{"Instances": [{"State": {"Name": "terminated"}}]}]})
            finish({"Reservations": [{"Instances": [{"InstanceId": "i-existing"}]}] if state.get("existing_runner") else []})
        if operation == "run-instances":
            finish({"Instances": [{"InstanceId": "i-disposable", "SubnetId": "subnet-private"}]})
        if operation == "describe-iam-instance-profile-associations":
            finish({"IamInstanceProfileAssociations": [{"State": "associated", "AssociationId": "iip-association"}]})
        if operation == "disassociate-iam-instance-profile":
            state["profile_detached"] = True
            finish()
        if operation == "describe-instance-credit-specifications":
            finish({"InstanceCreditSpecifications": [{"CpuCredits": "standard"}]})
        if operation == "get-console-output":
            if state.get("console_failure"):
                finish(error="fixture console unavailable")
            if state.get("buffered_console_empty") and "--latest" not in args:
                finish({"Output": ""})
            if state.get("console_save_failure"):
                launches = state_file.parent / "aws/runner-launches"
                (max(launches.iterdir()) / "console.PRIVATE.json").mkdir()
            finish({"Output": "fixture bootstrap output"})
        if operation == "terminate-instances":
            state["instance_terminated"] = True
            finish()
        finish(error="unexpected EC2 fixture operation")
    if service == "s3api":
        if operation == "get-bucket-versioning":
            finish({"Status": state.get("versioning", "Enabled")})
        if operation == "get-public-access-block":
            finish({"PublicAccessBlockConfiguration": {key: not state.get("public_bucket", False) for key in
                    ("BlockPublicAcls", "IgnorePublicAcls", "BlockPublicPolicy", "RestrictPublicBuckets")}})
        key = option("--key")
        objects = state.setdefault("objects", {})
        versions = objects.setdefault(key, [])
        if operation == "put-object":
            if state.get("fail_upload") and "/config/" in key:
                finish(error="(AccessDenied) fixture-private-api-output-NOT-FOR-LOGS")
            if versions or (state.get("lock_taken") and "/locks/" in key):
                finish(error="(PreconditionFailed) already exists")
            state["version_sequence"] = state.get("version_sequence", 0) + 1
            version = "version-" + str(state["version_sequence"])
            data = Path(option("--body")).read_bytes()
            versions.append({"id": version, "data": base64.b64encode(data).decode()})
            finish({"VersionId": version, "ChecksumSHA256": base64.b64encode(hashlib.sha256(data).digest()).decode()})
        if operation == "get-object":
            selected = next((v for v in versions if v["id"] == option("--version-id")), None) if "--version-id" in args else versions[-1]
            if selected is None:
                finish(error="(NoSuchVersion) missing configuration")
            Path(args[-1]).write_bytes(base64.b64decode(selected["data"]))
            finish({"VersionId": selected["id"]})
        if operation == "delete-object":
            objects[key] = [v for v in versions if v["id"] != option("--version-id")]
            finish({})
        finish(error="unexpected S3 command")
    if service == "eks" and operation == "update-kubeconfig":
        Path(option("--kubeconfig")).write_text("fixture kubeconfig, no credentials\n")
        finish("context configured")
    if service == "ecr":
        if operation == "describe-repositories":
            names = args[args.index("--repository-names") + 1:]
            finish({"repositories": [{"repositoryName": name, "imageTagMutability": state.get("mutability", "IMMUTABLE")}
                                     for name in names]})
        if operation == "get-login-password":
            finish("fixture-ecr-password")
        if operation == "describe-images":
            key = option("--repository-name") + ":" + option("--image-ids").split("=", 1)[1]
            if key not in state.get("images", {}):
                finish(error="(ImageNotFoundException) image does not exist")
            finish({"imageDetails": [{"imageDigest": state["images"][key]}]})
    finish(error="unexpected AWS command")
if command == "kubectl":
    if state.get("missing_secret") and "secret" in args:
        finish(error="missing existing Secret")
    finish("existing prerequisites available")
if command == "docker":
    if args[:2] == ["context", "inspect"]:
        finish("unix:///fixture/docker.sock")
    if args[0] == "info":
        finish([])
    if args[0] == "login":
        state["password_stdin_ok"] = sys.stdin.buffer.read() == b"fixture-ecr-password\n"
        finish("login complete")
    if args[:2] == ["image", "inspect"]:
        finish([{"Architecture": "amd64", "Os": "linux"}])
    if args[0] == "logout":
        state["logged_out"] = True
        finish("logout complete")
    finish(error="unexpected Docker command")
if command == "make":
    target = args[0]
    if target == "k8s-tools":
        finish()
    if target == "k8s-deploy":
        state["deploy_environment"] = {key: os.environ.get(key) for key in
            ("K8S_SKIP_BUILD", "K8S_PUSH_IMAGES", "K8S_IMAGE_PREFIX", "K8S_IMAGE_TAG", "K8S_CONTEXT",
             "K8S_OVERLAY", "KUBECONFIG", "DOCKER_CONFIG", "DOCKER_DEFAULT_PLATFORM")}
        if state.get("interruptible_deploy"):
            ready = state_file.with_suffix(".descendant-ready")
            child = subprocess.Popen([
                sys.executable, "-c",
                "import signal; from pathlib import Path; signal.alarm(15); Path(" + repr(str(ready)) + ").write_text('ready'); signal.pause()",
            ], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            state["descendant_pid"] = child.pid
            state_file.write_text(json.dumps(state))
            deadline = time.monotonic() + 5
            while not ready.exists() and time.monotonic() < deadline:
                time.sleep(0.01)
            if not ready.exists():
                child.terminate()
                finish(error="interruption fixture descendant did not start")
            os.kill(os.getppid(), signal.SIGINT)
            signal.alarm(15)
            signal.pause()
            finish(error="interruption fixture unexpectedly resumed")
        namespace = os.environ["K8S_IMAGE_PREFIX"].split("/", 1)[1]
        tag = os.environ["K8S_IMAGE_TAG"]
        for name in ("user-service", "supplier-service", "order-service", "credit-service", "communication-service", "frontend", "kafka-init", "migrations"):
            state.setdefault("images", {})[namespace + "/" + name + ":" + tag] = "sha256:" + hashlib.sha256(name.encode()).hexdigest()
        state.setdefault("stages", []).extend(["dependencies", "migrations"])
        if state.get("fail_migration"):
            finish(error="migration failure fixture-private-deployment-output-NOT-FOR-LOGS")
        state["stages"].append("applications")
        finish("operator completed")
    if target == "k8s-public-smoke":
        state["smoke_called"] = True
        if state.get("fail_smoke"):
            finish(error="public smoke failure")
        finish("public smoke passed")
    finish(error="unexpected Make target")
finish(error="unexpected fixture executable")
