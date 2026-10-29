import { connect } from "node:net";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
	K8S_APPS,
	K8S_CLUSTER,
	K8S_CONTEXT,
	K8S_EXISTING,
	K8S_IMAGE_PREFIX,
	K8S_IMAGE_TAG,
	K8S_ISTIO_VERSION,
	K8S_JOBS,
	K8S_NODE_IMAGE,
	K8S_OVERLAY,
	K8S_ROOT,
	K8S_STATE_DIR,
	K8S_TIMEOUT,
	K8S_TOOLS_DIR,
	ForwardSpec,
	OperatorError,
	applyManifest,
	checkContext,
	checkOverlay,
	configuredForwards,
	die,
	h,
	imageFor,
	isLocalOverlay,
	k,
	kubectlBinary,
	kubectlPrefix,
	hasIstioCharts,
	localOrigin,
	note,
	prepareJob,
	prepareState,
	preflightForwards,
	requireCommands,
	run,
	validateService,
	waitJob,
} from "./common";
import { generateLocalSecrets } from "./secrets";
import { installTools } from "./tools";

async function createLocalCluster(): Promise<void> {
	await requireCommands("kind");
	if (!/^[a-z0-9][a-z0-9-]*$/.test(K8S_CLUSTER)) die("K8S_CLUSTER must be a lowercase kind cluster name.");
	await prepareState();
	const existing = await run("kind", ["get", "clusters"], { capture: true });
	if (!existing.stdout.split(/\r?\n/).includes(K8S_CLUSTER)) {
		await run("kind", [
			"create", "cluster", "--name", K8S_CLUSTER, "--image", K8S_NODE_IMAGE,
			"--kubeconfig", join(K8S_STATE_DIR, "clusters", K8S_CLUSTER + ".config"),
			"--wait", K8S_TIMEOUT,
		]);
	} else {
		// Keep the developer's global kubeconfig untouched.
		await run("kind", [
			"export", "kubeconfig", "--name", K8S_CLUSTER,
			"--kubeconfig", join(K8S_STATE_DIR, "clusters", K8S_CLUSTER + ".config"),
		]);
	}
}

async function installMesh(): Promise<void> {
	await requireCommands("helm");
	const charts = join(K8S_TOOLS_DIR, "istio-" + K8S_ISTIO_VERSION, "manifests/charts");
	if (!(await hasIstioCharts())) die("Pinned Istio charts missing; run make k8s-tools.");
	await applyManifest(join(K8S_ROOT, "infra/k8s/platform/namespaces.yaml"));
	await applyManifest(join(K8S_ROOT, "infra/k8s/platform/admission.yaml"));
	await h(["upgrade", "--install", "istio-base", join(charts, "base"), "-n", "istio-system", "--wait", "--timeout", K8S_TIMEOUT]);
	await h([
		"upgrade", "--install", "istiod", join(charts, "istio-control/istio-discovery"), "-n", "istio-system",
		"-f", join(K8S_ROOT, "infra/k8s/platform/istiod-values.yaml"), "--wait", "--timeout", K8S_TIMEOUT,
	]);
	await h([
		"upgrade", "--install", "istio-ingressgateway", join(charts, "gateway"), "-n", "istio-ingress",
		"-f", join(K8S_ROOT, "infra/k8s/platform/gateway-values.yaml"), "--wait", "--timeout", K8S_TIMEOUT,
	]);
}

async function buildImage(name: string): Promise<void> {
	const dockerfile = name === "frontend" || name === "kafka-init" ? name : "service";
	await run("docker", [
		"build", "--file", join(K8S_ROOT, "docker/k8s", dockerfile + ".Dockerfile"),
		"--build-arg", "SERVICE=" + name,
		"--tag", imageFor(name),
		K8S_ROOT,
	]);
	if (!K8S_EXISTING) {
		await run("kind", ["load", "docker-image", "--name", K8S_CLUSTER, imageFor(name)]);
		return;
	}
	if (process.env.K8S_PUSH_IMAGES !== "1") {
		die("Existing cluster: set K8S_IMAGE_PREFIX to a registry and K8S_PUSH_IMAGES=1, or K8S_SKIP_BUILD=1 for already published images.");
	}
	await run("docker", ["push", imageFor(name)]);
}

async function buildImages(names: readonly string[]): Promise<void> {
	if (process.env.K8S_SKIP_BUILD === "1") return;
	await requireCommands("docker");
	if (K8S_EXISTING && process.env.K8S_PUSH_IMAGES !== "1") {
		die("Existing cluster image builds require K8S_PUSH_IMAGES=1 and a registry K8S_IMAGE_PREFIX; otherwise use K8S_SKIP_BUILD=1.");
	}
	for (const name of names) await buildImage(name);
}

async function renderStage(stage: string): Promise<string> {
	await prepareState();
	if (!/^[a-zA-Z0-9._:/-]+$/.test(K8S_IMAGE_PREFIX) || !/^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$/.test(K8S_IMAGE_TAG)) {
		die("Invalid image prefix or tag.");
	}
	const stageOutput = await k(["kustomize", join(K8S_OVERLAY, stage)], { capture: true });
	const directory = await mkdtemp(join(K8S_STATE_DIR, ".render-"));
	try {
		await writeFile(join(directory, "resources.yaml"), stageOutput.stdout, { mode: 0o600 });
		const imageLines = [...K8S_APPS, "kafka-init"].map((name) =>
			"  - name: foc/" + name + "\n    newName: " + K8S_IMAGE_PREFIX + "/" + name + "\n    newTag: \"" + K8S_IMAGE_TAG + "\"",
		);
		const config = [
			"apiVersion: kustomize.config.k8s.io/v1beta1",
			"kind: Kustomization",
			"resources:",
			"  - resources.yaml",
			"images:",
			...imageLines,
			"",
		].join("\n");
		await writeFile(join(directory, "kustomization.yaml"), config, { mode: 0o600 });
		return (await k(["kustomize", directory], { capture: true })).stdout;
	} finally {
		await rm(directory, { recursive: true, force: true });
	}
}

async function applyStage(stage: string): Promise<void> {
	const rendered = await renderStage(stage);
	const directory = await mkdtemp(join(K8S_STATE_DIR, ".apply-"));
	try {
		const path = join(directory, "resources.yaml");
		await writeFile(path, rendered, { mode: 0o600 });
		await applyManifest(path);
	} finally {
		await rm(directory, { recursive: true, force: true });
	}
}

async function patchLocalConfig(): Promise<void> {
	if (!isLocalOverlay()) return;
	const origin = localOrigin();
	const patch = JSON.stringify({
		data: {
			FRONTEND_BASE_URL: origin,
			ORIGIN: origin,
			PUBLIC_USER_SERVICE_URL: origin,
			PUBLIC_SUPPLIER_SERVICE_URL: origin,
			PUBLIC_ORDER_SERVICE_URL: origin,
			PUBLIC_COMMUNICATION_SERVICE_URL: origin,
			PUBLIC_CREDIT_SERVICE_URL: origin,
			ORDER_S3_PUBLIC_ENDPOINT: origin,
		},
	});
	await k(["-n", "foc", "patch", "configmap", "foc-config", "--type=merge", "--patch", patch]);
}

async function patchBootstrap(): Promise<void> {
	const email = process.env.K8S_BOOTSTRAP_SUPERADMIN_EMAIL;
	if (!email) return;
	const patch = JSON.stringify({
		data: {
			BOOTSTRAP_SUPERADMIN_EMAIL: email,
			BOOTSTRAP_SUPERADMIN_DISPLAY_NAME: process.env.K8S_BOOTSTRAP_SUPERADMIN_DISPLAY_NAME || "Admin",
		},
	});
	await k(["-n", "foc", "patch", "configmap", "foc-config", "--type=merge", "--patch", patch]);
}

async function deployAll(): Promise<void> {
	await checkOverlay();
	await checkContext();
	if (!isLocalOverlay()) {
		const dependencyConfig = await k(["kustomize", join(K8S_OVERLAY, "dependencies")], { capture: true });
		if (/example\.invalid|REPLACE_ME/.test(dependencyConfig.stdout)) {
			die("Production overlay is a template: configure a real hostname/origin and external Secrets first (docs/runbooks/kubernetes.md).");
		}
		if (K8S_IMAGE_PREFIX === "foc" || K8S_IMAGE_TAG === "local") {
			die("Production deployment requires a registry K8S_IMAGE_PREFIX and an immutable K8S_IMAGE_TAG.");
		}
	}
	await buildImages([...K8S_APPS, "kafka-init"]);
	if (isLocalOverlay()) {
		await generateLocalSecrets();
	} else {
		for (const secret of ["postgres-admin", "user-db", "supplier-db", "order-db", "credit-db", "communication-db", "jwt-signing", "smtp", "kafka-ui-auth", "telegram"]) {
			await k(["-n", "foc", "get", "secret", secret], { capture: true });
		}
		note("Using operator-provided configuration and Secrets; see the production overlay runbook.");
	}
	await applyStage("dependencies");
	await patchLocalConfig();
	await patchBootstrap();
	await k(["-n", "foc", "rollout", "status", "statefulset/postgres", "--timeout=" + K8S_TIMEOUT]);
	await k(["-n", "foc", "rollout", "status", "statefulset/kafka", "--timeout=" + K8S_TIMEOUT]);
	await k(["-n", "foc", "rollout", "status", "deployment/kafka-ui", "--timeout=" + K8S_TIMEOUT]);
	if (isLocalOverlay()) {
		await k(["-n", "foc", "rollout", "status", "deployment/mailpit", "--timeout=" + K8S_TIMEOUT]);
		await k(["-n", "foc", "rollout", "status", "deployment/floci", "--timeout=" + K8S_TIMEOUT]);
	}
	await prepareJob("db-provision");
	await applyStage("provisioning");
	await waitJob("db-provision");
	if (isLocalOverlay()) await prepareJob("order-storage-init");
	for (const job of K8S_JOBS) await prepareJob(job);
	await applyStage("initialization");
	for (const job of K8S_JOBS) await waitJob(job);
	if (isLocalOverlay()) await waitJob("order-storage-init");
	await applyStage("applications");
	for (const app of K8S_APPS) {
		await k(["-n", "foc", "rollout", "restart", "deployment/" + app]);
		await k(["-n", "foc", "rollout", "status", "deployment/" + app, "--timeout=" + K8S_TIMEOUT]);
	}
	note("Bootstrap coverage: User, Supplier, Order, Credit, Communication, frontend, PostgreSQL, Kafka, Kafka UI; local overlay also Mailpit and Floci. The complete D3 business workflow is not claimed.");
}

async function deployOne(service: string): Promise<void> {
	validateService(service);
	await checkOverlay();
	await checkContext();
	await buildImages([service]);
	await k(["-n", "foc", "set", "image", "deployment/" + service, service + "=" + imageFor(service)]);
	await k(["-n", "foc", "rollout", "restart", "deployment/" + service]);
	await k(["-n", "foc", "rollout", "status", "deployment/" + service, "--timeout=" + K8S_TIMEOUT]);
	note("Application updated. Schema changes require a full make k8s-deploy first.");
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

async function startForward(forward: ForwardSpec, children: Bun.Subprocess[]): Promise<void> {
	const child = Bun.spawn([
		kubectlBinary(),
		...kubectlPrefix(),
		"-n", forward.namespace,
		"port-forward", "--address", "127.0.0.1",
		"service/" + forward.service,
		forward.localPort + ":" + forward.remotePort,
	], { stdout: "inherit", stderr: "inherit", stdin: "ignore" });
	children.push(child);
	const exited = child.exited.then((code) => ({ code }));
	const deadline = Date.now() + 30_000;
	while (Date.now() < deadline) {
		const result = await Promise.race([
			waitForLocalListener(forward.localPort).then((ready) => ({ ready })),
			exited,
		]);
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

async function stopForwards(children: Bun.Subprocess[]): Promise<void> {
	for (const child of children) {
		if (child.exitCode === null) child.kill("SIGTERM");
	}
	await Promise.race([
		Promise.all(children.map((child) => child.exited)),
		Bun.sleep(3000),
	]);
	for (const child of children) {
		if (child.exitCode === null) child.kill("SIGKILL");
	}
}

async function checkGatewayOrigin(forwards: ForwardSpec[]): Promise<void> {
	if (K8S_EXISTING || !isLocalOverlay()) return;
	const gateway = forwards.find(forward => forward.name === "HTTPS gateway");
	if (!gateway) return;
	const expected = "https://localhost:" + gateway.localPort;
	const config = JSON.parse((await k(["-n", "foc", "get", "configmap", "foc-config", "-o", "json"], { capture: true })).stdout);
	const keys = ["ORIGIN", "FRONTEND_BASE_URL", "PUBLIC_USER_SERVICE_URL", "PUBLIC_SUPPLIER_SERVICE_URL", "PUBLIC_ORDER_SERVICE_URL", "PUBLIC_COMMUNICATION_SERVICE_URL", "PUBLIC_CREDIT_SERVICE_URL", "ORDER_S3_PUBLIC_ENDPOINT"];
	if (keys.some(key => config.data?.[key] !== expected)) {
		die("Gateway forward " + expected + " does not match deployed browser origins. Rerun K8S_HTTPS_PORT=" + gateway.localPort + " make k8s-up (or a full make k8s-deploy) to update application origins before forwarding.");
	}
}

async function superviseForwards(forwards: ForwardSpec[]): Promise<void> {
	if (forwards.length === 0) {
		note("No Kubernetes port-forwards configured; deployment is ready.");
		return;
	}
	await checkContext();
	await checkGatewayOrigin(forwards);
	await preflightForwards(forwards);
	const children: Bun.Subprocess[] = [];
	let signal: NodeJS.Signals | undefined;
	let signalReceived!: () => void;
	const interrupted = new Promise<void>(resolve => { signalReceived = resolve; });
	const onSignal = (received: NodeJS.Signals) => {
		signal = received;
		signalReceived();
		for (const child of children) {
			if (child.exitCode === null) child.kill("SIGTERM");
		}
	};
	// Install handlers before spawning: Ctrl+C during startup must close children too.
	process.once("SIGINT", onSignal);
	process.once("SIGTERM", onSignal);
	try {
		for (const forward of forwards) {
			await startForward(forward, children);
			if (signal) break;
		}
		if (!signal) {
			note("Kubernetes forwards are attached. Press Ctrl+C to stop the forwards; the cluster and its data will remain running.");
			const outcome = await Promise.race([
				interrupted.then(() => ({ type: "signal" as const })),
				...children.map(child => child.exited.then(code => ({ type: "child" as const, code }))),
			]);
			if (outcome.type === "child" && !signal) die("A kubectl port-forward exited unexpectedly with code " + outcome.code + ".");
		}
	} catch (error) {
		if (!signal) throw error;
	} finally {
		await stopForwards(children);
		process.removeListener("SIGINT", onSignal);
		process.removeListener("SIGTERM", onSignal);
	}
	if (signal) process.exitCode = signal === "SIGINT" ? 130 : 143;
}

async function showStatus(): Promise<void> {
	await checkContext();
	await k(["-n", "foc", "get", "pods,jobs,pvc,services", "-o", "wide"]);
	await k(["-n", "istio-system", "get", "pods"]);
	await k(["-n", "istio-ingress", "get", "pods,services"]);
	await k(["-n", "foc", "get", "events", "--field-selector", "type=Warning", "--sort-by=.metadata.creationTimestamp"]);
	if (await Bun.file(join(K8S_TOOLS_DIR, "istioctl")).exists()) {
		await run("istioctl", [
			...(K8S_EXISTING ? ["--context", K8S_CONTEXT] : [
				"--kubeconfig", join(K8S_STATE_DIR, "clusters", K8S_CLUSTER + ".config"),
				"--context", K8S_CONTEXT,
			]),
			"proxy-status",
		]);
	}
}

async function deleteCluster(): Promise<void> {
	if (K8S_EXISTING) die("Cluster deletion is local-only. Unset K8S_CONTEXT and select K8S_CLUSTER explicitly.");
	if (process.env.K8S_CONFIRM_DELETE !== K8S_CLUSTER) {
		die("This deletes cluster " + K8S_CLUSTER + " and its PostgreSQL/Kafka PVC data. Rerun with K8S_CONFIRM_DELETE=" + K8S_CLUSTER + ". Local credential files are retained.");
	}
	if (!/^[a-z0-9][a-z0-9-]*$/.test(K8S_CLUSTER)) die("Invalid kind cluster name.");
	await requireCommands("kind");
	await run("kind", ["delete", "cluster", "--name", K8S_CLUSTER]);
	note("Deleted local cluster " + K8S_CLUSTER + " and cluster data. Credentials remain in " + K8S_STATE_DIR + ".");
}

async function up(): Promise<void> {
	await checkOverlay();
	const forwards = isLocalOverlay() ? configuredForwards() : [];
	await preflightForwards(forwards);
	await installTools();
	if (!K8S_EXISTING) await createLocalCluster();
	await checkContext();
	await installMesh();
	await deployAll();
	if (!isLocalOverlay()) {
		note("Production overlay selected; automatic local port-forwards are disabled.");
		return;
	}
	await superviseForwards(forwards);
}

async function render(stage: string): Promise<void> {
	await checkOverlay();
	if (!["dependencies", "provisioning", "initialization", "applications"].includes(stage)) {
		die("render requires dependencies, provisioning, initialization, or applications.");
	}
	process.stdout.write(await renderStage(stage));
}

export async function runCLI(args: string[] = process.argv.slice(2)): Promise<void> {
	const command = args[0] || "help";
	switch (command) {
		case "up":
			await up();
			return;
		case "deploy":
			if (args[1]) await deployOne(args[1]);
			else await deployAll();
			return;
		case "render":
			await render(args[1] || "");
			return;
		case "access": {
			const port = configuredForwards().find((forward) => forward.name === "HTTPS gateway");
			if (!port) die("Set K8S_HTTPS_PORT to enable the gateway forward.");
			await superviseForwards([port]);
			return;
		}
		case "kafka-ui": {
			const port = configuredForwards().find((forward) => forward.name === "Kafka UI");
			if (!port) die("Set K8S_KAFKA_UI_PORT to enable Kafka UI forwarding.");
			await superviseForwards([port]);
			return;
		}
		case "mailpit": {
			const port = configuredForwards().find((forward) => forward.name === "Mailpit");
			if (!port) die("Set K8S_MAILPIT_PORT to enable Mailpit forwarding.");
			await superviseForwards([port]);
			return;
		}
		case "status":
			await showStatus();
			return;
		case "demo":
			die("D3 demo is unavailable: the integrated registration/credit/order workflow is not verified for this deployment.");
		case "delete-cluster":
			await deleteCluster();
			return;
		default:
			note("Usage: k8s.ts up|deploy [service]|render <stage>|access|status|demo|delete-cluster|kafka-ui|mailpit");
			process.exitCode = 2;
	}
}

if (import.meta.main) {
	try {
		await runCLI();
	} catch (error) {
		console.error("ERROR: " + (error instanceof Error ? error.message : "unexpected error"));
		process.exitCode = error instanceof OperatorError ? 1 : 1;
	}
}
