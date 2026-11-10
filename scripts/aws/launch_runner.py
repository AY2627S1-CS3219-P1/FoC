#!/usr/bin/env python3
"""Launch one disposable runner; default registration probe terminates it without a job."""

import argparse
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import subprocess
import sys
import time

from release_config import Commands, ROOT, ReleaseError, Store, private_path, require, save, encode

ORG = "AY2627S1-CS3219-P1"
REPOSITORY = ORG + "/FoC"
WORKFLOW = REPOSITORY + "/.github/workflows/aws-release.yml@refs/heads/main"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--configuration", type=Path, required=True, help="terraform output -json ci from infra/aws/ci")
    parser.add_argument("--runner-group", type=int, required=True)
    parser.add_argument("--keep-running", action="store_true", help="Leave one runner waiting for an approved release; two-hour guest shutdown timer remains")
    args = parser.parse_args()
    os.umask(0o077)
    config = json.loads(args.configuration.read_text())
    directory = private_path(ROOT / ".local/aws/runner-launches" / str(time.time_ns()))
    directory.mkdir(parents=True, mode=0o700)
    os.environ.update(AWS_PAGER="", AWS_CLI_AUTO_PROMPT="off")
    sequence = 0

    def command(arguments, body=None, allow_failure=False):
        nonlocal sequence
        sequence += 1
        result = subprocess.run(arguments, input=encode(body) if body is not None else None,
                                capture_output=True, cwd=ROOT)
        if result.returncode:
            log = directory / (str(sequence) + ".PRIVATE.log")
            save(log, result.stdout + result.stderr)
            require(allow_failure, "Command failed; protected diagnostic: " + str(log))
            return None
        return json.loads(result.stdout) if result.stdout.strip() else {}

    def aws(*arguments, body=None, allow_failure=False):
        values = ["aws", "--region", config["region"], "--cli-connect-timeout", "10", "--cli-read-timeout", "30", *arguments]
        if body is not None:
            path = directory / (str(sequence + 1) + ".PRIVATE.input.json")
            save(path, encode(body))
            values += ["--cli-input-json", "file://" + str(path)]
        return command(values, allow_failure=allow_failure)

    def github(path, method="GET", body=None, allow_failure=False):
        values = ["gh", "api", "--method", method, path]
        if body is not None:
            values += ["--input", "-"]
        return command(values, body, allow_failure)

    require(aws("sts", "get-caller-identity")["Account"] == config["account_id"], "AWS account mismatch")
    group_path = "orgs/" + ORG + "/actions/runner-groups/" + str(args.runner_group)
    group = github(group_path)
    selected = group.get("selected_workflows", [])
    require(group["name"] == "foc-release" and group["visibility"] == "selected" and group.get("restricted_to_workflows") is True,
            "Runner group must be selected-repository and workflow restricted")
    require(selected == ([WORKFLOW] if args.keep_running else []),
            "Registration probes require a closed group; release runners require exactly the protected main workflow")
    repositories = github(group_path + "/repositories")
    require(repositories["total_count"] == 1 and repositories["repositories"][0]["full_name"] == REPOSITORY,
            "Runner group must select only FoC")
    if args.keep_running:
        require(WORKFLOW in selected, "Activate the main-only workflow restriction after merge before launching a release runner")
    name = config["runner_name"] + "-" + str(time.time_ns())
    # Serialize through parameter cleanup, not merely the instance-state check.
    # This operator lock is not accessible to the application deployment role.
    store = Store(Commands(directory / "lock-diagnostics", config["region"]),
                  config["bucket"], config["prefix"], config["account_id"])
    store.check_versioning()
    owner = directory / "launch-lock-owner.json"
    save(owner, encode({"launch": name}))
    lock_key, lock_version = store.acquire("runner-bootstrap", owner)
    save(directory / "launch-lock-receipt.json", encode({"key": lock_key, "version_id": lock_version}))
    runner_id = instance_id = None
    parameter_created = False
    transferred = False
    uncertain = False
    try:
        reservations = aws("ec2", "describe-instances", "--filters",
                           "Name=tag:Name,Values=" + config["runner_name"],
                           "Name=instance-state-name,Values=pending,running,stopping,stopped,shutting-down")["Reservations"]
        require(not any(r["Instances"] for r in reservations), "An existing runner instance must finish or be cleaned up first")
        registration = github("orgs/" + ORG + "/actions/runners/generate-jitconfig", "POST",
                              {"name": name, "runner_group_id": args.runner_group,
                               "labels": ["self-hosted", "linux", "x64", "foc-deploy"], "work_folder": "_work"})
        runner_id = registration["runner"]["id"]
        value = registration["encoded_jit_config"]
        require(len(value.encode()) <= 8192, "JIT registration exceeds the encrypted parameter size limit")
        aws("ssm", "put-parameter", body={"Name": config["registration_parameter"], "Type": "SecureString",
            "Tier": "Advanced", "Value": value, "Overwrite": False,
            "Policies": json.dumps([{"Type": "Expiration", "Version": "1.0", "Attributes": {
                "Timestamp": (datetime.now(timezone.utc) + timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ")}}]),
            "Description": "One-job CI registration; consumed and deleted by its private runner"})
        parameter_created = True
        del value, registration
        print("INPUT: one-job runner group=" + str(args.runner_group) + "; pinned launch template=" + config["launch_template_id"] + ":" + str(config["launch_template_version"]), flush=True)
        response = aws("ec2", "run-instances", body={
            "LaunchTemplate": {"LaunchTemplateId": config["launch_template_id"], "Version": str(config["launch_template_version"])},
            "MinCount": 1, "MaxCount": 1, "ClientToken": name,
            "CreditSpecification": {"CpuCredits": "standard"},
        })
        instance = response["Instances"][0]
        instance_id = instance["InstanceId"]
        save(directory / "owner.json", encode({"instance_id": instance_id, "runner_id": runner_id, "runner_name": name}))
        require(not instance.get("PublicIpAddress"), "Runner must not have a public IP")
        print("OUTPUT: instance=" + instance_id + "; runner=" + str(runner_id) + "; private subnet=" + instance["SubnetId"], flush=True)
        deadline = time.monotonic() + 900
        while time.monotonic() < deadline:
            runner = github("orgs/" + ORG + "/actions/runners/" + str(runner_id))
            if runner["status"] == "online":
                break
            observed = aws("ec2", "describe-instances", "--instance-ids", instance_id)["Reservations"][0]["Instances"][0]
            require(observed["State"]["Name"] in ("pending", "running"), "Runner bootstrap stopped; inspect its protected console output")
            time.sleep(15)
        else:
            raise ReleaseError("Runner registration deadline exceeded; instance and registration will be cleaned up")
        # Bootstrap credentials are unnecessary once the parameter has been consumed.
        associations = aws("ec2", "describe-iam-instance-profile-associations", "--filters", "Name=instance-id,Values=" + instance_id)["IamInstanceProfileAssociations"]
        for association in associations:
            if association["State"] == "associated":
                aws("ec2", "disassociate-iam-instance-profile", "--association-id", association["AssociationId"])
        credit = aws("ec2", "describe-instance-credit-specifications", "--instance-ids", instance_id)["InstanceCreditSpecifications"][0]
        require(credit["CpuCredits"] == "standard", "Runner must use Standard CPU credits")
        console = aws("ec2", "get-console-output", "--instance-id", instance_id, "--latest")
        save(directory / "console.PRIVATE.json", encode(console))
        print("OUTPUT: GitHub runner online; private EKS TLS probe precedes registration; Standard CPU credits; bootstrap profile detached", flush=True)
        if args.keep_running:
            transferred = True
            print("OUTPUT: runner waiting for one approved job; owner receipt=" + str(directory / "owner.json"), flush=True)
    except BaseException as error:
        uncertain = not isinstance(error, Exception)
        if instance_id and not uncertain:
            # Optional diagnostics must not replace the primary failure or prevent cleanup.
            try:
                # Prefer buffered failure output; retain the live snapshot if buffering is delayed.
                console = aws("ec2", "get-console-output", "--instance-id", instance_id, allow_failure=True)
                if console is not None and not console.get("Output"):
                    latest = aws("ec2", "get-console-output", "--instance-id", instance_id, "--latest", allow_failure=True)
                    if latest is not None:
                        console = latest
                if console is not None:
                    save(directory / "console.PRIVATE.json", encode(console))
                    print("OUTPUT: bootstrap console saved: " + str(directory / "console.PRIVATE.json"), flush=True)
            except BaseException as diagnostic_error:
                if not isinstance(diagnostic_error, Exception):
                    uncertain = True
                    raise
        raise
    finally:
        cleanup_errors = []
        if not transferred:
            if not instance_id and parameter_created:
                # A timed-out launch may have succeeded. Recover only this
                # invocation's exact idempotency token, never another instance.
                recovered = aws("ec2", "describe-instances", "--filters", "Name=client-token,Values=" + name,
                                allow_failure=True)
                if recovered is None:
                    cleanup_errors.append("uncertain launch lookup for client token " + name)
                else:
                    found = [i["InstanceId"] for r in recovered["Reservations"] for i in r["Instances"]]
                    require(len(found) <= 1, "Unexpected multiple instances for the exact launch token; inspect private inputs")
                    instance_id = found[0] if found else None
            if instance_id:
                if aws("ec2", "terminate-instances", "--instance-ids", instance_id, allow_failure=True) is None:
                    cleanup_errors.append("instance termination")
                else:
                    print("CLEANUP: instance termination requested: " + instance_id, flush=True)
            if runner_id:
                if github("orgs/" + ORG + "/actions/runners/" + str(runner_id), "DELETE", allow_failure=True) is None:
                    cleanup_errors.append("GitHub runner removal")
        if parameter_created:
            # It may already have been consumed. Do not log the encrypted value.
            aws("ssm", "delete-parameter", "--name", config["registration_parameter"], allow_failure=True)
        for path in directory.glob("*.PRIVATE.input.json"):
            path.unlink()
        if cleanup_errors:
            raise ReleaseError("Manual cleanup required; launch lock retained: " + ", ".join(cleanup_errors) + "; owner receipt=" + str(directory / "owner.json"))
        if uncertain:
            print("INTERRUPTED: launch lock retained; confirm this launch and cleanup stopped before manual recovery", file=sys.stderr)
        else:
            store.release(lock_key, lock_version)
    print("OUTPUT: registration probe finished; no application job dispatched", flush=True)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print("ABORTED: inspect the private owner receipt and confirm instance/registration cleanup", file=sys.stderr)
        sys.exit(130)
    except (ReleaseError, OSError, ValueError, KeyError, TypeError) as error:
        print("FAILED: " + (str(error) if isinstance(error, ReleaseError) else "inspect protected diagnostics"), file=sys.stderr)
        sys.exit(1)
