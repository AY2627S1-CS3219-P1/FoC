import json
from pathlib import Path
import tempfile
import unittest

from run import Runner


class FaultRecoveryTests(unittest.TestCase):
    def test_restore_runs_after_failed_boundary_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            runner = Runner({"out": directory, "cases": [{
                "requirement": "crash-producer-ack", "apply": ["apply"], "restore": ["restore"],
                "injectionEvidence": ["boundary"], "core": ["core"], "recovery": ["recovery"],
            }]})
            commands = []

            def command(argv, **kwargs):
                commands.append(argv[0])
                return {"returncode": 0, "stdout": "" if argv[0] == "boundary" else "evidence"}

            runner.command = command
            runner.cases()
            self.assertEqual(commands, ["apply", "boundary", "restore", "recovery"])
            result = json.loads((Path(directory) / "crash-producer-ack.json").read_text())
            self.assertEqual(result["status"], "fail")
            self.assertTrue(result["details"]["restored"])

    def test_api_checks_need_successful_dataset_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            runner = Runner({"out": directory, "fixtures": "unused", "sessions": "unused",
                             "vus": 10000, "accountsPerVU": 1})
            runner.capture = lambda: True
            (Path(directory) / "dataset.json").write_text('{"status":"fail"}')
            runner.api()
            result = json.loads((Path(directory) / "api.json").read_text())
            self.assertEqual(result["status"], "blocked")


if __name__ == "__main__":
    unittest.main()
