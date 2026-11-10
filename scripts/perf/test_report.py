import json
from pathlib import Path
import tempfile
import unittest

from report import analyze_api, build_report


def point(metric, time, value, **tags):
    return {"type": "Point", "metric": metric, "data": {"time": time, "value": value, "tags": tags}}


class EvidenceTests(unittest.TestCase):
    def test_idle_vus_cannot_satisfy_concurrency_and_slow_failures_count(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "k6.json"
            records = [
                point("measurement_window_ms", "2026-01-01T00:00:00Z", 1767225600000, edge="start"),
                point("measurement_window_ms", "2026-01-01T00:00:00Z", 1767225602000, edge="end"),
                point("vus", "2026-01-01T00:00:00Z", 20000),
                point("call_boundary", "2026-01-01T00:00:00Z", 20000, phase="warmup"),
                point("call_boundary", "2026-01-01T00:00:02Z", -20000, phase="warmup"),
                point("call_boundary", "2026-01-01T00:00:00Z", 1, phase="measured"),
                point("call_boundary", "2026-01-01T00:00:00.500Z", -1, phase="measured"),
                point("call_boundary", "2026-01-01T00:00:01Z", 1, phase="measured"),
                point("call_boundary", "2026-01-01T00:00:02.500Z", -1, phase="measured"),
                point("attempts", "2026-01-01T00:00:01Z", 1, phase="measured", category="read"),
                point("full_call_ms", "2026-01-01T00:00:02.500Z", 1500, phase="measured"),
                point("attempt_failed", "2026-01-01T00:00:02.500Z", 1, phase="measured", name="RPC", cause="timeout"),
                point("successful_within_500ms", "2026-01-01T00:00:02.500Z", 0, phase="measured"),
            ]
            path.write_text("\n".join(map(json.dumps, records)))
            evidence = analyze_api(path)
            self.assertEqual(evidence["status"], "fail")
            details = evidence["details"]
            self.assertEqual(details["concurrencyPerSecond"], [.5, 1])
            self.assertEqual(details["failureRate"], 1)
            self.assertTrue(details["checks"]["completeCallsAndDrain"])
            self.assertFalse(details["checks"]["concurrencyEverySecond"])

    def test_missing_evidence_is_blocked(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence = build_report(Path(directory))
            self.assertEqual(evidence["status"], "incomplete")
            self.assertTrue(all(record["status"] == "blocked" for record in evidence["requirements"]))
            self.assertTrue((Path(directory) / "report.md").exists())

    def test_forced_interruption_fails_even_with_fast_successes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "k6.json"
            records = [
                point("measurement_window_ms", "2026-01-01T00:00:00Z", 1767225600000, edge="start"),
                point("measurement_window_ms", "2026-01-01T00:00:00Z", 1767226200000, edge="end"),
                point("attempts", "2026-01-01T00:00:01Z", 1, phase="measured", category="read"),
                point("call_boundary", "2026-01-01T00:00:01Z", 1),
            ]
            path.write_text("\n".join(map(json.dumps, records)))
            details = analyze_api(path)["details"]
            self.assertFalse(details["checks"]["completeCallsAndDrain"])
            self.assertEqual(details["successfulWithin500msRate"], 0)


if __name__ == "__main__":
    unittest.main()
