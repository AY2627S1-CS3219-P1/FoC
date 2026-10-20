import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { chmod, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer } from "node:net";
import { hasIstioCharts, K8S_ISTIO_VERSION, K8S_ROOT } from "./common";

async function freePort(): Promise<number> {
	const server = createServer();
	await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
	const address = server.address();
	if (!address || typeof address === "string") throw new Error("TCP address missing");
	await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
	return address.port;
}

test("complete chart files are required, and a verified warm tool cache needs no network", async () => {
	const directory = await mkdtemp(join(tmpdir(), "foc-tools-"));
	try {
		const charts = join(directory, "istio-" + K8S_ISTIO_VERSION, "manifests/charts");
		await mkdir(join(charts, "base"), { recursive: true });
		expect(await hasIstioCharts(directory)).toBe(false);
		for (const chart of ["base", "istio-control/istio-discovery", "gateway"]) {
			await mkdir(join(charts, chart), { recursive: true });
			await writeFile(join(charts, chart, "Chart.yaml"), "apiVersion: v2\n");
		}
		expect(await hasIstioCharts(directory)).toBe(true);
		for (const [name, version] of Object.entries({ kind: "0.33.0", kubectl: "1.35.0", helm: "3.20.1", istioctl: K8S_ISTIO_VERSION })) {
			const bytes = "#!/bin/sh\nexit 0\n";
			await writeFile(join(directory, name), bytes, { mode: 0o755 });
			await writeFile(join(directory, name + ".verified"), `${version} ${process.platform} ${process.arch === "x64" ? "amd64" : "arm64"} ${createHash("sha256").update(bytes).digest("hex")}\n`);
		}
		const child = Bun.spawn([process.execPath, "-e", `globalThis.fetch = () => { throw new Error("unexpected network access"); }; const {installTools} = await import(${JSON.stringify(join(K8S_ROOT, "scripts/k8s/tools.ts"))}); await installTools();`], {
			env: { ...process.env, K8S_TOOLS_DIR: directory }, stdout: "pipe", stderr: "pipe",
		});
		const stderr = await new Response(child.stderr).text();
		expect(await child.exited).toBe(0);
		expect(stderr).toContain("Verified Kubernetes tools");
	} finally {
		await rm(directory, { recursive: true, force: true });
	}
});

test.each(["ready", "starting"] as const)("Ctrl+C closes gateway children while %s", async (phase) => {
	const directory = await mkdtemp(join(tmpdir(), "foc-forward-"));
	const port = await freePort();
	let child: Bun.Subprocess | undefined;
	try {
		const kubectl = join(directory, "kubectl");
		await writeFile(kubectl, `#!${process.execPath}
const args = process.argv.slice(2);
if (args.includes("get-contexts")) console.log("kind-foc");
if (args.includes("configmap")) console.log(JSON.stringify({data:Object.fromEntries(["ORIGIN","FRONTEND_BASE_URL","PUBLIC_USER_SERVICE_URL","PUBLIC_SUPPLIER_SERVICE_URL","PUBLIC_ORDER_SERVICE_URL","PUBLIC_COMMUNICATION_SERVICE_URL","PUBLIC_CREDIT_SERVICE_URL","ORDER_S3_PUBLIC_ENDPOINT"].map(key=>[key,"https://localhost:${port}"]))}));
if (args.includes("port-forward")) {
 await Bun.write(${JSON.stringify(join(directory, "child.pid"))},String(process.pid));
 if (process.env.K8S_TEST_STARTING === "1") { console.log("forward waiting"); await Bun.sleep(30000); }
 const mapping = args.at(-1).split(":");
 const server = Bun.listen({hostname:"127.0.0.1",port:Number(mapping[0]),socket:{data(){},open(socket){socket.end();}}});
 process.on("SIGTERM",()=>{server.stop(true);process.exit(0);});
}
`);
		await chmod(kubectl, 0o755);
		child = Bun.spawn([process.execPath, "run", join(K8S_ROOT, "scripts/k8s/k8s.ts"), "access"], {
			env: { ...process.env, K8S_CONTEXT: "", K8S_CLUSTER: "foc", K8S_OVERLAY: "infra/k8s/overlays/local", K8S_TEST_STARTING: phase === "starting" ? "1" : "", K8S_TOOLS_DIR: directory, K8S_HTTPS_PORT: String(port), K8S_KAFKA_UI_PORT: "", K8S_MAILPIT_PORT: "" },
			stdout: "pipe", stderr: "pipe",
		});
		const stderr = phase === "starting" ? child.stdout : child.stderr;
		if (!stderr || typeof stderr === "number") throw new Error("stderr pipe missing");
		const reader = stderr.getReader();
		let output = "";
		try {
			while (!output.includes(phase === "starting" ? "forward waiting" : "Kubernetes forwards are attached")) {
				const next = await reader.read();
				if (next.done) throw new Error(output);
				output += new TextDecoder().decode(next.value);
			}
		} finally { reader.releaseLock(); }
		child.kill("SIGINT");
		expect(await child.exited).toBe(130);
		const pid = Number(await Bun.file(join(directory, "child.pid")).text());
		expect(() => process.kill(pid, 0)).toThrow();
		const server = createServer();
		await new Promise<void>((resolve, reject) => {
			server.once("error", reject);
			server.listen(port, "127.0.0.1", () => server.close(error => error ? reject(error) : resolve()));
		});
	} finally {
		if (child?.exitCode === null) { child.kill("SIGTERM"); await child.exited; }
		await rm(directory, { recursive: true, force: true });
	}
}, 10_000);

test.each(["ORIGIN", "PUBLIC_ORDER_SERVICE_URL", "ORDER_S3_PUBLIC_ENDPOINT"])("gateway access rejects mismatched %s before forwarding", async (key) => {
	const directory = await mkdtemp(join(tmpdir(), "foc-origin-"));
	try {
		const kubectl = join(directory, "kubectl");
		await writeFile(kubectl, `#!${process.execPath}
const args = process.argv.slice(2);
if (args.includes("get-contexts")) console.log("kind-foc");
if (args.includes("configmap")) {
 const data=Object.fromEntries(["ORIGIN","FRONTEND_BASE_URL","PUBLIC_USER_SERVICE_URL","PUBLIC_SUPPLIER_SERVICE_URL","PUBLIC_ORDER_SERVICE_URL","PUBLIC_COMMUNICATION_SERVICE_URL","PUBLIC_CREDIT_SERVICE_URL","ORDER_S3_PUBLIC_ENDPOINT"].map(key=>[key,"https://localhost:19443"]));
 data[${JSON.stringify(key)}]="https://localhost:5173";
 console.log(JSON.stringify({data}));
}
if (args.includes("port-forward")) { console.error("unexpected forward started"); process.exit(7); }
`);
		await chmod(kubectl, 0o755);
		const child = Bun.spawn([process.execPath, "run", join(K8S_ROOT, "scripts/k8s/k8s.ts"), "access"], {
			env: { ...process.env, K8S_CONTEXT: "", K8S_CLUSTER: "foc", K8S_OVERLAY: "infra/k8s/overlays/local", K8S_TOOLS_DIR: directory, K8S_HTTPS_PORT: "19443", K8S_KAFKA_UI_PORT: "", K8S_MAILPIT_PORT: "" },
			stdout: "pipe", stderr: "pipe",
		});
		const [stdout, stderr, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
		expect(code).toBe(1);
		expect(stderr).toContain("deployed browser origins");
		expect(stderr).toContain("make k8s-up");
		expect(stdout + stderr).not.toContain("unexpected forward started");
	} finally { await rm(directory, { recursive: true, force: true }); }
});

test.each([undefined, "19543"])("Bun loads .env gateway settings with exported override %s", async (override) => {
	const directory = await mkdtemp(join(tmpdir(), "foc-env-"));
	try {
		await writeFile(join(directory, ".env"), "K8S_HTTPS_PORT=19443\nK8S_MAILPIT_PORT=19025\n");
		const env: Record<string, string | undefined> = { ...process.env };
		if (override) env.K8S_HTTPS_PORT = override;
		else delete env.K8S_HTTPS_PORT;
		delete env.K8S_MAILPIT_PORT;
		const child = Bun.spawn([process.execPath, "-e", `const {configuredForwards,localOrigin} = await import(${JSON.stringify(join(K8S_ROOT, "scripts/k8s/common.ts"))}); console.log(JSON.stringify({origin:localOrigin(),ports:configuredForwards().map(f=>f.localPort)}));`], { cwd: directory, env, stdout: "pipe", stderr: "pipe" });
		const output = JSON.parse(await new Response(child.stdout).text());
		expect(await child.exited).toBe(0);
		expect(output.origin).toBe("https://localhost:" + (override ?? "19443"));
		expect(output.ports).toContain(Number(override ?? "19443"));
		expect(output.ports).toContain(19025);
	} finally { await rm(directory, { recursive: true, force: true }); }
});


test("secret retries preserve credentials and generate a browser-trustable localhost certificate", async () => {
	const directory = await mkdtemp(join(tmpdir(), "foc-secrets-"));
	try {
		const tools = join(directory, "tools");
		await mkdir(tools);
		const kubectl = join(tools, "kubectl");
		await writeFile(kubectl, `#!${process.execPath}
if (process.argv.includes("create")) console.log("apiVersion: v1\\nkind: Secret\\nmetadata: {name: fixture}");
`);
		await chmod(kubectl, 0o755);
		const invoke = async () => {
			const child = Bun.spawn([process.execPath, "run", join(K8S_ROOT, "scripts/k8s/secrets.ts")], {
				env: { ...process.env, K8S_STATE_DIR: join(directory, "state"), K8S_TOOLS_DIR: tools, K8S_CONTEXT: "", K8S_TELEGRAM_BOT_TOKEN: "" },
				stdout: "pipe", stderr: "pipe",
			});
			const diagnostics = await new Response(child.stderr).text();
			if (await child.exited !== 0) throw new Error("Secret generator failed: " + diagnostics);
		};
		await invoke();
		const secrets = join(directory, "state/secrets");
		const names = ["jwt-signing-private.pem", "tls.key", "tls.crt", "postgres-password", "communication-password"];
		const before = await Promise.all(names.map(name => Bun.file(join(secrets, name)).text()));
		await invoke();
		const after = await Promise.all(names.map(name => Bun.file(join(secrets, name)).text()));
		expect(after).toEqual(before);
		const certificate = Bun.spawn(["openssl", "x509", "-in", join(secrets, "tls.crt"), "-noout", "-text"], { stdout: "pipe" });
		const description = await new Response(certificate.stdout).text();
		expect(await certificate.exited).toBe(0);
		expect(description).toContain("CA:TRUE");
		expect(description).toContain("DNS:localhost");
		expect(description).toContain("IP Address:127.0.0.1");
	} finally { await rm(directory, { recursive: true, force: true }); }
}, 15_000);


test.each(["etcdserver: request timed out", "Forbidden: immutable Job template"])("manifest recovery handles %s", async (failure) => {
	const directory = await mkdtemp(join(tmpdir(), "foc-apply-"));
	try {
		const counter = join(directory, "attempts");
		const kubectl = join(directory, "kubectl");
		await writeFile(kubectl, `#!${process.execPath}
const counter = ${JSON.stringify(counter)};
const count = await Bun.file(counter).exists() ? Number(await Bun.file(counter).text()) : 0;
await Bun.write(counter, String(count + 1));
if (count === 0) { console.error(${JSON.stringify(failure)}); process.exit(1); }
console.log("job/fixture unchanged");
`);
		await chmod(kubectl, 0o755);
		const child = Bun.spawn([process.execPath, "-e", `const {applyManifest} = await import(${JSON.stringify(join(K8S_ROOT, "scripts/k8s/common.ts"))}); await applyManifest("fixture.yaml");`], {
			env: { ...process.env, K8S_TOOLS_DIR: directory, K8S_CONTEXT: "" }, stdout: "pipe", stderr: "pipe",
		});
		await new Response(child.stderr).text();
		expect(await child.exited).toBe(failure.startsWith("etcdserver") ? 0 : 1);
		expect(await Bun.file(counter).text()).toBe(failure.startsWith("etcdserver") ? "2" : "1");
	} finally { await rm(directory, { recursive: true, force: true }); }
});
