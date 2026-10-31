// Gateway smoke verification: the public contract through the local
// port-forward — public health RPCs respond, internal-only RPCs do not, and
// the Services wire to the expected listener ports.
import { join } from "node:path";
import {
	K8S_STATE_DIR,
	ForwardSpec,
	checkContext,
	configuredForwards,
	die,
	requireCommands,
	isLocalOverlay,
	k,
	note,
	preflightForwards,
	run,
	startForward,
	stopForwards,
	waitForGateway,
} from "./common";

async function gatewaySmokeRequest(
	forward: ForwardSpec,
	path: string,
	expectedStatus: number,
	connectRequest = false,
): Promise<string> {
	const args = [
		"--silent",
		"--show-error",
		"--max-time",
		"10",
		"--write-out",
		"\\n%{http_code}",
		"--cacert",
		join(K8S_STATE_DIR, "secrets/tls.crt"),
	];
	if (connectRequest) {
		args.push("--header", "Content-Type: application/json", "--header", "Connect-Protocol-Version: 1", "--data", "{}");
	}
	args.push("https://localhost:" + forward.localPort + path);

	const result = await run("curl", args, { capture: true });
	const statusLine = result.stdout.lastIndexOf("\n");
	if (statusLine < 0) die("Gateway smoke request did not return an HTTP status for " + path + ".");
	const status = Number(result.stdout.slice(statusLine + 1).trim());
	if (status !== expectedStatus) {
		die("Gateway smoke request " + path + " returned HTTP " + status + "; expected " + expectedStatus + ".");
	}
	return result.stdout.slice(0, statusLine);
}

async function assertPublicHealth(forward: ForwardSpec, path: string): Promise<void> {
	const body = await gatewaySmokeRequest(forward, path, 200, true);
	let response: { status?: string };
	try {
		response = JSON.parse(body);
	} catch {
		die("Gateway health RPC " + path + " did not return JSON.");
	}
	if (!response.status || !["ok", "SERVING", "SERVING_STATUS_SERVING"].includes(response.status)) {
		die("Gateway health RPC " + path + " did not report serving.");
	}
}

async function assertServiceTarget(name: string, targetPort: number): Promise<void> {
	const result = await k(["-n", "foc", "get", "service", name, "-o", "json"], { capture: true });
	const service = JSON.parse(result.stdout);
	const port = service.spec.ports.find((item: { port: number }) => item.port === 8080);
	if (port?.targetPort !== targetPort) {
		die("Service " + name + " targets " + port?.targetPort + "; expected application port " + targetPort + ".");
	}
}

// Public contract check: frontend and every public health RPC return 200
// through the gateway; internal-only RPCs (user keys, credit reservations)
// return 404; Service port wiring matches the listener split.
export async function smokeGateway(): Promise<void> {
	if (!isLocalOverlay()) die("The Gateway smoke command currently supports the local overlay only.");
	await checkContext();
	await waitForGateway();
	await requireCommands("curl");
	const forward = configuredForwards().find((item) => item.name === "HTTPS gateway");
	if (!forward) die("Set K8S_HTTPS_PORT to run the Gateway smoke check.");
	await preflightForwards([forward]);
	const children: Bun.Subprocess[] = [];
	try {
		await startForward(forward, children);
		await gatewaySmokeRequest(forward, "/", 200);
		for (const path of [
			"/user.v1.HealthService/Check",
			"/supplier.v1.HealthService/Check",
			"/order.v1.HealthService/Check",
			"/communication.v1.HealthService/Check",
			"/grpc.health.v1.Health/Check",
		]) {
			await assertPublicHealth(forward, path);
		}
		await gatewaySmokeRequest(forward, "/user.v1.PublicKeyService/GetPublicKeys", 404, true);
		await gatewaySmokeRequest(forward, "/credit.v1.CreditReservationService/Reserve", 404, true);
		await assertServiceTarget("user-service-external", 8080);
		await assertServiceTarget("user-service-internal", 8081);
		await assertServiceTarget("credit-service-external", 8080);
		await assertServiceTarget("credit-service-internal", 8081);
		note(
			"Gateway smoke passed: frontend and all public health RPCs respond; User keys and Credit reservations are not public; User and Credit Services target separate public/internal ports.",
		);
	} finally {
		await stopForwards(children);
	}
}
