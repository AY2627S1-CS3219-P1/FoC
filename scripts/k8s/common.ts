// Shared CLI/config plumbing for the Kubernetes operator scripts. See
// docs/runbooks/kubernetes.md for the operator model.
import { $ } from "bun";
import { access, chmod, mkdir } from "node:fs/promises";
import { constants } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { connect, createServer } from "node:net";

// Operator inputs and defaults. Constants that mirror an environment variable
// (K8S_CLUSTER, K8S_CONTEXT, K8S_OVERLAY, K8S_IMAGE_*, K8S_TIMEOUT,
// K8S_STATE_DIR) keep the variable's name so the docs and the code agree;
// anything derived is named for what it means.

// Repository root: every infra path resolves from here.
export const REPO_ROOT = resolve(import.meta.dir, "../..");

// Where the project's pinned tools are installed (git-ignored). aqua's own
// binary lives in the user's home directory — unrelated to this path.
export const AQUA_ROOT_DIR = resolve(process.env.AQUA_ROOT_DIR ?? join(REPO_ROOT, ".tools/aqua"));
export const AQUA_BIN_DIR = join(AQUA_ROOT_DIR, "bin");

// kubeconfigs, generated secrets and temporary render directories.
export const K8S_STATE_DIR = resolve(process.env.K8S_STATE_DIR ?? join(REPO_ROOT, ".local/k8s"));

// Make the pinned tools resolvable for every spawned command.
process.env.AQUA_ROOT_DIR = AQUA_ROOT_DIR;
process.env.PATH = AQUA_BIN_DIR + (process.platform === "win32" ? ";" : ":") + (process.env.PATH ?? "");

// Managed local kind cluster name.
export const K8S_CLUSTER = process.env.K8S_CLUSTER || "foc";

// kubectl context: "kind-<cluster>" for the managed cluster, or an explicit
// context when targeting a pre-existing cluster.
export const K8S_CONTEXT = process.env.K8S_CONTEXT || "kind-" + K8S_CLUSTER;

// True when K8S_CONTEXT targets a pre-existing (external) cluster instead of
// the managed local one; gates kind lifecycle, image pushing and delete guards.
export const USING_EXISTING_CLUSTER = Boolean(process.env.K8S_CONTEXT);

// Manifest overlay: the local one by default; external clusters must set
// K8S_OVERLAY explicitly.
export const K8S_OVERLAY_EXPLICIT = Boolean(process.env.K8S_OVERLAY);
export const K8S_OVERLAY = resolve(REPO_ROOT, process.env.K8S_OVERLAY || "infra/k8s/overlays/local");

// Default duration for kubectl --timeout waits and rollout statuses.
export const K8S_TIMEOUT = process.env.K8S_TIMEOUT || "600s";

// Registry prefix and tag for built application images.
export const K8S_IMAGE_TAG = process.env.K8S_IMAGE_TAG || "local";
export const K8S_IMAGE_PREFIX = process.env.K8S_IMAGE_PREFIX || "foc";

// Container image for the kind control-plane node — the local Kubernetes version.
export const K8S_NODE_IMAGE =
	process.env.K8S_NODE_IMAGE ||
	"kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f";

// Application Deployment names, in deploy order.
export const K8S_APPS = [
	"user-service",
	"supplier-service",
	"order-service",
	"credit-service",
	"communication-service",
	"frontend",
] as const;

// Initialization Jobs (run to completion before applications start).
export const K8S_JOBS = [
	"user-migrate",
	"supplier-migrate",
	"order-migrate",
	"credit-migrate",
	"communication-migrate",
	"kafka-init",
] as const;

export class OperatorError extends Error {}

export function note(message: string): void {
	console.error(message);
}

export function die(message: string): never {
	throw new OperatorError(message);
}

export interface CommandOptions {
	capture?: boolean;
	allowFailure?: boolean;
	env?: Record<string, string | undefined>;
	cwd?: string;
	stdinFile?: string;
}

export interface CommandResult {
	stdout: string;
	stderr: string;
	exitCode: number;
}

// Every external command goes through here: Bun shell with escaped arguments,
// inherited env, and a die() on nonzero exit unless the caller opts out.
export async function run(program: string, args: string[] = [], options: CommandOptions = {}): Promise<CommandResult> {
	const env: Record<string, string> = {};
	for (const [key, value] of Object.entries({ ...process.env, ...options.env })) {
		if (value !== undefined) env[key] = value;
	}
	const capture = Boolean(options.capture);
	const command = [program, ...args].map((argument) => $.escape(argument)).join(" ");
	const stdin = options.stdinFile ? " < " + $.escape(options.stdinFile) : "";
	let task = $`${{ raw: command + stdin }}`
		.cwd(options.cwd ?? REPO_ROOT)
		.env(env)
		.nothrow();
	if (capture) task = task.quiet();
	const result = await task;
	const stdout = capture ? result.stdout.toString() : "";
	const stderr = capture ? result.stderr.toString() : "";
	const exitCode = result.exitCode;
	if (exitCode !== 0 && !options.allowFailure) {
		const detail = stderr.trim();
		die(program + " " + args.join(" ") + " failed with exit code " + exitCode + (detail ? ": " + detail : ""));
	}
	return { stdout, stderr, exitCode };
}

// kind's kubeconfig stays in the state directory; the developer's global
// kubeconfig is never written to.
export function kubectlPrefix(): string[] {
	return USING_EXISTING_CLUSTER
		? ["--context", K8S_CONTEXT]
		: ["--kubeconfig", join(K8S_STATE_DIR, "clusters", K8S_CLUSTER + ".config"), "--context", K8S_CONTEXT];
}

export async function aquaWhich(name: string): Promise<string> {
	const result = await run("aqua", ["which", name], { capture: true });
	const path = result.stdout.trim();
	if (!path) die("aqua could not find installed package binary " + name + ". Run make k8s-tools.");
	return path;
}

export async function kubectlBinary(): Promise<string> {
	return aquaWhich("kubectl");
}

// The Helm charts ship inside the Istio archive, next to the istioctl binary.
export async function istioChartsDirectory(): Promise<string> {
	const installedIstioctl = await aquaWhich("istioctl");
	return join(dirname(dirname(installedIstioctl)), "manifests/charts");
}

export function helmPrefix(): string[] {
	return USING_EXISTING_CLUSTER
		? ["--kube-context", K8S_CONTEXT]
		: ["--kubeconfig", join(K8S_STATE_DIR, "clusters", K8S_CLUSTER + ".config"), "--kube-context", K8S_CONTEXT];
}

export async function k(args: string[], options: CommandOptions = {}): Promise<CommandResult> {
	return run("kubectl", [...kubectlPrefix(), ...args], options);
}

export async function h(args: string[], options: CommandOptions = {}): Promise<CommandResult> {
	return run("helm", [...helmPrefix(), ...args], options);
}

export async function commandExists(name: string): Promise<boolean> {
	for (const directory of (process.env.PATH ?? "").split(process.platform === "win32" ? ";" : ":")) {
		if (!directory) continue;
		try {
			await access(join(directory, name), constants.X_OK);
			return true;
		} catch {
			// Continue searching PATH.
		}
	}
	return false;
}

export async function requireCommands(...names: string[]): Promise<void> {
	for (const name of names) {
		if (!(await commandExists(name))) {
			die("Missing " + name + "; run make k8s-tools (Docker and OpenSSL are host prerequisites).");
		}
	}
}

export async function prepareState(): Promise<void> {
	try {
		await mkdir(join(K8S_STATE_DIR, "clusters"), { recursive: true, mode: 0o700 });
		await chmod(K8S_STATE_DIR, 0o700);
		await chmod(join(K8S_STATE_DIR, "clusters"), 0o700);
	} catch {
		die(
			"Cannot create " +
				K8S_STATE_DIR +
				". If Compose created a root-owned .local, create only this directory with: sudo install -d -o $(id -u) -g $(id -g) -m 700 '" +
				K8S_STATE_DIR +
				"'; or set K8S_STATE_DIR to a writable private directory.",
		);
	}
}

export async function checkContext(): Promise<void> {
	await requireCommands("kubectl");
	const contexts = await k(["config", "get-contexts", K8S_CONTEXT, "-o", "name"], { capture: true });
	if (contexts.stdout.trim() !== K8S_CONTEXT) {
		die("Context " + K8S_CONTEXT + " does not exist. Run make k8s-up for the local cluster.");
	}
	await k(["cluster-info"], { capture: true });
}

export async function checkOverlay(): Promise<void> {
	if (USING_EXISTING_CLUSTER && !K8S_OVERLAY_EXPLICIT) {
		die("An existing cluster requires explicit K8S_CONTEXT and K8S_OVERLAY.");
	}
	for (const stage of ["gateway", "dependencies", "provisioning", "initialization", "applications"]) {
		const path = join(K8S_OVERLAY, stage, "kustomization.yaml");
		if (!(await Bun.file(path).exists())) die("Missing " + path);
	}
}

export function isLocalOverlay(): boolean {
	return K8S_OVERLAY === resolve(REPO_ROOT, "infra/k8s/overlays/local");
}

export function validateService(service: string): asserts service is (typeof K8S_APPS)[number] {
	if (!(K8S_APPS as readonly string[]).includes(service)) {
		die("Unknown service '" + service + "'; use " + K8S_APPS.join(", ") + ".");
	}
}

export function imageFor(name: string): string {
	return K8S_IMAGE_PREFIX + "/" + name + ":" + K8S_IMAGE_TAG;
}

// Block until the Job reports success; dump its logs and die on failure or timeout.
export async function waitJob(name: string): Promise<void> {
	const deadline = Date.now() + Number(process.env.K8S_JOB_TIMEOUT_SECONDS || 600) * 1000;
	while (Date.now() < deadline) {
		const result = await k(
			[
				"-n",
				"foc",
				"get",
				"job",
				name,
				"-o",
				'jsonpath={.status.succeeded}:{.status.conditions[?(@.type=="Failed")].status}',
			],
			{ capture: true },
		);
		const status = result.stdout.trim();
		if (/^[1-9][0-9]*:/.test(status)) return;
		if (status.endsWith(":True")) {
			await k(["-n", "foc", "logs", "job/" + name, "--all-containers", "--tail=100"], { allowFailure: true });
			die("Job " + name + " failed; fix the cause before retrying.");
		}
		await Bun.sleep(2000);
	}
	await k(["-n", "foc", "describe", "job", name], { allowFailure: true });
	die("Timed out waiting for Job " + name + ". Existing running Jobs are preserved.");
}

// Kubernetes Jobs are immutable: only completed or failed Jobs may be deleted
// so the next apply recreates them. A running Job must never be touched.
export async function prepareJob(name: string): Promise<void> {
	const result = await k(
		[
			"-n",
			"foc",
			"get",
			"job",
			name,
			"--ignore-not-found",
			"-o",
			'jsonpath={.metadata.name}:{.status.succeeded}:{.status.conditions[?(@.type=="Failed")].status}',
		],
		{ capture: true },
	);
	const status = result.stdout.trim();
	if (!status) return;
	const remainder = status.slice(name.length + 1);
	if (status.startsWith(name + ":") && (/^[1-9]/.test(remainder) || status.endsWith(":True"))) {
		await k(["-n", "foc", "delete", "job", name, "--wait=true", "--timeout=" + K8S_TIMEOUT]);
		return;
	}
	die("Job " + name + " is not terminal; inspect or wait for it before retrying. It has not been deleted.");
}

export interface ForwardSpec {
	name: string;
	namespace: string;
	service: string;
	localPort: number;
	remotePort: number;
	scheme: "http" | "https";
}

export function parsePort(name: string, value: string | undefined): number | null {
	if (value === undefined || value.trim() === "") return null;
	if (!/^\d+$/.test(value.trim())) die(name + " must be a port number or blank to disable forwarding.");
	const port = Number(value.trim());
	if (!Number.isInteger(port) || port < 1 || port > 65535) {
		die(name + " must be between 1 and 65535, or blank to disable forwarding.");
	}
	return port;
}

// Build the forward list from the K8S_*_PORT inputs; a blank value disables its forward.
export function configuredForwards(env: Record<string, string | undefined> = process.env): ForwardSpec[] {
	const values = [
		{
			key: "K8S_HTTPS_PORT",
			name: "HTTPS gateway",
			namespace: "istio-ingress",
			service: "foc-public-istio",
			remotePort: 443,
			scheme: "https" as const,
		},
		{
			key: "K8S_KAFKA_UI_PORT",
			name: "Kafka UI",
			namespace: "foc",
			service: "kafka-ui",
			remotePort: 8080,
			scheme: "http" as const,
		},
		{
			key: "K8S_MAILPIT_PORT",
			name: "Mailpit",
			namespace: "foc",
			service: "mailpit",
			remotePort: 8025,
			scheme: "http" as const,
		},
	];
	const forwards: ForwardSpec[] = [];
	const seenPorts = new Set<number>();
	for (const { key, ...forward } of values) {
		const localPort = parsePort(key, env[key]);
		if (localPort === null) continue;
		if (seenPorts.has(localPort)) {
			die("Multiple Kubernetes forwards use host port " + localPort + ". Choose distinct K8S_*_PORT values.");
		}
		seenPorts.add(localPort);
		forwards.push({ ...forward, localPort });
	}
	return forwards;
}

// The browser origin implied by the gateway forward (5173 when no port is configured).
export function localOrigin(env: Record<string, string | undefined> = process.env): string {
	return "https://localhost:" + (parsePort("K8S_HTTPS_PORT", env.K8S_HTTPS_PORT) ?? 5173);
}

export async function assertPortAvailable(port: number): Promise<void> {
	await new Promise<void>((resolvePromise, reject) => {
		const server = createServer();
		server.once("error", () =>
			reject(
				new OperatorError(
					"Host port " + port + " is already in use. Stop Compose or the other listener, or choose another K8S_*_PORT.",
				),
			),
		);
		server.listen(port, "127.0.0.1", () => server.close((error) => (error ? reject(error) : resolvePromise())));
	});
}

export async function preflightForwards(forwards: ForwardSpec[]): Promise<void> {
	for (const forward of forwards) await assertPortAvailable(forward.localPort);
}

// Check chart files: Bun.file(directory).exists() is false even for directories.
export async function hasIstioCharts(chartsDirectory: string): Promise<boolean> {
	const charts = ["base", "istio-control/istio-discovery"];
	return (
		await Promise.all(charts.map((chart) => Bun.file(join(chartsDirectory, chart, "Chart.yaml")).exists()))
	).every(Boolean);
}

// An API timeout can follow a partially successful apply. Reapply the exact same
// manifest; never delete/recreate running Jobs to recover an ambiguous response.
export async function applyManifest(path: string): Promise<void> {
	for (let attempt = 0; attempt < 3; attempt++) {
		const result = await k(["apply", "-f", path], { capture: true, allowFailure: true });
		if (result.stdout) process.stdout.write(result.stdout);
		if (result.exitCode === 0) return;
		const transient =
			/etcdserver: request timed out|context deadline exceeded|i\/o timeout|TLS handshake timeout|unexpected EOF|ServiceUnavailable|TooManyRequests/.test(
				result.stderr,
			);
		if (!transient || attempt === 2) die("kubectl apply failed: " + result.stderr.trim());
		note("Transient Kubernetes API failure; retrying the same manifest (attempt " + (attempt + 2) + "/3).");
		await Bun.sleep(1000 * 2 ** attempt);
	}
}

export async function waitForGateway(): Promise<void> {
	await k(["wait", "--for=condition=Accepted", "gatewayclass/istio", "--timeout=" + K8S_TIMEOUT]);
	await k([
		"-n",
		"istio-ingress",
		"wait",
		"--for=condition=Programmed",
		"gateway/foc-public",
		"--timeout=" + K8S_TIMEOUT,
	]);
	const routes = [
		"user-service",
		"supplier-service",
		"order-service",
		"communication-service",
		"credit-service",
		"frontend",
	];
	if (isLocalOverlay()) routes.push("floci-images");
	for (const route of routes) {
		await waitForHTTPRoute(route);
	}
	await k(["-n", "istio-ingress", "rollout", "status", "deployment/foc-public-istio", "--timeout=" + K8S_TIMEOUT]);
}

// Wait until the Gateway controller accepts the route and resolves its
// backends; die with the controller's rejection reason otherwise.
async function waitForHTTPRoute(name: string): Promise<void> {
	const timeout = /^(\d+)(s|m|h)$/.exec(K8S_TIMEOUT);
	if (!timeout) die("K8S_TIMEOUT must be an integer duration such as 600s or 10m.");
	const multiplier = timeout[2] === "h" ? 3_600_000 : timeout[2] === "m" ? 60_000 : 1_000;
	const deadline = Date.now() + Number(timeout[1]) * multiplier;
	while (Date.now() < deadline) {
		const result = await k(["-n", "foc", "get", "httproute", name, "-o", "json"], { capture: true });
		const route = JSON.parse(result.stdout);
		const parent = route.status?.parents?.find(
			(item: { parentRef?: { name?: string }; controllerName?: string }) =>
				item.parentRef?.name === "foc-public" && item.controllerName === "istio.io/gateway-controller",
		);
		const conditions = parent?.conditions ?? [];
		const accepted = conditions.find((item: { type: string }) => item.type === "Accepted");
		const resolved = conditions.find((item: { type: string }) => item.type === "ResolvedRefs");
		if (accepted?.status === "True" && resolved?.status === "True") return;
		const rejected = [accepted, resolved].find((item) => item?.status === "False");
		if (rejected) {
			die("HTTPRoute " + name + " was rejected: " + rejected.reason + " — " + rejected.message);
		}
		await Bun.sleep(1000);
	}
	die("Timed out waiting for HTTPRoute " + name + " to be accepted with resolved backend references.");
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

// kubectl binds the local listener before the tunnel is usable and can also
// exit early; consider the forward ready only when the port accepts a TCP
// connection.
export async function startForward(forward: ForwardSpec, children: Bun.Subprocess[]): Promise<void> {
	const kubectl = await kubectlBinary();
	const child = Bun.spawn(
		[
			kubectl,
			...kubectlPrefix(),
			"-n",
			forward.namespace,
			"port-forward",
			"--address",
			"127.0.0.1",
			"service/" + forward.service,
			forward.localPort + ":" + forward.remotePort,
		],
		{ stdout: "inherit", stderr: "inherit", stdin: "ignore" },
	);
	children.push(child);
	const exited = child.exited.then((code) => ({ code }));
	const deadline = Date.now() + 30_000;
	while (Date.now() < deadline) {
		const result = await Promise.race([waitForLocalListener(forward.localPort).then((ready) => ({ ready })), exited]);
		if ("code" in result) {
			die(forward.name + " port-forward exited before becoming ready (code " + result.code + ").");
		}
		if (result.ready) {
			note(forward.name + ": " + forward.scheme + "://localhost:" + forward.localPort);
			return;
		}
		await Bun.sleep(100);
	}
	child.kill("SIGTERM");
	die(forward.name + " port-forward did not become ready on host port " + forward.localPort + ".");
}

// SIGTERM first, SIGKILL after a grace period, so Ctrl+C never leaks a forward.
export async function stopForwards(children: Bun.Subprocess[]): Promise<void> {
	for (const child of children) {
		if (child.exitCode === null) child.kill("SIGTERM");
	}
	await Promise.race([Promise.all(children.map((child) => child.exited)), Bun.sleep(3000)]);
	for (const child of children) {
		if (child.exitCode === null) child.kill("SIGKILL");
	}
}
