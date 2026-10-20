import { afterEach, describe, expect, test } from "bun:test";
import { createServer } from "node:net";
import { join } from "node:path";
import {
	K8S_CLUSTER,
	K8S_CONTEXT,
	K8S_EXISTING,
	K8S_ROOT,
	K8S_STATE_DIR,
	OperatorError,
	assertPortAvailable,
	commandExists,
	configuredForwards,
	kubectlPrefix,
	localOrigin,
	parsePort,
	run,
	validateService,
} from "./common";

describe("Kubernetes operator configuration", () => {
	test("blank or missing port settings disable forwards", () => {
		expect(configuredForwards({})).toEqual([]);
		expect(configuredForwards({ K8S_HTTPS_PORT: "", K8S_KAFKA_UI_PORT: "  " })).toEqual([]);
	});

	test("valid ports map to the expected local-only services", () => {
		expect(configuredForwards({
			K8S_HTTPS_PORT: "5173",
			K8S_KAFKA_UI_PORT: "8080",
			K8S_MAILPIT_PORT: "8025",
		})).toEqual([
			{ name: "HTTPS gateway", namespace: "istio-ingress", service: "istio-ingressgateway", localPort: 5173, remotePort: 443, scheme: "https" },
			{ name: "Kafka UI", namespace: "foc", service: "kafka-ui", localPort: 8080, remotePort: 8080, scheme: "http" },
			{ name: "Mailpit", namespace: "foc", service: "mailpit", localPort: 8025, remotePort: 8025, scheme: "http" },
		]);
	});

	test("invalid and duplicate ports fail before deployment", () => {
		expect(() => parsePort("K8S_HTTPS_PORT", "https")).toThrow(OperatorError);
		expect(() => parsePort("K8S_HTTPS_PORT", "65536")).toThrow(OperatorError);
		expect(() => configuredForwards({ K8S_HTTPS_PORT: "5173", K8S_KAFKA_UI_PORT: "5173" })).toThrow(/Multiple Kubernetes forwards/);
	});

	test("local origin follows the gateway port and has a stable fallback when disabled", () => {
		expect(localOrigin({ K8S_HTTPS_PORT: "9443" })).toBe("https://localhost:9443");
		expect(localOrigin({ K8S_HTTPS_PORT: "" })).toBe("https://localhost:5173");
	});

	test("service and kubectl target validation stays explicit", () => {
		expect(() => validateService("user-service")).not.toThrow();
		expect(() => validateService("credit-service")).toThrow(/Unknown service/);
		if (K8S_EXISTING) {
			expect(kubectlPrefix()).toEqual(["--context", K8S_CONTEXT]);
		} else {
			expect(kubectlPrefix()).toEqual([
				"--kubeconfig", join(K8S_STATE_DIR, "clusters", K8S_CLUSTER + ".config"),
				"--context", K8S_CONTEXT,
			]);
		}
	});
});

describe("Bun shell command runner", () => {
	test("arguments remain literal and stdin files are supported", async () => {
		const argument = "literal text; printf injected";
		const literal = await run("printf", ["%s", argument], { capture: true });
		const stdin = await run("cat", [], { capture: true, stdinFile: join(K8S_ROOT, ".bun-version") });
		expect(literal.stdout).toBe(argument);
		expect(stdin.stdout.trim()).toBe("1.4.3");
	});

	test("failed commands return bounded diagnostic errors", async () => {
		await expect(run("sh", ["-c", "exit 7"], { capture: true })).rejects.toThrow(/exit code 7/);
		const result = await run("sh", ["-c", "exit 7"], { capture: true, allowFailure: true });
		expect(result.exitCode).toBe(7);
	});
});

describe("host port preflight", () => {
	let server: ReturnType<typeof createServer> | undefined;

	afterEach(async () => {
		if (!server?.listening) return;
		await new Promise<void>((resolve, reject) => server!.close((error) => error ? reject(error) : resolve()));
	});

	test("detects an occupied loopback port", async () => {
		server = createServer();
		await new Promise<void>((resolve, reject) => {
			server!.once("error", reject);
			server!.listen(0, "127.0.0.1", resolve);
		});
		const address = server.address();
		if (!address || typeof address === "string") throw new Error("expected a TCP address");
		await expect(assertPortAvailable(address.port)).rejects.toThrow(/already in use/);
	});

	test("accepts a free ephemeral loopback port", async () => {
		server = createServer();
		await new Promise<void>((resolve, reject) => {
			server!.once("error", reject);
			server!.listen(0, "127.0.0.1", resolve);
		});
		const address = server.address();
		if (!address || typeof address === "string") throw new Error("expected a TCP address");
		const port = address.port;
		await new Promise<void>((resolve, reject) => server!.close((error) => error ? reject(error) : resolve()));
		server = undefined;
		await expect(assertPortAvailable(port)).resolves.toBeUndefined();
	});
});

test("Kubernetes setup pins Bun and reuses the expected CLI binary", async () => {
	expect((await Bun.file(join(K8S_ROOT, ".bun-version")).text()).trim()).toBe("1.4.3");
	expect(await commandExists("sh")).toBe(true);
});

async function invokeCLI(args: string[], overrides: Record<string, string>, unset: string[] = []) {
	const env: Record<string, string> = {};
	for (const [key, value] of Object.entries(process.env)) {
		if (value !== undefined) env[key] = value;
	}
	for (const key of unset) delete env[key];
	Object.assign(env, overrides);
	const child = Bun.spawn([
		process.execPath,
		"run",
		join(K8S_ROOT, "scripts/k8s/k8s.ts"),
		...args,
	], { cwd: K8S_ROOT, env, stdout: "pipe", stderr: "pipe" });
	const [stdout, stderr, exitCode] = await Promise.all([
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
		child.exited,
	]);
	return { output: stdout + stderr, exitCode };
}

describe("operator safety guards", () => {
	test("D3 demo does not claim unavailable workflow coverage", async () => {
		const result = await invokeCLI(["demo"], {});
		expect(result.exitCode).toBe(1);
		expect(result.output).toContain("D3 demo is unavailable");
	});

	test("cluster deletion rejects external contexts before invoking tools", async () => {
		const result = await invokeCLI(["delete-cluster"], { K8S_CONTEXT: "production-review" });
		expect(result.exitCode).toBe(1);
		expect(result.output).toContain("Cluster deletion is local-only");
	});

	test("local cluster deletion requires the exact confirmation", async () => {
		const result = await invokeCLI(["delete-cluster"], { K8S_CONFIRM_DELETE: "" }, ["K8S_CONTEXT"]);
		expect(result.exitCode).toBe(1);
		expect(result.output).toContain("Rerun with K8S_CONFIRM_DELETE=foc");
	});

	test("proxy startup disruption smoke rejects external clusters", async () => {
		const result = await invokeCLI(["proxy-startup-smoke"], { K8S_CONTEXT: "production-review" });
		expect(result.exitCode).toBe(1);
		expect(result.output).toContain("restricted to the managed local kind cluster");
	});

	test("all local and production Kustomize stages render with overridden images", async () => {
		if (!(await commandExists("kubectl"))) {
			console.warn("SKIP manifest rendering: kubectl is not installed; run make k8s-tools first");
			return;
		}
		for (const overlay of ["infra/k8s/overlays/local", "infra/k8s/overlays/production"]) {
			for (const stage of ["dependencies", "provisioning", "initialization", "applications"]) {
				const result = await invokeCLI(["render", stage], {
					K8S_CONTEXT: "",
					K8S_OVERLAY: overlay,
					K8S_IMAGE_PREFIX: "registry.example/foc",
					K8S_IMAGE_TAG: "unit-test",
				});
				expect(result.exitCode).toBe(0);
				if (overlay.endsWith("/local") && stage === "dependencies") {
					const floci = result.output.split(/\n---\n/).find(document => document.includes("kind: Deployment") && document.includes("\n  name: floci\n"));
					expect(floci).toContain("enableServiceLinks: false");
				}

				if (stage === "initialization" || stage === "applications") {
					expect(result.output).toContain("image: registry.example/foc/user-service:unit-test");
					expect(result.output).not.toContain("image: foc/");
				}
			}
		}
	});
});
