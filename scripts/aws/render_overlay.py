#!/usr/bin/env python3
"""Render private AWS overlays from non-secret Terraform outputs. Never contacts AWS."""

import argparse
import json
import os
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]
SERVICES = ("user", "supplier", "order", "credit", "communication")
ORIGIN_KEYS = (
    "FRONTEND_BASE_URL", "ORIGIN", "PUBLIC_USER_SERVICE_URL",
    "PUBLIC_SUPPLIER_SERVICE_URL", "PUBLIC_ORDER_SERVICE_URL",
    "PUBLIC_COMMUNICATION_SERVICE_URL", "PUBLIC_CREDIT_SERVICE_URL",
)


def save(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n")


def save_kustomization(directory, value):
    # The Go operator requires this filename, even when its contents are JSON.
    # Remove the previous generated name so Kustomize cannot find two definitions.
    (directory / "kustomization.json").unlink(missing_ok=True)
    save(directory / "kustomization.yaml", value)


def patch(kind, name, value):
    target = {"kind": kind}
    if name:
        target["name"] = name
    return {"target": target, "patch": json.dumps(value)}


def certificate_patch():
    return {
        "spec": {"template": {"spec": {
            "containers": [{
                "name": "PLACEHOLDER",
                "volumeMounts": [{"name": "rds-ca", "mountPath": "/var/run/foc/rds", "readOnly": True}],
            }],
            "volumes": [{"name": "rds-ca", "configMap": {"name": "rds-ca"}}],
        }}},
    }


def render(config, destination, ca):
    required = ("region", "cluster_name", "vpc_id", "public_hostname", "image_prefix",
                "order_bucket", "order_role_arn", "ingress_role_arn", "gateway_target_arn")
    for key in required:
        if not config.get(key):
            raise ValueError(f"Missing Terraform output: {key}")
    ses_sender = config.get("ses_sender_email")
    ses_role = config.get("user_email_role_arn")
    if bool(ses_sender) != bool(ses_role):
        raise ValueError("SES requires both ses_sender_email and user_email_role_arn")
    if ses_sender and not re.fullmatch(r"[^@\s<>]+@[^@\s<>]+\.[^@\s<>]+", ses_sender):
        raise ValueError("ses_sender_email must be a bare verified email address")
    hostname = config["public_hostname"]
    if not re.fullmatch(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}", hostname):
        raise ValueError("public_hostname must be a hostname, not a URL")
    if "-----BEGIN CERTIFICATE-----" not in ca:
        raise ValueError("Supply the RDS CA PEM bundle from the AWS trust store")
    destination = destination.resolve()
    # Rendering must never overwrite a tracked production template or application source.
    private_root = (ROOT / ".local").resolve()
    if not destination.is_relative_to(private_root):
        raise ValueError("Output must be inside this checkout's ignored .local directory")

    stages = ("gateway", "dependencies", "provisioning", "initialization", "applications")
    for stage in stages:
        directory = destination / stage
        base = ROOT / "infra/k8s/base" / stage
        kustomization = {
            "apiVersion": "kustomize.config.k8s.io/v1beta1",
            "kind": "Kustomization",
            "resources": [] if stage == "provisioning" else [os.path.relpath(base, directory)],
        }
        patches = []
        if stage == "gateway":
            service = {
                "spec": {"type": "ClusterIP"},
            }
            patches.append(patch("ConfigMap", "foc-public-infrastructure", [{
                "op": "replace", "path": "/data/service", "value": json.dumps(service),
            }]))
            patches.append(patch("Gateway", "foc-public", [
                {"op": "replace", "path": "/spec/listeners/0/port", "value": 80},
                {"op": "replace", "path": "/spec/listeners/0/protocol", "value": "HTTP"},
                {"op": "remove", "path": "/spec/listeners/0/tls"},
            ]))
            # Keep the listener section name so existing HTTPRoute parentRefs
            # remain valid. Only private ingress changes from HTTPS to HTTP.
            save(directory / "target-group-binding.json", {
                "apiVersion": "elbv2.k8s.aws/v1beta1", "kind": "TargetGroupBinding",
                "metadata": {"name": "foc-public", "namespace": "istio-ingress"},
                "spec": {
                    "serviceRef": {"name": "foc-public-istio", "port": 80},
                    "targetGroupARN": config["gateway_target_arn"],
                    "targetType": "ip", "vpcID": config["vpc_id"],
                },
            })
            kustomization["resources"].append("target-group-binding.json")
        if stage == "dependencies":
            values = {key: f"https://{hostname}" for key in ORIGIN_KEYS}
            values.update({
                "AWS_REGION": config["region"],
                "AWS_STS_REGIONAL_ENDPOINTS": "regional",
                "ORDER_S3_BUCKET": config["order_bucket"],
                "ORDER_S3_ENDPOINT": "",
                "ORDER_S3_PUBLIC_ENDPOINT": f"https://s3.{config['region']}.amazonaws.com",
            })
            if ses_sender:
                values.update({"EMAIL_PROVIDER": "ses", "EMAIL_FROM": ses_sender})
                patches.append(patch("ServiceAccount", "user-service", [{
                    "op": "add", "path": "/metadata/annotations",
                    "value": {"eks.amazonaws.com/role-arn": ses_role},
                }]))
            patches.append(patch("ConfigMap", "foc-config", [
                {"op": "add", "path": f"/data/{key}", "value": value}
                for key, value in values.items()
            ]))
            patches.append(patch("ServiceAccount", "order-service", [{
                "op": "add", "path": "/metadata/annotations",
                "value": {"eks.amazonaws.com/role-arn": config["order_role_arn"]},
            }]))
            patches.append(patch("StatefulSet", "kafka", [{
                "op": "add", "path": "/spec/volumeClaimTemplates/0/spec/storageClassName", "value": "foc-gp3",
            }]))
            save(directory / "storage-class.json", {
                "apiVersion": "storage.k8s.io/v1", "kind": "StorageClass",
                "metadata": {"name": "foc-gp3"}, "provisioner": "ebs.csi.aws.com",
                "parameters": {"type": "gp3", "encrypted": "true"},
                "volumeBindingMode": "WaitForFirstConsumer", "reclaimPolicy": "Retain",
                "allowVolumeExpansion": True,
            })
            save(directory / "rds-ca.json", {
                "apiVersion": "v1", "kind": "ConfigMap",
                "metadata": {"name": "rds-ca", "namespace": "foc"},
                "data": {"global-bundle.pem": ca},
            })
            kustomization["resources"] += ["storage-class.json", "rds-ca.json"]
        if stage == "applications":
            for service in SERVICES:
                value = certificate_patch()
                value["apiVersion"] = "apps/v1"
                value["kind"] = "Deployment"
                value["metadata"] = {"name": f"{service}-service"}
                value["spec"]["template"]["spec"]["containers"][0]["name"] = f"{service}-service"
                if service == "user" and ses_sender:
                    value["spec"]["template"]["spec"]["containers"][0]["envFrom"] = [
                        {"configMapRef": {"name": "foc-config"}},
                        {"secretRef": {"name": "user-db"}},
                    ]
                patches.append({"patch": json.dumps(value)})
        if stage == "initialization":
            for service in SERVICES:
                value = certificate_patch()
                value["apiVersion"] = "batch/v1"
                value["kind"] = "Job"
                value["metadata"] = {"name": f"{service}-migrate"}
                value["spec"]["template"]["spec"]["containers"][0]["name"] = f"{service}-migrate"
                patches.append({"patch": json.dumps(value)})
        if patches:
            kustomization["patches"] = patches
        save_kustomization(directory, kustomization)
    save_kustomization(destination, {
        "apiVersion": "kustomize.config.k8s.io/v1beta1", "kind": "Kustomization", "resources": list(stages),
    })
    print(f"Rendered five deployment stages in {destination}")
    print(f"K8S_IMAGE_PREFIX={config['image_prefix']}")
    print("Build for linux/amd64; choose a new immutable K8S_IMAGE_TAG for each image build.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--outputs", type=Path, required=True, help="terraform output -json deployment")
    parser.add_argument("--rds-ca", type=Path, required=True, help="AWS RDS global CA PEM bundle")
    parser.add_argument("--output", type=Path, default=ROOT / ".local/aws/production")
    args = parser.parse_args()
    render(json.loads(args.outputs.read_text()), args.output, args.rds_ca.read_text())


if __name__ == "__main__":
    main()
