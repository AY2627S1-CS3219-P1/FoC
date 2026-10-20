import { access, chmod, copyFile, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { constants } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import {
	K8S_ISTIO_VERSION,
	K8S_TOOLS_DIR,
	die,
	note,
	hasIstioCharts,
	run,
} from "./common";

const hostOS = process.platform === "darwin" ? "darwin" : process.platform === "linux" ? "linux" : null;
const hostArch = process.arch === "x64" ? "amd64" : process.arch === "arm64" ? "arm64" : null;
if (!hostOS || !hostArch) die("Supported Kubernetes tool hosts: Linux and macOS, amd64 and arm64.");

async function sha256(path: string): Promise<string> {
	const data = await Bun.file(path).arrayBuffer();
	return createHash("sha256").update(Buffer.from(data)).digest("hex");
}

async function download(url: string, path: string): Promise<void> {
	const response = await fetch(url, { redirect: "follow" });
	if (!response.ok) die("Download failed (" + response.status + "): " + url);
	await Bun.write(path, response);
}

async function verifyDownload(url: string, checksumURL: string, destination: string): Promise<void> {
	const checksumResponse = await fetch(checksumURL, { redirect: "follow" });
	if (!checksumResponse.ok) die("Could not fetch published checksum: " + checksumURL);
	const checksumText = await checksumResponse.text();
	const expected = checksumText.trim().split(/\s+/)[0]?.toLowerCase();
	if (!expected || !/^[a-f0-9]{64}$/.test(expected)) die("Invalid published checksum from " + checksumURL);
	if (await Bun.file(destination).exists() && await sha256(destination) === expected) return;
	await download(url, destination);
	if (await sha256(destination) !== expected) die("Checksum mismatch: " + url);
}

async function receiptValid(name: string, version: string): Promise<boolean> {
	const binary = join(K8S_TOOLS_DIR, name);
	const receipt = join(K8S_TOOLS_DIR, name + ".verified");
	if (!(await Bun.file(binary).exists()) || !(await Bun.file(receipt).exists())) return false;
	try {
		await access(binary, constants.X_OK);
	} catch {
		return false;
	}
	const contents = (await Bun.file(receipt).text()).trim();
	const parts = contents.split(/\s+/);
	return parts.length === 4 &&
		parts[0] === version &&
		parts[1] === hostOS &&
		parts[2] === hostArch &&
		parts[3] === await sha256(binary);
}

async function installBinary(name: string, version: string, source: string): Promise<void> {
	const destination = join(K8S_TOOLS_DIR, name);
	await copyFile(source, destination);
	await chmod(destination, 0o755);
	await writeFile(
		join(K8S_TOOLS_DIR, name + ".verified"),
		version + " " + hostOS + " " + hostArch + " " + await sha256(destination) + "\n",
		{ mode: 0o600 },
	);
}

async function installReleaseBinary(
	name: string,
	version: string,
	url: string,
	checksumURL: string,
	temporaryDirectory: string,
	sourceName = name,
): Promise<void> {
	if (await receiptValid(name, version)) return;
	const archive = join(temporaryDirectory, sourceName);
	await verifyDownload(url, checksumURL, archive);
	await installBinary(name, version, archive);
}

async function installPinnedTools(temporaryDirectory: string): Promise<void> {
	await mkdir(K8S_TOOLS_DIR, { recursive: true });

	const kindURL = "https://github.com/kubernetes-sigs/kind/releases/download/v0.33.0/kind-" + hostOS + "-" + hostArch;
	await installReleaseBinary("kind", "0.33.0", kindURL, kindURL + ".sha256sum", temporaryDirectory);

	const kubectlURL = "https://dl.k8s.io/release/v1.35.0/bin/" + hostOS + "/" + hostArch + "/kubectl";
	await installReleaseBinary("kubectl", "1.35.0", kubectlURL, kubectlURL + ".sha256", temporaryDirectory);

	const helmArchiveName = "helm-v3.20.1-" + hostOS + "-" + hostArch + ".tar.gz";
	const helmURL = "https://get.helm.sh/" + helmArchiveName;
	if (!(await receiptValid("helm", "3.20.1"))) {
		const archive = join(temporaryDirectory, helmArchiveName);
		await verifyDownload(helmURL, helmURL + ".sha256sum", archive);
		await run("tar", ["-xzf", archive, "-C", temporaryDirectory, hostOS + "-" + hostArch + "/helm"]);
		await installBinary("helm", "3.20.1", join(temporaryDirectory, hostOS + "-" + hostArch, "helm"));
	}

	if (!(await receiptValid("istioctl", K8S_ISTIO_VERSION)) || !(await hasIstioCharts())) {
		const istioOS = hostOS === "darwin" ? "osx" : hostOS;
		const archiveName = "istio-" + K8S_ISTIO_VERSION + "-" + istioOS + "-" + hostArch + ".tar.gz";
		const url = "https://github.com/istio/istio/releases/download/" + K8S_ISTIO_VERSION + "/" + archiveName;
		const archive = join(K8S_TOOLS_DIR, "istio.tar.gz");
		await verifyDownload(url, url + ".sha256", archive);
		await run("tar", ["-xzf", archive, "-C", K8S_TOOLS_DIR]);
		await installBinary(
			"istioctl",
			K8S_ISTIO_VERSION,
			join(K8S_TOOLS_DIR, "istio-" + K8S_ISTIO_VERSION, "bin/istioctl"),
		);
	}

	note("Verified Kubernetes tools installed in " + K8S_TOOLS_DIR);
}

export async function installTools(): Promise<void> {
	await mkdir(K8S_TOOLS_DIR, { recursive: true });
	const temporaryDirectory = await mkdtemp(join(K8S_TOOLS_DIR, ".download-"));
	try {
		await installPinnedTools(temporaryDirectory);
	} finally {
		await rm(temporaryDirectory, { recursive: true, force: true });
	}
}

if (import.meta.main) {
	await installTools();
}
