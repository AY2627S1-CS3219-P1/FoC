#!/usr/bin/env python3
"""Prepare private database Secrets and an explicit RDS provisioning Job, without applying them."""

import argparse
import json
import os
from pathlib import Path
import secrets
from urllib.parse import quote, urlencode

from render_overlay import ROOT, SERVICES


def prepare(config, admin, directory):
    directory = directory.resolve()
    if not directory.is_relative_to((ROOT / ".local").resolve()):
        raise ValueError("Store database artifacts only in this checkout's ignored .local directory")
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(directory, 0o700)
    host = config["database_host"]
    if admin.get("host", host) != host:
        raise ValueError("Administrator secret belongs to a different database host")
    objects = []
    statements = ["\\set ON_ERROR_STOP on"]
    membership_cleanup = []
    for service in SERVICES:
        path = directory / f"{service}.json"
        # Preserve credentials on retries. Never regenerate passwords while retaining RDS data.
        if path.exists():
            value = json.loads(path.read_text())
            if value["DB_HOST"] != host:
                raise ValueError(f"Retained {service} credentials belong to a different RDS host")
        else:
            username = f"foc_{service}"
            database = f"{service}_db"
            password = secrets.token_urlsafe(32)
            query = urlencode({"sslmode": "verify-full", "sslrootcert": "/var/run/foc/rds/global-bundle.pem"})
            value = {
                "DB_HOST": host, "DB_USER": username, "DB_PASSWORD": password, "DB_NAME": database,
                "DATABASE_URL": f"postgresql://{quote(username, safe='')}:{quote(password, safe='')}@{host}:5432/{database}?{query}",
            }
            path.write_text(json.dumps(value, indent=2) + "\n")
        os.chmod(path, 0o600)
        objects.append({
            "apiVersion": "v1", "kind": "Secret", "type": "Opaque",
            "metadata": {"name": f"{service}-db", "namespace": "foc"}, "stringData": value,
        })
        # psql variables quote identifiers and literals independently, including retained passwords.
        for key in ("DB_USER", "DB_PASSWORD", "DB_NAME"):
            escaped = value[key].replace("'", "''").replace("\\", "\\\\")
            statements.append(f"\\set {key.lower()} '{escaped}'")
        statements += [
            "SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'db_user', :'db_password') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'db_user') \\gexec",
            # PostgreSQL 16+ CREATEROLE does not imply SET ROLE for new roles.
            "SELECT format('GRANT %I TO %I WITH INHERIT TRUE, SET TRUE', :'db_user', current_user) \\gexec",
            "SELECT format('CREATE DATABASE %I OWNER %I', :'db_name', :'db_user') WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db_name') \\gexec",
            "SELECT pg_get_userbyid(datdba) = :'db_user' AS owner_matches FROM pg_database WHERE datname = :'db_name' \\gset",
            "\\if :owner_matches",
            "\\else",
            # psql 17 ignores an exit-code argument to \\quit. A server error
            # makes ON_ERROR_STOP return nonzero for both stdin and file input.
            "DO $$ BEGIN RAISE EXCEPTION 'Existing database owner does not match the expected service role. Aborting.'; END $$;",
            "\\endif",
            "SELECT format('REVOKE CONNECT ON DATABASE %I FROM PUBLIC', :'db_name') \\gexec",
            "SELECT format('GRANT CONNECT ON DATABASE %I TO %I', :'db_name', :'db_user') \\gexec",
        ]
        username_literal = value["DB_USER"].replace("'", "''")
        # Keep ADMIN for repeat provisioning but remove temporary use of service
        # privileges. Revoking the whole membership would also remove ADMIN.
        membership_cleanup.append(
            f"SELECT format('GRANT %I TO %I WITH INHERIT FALSE, SET FALSE', '{username_literal}', current_user) \\gexec"
        )
    statements += [
        "\\connect supplier_db",
        "CREATE EXTENSION IF NOT EXISTS postgis;",
        "CREATE EXTENSION IF NOT EXISTS pg_trgm;",
        "\\connect postgres",
    ] + membership_cleanup
    objects.append({
        "apiVersion": "v1", "kind": "Secret", "type": "Opaque",
        "metadata": {"name": "rds-provision-admin", "namespace": "foc"},
        "stringData": {
            "PGHOST": host, "PGPORT": "5432", "PGDATABASE": "postgres",
            "PGUSER": admin["username"], "PGPASSWORD": admin["password"],
            "PGSSLMODE": "verify-full", "PGSSLROOTCERT": "/var/run/foc/rds/global-bundle.pem",
            "setup.sql": "\n".join(statements) + "\n",
        },
    })
    objects.append({
        "apiVersion": "v1", "kind": "ServiceAccount",
        "metadata": {"name": "rds-provision", "namespace": "foc"},
        "automountServiceAccountToken": False,
    })
    manifest = directory / "secrets.json"
    manifest.write_text(json.dumps({"apiVersion": "v1", "kind": "List", "items": objects}, indent=2) + "\n")
    os.chmod(manifest, 0o600)
    job = {
        "apiVersion": "batch/v1", "kind": "Job",
        "metadata": {"name": "rds-provision", "namespace": "foc"},
        "spec": {"backoffLimit": 0, "activeDeadlineSeconds": 300, "template": {
            "metadata": {"labels": {"app": "rds-provision"}, "annotations": {"sidecar.istio.io/nativeSidecar": "true"}},
            "spec": {
                "serviceAccountName": "rds-provision", "restartPolicy": "Never",
                "containers": [{
                    "name": "provision", "image": "postgres:18.1",
                    "command": ["psql", "-X", "--set", "ON_ERROR_STOP=1", "-f", "/setup/setup.sql"],
                    "env": [
                        {"name": key, "valueFrom": {"secretKeyRef": {"name": "rds-provision-admin", "key": key}}}
                        for key in ("PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSSLMODE", "PGSSLROOTCERT")
                    ],
                    "securityContext": {"runAsNonRoot": True, "runAsUser": 65532, "allowPrivilegeEscalation": False, "capabilities": {"drop": ["ALL"]}},
                    "volumeMounts": [
                        {"name": "setup", "mountPath": "/setup", "readOnly": True},
                        {"name": "rds-ca", "mountPath": "/var/run/foc/rds", "readOnly": True},
                    ],
                    "resources": {"requests": {"cpu": "50m", "memory": "64Mi"}, "limits": {"memory": "256Mi"}},
                }],
                "volumes": [
                    {"name": "setup", "secret": {"secretName": "rds-provision-admin", "items": [{"key": "setup.sql", "path": "setup.sql"}]}},
                    {"name": "rds-ca", "configMap": {"name": "rds-ca"}},
                ],
            },
        }},
    }
    job_path = directory / "job.json"
    job_path.write_text(json.dumps(job, indent=2) + "\n")
    os.chmod(job_path, 0o600)
    print(f"Prepared {len(SERVICES)} service database Secrets and the RDS provisioning Job in {directory}")
    print("No resources applied. Do not print or commit secrets.json or the per-service credential files.")


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--outputs", type=Path, required=True)
    parser.add_argument("--admin-secret", type=Path, required=True, help="Private AWS Secrets Manager SecretString JSON")
    parser.add_argument("--directory", type=Path, default=ROOT / ".local/aws/db")
    args = parser.parse_args()
    prepare(json.loads(args.outputs.read_text()), json.loads(args.admin_secret.read_text()), args.directory)


if __name__ == "__main__":
    main()
