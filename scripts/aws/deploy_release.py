#!/usr/bin/env python3
"""Fetch a pinned release configuration, regenerate its overlay, and invoke the existing operator."""

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import sys
import urllib.request

from release_config import (Commands, IMAGES, ROOT, ReleaseError, Store, checksum, digest_value,
                            encode, identifier, private_path, require, save, validate)

CA_URL = "https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem"


def release_environment(directory, config, tag):
    # Do not inherit a developer's single-service/build/context overrides. The
    # operator loads .env, so a release checkout must have no hidden .env either.
    require(not (ROOT / ".env").exists(), "Use a release checkout without a root .env file")
    env = {key: value for key, value in os.environ.items()
           if not key.startswith("K8S_") and key not in ("MAKEFLAGS", "MAKEOVERRIDES", "MFLAGS", "service")}
    env.update({
        "KUBECONFIG": str(directory / "kubeconfig"),
        "K8S_CONTEXT": "foc-release", "K8S_CLUSTER": config["cluster_name"],
        "K8S_OVERLAY": str(directory / "overlay"), "K8S_STATE_DIR": str(directory / "operator"),
        "K8S_IMAGE_PREFIX": config["image_prefix"], "K8S_IMAGE_TAG": tag,
        "K8S_PUSH_IMAGES": "1", "K8S_SKIP_BUILD": "0", "K8S_TIMEOUT": "900s",
        "K8S_JOB_TIMEOUT_SECONDS": "900", "K8S_PUBLIC_ORIGIN": config["public_origin"],
        "K8S_LOKI_BUCKET": config["loki_bucket"], "K8S_LOKI_REGION": config["region"],
        "K8S_LOKI_IRSA_ROLE": config["loki_role_arn"], "K8S_LOKI_STORAGE_CLASS": "foc-gp3",
        "K8S_HTTPS_PORT": "", "K8S_KAFKA_UI_PORT": "", "K8S_MAILPIT_PORT": "",
        "DOCKER_DEFAULT_PLATFORM": "linux/amd64", "AQUA_ROOT_DIR": str(ROOT / ".tools/aqua"),
    })
    env["PATH"] = str(ROOT / ".tools/aqua/bin") + os.pathsep + env.get("PATH", "")
    return env


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--region", required=True)
    parser.add_argument("--role-arn", required=True, help="Already-assumed, scoped deployment role; never infrastructure credentials")
    parser.add_argument("--environment", required=True)
    parser.add_argument("--bucket", required=True)
    parser.add_argument("--prefix", default="foc-releases")
    parser.add_argument("--config-version", required=True)
    parser.add_argument("--config-sha256", required=True)
    parser.add_argument("--release-id", required=True, help="New attempt identifier; existing image tags are never overwritten")
    parser.add_argument("--rds-ca", type=Path, help="Optional pre-fetched pinned CA bundle; otherwise use the AWS trust store")
    args = parser.parse_args()
    os.umask(0o077)
    os.environ.update(AWS_PAGER="", AWS_CLI_AUTO_PROMPT="off")
    environment = identifier(args.environment)
    attempt = identifier(args.release_id)
    directory = private_path(ROOT / ".local/aws/releases" / environment / attempt)
    require(not directory.exists(), "Release attempt already exists locally; use a fresh release ID")
    directory.mkdir(parents=True, mode=0o700)
    commands = Commands(directory / "diagnostics", args.region)
    commands.identity(args.account_id, args.role_arn)
    source = commands.run(["git", "rev-parse", "HEAD"]).stdout.decode().strip()
    require(re.fullmatch(r"[a-f0-9]{40}", source), "Invalid release source commit")
    require(not commands.run(["git", "status", "--porcelain"]).stdout.strip(), "Release requires a clean committed checkout")
    sha = digest_value(args.config_sha256)
    store = Store(commands, args.bucket, args.prefix, args.account_id)
    document = store.fetch(environment, sha, args.config_version, directory / "config.json")
    config = validate(document, environment, args.account_id, args.region)
    env = release_environment(directory, config, source + "-" + attempt)
    if args.rds_ca:
        ca = args.rds_ca.read_bytes()
    else:
        with urllib.request.urlopen(CA_URL, timeout=30) as response:
            require(response.url == CA_URL, "Unexpected RDS CA redirect")
            ca = response.read(1024 * 1024)
    require(checksum(ca) == document["rds_ca_sha256"], "RDS CA checksum mismatch; review a new configuration publication")
    require(b"-----BEGIN CERTIFICATE-----" in ca and b"PRIVATE KEY" not in ca, "Expected an RDS CA PEM bundle")
    save(directory / "global-bundle.pem", ca)
    save(directory / "deployment.json", encode(config))
    commands.logged(["make", "aws-tools"], env, directory / "aws-tools.PRIVATE.log")
    commands.logged([str(ROOT / ".tools/bin/aws"), "render-overlay",
                     "--outputs", str(directory / "deployment.json"),
                     "--rds-ca", str(directory / "global-bundle.pem"),
                     "--output", str(directory / "overlay")], env, directory / "render.PRIVATE.log")
    print("INPUT: source=" + source + "; config version=" + args.config_version + "; SHA-256=" + sha, flush=True)
    store.check_versioning()
    save(directory / "lock-owner.json", encode({"release_id": attempt, "source_commit": source, "config_sha256": sha}))
    key, version = store.acquire(environment, directory / "lock-owner.json")
    docker_config = directory / "docker-config"
    registry = config["image_prefix"].split("/", 1)[0]
    logged_in = False
    unlock = True
    try:
        save(directory / "lock-receipt.json", encode({"key": key, "version_id": version}))
        commands.logged(["make", "k8s-tools"], env, directory / "tools.PRIVATE.log")
        commands.run(["aws", "--region", args.region, "eks", "update-kubeconfig", "--name", config["cluster_name"],
                      "--alias", "foc-release", "--kubeconfig", env["KUBECONFIG"]], env=env)
        Path(env["KUBECONFIG"]).chmod(0o600)
        kubectl = ["kubectl", "--context", "foc-release", "--request-timeout=15s"]
        secrets = [name + "-db" for name in ("user", "supplier", "order", "credit", "communication", "moderation")]
        secrets += ["jwt-signing", "kafka-ui-auth", "telegram", "moderation-llm"]
        if not config["ses_sender_email"]:
            secrets.append("smtp")
        commands.run(kubectl + ["-n", "foc", "get", "secret", *secrets, "-o", "name"], env=env)
        commands.run(kubectl + ["-n", "foc-observability", "get", "secret", "grafana-admin", "-o", "name"], env=env)
        commands.run(kubectl + ["get", "crd", "gateways.gateway.networking.k8s.io",
                                "targetgroupbindings.elbv2.k8s.aws", "-o", "name"], env=env)
        namespace = config["image_prefix"].split("/", 1)[1]
        names = [namespace + "/" + image for image in IMAGES]
        repos = commands.json("ecr", "describe-repositories", "--repository-names", *names)["repositories"]
        require({r["repositoryName"] for r in repos} == set(names) and
                all(r["imageTagMutability"] == "IMMUTABLE" and not r.get("imageTagMutabilityExclusionFilters") for r in repos),
                "All nine release repositories must enforce immutable tags without exclusions")
        for name in names:
            result = commands.aws("ecr", "describe-images", "--repository-name", name,
                                  "--image-ids", "imageTag=" + env["K8S_IMAGE_TAG"], allow_failure=True)
            require(result.returncode != 0 and b"(ImageNotFoundException)" in result.stderr,
                    "Image tag exists or cannot be inspected; use a fresh release ID, never overwrite or resume partial pushes")
        # A private Docker config must preserve the selected host and plugin paths.
        host = env.get("DOCKER_HOST") or commands.run(["docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}"], env=env).stdout.decode().strip()
        plugins = json.loads(commands.run(["docker", "info", "--format", "{{json .ClientInfo.Plugins}}"], env=env).stdout)
        directories = sorted({str(Path(p["Path"]).parent) for p in plugins or [] if p.get("Path")})
        save(docker_config / "config.json", encode({"auths": {}, "cliPluginsExtraDirs": directories}))
        env.update(DOCKER_HOST=host, DOCKER_CONFIG=str(docker_config))
        password = commands.aws("ecr", "get-login-password").stdout
        commands.run(["docker", "login", "--username", "AWS", "--password-stdin", registry], env=env, input_data=password)
        logged_in = True
        del password
        print("OPERATOR: build/push nine linux/amd64 images, deploy dependencies, wait for migrations, and roll out applications", flush=True)
        commands.logged(["make", "k8s-deploy", "service="], env, directory / "deploy.PRIVATE.log")
        images = []
        for name, image in zip(names, IMAGES):
            ref = config["image_prefix"] + "/" + image + ":" + env["K8S_IMAGE_TAG"]
            local = json.loads(commands.run(["docker", "image", "inspect", ref], env=env).stdout)[0]
            require(local["Architecture"] == "amd64" and local["Os"] == "linux", "Wrong built image platform")
            detail = commands.json("ecr", "describe-images", "--repository-name", name,
                                   "--image-ids", "imageTag=" + env["K8S_IMAGE_TAG"])["imageDetails"][0]
            require(re.fullmatch(r"sha256:[a-f0-9]{64}", detail["imageDigest"]), "Missing ECR image digest")
            images.append({"reference": ref, "digest": detail["imageDigest"]})
        commands.logged(["make", "k8s-public-smoke"], env, directory / "smoke.PRIVATE.log")
        save(directory / "release-receipt.json", encode({
            "environment": environment, "release_id": attempt, "source_commit": source,
            "configuration": {"bucket": args.bucket, "key": store.key(environment, sha),
                              "version_id": args.config_version, "sha256": sha},
            "infrastructure_commit": document["infrastructure_commit"], "images": images,
            "public_origin": config["public_origin"], "public_smoke": "passed",
        }))
    except BaseException as error:
        # An interrupted subprocess may have surviving descendants. Never make
        # another release possible until an operator confirms they have stopped.
        if not isinstance(error, Exception):
            unlock = False
        raise
    finally:
        if not unlock:
            print("INTERRUPTED: environment lock and private runtime files retained; confirm the owner and all deployment descendants have stopped before manual recovery", file=sys.stderr, flush=True)
        else:
            try:
                if logged_in:
                    commands.run(["docker", "logout", registry], env=env, allow_failure=True)
            finally:
                shutil.rmtree(docker_config, ignore_errors=True)
                store.release(key, version)
    print("OUTPUT: release complete; nine immutable ECR digests recorded, public HTTPS smoke passed, environment lock released", flush=True)
    print("Receipt: " + str(directory / "release-receipt.json"), flush=True)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print("ABORTED: inspect private lock-receipt.json if a lock was acquired; do not automatically retry or unlock", file=sys.stderr)
        sys.exit(130)
    except ReleaseError as error:
        print("FAILED: " + str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError, KeyError, TypeError):
        print("FAILED: release did not complete; inspect protected inputs/diagnostics. Do not retry the same release ID.", file=sys.stderr)
        sys.exit(1)
