#!/usr/bin/env python3
"""Evaluate raw k6 calls and aggregate evidence without inventing live results."""
import argparse
from collections import Counter, defaultdict
from datetime import datetime
import gzip
import json
import math
import os
from pathlib import Path


REQUIREMENTS = [
    "environment", "generator", "dataset", "api", "contention", "events", "replay",
    *[f"outage-{feature}-{mode}" for feature in ("map", "chat", "ai", "notifications")
      for mode in ("connection_failure", "server_error", "timeout")],
    "outage-combined", "crash-producer-ack", "crash-consumer-before-commit",
    "crash-consumer-after-commit", "crash-notification-send", "overdue",
]


def points(path, only=None):
    opener = gzip.open if str(path).endswith(".gz") else open
    with opener(path, "rt", encoding="utf-8") as stream:
        for line in stream:
            if only and f'"{only}"' not in line:
                continue
            point = json.loads(line)
            if point.get("type") == "Point":
                yield point


def epoch(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def percentile(values, quantile):
    if not values:
        return None
    position = (len(values) - 1) * quantile
    lower = int(position)
    upper = min(lower + 1, len(values) - 1)
    return values[lower] + (values[upper] - values[lower]) * (position - lower)


def analyze_api(path):
    edges = {}
    for point in points(path, only="measurement_window_ms"):
        if point["metric"] == "measurement_window_ms":
            edges[point["data"]["tags"]["edge"]] = point["data"]["value"] / 1000
    if not {"start", "end"}.issubset(edges) or edges["end"] <= edges["start"]:
        raise ValueError("raw output has no complete measurement window")
    start, end = edges["start"], edges["end"]
    seconds = math.ceil(end - start)
    deltas, weighted = defaultdict(int), defaultdict(float)
    before = 0
    balance = 0
    latencies, http_latencies = [], []
    categories, endpoint_errors = Counter(), Counter()
    attempted = finished = failures = timely = 0
    for point in points(path):
        metric, data = point["metric"], point["data"]
        value, tags = data["value"], data.get("tags", {})
        if metric == "call_boundary":
            timestamp = epoch(data["time"])
            balance += value
            if tags.get("phase") != "measured":
                continue
            if timestamp < start:
                before += value
            elif timestamp < end:
                relative = timestamp - start
                bucket = int(relative)
                deltas[bucket] += value
                weighted[bucket] += value * (1 - (relative - bucket))
        if tags.get("phase") != "measured":
            continue
        if metric == "attempts":
            attempted += value
            categories[tags.get("category", "missing")] += value
        elif metric == "full_call_ms":
            latencies.append(value)
        elif metric == "http_req_duration":
            http_latencies.append(value)
        elif metric == "attempt_failed":
            finished += 1
            failures += int(bool(value))
            if value:
                endpoint_errors[(tags.get("name"), tags.get("cause"))] += 1
        elif metric == "successful_within_500ms":
            timely += int(bool(value))
    concurrency = []
    active = before
    for second in range(seconds):
        # Integral of a step function: active at interval start plus each
        # signed boundary weighted by the remaining fraction of that second.
        concurrency.append(active + weighted[second])
        active += deltas[second]
    shares = {key: count / attempted if attempted else 0 for key, count in categories.items()}
    latencies.sort()
    http_latencies.sort()
    complete = attempted > 0 and attempted == finished == len(latencies) and balance == 0
    error_rate = failures / attempted if attempted else 1
    timely_rate = timely / attempted if attempted else 0
    p95 = percentile(latencies, .95)
    checks = {
        "completeCallsAndDrain": complete,
        "twoMinuteWarmup": "warmupStart" in edges and start - edges["warmupStart"] >= 120,
        "tenMinuteWindow": end - start >= 600 and edges.get("finished", 0) >= end,
        "drainWithin30Seconds": end <= edges.get("finished", 0) <= end + 30,
        "concurrencyEverySecond": len(concurrency) >= 600 and min(concurrency, default=0) >= 10000,
        "successfulWithin500ms": timely_rate >= .95,
        "p95AtMost500ms": p95 is not None and p95 <= 500,
        "failedBelowOnePercent": error_rate < .01,
        "workloadMix": attempted > 0 and all(categories.get(k, 0) * 10 == attempted * weight
                           for k, weight in {"read": 7, "auth": 2, "mutation": 1}.items()),
    }
    return {
        "requirement": "api", "status": "pass" if all(checks.values()) else "fail",
        "details": {
            "checks": checks, "measurementStart": start, "measurementEnd": end,
            "durationSeconds": end - start, "attempts": attempted, "finished": finished,
            "scenarioFinishedAt": edges.get("finished"),
            "requestsPerSecond": attempted / (end - start), "failureRate": error_rate,
            "successfulWithin500msRate": timely_rate, "categoryCounts": dict(categories),
            "categoryShares": shares, "mixTolerancePercentagePoints": 0,
            "latencyMs": {"p50": percentile(latencies, .5), "p90": percentile(latencies, .9),
                          "p95": p95, "p99": percentile(latencies, .99),
                          "max": max(latencies, default=None)},
            "httpDurationMs": {"p95": percentile(http_latencies, .95)},
            "concurrencyPerSecond": concurrency, "minimumConcurrency": min(concurrency, default=0),
            "concurrencyDefinition": "time-weighted mean actual outstanding measured calls in each one-second measurement interval; warmup calls excluded",
            "callBoundaryBalanceAfterDrain": balance,
            "errors": [{"endpoint": key[0], "cause": key[1], "count": count}
                       for key, count in sorted(endpoint_errors.items())],
        },
    }


def build_report(directory):
    evidence = {}
    for name in REQUIREMENTS:
        file = directory / f"{name}.json"
        if file.exists():
            record = json.loads(file.read_text())
            if record.get("requirement") != name or record.get("status") not in {"pass", "fail", "blocked"}:
                raise ValueError(f"invalid requirement evidence: {file}")
            evidence[name] = record
        else:
            evidence[name] = {"requirement": name, "status": "blocked", "details": "No completed evidence for this run."}
    report = {"status": "pass" if all(item["status"] == "pass" for item in evidence.values()) else "incomplete",
              "requirements": list(evidence.values())}
    (directory / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    lines = ["# AWS performance and resilience results", "",
             "A pass requires recorded evidence. Missing deployment configuration, features, instrumentation, or tools remain blocked.", "",
             "| Requirement | Result | Evidence |", "| --- | --- | --- |"]
    for name, item in evidence.items():
        link = f"[{name}.json]({name}.json)" if (directory / f"{name}.json").exists() else "Missing"
        lines.append(f"| {name} | {item['status']} | {link} |")
    if "api" in evidence and isinstance(evidence["api"]["details"], dict):
        d = evidence["api"]["details"]
        lines += ["", "Measured API calls include supporting authentication and drain completions.", "",
                  f"Attempted: {d['attempts']}; failure rate: {d['failureRate']:.4%}; latency (ms): `{json.dumps(d['latencyMs'])}`.",
                  f"Actual category proportions: `{json.dumps(d['categoryShares'])}`.",
                  f"Minimum one-second mean concurrency: {d['minimumConcurrency']:.2f}. Full per-second values are in api.json."]
    lines += ["", "Inspect environment.json, commands.jsonl, generator.jsonl, telemetry.jsonl, event receipts, reconciliation results, and browser screenshots alongside this report.", ""]
    (directory / "report.md").write_text("\n".join(lines))
    return report


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--k6", type=Path)
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    if args.k6:
        (args.out / "api.json").write_text(json.dumps(analyze_api(args.k6), indent=2) + "\n")
    build_report(args.out)


if __name__ == "__main__":
    main()
