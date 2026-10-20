import { join } from "node:path";
import { K8S_EXISTING, K8S_ROOT, K8S_STATE_DIR, checkContext, die, note, parsePort, preflightForwards, requireCommands, run } from "./common";
import { startTestForward, stopForward } from "./smoke";

export async function runStorageSmoke(): Promise<void> {
	if (K8S_EXISTING) die("Storage smoke is restricted to the managed local kind cluster.");
	await requireCommands("kubectl", "go");
	await checkContext();
	const forwards = [
		{ name: "storage gateway", namespace: "istio-ingress", service: "istio-ingressgateway", localPort: parsePort("K8S_STORAGE_SMOKE_HTTPS_PORT", process.env.K8S_STORAGE_SMOKE_HTTPS_PORT) ?? 19443, remotePort: 443, scheme: "https" as const },
		{ name: "private storage cleanup", namespace: "foc", service: "floci", localPort: parsePort("K8S_STORAGE_SMOKE_S3_PORT", process.env.K8S_STORAGE_SMOKE_S3_PORT) ?? 19466, remotePort: 4566, scheme: "http" as const },
	];
	await preflightForwards(forwards);
	const children: Bun.Subprocess[] = [];
	try {
		for (const forward of forwards) children.push(await startTestForward(forward));
		await run("go", ["test", "-mod=readonly", "./internal/images", "-run", "^TestMeshObjectStorage$", "-count=1", "-v"], {
			cwd: join(K8S_ROOT, "order-service"),
			env: {
				GOWORK: "off", AWS_REGION: "us-east-1", AWS_ACCESS_KEY_ID: "test", AWS_SECRET_ACCESS_KEY: "test",
				S3_SMOKE_ENDPOINT: "http://127.0.0.1:" + forwards[1]!.localPort,
				S3_SMOKE_PUBLIC_ENDPOINT: "https://localhost:" + forwards[0]!.localPort,
				S3_SMOKE_CA_FILE: join(K8S_STATE_DIR, "secrets/tls.crt"),
			},
		});
		note("PASS signed HTTPS object upload/download, unsigned/tampered and bucket-root rejection, and private cleanup.");
	} finally {
		for (const child of children) await stopForward(child);
	}
}

if (import.meta.main) {
	try { await runStorageSmoke(); }
	catch (error) { console.error("ERROR: " + (error instanceof Error ? error.message : "unexpected error")); process.exitCode = 1; }
}
