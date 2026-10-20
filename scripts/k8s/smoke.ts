import { connect } from "node:net";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
	K8S_APPS,
	K8S_JOBS,
	K8S_STATE_DIR,
	ForwardSpec,
	checkContext,
	die,
	k,
	kubectlBinary,
	kubectlPrefix,
	localOrigin,
	note,
	parsePort,
	prepareState,
	preflightForwards,
	requireCommands,
	run,
	waitJob,
} from "./common";

function pass(message: string): void {
	note("PASS " + message);
}

function fail(message: string): never {
	die("FAIL " + message);
}

function waitForLocalListener(port: number): Promise<boolean> {
	return new Promise((resolvePromise) => {
		const socket = connect({ host: "127.0.0.1", port });
		const timer = setTimeout(() => {
			socket.destroy();
			resolvePromise(false);
		}, 250);
		socket.once("connect", () => {
			clearTimeout(timer);
			socket.destroy();
			resolvePromise(true);
		});
		socket.once("error", () => {
			clearTimeout(timer);
			resolvePromise(false);
		});
	});
}

export async function startTestForward(forward: ForwardSpec): Promise<Bun.Subprocess> {
	const child = Bun.spawn([
		kubectlBinary(), ...kubectlPrefix(),
		"-n", forward.namespace,
		"port-forward", "--address", "127.0.0.1",
		"service/" + forward.service,
		forward.localPort + ":" + forward.remotePort,
	], { stdout: "pipe", stderr: "pipe", stdin: "ignore" });
	const stdout = new Response(child.stdout).text();
	const stderr = new Response(child.stderr).text();
	const exited = child.exited.then((code) => ({ code }));
	const deadline = Date.now() + 30_000;
	while (Date.now() < deadline) {
		const outcome = await Promise.race([
			waitForLocalListener(forward.localPort).then((ready) => ({ ready })),
			exited,
		]);
		if ("code" in outcome) {
			const [out, err] = await Promise.all([stdout, stderr]);
			fail("Port forward " + forward.localPort + " failed: " + (err || out).trim());
		}
		if (outcome.ready) {
			pass("Temporary port-forward ready on 127.0.0.1:" + forward.localPort);
			return child;
		}
		await Bun.sleep(100);
	}
	child.kill("SIGTERM");
	fail("Port forward " + forward.localPort + " did not start");
}

export async function stopForward(child: Bun.Subprocess): Promise<void> {
	if (child.exitCode === null) child.kill("SIGTERM");
	await Promise.race([child.exited, Bun.sleep(3000)]);
	if (child.exitCode === null) child.kill("SIGKILL");
}

async function makeClient(namespace: string, name: string, account: string, injected: boolean, evidenceDir: string): Promise<void> {
	const image = process.env.K8S_SMOKE_IMAGE || "curlimages/curl:8.12.1";
	if (!/^[a-zA-Z0-9._:/@-]+$/.test(image)) die("K8S_SMOKE_IMAGE contains unsupported characters.");
	const manifest = {
		apiVersion: "v1",
		kind: "Pod",
		metadata: {
			name,
			namespace,
			labels: { app: "foc-smoke", "sidecar.istio.io/inject": String(injected) },
			annotations: { "sidecar.istio.io/nativeSidecar": "true" },
		},
		spec: {
			serviceAccountName: account,
			restartPolicy: "Never",
			activeDeadlineSeconds: 600,
			containers: [{
				name: "client",
				image,
				command: ["sleep", "600"],
				securityContext: {
					runAsNonRoot: true,
					runAsUser: 65532,
					allowPrivilegeEscalation: false,
					capabilities: { drop: ["ALL"] },
				},
				resources: {
					requests: { cpu: "10m", memory: "16Mi" },
					limits: { memory: "64Mi" },
				},
			}],
		},
	};
	const path = join(evidenceDir, name + ".json");
	await writeFile(path, JSON.stringify(manifest, null, 2), { mode: 0o600 });
	await k(["apply", "-f", path]);
	await k(["-n", namespace, "wait", "--for=condition=Ready", "pod/" + name, "--timeout=" + (process.env.K8S_TIMEOUT || "600s")]);
}

async function assertRPC(
	namespace: string,
	pod: string,
	url: string,
	expected: number,
	description: string,
): Promise<void> {
	const response = await k([
		"-n", namespace, "exec", pod, "-c", "client", "--",
		"curl", "-sS", "--max-time", "15", "-w", "\n%{http_code}",
		"-H", "Content-Type: application/json",
		"-H", "Connect-Protocol-Version: 1",
		"-d", "{}", url,
	], { capture: true, allowFailure: true });
	if (response.exitCode !== 0) fail(description + ": expected HTTP " + expected + ", transport failed");
	const lines = response.stdout.trimEnd().split(/\r?\n/);
	const actual = lines.pop();
	if (actual !== String(expected)) {
		fail(description + ": expected HTTP " + expected + ", got " + actual + ": " + response.stdout);
	}
	if (expected === 200 && url.includes("GetPublicKeys") && !/ES256/.test(response.stdout)) {
		fail(description + ": missing ES256 keys");
	}
	pass(description + " (expected=" + expected + " actual=" + actual + ")");
}

async function assertDirectBlocked(namespace: string, pod: string, url: string, description: string, evidenceDir: string): Promise<void> {
	const response = await k([
		"-n", namespace, "exec", pod, "-c", "client", "--",
		"curl", "-sS", "--max-time", "10", "-o", "/dev/null", "-w", "%{http_code}",
		"-H", "Content-Type: application/json", "-d", "{}", url,
	], { capture: true, allowFailure: true });
	await writeFile(join(evidenceDir, "direct-" + namespace + "-" + pod + ".txt"), response.stderr, { mode: 0o600 });
	if (response.exitCode === 0 && response.stdout.trim() !== "403") {
		fail(description + ": expected mesh denial, got HTTP " + response.stdout.trim());
	}
	if (response.exitCode !== 0 && ![35, 52, 56].includes(response.exitCode)) {
		fail(description + ": expected TLS rejection/reset, got curl exit " + response.exitCode);
	}
	pass(description + " (HTTP=" + (response.stdout.trim() || "000") + " curl=" + response.exitCode + ")");
}

export async function runSmoke(): Promise<void> {
	await requireCommands("kubectl", "curl");
	await checkContext();
	await prepareState();
	await mkdir(K8S_STATE_DIR, { recursive: true, mode: 0o700 });
	const evidenceDir = await mkdtemp(join(K8S_STATE_DIR, "smoke."));
	const suffix = Date.now().toString(36) + "-" + Math.floor(Math.random() * 1_000_000).toString(36);
	const outsiderNamespace = "foc-untrusted-" + suffix;
	const client = "foc-smoke-" + suffix;
	let gatewayForward: Bun.Subprocess | undefined;
	let mailpitForward: Bun.Subprocess | undefined;

	try {
		for (const app of K8S_APPS) {
			await k(["-n", "foc", "rollout", "status", "deployment/" + app, "--timeout=" + (process.env.K8S_TIMEOUT || "600s")]);
		}
		for (const dependency of ["postgres", "kafka"]) {
			await k(["-n", "foc", "rollout", "status", "statefulset/" + dependency, "--timeout=" + (process.env.K8S_TIMEOUT || "600s")]);
		}
		for (const job of ["db-provision", ...K8S_JOBS]) await waitJob(job);
		const mtls = await k(["-n", "foc", "get", "peerauthentication", "strict", "-o", "jsonpath={.spec.mtls.mode}"], { capture: true });
		if (mtls.stdout.trim() !== "STRICT") fail("STRICT mTLS policy missing");
		pass("Application, dependency and initialization readiness; STRICT mTLS declared");

		await k(["create", "namespace", outsiderNamespace, "--dry-run=client", "-o", "yaml"], { capture: true }).then(async (result) => {
			const path = join(evidenceDir, outsiderNamespace + ".yaml");
			await writeFile(path, result.stdout, { mode: 0o600 });
			await k(["apply", "-f", path]);
		});
		await makeClient("foc", client, "default", true, evidenceDir);
		await makeClient("istio-ingress", client, "istio-ingressgateway", true, evidenceDir);
		await makeClient(outsiderNamespace, "meshed", "default", true, evidenceDir);
		await makeClient(outsiderNamespace, "plaintext", "default", false, evidenceDir);

		const keyPath = "/user.v1.PublicKeyService/GetPublicKeys";
		const internal = "http://user-service-internal.foc.svc.cluster.local:8080" + keyPath;
		await assertRPC("foc", client, internal, 200, "Authenticated app identity reaches internal key RPC");
		await assertRPC(
			"foc", client,
			"http://user-service-internal.foc.svc.cluster.local:8080/user.v1.HealthService/Check",
			200, "Public health uses the same internal destination URL",
		);
		await assertRPC("istio-ingress", client, internal, 403, "Gateway identity denied internal RPC");
		await assertRPC(outsiderNamespace, "meshed", internal, 403, "Foreign mesh namespace denied internal RPC");
		const userPodResult = await k(["-n", "foc", "get", "pod", "-l", "app=user-service", "-o", "jsonpath={.items[0].metadata.name}"], { capture: true });
		const userPod = userPodResult.stdout.trim();
		const userIPResult = await k(["-n", "foc", "get", "pod", userPod, "-o", "jsonpath={.status.podIP}"], { capture: true });
		const userIP = userIPResult.stdout.trim();
		await assertDirectBlocked("foc", client, "http://" + userIP + ":8081" + keyPath, "Raw pod-IP traffic cannot bypass protected listener", evidenceDir);
		await assertDirectBlocked(outsiderNamespace, "meshed", "http://" + userIP + ":8081" + keyPath, "Foreign raw pod-IP traffic denied", evidenceDir);
		const plaintext = await k([
			"-n", outsiderNamespace, "exec", "plaintext", "-c", "client", "--", "curl", "-fsS", "--max-time", "10",
			"-H", "Content-Type: application/json", "-d", "{}", internal,
		], { capture: true, allowFailure: true });
		if (plaintext.exitCode === 0) fail("Plaintext pod unexpectedly reached a protected listener");
		await writeFile(join(evidenceDir, "plaintext.txt"), plaintext.stderr, { mode: 0o600 });
		const config = await k(["-n", "foc", "exec", userPod, "-c", "istio-proxy", "--", "pilot-agent", "request", "GET", "config_dump"], { capture: true });
		await writeFile(join(evidenceDir, "user-proxy.json"), config.stdout, { mode: 0o600 });
		if (!/"require_client_certificate"\s*:\s*true|"requireClientCertificate"\s*:\s*true/.test(config.stdout)) {
			fail("Proxy config does not require client certificates");
		}
		pass("Plaintext rejected; destination proxy config requires client certificates");

		const tlsCert = process.env.K8S_TLS_CA_FILE || join(K8S_STATE_DIR, "secrets/tls.crt");
		if (!(await Bun.file(tlsCert).size)) fail("Set K8S_TLS_CA_FILE to the gateway trust certificate");
		const httpsPort = parsePort("K8S_SMOKE_HTTPS_PORT", process.env.K8S_SMOKE_HTTPS_PORT) ?? 18443;
		await preflightForwards([{ name: "smoke gateway", namespace: "istio-ingress", service: "istio-ingressgateway", localPort: httpsPort, remotePort: 443, scheme: "https" }]);
		const gatewaySpec: ForwardSpec = {
			name: "smoke gateway",
			namespace: "istio-ingress",
			service: "istio-ingressgateway",
			localPort: httpsPort,
			remotePort: 443,
			scheme: "https",
		};
		gatewayForward = await startTestForward(gatewaySpec);
		const base = "https://localhost:" + httpsPort;
		const frontend = await run("curl", ["--cacert", tlsCert, "-sS", "--max-time", "20", "-o", join(evidenceDir, "frontend.html"), "-w", "%{http_code}", base + "/"], { capture: true });
		if (frontend.stdout.trim() !== "200") fail("Frontend HTTPS expected 200, got " + frontend.stdout.trim());
		for (const namespace of ["user", "supplier", "order", "communication"]) {
			const response = await run("curl", [
				"--cacert", tlsCert, "-sS", "--max-time", "20", "-o", join(evidenceDir, namespace + "-health.json"), "-w", "%{http_code}",
				"-H", "Content-Type: application/json", "-H", "Connect-Protocol-Version: 1", "-d", "{}",
				base + "/" + namespace + ".v1.HealthService/Check" + (namespace === "user" ? "?foc-query-canary=" + suffix : ""),
			], { capture: true });
			if (response.stdout.trim() !== "200") fail(namespace + " public health expected 200, got " + response.stdout.trim());
		}
		const publicKeyResponse = await run("curl", [
			"--cacert", tlsCert, "-sS", "--max-time", "20", "-o", join(evidenceDir, "public-key-response.txt"), "-w", "%{http_code}",
			"-H", "Content-Type: application/json", "-H", "Connect-Protocol-Version: 1", "-d", "{}", base + keyPath,
		], { capture: true });
		if (publicKeyResponse.stdout.trim() !== "404") {
			fail("Internal procedure via public gateway expected 404, got " + publicKeyResponse.stdout.trim());
		}
		const publicBody = await Bun.file(join(evidenceDir, "public-key-response.txt")).text();
		if (/<html/i.test(publicBody)) fail("Internal RPC returned frontend HTML");
		pass("Trusted HTTPS frontend and public health; internal procedure absent publicly (expected=404 actual=404)");

		await Bun.sleep(1000);
		const gatewayLogs = (await k(["-n", "istio-ingress", "logs", "deployment/istio-ingressgateway", "-c", "istio-proxy", "--since=2m"], { capture: true })).stdout;
		if (!gatewayLogs.includes("/user.v1.HealthService/Check") || gatewayLogs.includes("foc-query-canary=" + suffix)) {
			fail("Gateway access logs must record paths without credential-bearing query strings");
		}
		pass("Gateway access logs exclude request query credentials");

		if (process.env.K8S_BOOTSTRAP_SUPERADMIN_EMAIL) {
			const mailPort = parsePort("K8S_SMOKE_MAILPIT_PORT", process.env.K8S_SMOKE_MAILPIT_PORT) ?? 19025;
			await preflightForwards([{ name: "smoke Mailpit", namespace: "foc", service: "mailpit", localPort: mailPort, remotePort: 8025, scheme: "http" }]);
			mailpitForward = await startTestForward({
				name: "smoke Mailpit",
				namespace: "foc",
				service: "mailpit",
				localPort: mailPort,
				remotePort: 8025,
				scheme: "http",
			});
			await run(process.execPath, [join(import.meta.dir, "auth-smoke.mjs")], {
				env: {
					SMOKE_TLS_CA_FILE: tlsCert,
					SMOKE_SHARED_GATEWAY: "1",
					SMOKE_USER_ORIGIN: base,
					SMOKE_ORIGIN: localOrigin(),
					SMOKE_MAILPIT_URL: "http://127.0.0.1:" + mailPort,
				},
			});
		} else {
			note("SKIP browser auth: set K8S_BOOTSTRAP_SUPERADMIN_EMAIL during bootstrap and smoke to test magic-link/login/refresh/logout.");
		}
		pass("Infrastructure smoke complete; evidence " + evidenceDir + ". Credit/business workflow remains unverified.");
	} finally {
		if (mailpitForward) await stopForward(mailpitForward);
		if (gatewayForward) await stopForward(gatewayForward);
		for (const [namespace, name] of [["foc", client], ["istio-ingress", client]] as const) {
			await k(["-n", namespace, "delete", "pod", name, "--ignore-not-found", "--wait=false"], { capture: true, allowFailure: true });
		}
		for (const [namespace, name] of [[outsiderNamespace, "meshed"], [outsiderNamespace, "plaintext"]] as const) {
			await k(["-n", namespace, "delete", "pod", name, "--ignore-not-found", "--wait=false"], { capture: true, allowFailure: true });
		}
		await k(["delete", "namespace", outsiderNamespace, "--ignore-not-found", "--wait=false"], { capture: true, allowFailure: true });
	}
}

if (import.meta.main) {
	try {
		await runSmoke();
	} catch (error) {
		console.error("ERROR: " + (error instanceof Error ? error.message : "unexpected error"));
		process.exitCode = 1;
	}
}
