#!/usr/bin/env python3
"""Check published CLI projections, review reports and their Git source bindings.

This verifies the inspectable records, not provider authenticity of private exports.
It never invokes a reviewer, performs network access or changes source files.
"""

from collections import Counter
from datetime import datetime
from hashlib import sha256
from io import BytesIO
import json
from pathlib import Path, PurePosixPath
import re
import subprocess


PRIVATE_CONTENT = re.compile(
    r"file://|/(?:Users|home|private)/|/var/folders/|\.local/|"
    r"-----BEGIN [^\n]*PRIVATE KEY-----|(?:AKIA|ASIA)[A-Z0-9]{16}|"
    r"gh[pousr]_[A-Za-z0-9]{20,}|Bearer [A-Za-z0-9._-]{20,}"
)


def require(condition, message):
    if not condition:
        raise ValueError(message)


def public_artifact(directory, name):
    require(isinstance(name, str) and Path(name).name == name, "Invalid evidence filename")
    path = directory / name
    content = path.read_bytes()
    require(not PRIVATE_CONTENT.search(content.decode()), f"Private content in {name}")
    return content


def verify_sources(repository, commit, expected_hashes):
    require(re.fullmatch(r"[0-9a-f]{40}", commit) is not None, "Invalid source commit")
    requests = []
    for name, expected in expected_hashes.items():
        path = PurePosixPath(name)
        require(not path.is_absolute() and ".." not in path.parts, "Invalid source path")
        require(not any(char in name for char in "\n\r\0"), "Invalid source filename")
        require(not PRIVATE_CONTENT.search(name), "Private source path")
        require(re.fullmatch(r"[0-9a-f]{64}", expected) is not None, "Invalid source digest")
        requests.append(f"{commit}:{name}\n")

    output = subprocess.check_output(
        ["git", "cat-file", "--batch"], input="".join(requests).encode(), cwd=repository
    )
    stream = BytesIO(output)
    for name, expected in expected_hashes.items():
        header = stream.readline().split()
        require(len(header) == 3 and header[1] == b"blob", f"Missing Git source: {name}")
        content = stream.read(int(header[2]))
        require(stream.read(1) == b"\n", "Invalid Git batch framing")
        require(sha256(content).hexdigest() == expected, f"Reviewed source mismatch: {name}")
    require(stream.read() == b"", "Unexpected Git batch output")


def verify(repository, directory):
    provenance = json.loads(public_artifact(directory, "delivery-proof-review-provenance.json"))
    combined_sources = {}
    generation_count = 0
    scopes = set()

    for summary in provenance["native_cli_reviews"]:
        records = public_artifact(directory, summary["generation_records_path"])
        require(sha256(records).hexdigest() == summary["generation_records_sha256"], "Record digest mismatch")
        projection = json.loads(records)
        require(set(projection) == {"format", "scope", "source_export_sha256", "disclosure", "generation_records", "final_report_step_id", "final_report", "source_binding"}, "Unexpected projection fields")
        require(projection["format"] == "sanitized-native-cli-review-v1", "Unknown evidence format")
        require(projection["scope"] == summary["scope"], "Review scope mismatch")
        require(projection["scope"] not in scopes, "Duplicate review scope")
        scopes.add(projection["scope"])
        require(projection["source_export_sha256"] == summary["export_sha256"], "Original export identifier mismatch")

        rows = projection["generation_records"]
        require(bool(rows), "Empty generation records")
        ids = []
        times = []
        models = []
        for row in rows:
            require(set(row) == {"step_id", "timestamp", "source", "extra"}, "Unexpected generation fields")
            require(type(row["step_id"]) is int, "Invalid generation step")
            require(row["source"] == "agent", "Not an actual agent generation")
            require(set(row["extra"]) == {"generation_model"}, "Unexpected generation metadata")
            require(row["extra"]["generation_model"] == "claude-opus-5-5-medium", "Non-Opus generation")
            timestamp = datetime.fromisoformat(row["timestamp"])
            require(timestamp.tzinfo is not None, "Generation timestamp lacks timezone")
            ids.append(row["step_id"])
            times.append(timestamp)
            models.append(row["extra"]["generation_model"])
        require(ids == sorted(set(ids)), "Duplicate or unordered generation steps")
        require(times == sorted(times), "Unordered generation timestamps")
        require(dict(Counter(models)) == summary["actual_generation_models"], "Generation count mismatch")
        require(projection["final_report_step_id"] == ids[-1], "Final report not bound to final generation")

        report = projection["final_report"]
        require(report["path"] == summary["final_report_path"], "Final review path mismatch")
        text = public_artifact(directory, report["path"])
        require(sha256(text).hexdigest() == report["sha256"] == summary["final_report_sha256"], "Review digest mismatch")
        require(b"Verdict" in text and b"F1" in text and b"F2" in text, "Missing substantive final review")

        binding = projection["source_binding"]
        verify_sources(repository, binding["materialized_commit"], binding["source_sha256"])
        combined_sources.update(binding["source_sha256"])
        generation_count += len(rows)
        print(f"{summary['scope']}: {len(rows)} actual Opus generation records; final review digest and {len(binding['source_sha256'])} source bindings match")

    require(scopes == {"full", "focused_fixes"}, "Missing required review scope")
    require(combined_sources == provenance["source_sha256"], "Combined review coverage mismatch")
    verify_sources(repository, provenance["reviewed_commit"], combined_sources)
    runtime = {name: value for name, value in combined_sources.items() if not name.startswith("docs/") and name != "glossary.md"}
    require(runtime == provenance["runtime_source_sha256"], "Runtime scope mismatch")
    for name, expected in runtime.items():
        require(sha256((repository / name).read_bytes()).hexdigest() == expected, f"Current runtime differs: {name}")
    print(f"PASS: {generation_count} actual model records; {len(combined_sources)} reviewed sources; {len(runtime)} current runtime files unchanged")
    print("Scope: sanitized export projections, not provider-signed attestations; private originals and input prompts are intentionally excluded")


if __name__ == "__main__":
    root = Path(__file__).resolve().parents[2]
    verify(root, root / "docs/explanations")
