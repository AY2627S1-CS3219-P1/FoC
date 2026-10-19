import { randomUUID } from "node:crypto";
import { rm } from "node:fs/promises";
import { join } from "node:path";
import {
	K8S_APPS,
	K8S_CLUSTER,
	K8S_CONTEXT,
	K8S_EXISTING,
	K8S_STATE_DIR,
	K8S_TIMEOUT,
	checkContext,
	die,
	k,
	kubectlPrefix,
	note,
	requireCommands,
	run,
} from "./common";

export async function runPersistenceSmoke(): Promise<void> {
	await requireCommands("kubectl");
	if (K8S_EXISTING || K8S_CONTEXT !== "kind-" + K8S_CLUSTER) {
		die("Persistence smoke is restricted to the managed local kind cluster.");
	}
	await checkContext();
	note("Restarting local PostgreSQL and Kafka pods without deleting PVCs. Applications may briefly become unready.");
	const query = "SELECT count(*)::text || ':' || coalesce(min(user_id::text), '') FROM admin_bootstrap;";
	const database = (await k([
		"-n", "foc", "get", "secret", "user-db", "-o", "go-template={{.data.DB_NAME | base64decode}}",
	], { capture: true })).stdout.trim();
	const operator = (await k([
		"-n", "foc", "get", "secret", "postgres-admin", "-o",
		"go-template={{.data.POSTGRES_USER | base64decode}}",
	], { capture: true })).stdout.trim();
	const postgresArgs = ["-n", "foc", "exec", "statefulset/postgres", "-c", "postgres", "--", "psql", "-X", "-U", operator, "-d", database, "-Atc", query];
	const before = (await k(postgresArgs, { capture: true })).stdout.trim();
	const suffix = randomUUID().replaceAll("-", "");
	const topic = "foc.infrastructure-smoke-" + Date.now() + "-" + suffix.slice(0, 8);
	const record = "persistence-" + suffix;
	const heap = "KAFKA_HEAP_OPTS=-Xms32m -Xmx128m";
	const kafka = async (args: string[], options: { capture?: boolean; allowFailure?: boolean; stdinFile?: string } = {}) =>
		run("kubectl", [
			...kubectlPrefix(),
			"-n", "foc", "exec", "statefulset/kafka", "-c", "kafka", "--",
			"env", heap, ...args,
		], options);

	const topicCommand = [
		"/opt/kafka/bin/kafka-topics.sh", "--bootstrap-server", "127.0.0.1:9092",
		"--create", "--topic", topic, "--partitions", "1", "--replication-factor", "1",
	];
	await kafka(topicCommand);
	const inputPath = join(K8S_STATE_DIR, "persistence-smoke-" + suffix + ".txt");
	await Bun.write(inputPath, record + "\n");
	try {
		await kafka([
			"/opt/kafka/bin/kafka-console-producer.sh", "--bootstrap-server", "127.0.0.1:9092",
			"--topic", topic, "--command-property", "acks=all",
		], { capture: true, stdinFile: inputPath });
	} finally {
		await rm(inputPath, { force: true });
	}

	await k(["-n", "foc", "rollout", "restart", "statefulset/postgres", "statefulset/kafka"]);
	await k(["-n", "foc", "rollout", "status", "statefulset/postgres", "--timeout=" + K8S_TIMEOUT]);
	await k(["-n", "foc", "rollout", "status", "statefulset/kafka", "--timeout=" + K8S_TIMEOUT]);
	const after = (await k(postgresArgs, { capture: true })).stdout.trim();
	if (after !== before) die("FAIL: PostgreSQL bootstrap state changed after pod replacement.");
	const received = await kafka([
		"/opt/kafka/bin/kafka-console-consumer.sh", "--bootstrap-server", "127.0.0.1:9092",
		"--topic", topic, "--from-beginning", "--max-messages", "1", "--timeout-ms", "45000",
	], { capture: true });
	if (received.stdout.trim() !== record) die("FAIL: Kafka did not retain the acknowledged record.");
	for (const app of K8S_APPS) {
		await k(["-n", "foc", "rollout", "status", "deployment/" + app, "--timeout=" + K8S_TIMEOUT]);
	}
	note("PASS: PostgreSQL bootstrap identity and Kafka record survived pod replacement; PVCs retained. Test topic " + topic + " retained.");
}

if (import.meta.main) {
	try {
		await runPersistenceSmoke();
	} catch (error) {
		console.error("ERROR: " + (error instanceof Error ? error.message : "unexpected error"));
		process.exitCode = 1;
	}
}
