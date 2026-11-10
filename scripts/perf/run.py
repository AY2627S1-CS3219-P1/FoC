#!/usr/bin/env python3
"""Configurable private AWS test runner. No provisioning or automatic resizing."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import resource
import shutil
import subprocess
import threading
import time
from urllib.parse import urlsplit

from report import analyze_api, build_report


ROOT = Path(__file__).resolve().parents[2]


def now():
    return datetime.now(timezone.utc).isoformat()


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    path.write_text(json.dumps(value, indent=2) + "\n")
    path.chmod(0o600)


def append(path, value):
    with path.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(value) + "\n")


class Runner:
    def __init__(self, config):
        self.config = config
        self.out = ROOT / config["out"]
        self.out.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.env = {**os.environ, "PERF_BASE_URL": config.get("baseUrl", "")}

    def result(self, name, status, details):
        write(self.out / f"{name}.json", {"requirement": name, "status": status, "details": details})

    def command(self, argv, timeout=60, env=None):
        if not isinstance(argv, list) or not argv or not all(isinstance(s, str) for s in argv):
            raise ValueError("commands must be nonempty argument arrays, without shell expansion")
        started = now()
        try:
            p = subprocess.run(argv, cwd=ROOT, env=env or self.env, capture_output=True, text=True, timeout=timeout, check=False)
            result = {"command": argv, "started": started, "finished": now(), "returncode": p.returncode,
                      "stdout": p.stdout, "stderr": p.stderr}
        except (OSError, subprocess.TimeoutExpired) as error:
            result = {"command": argv, "started": started, "finished": now(), "returncode": -1, "error": type(error).__name__}
        append(self.out / "commands.jsonl", result)
        return result

    def aws(self, *args):
        result = ["aws", "--region", self.config["region"], "--output", "json"]
        if self.config.get("awsProfile"):
            result += ["--profile", self.config["awsProfile"]]
        return result + list(args)

    def kubectl(self, *args):
        return ["kubectl", "--context", self.config["kubeContext"], "-n", self.config["namespace"], *args]

    def capture(self):
        config = self.config
        missing = [name for name in ("baseUrl", "region", "kubeContext", "apiGatewayId", "rdsInstanceId", "workerInstanceIds") if not config.get(name)]
        tools = {}
        for name, argv in {"go": ["go", "version"], "node": ["node", "--version"], "k6": ["k6", "version"],
                           "aws": ["aws", "--version"], "kubectl": ["kubectl", "version", "--client", "-o", "json"]}.items():
            if not shutil.which(name):
                missing.append(name)
            else:
                tools[name] = self.command(argv)
                if tools[name]["returncode"]:
                    missing.append(name)
        browser_version = self.command(["node", "-p", "require('./frontend/node_modules/playwright/package.json').version"])
        tools["playwright"] = browser_version
        if browser_version["returncode"]:
            missing.append("playwright")
        env = {"capturedAt": now(), "configuration": config, "tools": tools,
               "revision": self.command(["git", "rev-parse", "HEAD"]),
               "changes": self.command(["git", "status", "--short"]),
               "generatorHost": os.uname().nodename, "generatorCPUs": os.cpu_count(),
               "fileDescriptorLimit": resource.getrlimit(resource.RLIMIT_NOFILE), "missing": missing,
               "deployment": {}}
        if config.get("kubeContext") and shutil.which("kubectl"):
            for name, argv in {
                # Do not collect Secret or ConfigMap contents, environment values, or tokens.
                "pods": self.kubectl("get", "pods", "-o", 'jsonpath={range .items[*]}{.metadata.name}{" "}{.spec.nodeName}{" "}{.status.containerStatuses}{"\\n"}{end}'),
                "nodes": self.kubectl("get", "nodes", "-o", "json"),
                "resources": self.kubectl("get", "deployments,statefulsets", "-o", 'jsonpath={range .items[*]}{.metadata.name}{" "}{.spec.replicas}{" "}{range .spec.template.spec.containers[*]}{.name}{" "}{.image}{" "}{.resources}{"\\n"}{end}{end}'),
            }.items():
                env["deployment"][name] = self.command(argv)
        if shutil.which("aws") and config.get("region"):
            commands = {"identity": self.aws("sts", "get-caller-identity")}
            if config.get("apiGatewayId"):
                commands["gateway"] = self.aws("apigatewayv2", "get-stages", "--api-id", config["apiGatewayId"])
                commands["quotas"] = self.aws("service-quotas", "list-service-quotas", "--service-code", "apigateway")
            if config.get("rdsInstanceId"):
                commands["database"] = self.aws("rds", "describe-db-instances", "--db-instance-identifier", config["rdsInstanceId"])
            if config.get("workerInstanceIds"):
                commands["workers"] = self.aws("ec2", "describe-instances", "--instance-ids", *config["workerInstanceIds"])
                commands["creditMode"] = self.aws("ec2", "describe-instance-credit-specifications", "--instance-ids", *config["workerInstanceIds"])
            for name, argv in commands.items():
                env["deployment"][name] = self.command(argv)
        failed = [name for name, record in env["deployment"].items() if record["returncode"]]
        env["missing"] += failed
        for name in ("deployedRevision", "brokerConfiguration", "databaseParameters", "startingCpuCredits"):
            argv = config.get("environmentCommands", {}).get(name)
            if not argv:
                env["missing"].append(name)
                continue
            record = self.command(argv)
            env["deployment"][name] = record
            if record["returncode"] or not record.get("stdout", "").strip():
                env["missing"].append(name)
        self.result("environment", "blocked" if env["missing"] else "pass", env)
        return not env["missing"]

    def helper(self, command):
        config = self.config
        binary = ROOT / ".tools/bin/perf"
        binary.parent.mkdir(parents=True, exist_ok=True)
        build = self.command(["go", "-C", "scripts", "build", "-mod=readonly", "-o", str(binary), "./perf"], timeout=180,
                             env={**self.env, "GOWORK": "off"})
        if build["returncode"]:
            self.result(command, "blocked", "Go helper build failed; inspect commands.jsonl.")
            return
        argv = [str(binary), command, "--fixtures", str(ROOT / config["fixtures"]), "--out", str(self.out)]
        if command == "fixtures":
            argv = [str(binary), command, "--out", str((ROOT / config["fixtures"]).parent),
                    "--seed", str(config.get("fixtureSeed", 3219)), "--users", str(config.get("users", 20000)),
                    "--suppliers", str(config.get("suppliers", 1000)), "--open", str(config.get("openRequests", 10000)),
                    "--history", str(config.get("historicalRequests", 100000))]
            if config.get("fixtureAt"):
                argv += ["--at", config["fixtureAt"]]
        elif command == "sessions":
            argv += ["--challenges", str(ROOT / config["challenges"])]
            # Session output belongs to the configured private pool directory.
            argv += ["--out", str((ROOT / config["sessions"]).parent)]
        elif command in ("workflow", "contention"):
            argv += ["--sessions", str(ROOT / config["sessions"])]
        elif command in ("events", "replay"):
            argv += ["--corpus", str(ROOT / config["corpus"])]
            if command == "replay":
                argv += ["--count", str(config.get("replayCount", 4))]
        stopped = threading.Event()
        monitor = None
        if command in ("events", "replay", "contention"):
            monitor = threading.Thread(target=self.telemetry, args=(stopped,), daemon=True)
            monitor.start()
        try:
            result = self.command(argv, timeout=1200)
        finally:
            stopped.set()
            if monitor:
                monitor.join(timeout=60)
        if result["returncode"] and command in ("verify", "contention", "events", "replay", "workflow"):
            name = "dataset" if command == "verify" else command
            if not (self.out / f"{name}.json").exists():
                self.result(name, "fail", {"command": result, "reason": "Helper did not produce completed acceptance evidence."})

    def telemetry(self, stopped, pid=None):
        interval = float(self.config.get("telemetryIntervalSeconds", 10))
        next_cloud = 0
        while not stopped.is_set():
            sample = {"at": now(), "pid": pid}
            for name, file in {"cpu": "/proc/stat", "memory": "/proc/meminfo", "network": "/proc/net/dev", "connections": "/proc/net/sockstat"}.items():
                sample[name] = Path(file).read_text()
            if pid:
                for name, file in {"process": f"/proc/{pid}/stat", "processLimits": f"/proc/{pid}/limits"}.items():
                    try:
                        sample[name] = Path(file).read_text()
                    except FileNotFoundError:
                        pass
            append(self.out / "generator.jsonl", sample)
            if time.monotonic() >= next_cloud:
                commands = list(self.config.get("telemetryCommands", []))
                if self.config.get("kubeContext"):
                    commands += [self.kubectl("top", "pods", "--containers"), self.kubectl("top", "nodes")]
                for argv in commands:
                    append(self.out / "telemetry.jsonl", self.command(argv, timeout=15))
                # CPU credits, resource usage and API Gateway throttles use real
                # CloudWatch series; each result includes its native timestamps.
                end = datetime.now(timezone.utc)
                start = end - timedelta(minutes=5)
                metrics = []
                for instance in self.config.get("workerInstanceIds", []):
                    metrics += [("AWS/EC2", "InstanceId", instance, name) for name in ("CPUUtilization", "CPUCreditBalance", "CPUCreditUsage", "NetworkIn", "NetworkOut")]
                if self.config.get("rdsInstanceId"):
                    metrics += [("AWS/RDS", "DBInstanceIdentifier", self.config["rdsInstanceId"], name) for name in ("CPUUtilization", "CPUCreditBalance", "DatabaseConnections", "FreeableMemory", "ReadLatency", "WriteLatency")]
                if self.config.get("apiGatewayId"):
                    metrics += [("AWS/ApiGateway", "ApiId", self.config["apiGatewayId"], name) for name in ("Count", "4xx", "5xx", "Latency", "IntegrationLatency")]
                if shutil.which("aws"):
                    for namespace, dimension, value, name in metrics:
                        argv = self.aws("cloudwatch", "get-metric-statistics", "--namespace", namespace, "--metric-name", name,
                                        "--dimensions", f"Name={dimension},Value={value}", "--start-time", start.isoformat(),
                                        "--end-time", end.isoformat(), "--period", "60", "--statistics", "Average", "Minimum", "Maximum", "Sum")
                        append(self.out / "telemetry.jsonl", self.command(argv, timeout=15))
                next_cloud = time.monotonic() + interval
            stopped.wait(1)

    def api(self, pilot=False):
        if not self.capture():
            self.result("generator" if pilot else "api", "blocked", "Complete deployment configuration and install missing tools; see environment.json.")
            return
        config = self.config
        raw = self.out / ("pilot-k6.json.gz" if pilot else "k6.json.gz")
        if raw.exists():
            raise ValueError("use a fresh output directory; raw measurements must not be overwritten")
        env = {**self.env, "PERF_VUS": str(config["pilotVus"] if pilot else config["vus"]),
               "PERF_ACCOUNTS_PER_VU": str(config["accountsPerVU"]),
               "PERF_FIXTURES": str(ROOT / config["fixtures"]), "PERF_SESSIONS": str(ROOT / config["sessions"]),
               "PERF_SUMMARY": str(self.out / ("pilot-summary.json" if pilot else "k6-summary.json"))}
        if pilot:
            env.update(PERF_WARMUP_SECONDS="10", PERF_MEASURE_SECONDS=str(config["pilotSeconds"]))
        else:
            dataset = self.out / "dataset.json"
            if not dataset.exists() or json.loads(dataset.read_text()).get("status") != "pass":
                self.result("api", "blocked", "Run perf verify successfully into this output directory before the measured run.")
                return
        argv = ["k6", "run", "--out", f"json={raw}", str(ROOT / "scripts/perf/api.js")]
        with (self.out / "k6.log").open("w") as log:
            started = now()
            p = subprocess.Popen(argv, cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT)
            stopped = threading.Event()
            monitor = threading.Thread(target=self.telemetry, args=(stopped, p.pid), daemon=True)
            monitor.start()
            try:
                returncode = p.wait()
            finally:
                stopped.set()
                monitor.join(timeout=60)
            append(self.out / "commands.jsonl", {"command": argv, "started": started, "finished": now(), "returncode": returncode})
        result = analyze_api(raw)
        if pilot:
            self.result("generator", "blocked", {"pilot": result, "reason": "Review generator.jsonl for CPU, memory, network, FD and connection headroom before calibrating workers; pilot does not establish 10,000 concurrent calls."})
        else:
            if returncode:
                result["status"] = "fail"
                result["details"]["k6ExitCode"] = returncode
            write(self.out / "api.json", result)

    def cases(self):
        for case in self.config.get("cases", []):
            name = case["requirement"]
            if not (name.startswith("outage-") or name.startswith("crash-") or name == "overdue"):
                raise ValueError(f"unknown fault requirement {name}")
            required = ("apply", "restore", "injectionEvidence", "core", "recovery")
            if not all(case.get(key) for key in required):
                self.result(name, "blocked", "Configure apply, restore, injectionEvidence, core, and recovery commands.")
                continue
            records = []
            restored = False
            fault_error = None
            try:
                records.append(self.command(case["apply"]))
                if records[-1]["returncode"]:
                    raise RuntimeError("fault injection command failed")
                records.append(self.command(case["injectionEvidence"]))
                try:
                    boundary = json.loads(records[-1].get("stdout", ""))
                except json.JSONDecodeError:
                    boundary = {}
                if records[-1]["returncode"] or boundary.get("status") != "pass" or not boundary.get("checks"):
                    raise RuntimeError("failure boundary was not evidenced")
                records.append(self.command(case["core"], timeout=180))
                if case.get("browser"):
                    records.append(self.command(case["browser"], timeout=180))
            except RuntimeError as error:
                fault_error = str(error)
            finally:
                recovery = self.command(case["restore"])
                records.append(recovery)
                restored = recovery["returncode"] == 0
                self.result(name, "fail", {"commands": records, "restored": restored})
            if restored:
                records.append(self.command(case["recovery"], timeout=180))
                if case.get("browserRecovery"):
                    records.append(self.command(case["browserRecovery"], timeout=180))
            status = "pass" if all(record["returncode"] == 0 for record in records) else "fail"
            if fault_error:
                status = "fail"
            if name.startswith("outage-") and not (case.get("browser") and case.get("browserRecovery")):
                status = "blocked" if status == "pass" else status
            if name == "overdue":
                telemetry = case.get("operationalTelemetry", {})
                if not isinstance(telemetry, dict) or not telemetry.get("command") or not telemetry.get("source"):
                    status = "blocked" if status == "pass" else status
                else:
                    native = self.command(telemetry["command"])
                    records.append(native)
                    try:
                        alerts = json.loads(native.get("stdout", ""))["alerts"]
                        valid = native["returncode"] == 0 and bool(alerts) and all(
                            alert.get("eventId") and alert.get("topic") and alert.get("consumer")
                            and alert.get("ageSeconds", 0) > 60 and alert.get("failureOrBacklog") for alert in alerts)
                    except (json.JSONDecodeError, KeyError, TypeError):
                        valid = False
                    if not valid:
                        status = "fail"
            self.result(name, status, {"commands": records, "restored": restored,
                                      "error": fault_error,
                                      "operationalTelemetry": case.get("operationalTelemetry")})
            if not restored:
                raise RuntimeError("dependency restoration failed; stopping subsequent tests")


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("command", choices=["preflight", "pilot", "api", "cases", "report", "fixtures", "seed", "verify", "sessions", "workflow", "contention", "events", "replay"])
    args = parser.parse_args()
    config = json.loads(args.config.read_text())
    if config.get("baseUrl"):
        target = urlsplit(config["baseUrl"])
        if target.scheme != "https" or not target.hostname or target.username or target.query or target.fragment or target.path not in ("", "/"):
            raise ValueError("baseUrl must be the dedicated deployment's HTTPS origin")
    runner = Runner(config)
    if args.command == "preflight":
        runner.capture()
    elif args.command == "pilot":
        runner.api(pilot=True)
    elif args.command == "api":
        runner.api()
    elif args.command == "cases":
        runner.cases()
    elif args.command != "report":
        runner.helper(args.command)
    build_report(runner.out)


if __name__ == "__main__":
    main()
