"""Optional real PostgreSQL 16+ role-contract check, using a disposable local fixture.

Source: PostgreSQL 16+ requires SET ROLE permission when assigning database
ownership. An RDS administrator is not a PostgreSQL superuser. No AWS calls.
PostGIS installation is deliberately excluded and still needs RDS validation.
"""

import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from prepare_database import prepare
from render_overlay import ROOT


@unittest.skipUnless(os.environ.get("AWS_SQL_TEST_SOCKET"), "optional disposable PostgreSQL fixture not configured")
class PostgreSQLRoleContractTest(unittest.TestCase):
    def sql(self, query, user="fixture_owner"):
        return subprocess.run([
            os.environ.get("PSQL", "psql"), "-X", "-h", os.environ["AWS_SQL_TEST_SOCKET"],
            "-p", os.environ.get("AWS_SQL_TEST_PORT", "55439"), "-U", user,
            "-d", "postgres", "--set", "ON_ERROR_STOP=1", "-At",
        ], input=query, text=True, capture_output=True)

    def test_non_superuser_provisioning_retry_and_wrong_owner_rejection(self):
        result = self.sql("CREATE ROLE foc_admin LOGIN CREATEROLE CREATEDB;")
        self.assertEqual(result.returncode, 0, result.stderr)
        private = ROOT / ".local/aws"
        private.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="sql-contract-", dir=private) as directory:
            with contextlib.redirect_stdout(io.StringIO()):
                prepare({"database_host": "offline.database.invalid"}, {
                    "username": "foc_admin", "password": "offline-test-only",
                }, Path(directory))
            objects = json.loads((Path(directory) / "secrets.json").read_text())["items"]
            admin = next(obj for obj in objects if obj["metadata"]["name"] == "rds-provision-admin")
            setup = admin["stringData"]["setup.sql"]
            # The local unprivileged role is not RDS's rds_superuser. This check
            # exercises database/role SQL, not privileged RDS extension setup.
            setup = "\n".join(line for line in setup.splitlines() if not line.startswith("CREATE EXTENSION"))
            for attempt in ("initial provisioning", "idempotent retry"):
                result = self.sql(setup, "foc_admin")
                self.assertEqual(result.returncode, 0, f"{attempt}: {result.stderr}")
            result = self.sql("SELECT datname || ':' || pg_get_userbyid(datdba) FROM pg_database WHERE datname IN ('user_db','supplier_db','order_db','credit_db','communication_db');")
            self.assertEqual(set(result.stdout.splitlines()), {
                "user_db:foc_user", "supplier_db:foc_supplier", "order_db:foc_order",
                "credit_db:foc_credit", "communication_db:foc_communication",
            })
            result = self.sql("SELECT count(*) FROM pg_auth_members WHERE member = 'foc_admin'::regrole AND (set_option OR inherit_option);")
            self.assertEqual(result.stdout.strip(), "0")
            result = self.sql("ALTER DATABASE order_db OWNER TO fixture_owner;")
            self.assertEqual(result.returncode, 0, result.stderr)
            result = self.sql(setup, "foc_admin")
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Existing database owner does not match", result.stderr)
            result = self.sql("ALTER DATABASE order_db OWNER TO foc_order;")
            self.assertEqual(result.returncode, 0, result.stderr)
            result = self.sql(setup, "foc_admin")
            self.assertEqual(result.returncode, 0, result.stderr)
        print("PASS: real PostgreSQL non-superuser created five owned databases, retried, rejected wrong ownership, and removed temporary role-use privileges")


if __name__ == "__main__":
    unittest.main(verbosity=2)
