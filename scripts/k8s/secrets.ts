import { chmod, mkdir, rename, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
	K8S_STATE_DIR,
	applyManifest,
	die,
	kubectlPrefix,
	localOrigin,
	note,
	prepareState,
	requireCommands,
	run,
} from "./common";

const secretDirectory = join(K8S_STATE_DIR, "secrets");
const lockDirectory = join(secretDirectory, ".generating");

async function persistValue(name: string, value: string, allowEmpty = false): Promise<void> {
	const path = join(secretDirectory, name);
	if (await Bun.file(path).exists()) {
		if (!(await Bun.file(path).size) && !allowEmpty) {
			die("Empty credential " + path + "; restore it rather than rotating a database password.");
		}
		return;
	}
	await writeFile(path, value, { mode: 0o600, flag: "wx" });
}

async function applySecret(namespace: string, name: string, options: string[]): Promise<void> {
	const manifest = await run(
		"kubectl",
		[
			...kubectlPrefix(),
			"-n", namespace,
			"create", "secret", "generic", name,
			...options,
			"--dry-run=client", "-o", "yaml",
		],
		{ capture: true },
	);
	const path = join(secretDirectory, "." + name + ".yaml");
	await writeFile(path, manifest.stdout, { mode: 0o600 });
	try {
		await applyManifest(path);
	} finally {
		await rm(path, { force: true });
	}
}

export async function generateLocalSecrets(): Promise<void> {
	await requireCommands("kubectl", "openssl");
	await prepareState();
	await mkdir(secretDirectory, { recursive: true, mode: 0o700 });
	await chmod(K8S_STATE_DIR, 0o700);
	await chmod(secretDirectory, 0o700);
	try {
		await mkdir(lockDirectory, { mode: 0o700 });
	} catch {
		die("Another secret generator holds " + lockDirectory + "; inspect before removing a stale lock.");
	}

	try {
		for (const name of ["postgres-password", "user-password", "supplier-password", "order-password", "communication-password", "kafka-ui-password"]) {
			const password = await run("openssl", ["rand", "-hex", "32"], { capture: true });
			await persistValue(name, password.stdout.trim());
		}
		for (const [name, value] of [
			["postgres-user", "foc"],
			["kafka-ui-user", "operator"],
			["SMTP_ADDR", "mailpit:1025"],
			["SMTP_FROM", "FoC <no-reply@foc.local>"],
		]) {
			await persistValue(name, value);
		}
		await persistValue("SMTP_USERNAME", "", true);
		await persistValue("SMTP_PASSWORD", "", true);
		await persistValue("TELEGRAM_BOT_TOKEN", process.env.K8S_TELEGRAM_BOT_TOKEN || "", true);

		const privateKey = join(secretDirectory, "jwt-signing-private.pem");
		if (!(await Bun.file(privateKey).exists())) {
			const temporaryKey = join(lockDirectory, "jwt.pem");
			await run("openssl", [
				"genpkey", "-algorithm", "EC", "-pkeyopt", "ec_paramgen_curve:P-256", "-out", temporaryKey,
			]);
			await rename(temporaryKey, privateKey);
		}
		await run("openssl", ["pkey", "-in", privateKey, "-check", "-noout"], { capture: true });

		const tlsKey = join(secretDirectory, "tls.key");
		const tlsCert = join(secretDirectory, "tls.crt");
		if (!(await Bun.file(tlsKey).exists()) && !(await Bun.file(tlsCert).exists())) {
			const temporaryKey = join(lockDirectory, "tls.key");
			const temporaryCert = join(lockDirectory, "tls.crt");
			const config = join(lockDirectory, "tls.cnf");
			await writeFile(config, "[req]\nprompt=no\ndistinguished_name=dn\nx509_extensions=ext\n[dn]\nCN=localhost\n[ext]\nbasicConstraints=critical,CA:TRUE\nsubjectAltName=DNS:localhost,IP:127.0.0.1\n", { mode: 0o600 });
			await run("openssl", [
				"req", "-x509", "-newkey", "rsa:3072", "-sha256", "-nodes", "-days", "365",
				"-config", config,
				"-keyout", temporaryKey, "-out", temporaryCert,
			], { capture: true });
			await rename(temporaryKey, tlsKey);
			await rename(temporaryCert, tlsCert);
		}
		if (!(await Bun.file(tlsKey).size) || !(await Bun.file(tlsCert).size)) {
			die("Incomplete local TLS keypair; restore its matching files.");
		}
		await run("openssl", ["x509", "-in", tlsCert, "-noout", "-checkend", "0"], { capture: true });
		const certificatePublicKey = await run("openssl", ["x509", "-in", tlsCert, "-pubkey", "-noout"], { capture: true });
		const privatePublicKey = await run("openssl", ["pkey", "-in", tlsKey, "-pubout"], { capture: true });
		if (certificatePublicKey.stdout !== privatePublicKey.stdout) {
			die("Local TLS certificate and key do not match.");
		}

		for (const service of ["user", "supplier", "order", "communication"]) {
			await persistValue(service + "-db-user", service + "_app");
			await persistValue(service + "-db-name", service + "_db");
			const username = (await Bun.file(join(secretDirectory, service + "-db-user")).text()).trim();
			const database = (await Bun.file(join(secretDirectory, service + "-db-name")).text()).trim();
			const password = (await Bun.file(join(secretDirectory, service + "-password")).text()).trim();
			const databaseURL = "postgres://" + username + ":" + password + "@postgres:5432/" + database + "?sslmode=disable";
			await persistValue(service + "-database-url", databaseURL);
		}

		await applySecret("foc", "postgres-admin", [
			"--from-file=POSTGRES_USER=" + join(secretDirectory, "postgres-user"),
			"--from-file=POSTGRES_PASSWORD=" + join(secretDirectory, "postgres-password"),
		]);
		for (const service of ["user", "supplier", "order", "communication"]) {
			await applySecret("foc", service + "-db", [
				"--from-file=DB_USER=" + join(secretDirectory, service + "-db-user"),
				"--from-file=DB_NAME=" + join(secretDirectory, service + "-db-name"),
				"--from-file=DB_PASSWORD=" + join(secretDirectory, service + "-password"),
				"--from-file=DATABASE_URL=" + join(secretDirectory, service + "-database-url"),
			]);
		}
		await applySecret("foc", "jwt-signing", [
			"--from-file=jwt-signing-private.pem=" + privateKey,
		]);
		await applySecret("foc", "smtp", [
			"--from-file=" + join(secretDirectory, "SMTP_ADDR"),
			"--from-file=" + join(secretDirectory, "SMTP_FROM"),
			"--from-file=" + join(secretDirectory, "SMTP_USERNAME"),
			"--from-file=" + join(secretDirectory, "SMTP_PASSWORD"),
		]);
		await applySecret("foc", "telegram", ["--from-file=" + join(secretDirectory, "TELEGRAM_BOT_TOKEN")]);
		await applySecret("foc", "object-storage", ["--from-literal=AWS_ACCESS_KEY_ID=test", "--from-literal=AWS_SECRET_ACCESS_KEY=test"]);
		await applySecret("foc", "kafka-ui-auth", [
			"--from-file=SPRING_SECURITY_USER_NAME=" + join(secretDirectory, "kafka-ui-user"),
			"--from-file=SPRING_SECURITY_USER_PASSWORD=" + join(secretDirectory, "kafka-ui-password"),
		]);
		await applySecret("istio-ingress", "foc-local-tls", [
			"--type=kubernetes.io/tls",
			"--from-file=tls.crt=" + tlsCert,
			"--from-file=tls.key=" + tlsKey,
		]);
		note("Local secrets preserved in " + secretDirectory + ". Trust tls.crt for " + localOrigin() + ".");
	} finally {
		await rm(lockDirectory, { recursive: true, force: true });
	}
}

if (import.meta.main) {
	try {
		await generateLocalSecrets();
	} catch (error) {
		console.error("ERROR: " + (error instanceof Error ? error.message : "unexpected error"));
		process.exitCode = 1;
	}
}
