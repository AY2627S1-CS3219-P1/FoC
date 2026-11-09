#!/usr/bin/env python3
"""Apply one reviewed Terraform plan and publish only the non-secret release contract."""

import argparse
import json
import os
from pathlib import Path
import sys

from release_config import (Commands, ROOT, ReleaseError, Store, checksum, configuration,
                            digest_value, encode, identifier, private_path, require, save, validate)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--publish-only", action="store_true", help="Upload an already exported document; never invoke Terraform")
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--region", required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--bucket", required=True)
    parser.add_argument("--prefix", default="foc-releases")
    parser.add_argument("--document", type=Path, required=True, help="Private local export, retained for upload recovery")
    parser.add_argument("--config-sha256", help="Required for publish-only recovery")
    parser.add_argument("--plan", type=Path)
    parser.add_argument("--plan-sha256", help="Fingerprint of the explicitly reviewed saved plan")
    parser.add_argument("--rds-ca", type=Path, help="Trusted RDS CA PEM used to pin release retrieval")
    parser.add_argument("--terraform", default="terraform")
    args = parser.parse_args()
    os.umask(0o077)
    os.environ.update(AWS_PAGER="", AWS_CLI_AUTO_PROMPT="off", TF_IN_AUTOMATION="1")
    environment = identifier(args.environment)
    document_path = private_path(args.document)
    commands = Commands(document_path.parent / "publication-diagnostics", args.region)
    store = Store(commands, args.bucket, args.prefix, args.account_id)

    # Inspect storage before any infrastructure mutation; upload authorization may
    # still fail later, so the exact exported document is always retained.
    commands.identity(args.account_id)
    store.check_publication_bucket()
    if args.publish_only:
        require(not args.plan and not args.plan_sha256 and not args.rds_ca,
                "Publish-only cannot accept Terraform/apply inputs")
        data = document_path.read_bytes()
        require(checksum(data) == digest_value(args.config_sha256), "Recovery document checksum mismatch")
        document = json.loads(data)
        validate(document, environment, args.account_id, args.region)
    else:
        require(args.plan and args.plan_sha256 and args.rds_ca and not args.config_sha256,
                "Apply requires --plan, --plan-sha256, and --rds-ca")
        marker = document_path.with_suffix(".applied.json")
        require(not document_path.exists() and not marker.exists(),
                "Apply/export was already attempted here; do not reapply a completed plan. Recover publication or review a fresh plan")
        plan = private_path(args.plan)
        require(plan.stat().st_mode & 0o077 == 0, "Reviewed Terraform plan must not be group/world readable")
        plan_sha = digest_value(args.plan_sha256)
        plan_bytes = plan.read_bytes()
        require(checksum(plan_bytes) == plan_sha, "Reviewed Terraform plan checksum mismatch")
        ca = args.rds_ca.read_bytes()
        require(b"-----BEGIN CERTIFICATE-----" in ca and b"PRIVATE KEY" not in ca, "Expected an RDS CA PEM bundle")
        source = commands.run(["git", "rev-parse", "HEAD"]).stdout.decode().strip()
        require(not commands.run(["git", "status", "--porcelain"]).stdout.strip(), "Apply requires a clean committed checkout")
        print("INPUT: reviewed plan SHA-256=" + plan_sha + "; source=" + source, flush=True)
        # Do not synthesize a new plan or auto-approve an unreviewed one.
        snapshot = document_path.with_suffix(".reviewed.tfplan")
        save(snapshot, plan_bytes)
        result = commands.run([args.terraform, "-chdir=" + str(ROOT / "infra/aws"), "apply", "-input=false", str(snapshot)])
        save(marker, encode({"infrastructure_commit": source, "terraform_plan_sha256": plan_sha}))
        save(document_path.with_suffix(".apply.PRIVATE.log"), result.stdout + result.stderr)
        print("OUTPUT: saved-plan apply completed; exporting the non-secret configuration", flush=True)
        outputs = json.loads(commands.run([args.terraform, "-chdir=" + str(ROOT / "infra/aws"),
                                           "output", "-json", "deployment"]).stdout)
        document = configuration(outputs, environment, source, plan_sha, checksum(ca), args.account_id, args.region)
        save(document_path, encode(document))
    sha = checksum(document_path.read_bytes())
    print("EXPORT: " + str(document_path) + "; SHA-256=" + sha, flush=True)
    print("RECOVERY: --publish-only --document <same-file> --config-sha256 " + sha + " (same bucket, prefix, account, region, environment)", flush=True)
    receipt = store.publish(document_path, environment)
    save(document_path.with_suffix(".receipt.json"), encode(receipt))
    print(json.dumps({"publication": receipt}, sort_keys=True), flush=True)


if __name__ == "__main__":
    try:
        main()
    except ReleaseError as error:
        print("FAILED: " + str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError, KeyError, TypeError):
        # Provider/API errors can contain secrets. Protected diagnostics and the
        # preceding phase/checksum identify recovery without echoing response bodies.
        print("FAILED: publication did not complete; inspect private inputs/diagnostics. If EXPORT was printed, retry publish-only, not apply.", file=sys.stderr)
        sys.exit(1)
