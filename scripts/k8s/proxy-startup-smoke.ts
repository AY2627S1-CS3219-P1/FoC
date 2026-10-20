import { randomUUID } from "node:crypto";
import { rm } from "node:fs/promises";
import { join } from "node:path";
import {
	K8S_CLUSTER,
	K8S_CONTEXT,
	K8S_EXISTING,
	K8S_STATE_DIR,
	checkContext,
	die,
	k,
	note,
	prepareState,
	requireCommands,
} from "./common";

export async function runProxyStartupSmoke(): Promise<void> {
	await requireCommands("kubectl");
	if (K8S_EXISTING || K8S_CONTEXT !== "kind-" + K8S_CLUSTER) {
		die("Proxy startup smoke is restricted to the managed local kind cluster.");
	}
	await checkContext();
	await prepareState();
	const namespace = "foc-proxy-startup-" + randomUUID().replaceAll("-", "").slice(0, 12);
	try {
		await k(["create", "namespace", namespace]);
		const image = process.env.K8S_SMOKE_IMAGE || "curlimages/curl:8.12.1";
		if (!/^[a-zA-Z0-9._:/@-]+$/.test(image)) die("K8S_SMOKE_IMAGE contains unsupported characters.");
		const manifest = {
			apiVersion: "v1",
			kind: "Pod",
			metadata: {
				name: "blocked-app",
				namespace,
				labels: { "sidecar.istio.io/inject": "true" },
				annotations: {
					"sidecar.istio.io/nativeSidecar": "true",
					"proxy.istio.io/config": '{"discoveryAddress":"127.0.0.1:1"}',
				},
			},
			spec: {
				restartPolicy: "Never",
				activeDeadlineSeconds: 420,
				containers: [{
					name: "application",
					image,
					command: ["sleep", "400"],
					securityContext: {
						runAsNonRoot: true,
						runAsUser: 65532,
						allowPrivilegeEscalation: false,
						capabilities: { drop: ["ALL"] },
					},
				}],
			},
		};
		const path = join(K8S_STATE_DIR, namespace + ".json");
		await Bun.write(path, JSON.stringify(manifest, null, 2));
		try {
			await k(["apply", "-f", path]);
		} finally {
			await rm(path, { force: true });
		}
		// A failed HTTP probe can consume timeoutSeconds, not just periodSeconds.
		// Include pull/scheduling time; the production probe configuration stays intact.
		const pod = JSON.parse((await k(["-n", namespace, "get", "pod", "blocked-app", "-o", "json"], { capture: true })).stdout);
		const probe = pod.spec.initContainers.find((container: { name: string }) => container.name === "istio-proxy")?.startupProbe;
		if (!probe) die("FAIL: injected proxy has no startup probe.");
		const budgetSeconds = probe.failureThreshold * (probe.periodSeconds + (probe.timeoutSeconds || 1)) + 90;
		const deadline = Date.now() + budgetSeconds * 1000;
		while (Date.now() < deadline) {
			const state = await k([
				"-n", namespace, "get", "pod", "blocked-app", "-o",
				'jsonpath={.status.containerStatuses[?(@.name=="application")].containerID}:{.status.initContainerStatuses[?(@.name=="istio-proxy")].restartCount}',
			], { capture: true });
			const [containerID, restarts] = state.stdout.trim().split(":");
			if (containerID) die("FAIL: application started despite an unavailable proxy.");
			if (/^[1-9][0-9]*$/.test(restarts || "")) {
				note("PASS: unavailable Istio proxy exhausted its startup probe and restarted; application never started.");
				return;
			}
			await Bun.sleep(2000);
		}
		await k(["-n", namespace, "describe", "pod", "blocked-app"], { allowFailure: true });
		die("FAIL: did not observe a bounded proxy startup failure; application has not started.");
	} finally {
		await k(["delete", "namespace", namespace, "--ignore-not-found", "--wait=false"], { capture: true, allowFailure: true });
	}
}

if (import.meta.main) {
	try {
		await runProxyStartupSmoke();
	} catch (error) {
		console.error("ERROR: " + (error instanceof Error ? error.message : "unexpected error"));
		process.exitCode = 1;
	}
}
