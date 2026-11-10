"""Versioned non-secret release configuration and private S3 publication boundaries."""

import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
FIELDS = (
    "region", "cluster_name", "vpc_id", "public_hostname", "public_origin",
    "gateway_target_arn", "image_prefix", "order_bucket", "order_role_arn",
    "ingress_role_arn", "ses_sender_email", "communication_email_role_arn",
    "loki_bucket", "loki_role_arn", "evidence_bucket", "evidence_role_arn",
)
ENVELOPE = {"schema_version", "environment", "infrastructure_commit", "terraform_plan_sha256",
            "rds_ca_sha256", "deployment"}
IMAGES = ("user-service", "supplier-service", "order-service", "credit-service",
          "communication-service", "content-moderation-service", "moderation-service", "frontend", "kafka-init",
          "migrations")


class ReleaseError(Exception):
    """An operator-facing failure whose message must not contain credentials."""


def require(condition, message):
    if not condition:
        raise ReleaseError(message)


def identifier(value):
    require(isinstance(value, str) and re.fullmatch(r"[a-z0-9][a-z0-9-]{0,62}", value),
            "Environment/release identifier must be lowercase letters, digits, or hyphens (1-63 characters)")
    return value


def checksum(data):
    return hashlib.sha256(data).hexdigest()


def digest_value(value):
    require(isinstance(value, str) and re.fullmatch(r"[a-f0-9]{64}", value), "Expected a lowercase SHA-256 digest")
    return value


def private_path(path):
    path = Path(path).resolve()
    require(path.is_relative_to((ROOT / ".local").resolve()), "Generated files must stay inside this checkout's .local")
    return path


def save(path, data):
    path = private_path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_bytes(data)
    temporary.chmod(0o600)
    temporary.replace(path)


def encode(value):
    return (json.dumps(value, sort_keys=True, indent=2) + "\n").encode()


def validate(document, environment, account, region):
    require(isinstance(document, dict) and set(document) == ENVELOPE, "Unexpected release configuration fields")
    require(type(document["schema_version"]) is int and document["schema_version"] == 3,
            "Unsupported release configuration schema")
    require(document["environment"] == identifier(environment), "Release configuration environment mismatch")
    require(isinstance(document["infrastructure_commit"], str) and
            re.fullmatch(r"[a-f0-9]{40}", document["infrastructure_commit"]), "Invalid infrastructure commit")
    digest_value(document["terraform_plan_sha256"])
    digest_value(document["rds_ca_sha256"])
    require(re.fullmatch(r"[0-9]{12}", account or ""), "Expected a 12-digit AWS account")
    config = document["deployment"]
    require(isinstance(config, dict) and set(config) == set(FIELDS), "Unexpected deployment fields")
    for name, value in config.items():
        if name in ("ses_sender_email", "communication_email_role_arn") and value is None:
            continue
        require(isinstance(value, str) and 0 < len(value) < 2048 and not any(c.isspace() for c in value),
                "Invalid deployment field: " + name)
    require(config["region"] == region and re.fullmatch(r"[a-z]{2}-[a-z]+-[0-9]", region), "AWS region mismatch")
    require(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,99}", config["cluster_name"]), "Invalid EKS cluster name")
    require(re.fullmatch(r"vpc-(?:[a-f0-9]{8}|[a-f0-9]{17})", config["vpc_id"]), "Invalid VPC ID")
    hostname = config["public_hostname"]
    require(re.fullmatch(r"[a-z0-9][a-z0-9.-]*\.[a-z]{2,}", hostname), "Invalid public hostname")
    require(config["public_origin"] == "https://" + hostname, "Public origin must match the HTTPS hostname")
    prefix = account + ".dkr.ecr." + region + ".amazonaws.com/"
    require(config["image_prefix"].startswith(prefix) and
            re.fullmatch(r"[a-z0-9]+(?:[._/-][a-z0-9]+)*", config["image_prefix"][len(prefix):]), "ECR account/region mismatch")
    for field in ("order_bucket", "loki_bucket", "evidence_bucket"):
        require(re.fullmatch(r"[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]", config[field]), "Invalid storage bucket: " + field)
    require(config["loki_bucket"] != config["order_bucket"] and config["loki_role_arn"] != config["order_role_arn"],
            "Loki requires its own bucket and workload role")
    require(len({config["order_bucket"], config["loki_bucket"], config["evidence_bucket"]}) == 3,
            "Moderation evidence requires its own bucket")
    for field in ("order_role_arn", "ingress_role_arn", "communication_email_role_arn", "loki_role_arn", "evidence_role_arn"):
        if config[field] is not None:
            require(re.fullmatch(r"arn:aws:iam::" + account + r":role/[A-Za-z0-9+=,.@_/-]+", config[field]),
                    "Workload role account mismatch: " + field)
    require(config["gateway_target_arn"].startswith("arn:aws:elasticloadbalancing:" + region + ":" + account + ":targetgroup/"),
            "Gateway target account/region mismatch")
    require(bool(config["ses_sender_email"]) == bool(config["communication_email_role_arn"]), "Incomplete SES configuration")
    if config["ses_sender_email"]:
        require(re.fullmatch(r"[^@\s<>]+@[^@\s<>]+\.[^@\s<>]+", config["ses_sender_email"]), "Invalid SES sender")
    return config


def configuration(outputs, environment, source, plan_sha, ca_sha, account, region):
    # Deliberately exclude database_admin/database_secret_arn and any future outputs.
    config = {key: outputs.get(key) for key in FIELDS}
    document = {"schema_version": 3, "environment": environment, "infrastructure_commit": source,
                "terraform_plan_sha256": plan_sha, "rds_ca_sha256": ca_sha, "deployment": config}
    validate(document, environment, account, region)
    return document


class Commands:
    def __init__(self, directory, region):
        self.directory = private_path(directory)
        self.directory.mkdir(parents=True, exist_ok=True)
        self.region = region
        self.sequence = 0

    def run(self, args, env=None, input_data=None, allow_failure=False):
        result = subprocess.run([str(arg) for arg in args], cwd=ROOT, env=env, input=input_data,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if result.returncode:
            self.sequence += 1
            log = self.directory / ("command-" + str(self.sequence) + ".PRIVATE.log")
            save(log, result.stdout + result.stderr)
            if not allow_failure:
                raise ReleaseError("Command failed; protected diagnostic: " + str(log))
        return result

    def logged(self, args, env, path):
        path = private_path(path)
        with path.open("wb") as output:
            path.chmod(0o600)
            result = subprocess.run(args, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT)
        require(result.returncode == 0, "Command failed; protected diagnostic: " + str(path))

    def aws(self, *args, allow_failure=False):
        return self.run(["aws", "--region", self.region, "--cli-connect-timeout", "10",
                         "--cli-read-timeout", "60", *args], allow_failure=allow_failure)

    def json(self, *args):
        return json.loads(self.aws(*args).stdout)

    def identity(self, account, role=None):
        identity = self.json("sts", "get-caller-identity")
        require(identity["Account"] == account, "AWS caller account mismatch")
        if role:
            require(re.fullmatch(r"arn:aws:iam::" + account + r":role/[A-Za-z0-9+=,.@_/-]+", role), "Invalid deployment role ARN")
            expected = "arn:aws:sts::" + account + ":assumed-role/" + role.rsplit("/", 1)[1] + "/"
            require(identity["Arn"].startswith(expected), "Caller must already assume the specified deployment role")
        return identity


class Store:
    def __init__(self, commands, bucket, prefix, account):
        require(re.fullmatch(r"[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]", bucket), "Invalid configuration bucket")
        require(re.fullmatch(r"[a-z0-9][a-z0-9/-]*", prefix) and
                all(part not in ("", ".", "..") for part in prefix.split("/")), "Invalid configuration prefix")
        self.commands, self.bucket, self.prefix, self.account = commands, bucket, prefix, account

    def args(self, key):
        return ["--bucket", self.bucket, "--key", key, "--expected-bucket-owner", self.account]

    def key(self, environment, sha):
        return self.prefix + "/config/" + identifier(environment) + "/" + digest_value(sha) + ".json"

    def check_versioning(self):
        version = self.commands.json("s3api", "get-bucket-versioning", "--bucket", self.bucket, "--expected-bucket-owner", self.account)
        require(version.get("Status") == "Enabled", "Configuration bucket must have versioning enabled")

    def check_publication_bucket(self):
        self.check_versioning()
        block = self.commands.json("s3api", "get-public-access-block", "--bucket", self.bucket, "--expected-bucket-owner", self.account)
        require(all(block["PublicAccessBlockConfiguration"].get(k) is True for k in
                    ("BlockPublicAcls", "IgnorePublicAcls", "BlockPublicPolicy", "RestrictPublicBuckets")),
                "Configuration bucket must block all public access")

    def publish(self, path, environment):
        data = path.read_bytes()
        sha = checksum(data)
        key = self.key(environment, sha)
        result = self.commands.aws("s3api", "put-object", *self.args(key), "--body", str(path),
                                   "--server-side-encryption", "AES256", "--if-none-match", "*",
                                   "--checksum-algorithm", "SHA256", "--checksum-sha256",
                                   base64.b64encode(hashlib.sha256(data).digest()).decode(), allow_failure=True)
        if result.returncode:
            require(b"(PreconditionFailed)" in result.stderr, "Publication failed; retry publish-only with the saved document, never reapply")
            existing = self.commands.directory / "existing-config.json"
            metadata = self.commands.json("s3api", "get-object", *self.args(key), "--checksum-mode", "ENABLED", str(existing))
            require(existing.read_bytes() == data, "Existing immutable configuration differs")
        else:
            metadata = json.loads(result.stdout)
        version = metadata.get("VersionId")
        require(isinstance(version, str) and version not in ("", "null"), "S3 did not return a versioned object")
        return {"bucket": self.bucket, "key": key, "version_id": version, "sha256": sha, "environment": environment}

    def fetch(self, environment, sha, version, path):
        require(isinstance(version, str) and version not in ("", "null"), "An exact S3 object version is required")
        metadata = self.commands.json("s3api", "get-object", *self.args(self.key(environment, sha)),
                                      "--version-id", version, "--checksum-mode", "ENABLED", str(path))
        require(metadata.get("VersionId") == version, "S3 object version mismatch")
        path.chmod(0o600)
        require(path.stat().st_size <= 65536 and checksum(path.read_bytes()) == sha, "Configuration checksum/size mismatch")
        return json.loads(path.read_bytes())

    def acquire(self, environment, owner_path):
        key = self.prefix + "/locks/" + identifier(environment) + ".json"
        result = self.commands.aws("s3api", "put-object", *self.args(key), "--body", str(owner_path),
                                   "--server-side-encryption", "AES256", "--if-none-match", "*", allow_failure=True)
        require(result.returncode == 0, "Release lock unavailable; do not retry or clear it until its owner is confirmed stopped")
        version = json.loads(result.stdout).get("VersionId")
        require(isinstance(version, str) and version not in ("", "null"), "Release lock requires a versioned bucket")
        return key, version

    def release(self, key, version):
        # Delete exactly our version, never a successor's lock or a delete marker.
        self.commands.json("s3api", "delete-object", *self.args(key), "--version-id", version)
