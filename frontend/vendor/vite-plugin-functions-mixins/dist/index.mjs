import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";

//#region src/index.ts
const SOURCE_EXTENSIONS = new Set([
	"css",
	"scss",
	"sass",
	"less",
	"styl",
	"stylus"
]);
const IGNORED_DIRS = new Set([".git", "dist"]);
const MAX_RECURSION_DEPTH = 10;
const OPEN_BRACKETS = new Set([
	"(",
	"{",
	"["
]);
const CLOSE_BRACKETS = new Set([
	")",
	"}",
	"]"
]);
function findMatchingBrace(code, start) {
	let depth = 1;
	for (let i = start; i < code.length; i++) {
		if (OPEN_BRACKETS.has(code[i])) depth++;
		else if (CLOSE_BRACKETS.has(code[i])) depth--;
		if (depth == 0) return i;
	}
	throw new Error(`Unmatched brace at position ${start}`);
}
function splitByComma(str) {
	const parts = [];
	let current = "";
	let depth = 0;
	for (const char of str) {
		if (OPEN_BRACKETS.has(char)) depth++;
		else if (CLOSE_BRACKETS.has(char)) depth--;
		if (char == "," && depth == 0) {
			parts.push(current.trim());
			current = "";
		} else current += char;
	}
	if (current.trim()) parts.push(current.trim());
	return parts;
}
function parseParams(paramStr) {
	const parts = splitByComma(paramStr);
	const params = [];
	let hasContents = false;
	for (const p of parts) {
		if (p.trim() == "@contents") {
			hasContents = true;
			continue;
		}
		const colonIdx = p.indexOf(":");
		const namePart = colonIdx > -1 ? p.slice(0, colonIdx).trim() : p;
		const defaultValue = colonIdx > -1 ? p.slice(colonIdx + 1).trim() : void 0;
		const name = namePart.match(/^(--[\w-]+)/)?.[1] ?? namePart;
		if (name) params.push({
			name,
			defaultValue
		});
	}
	return {
		params,
		hasContents
	};
}
function extractResult(body) {
	const match = body.match(/result\s*:\s*([^;]+)(?:;|$)/);
	if (!match) throw new Error(`Missing "result:" in function body`);
	return match[1].trim();
}
function substituteVars(template, params, args) {
	const argMap = new Map(params.map((param, i) => [param.name, args[i] ?? param.defaultValue]));
	return template.replace(/var\(\s*(--[\w-]+)\s*(?:,\s*[^)]+)?\s*\)/g, (match, varName) => {
		return argMap.get(varName) ?? match;
	});
}
function substituteEnvVars(template, params, args) {
	const argMap = new Map(params.map((param, i) => [param.name, args[i] ?? param.defaultValue]));
	return template.replace(/env\(\s*(--[\w-]+)\s*(?:,\s*([^)]+))?\s*\)/g, (match, varName, fallback) => argMap.get(varName) ?? fallback?.trim() ?? match);
}
function extractFunctions(code, registry) {
	const regex = /@function\s+(--[\w-]+)\s*\(([^)]*)\)(?:\s*returns\s*[^{]+)?\s*\{/g;
	const removals = [];
	for (const match of code.matchAll(regex)) {
		const [fullMatch, name, paramStr] = match;
		const bodyStart = match.index + fullMatch.length;
		const bodyEnd = findMatchingBrace(code, bodyStart);
		const { params } = parseParams(paramStr);
		registry.set(name, {
			name,
			params,
			body: code.slice(bodyStart, bodyEnd)
		});
		removals.push([match.index, bodyEnd + 1]);
	}
	return removals;
}
function extractMixins(code, registry) {
	const regex = /@mixin\s+(--[\w-]+)\s*(\([^)]*\))?\s*\{/g;
	const removals = [];
	for (const match of code.matchAll(regex)) {
		const [fullMatch, name, paramsWithParens] = match;
		const bodyStart = match.index + fullMatch.length;
		const bodyEnd = findMatchingBrace(code, bodyStart);
		const { params, hasContents } = parseParams(paramsWithParens ? paramsWithParens.slice(1, -1) : "");
		registry.set(name, {
			name,
			params,
			body: code.slice(bodyStart, bodyEnd),
			hasContents
		});
		removals.push([match.index, bodyEnd + 1]);
	}
	return removals;
}
function resolveFunctionCalls(code, registry) {
	let result = code;
	for (let i = 0; i < MAX_RECURSION_DEPTH; i++) {
		const next = resolveOnce(result, registry);
		if (next == result) break;
		result = next;
	}
	return result;
}
function resolveOnce(code, registry) {
	const callRegex = /(--[\w-]+)\(/g;
	let result = "";
	let lastIdx = 0;
	for (const match of code.matchAll(callRegex)) {
		const [fullMatch, fnName] = match;
		const start = match.index;
		const argsStart = start + fullMatch.length;
		result += code.slice(lastIdx, start);
		const def = registry.get(fnName);
		if (!def) {
			console.warn(`Unknown function ${fnName}. Registry size is ${registry.size}.`);
			result += fullMatch;
			lastIdx = argsStart;
			continue;
		}
		const argsEnd = findMatchingBrace(code, argsStart);
		const args = splitByComma(code.slice(argsStart, argsEnd));
		result += substituteVars(extractResult(def.body), def.params, args);
		lastIdx = argsEnd + 1;
	}
	return result + code.slice(lastIdx);
}
function resolveMixinApplications(code, registry) {
	let result = code;
	for (let i = 0; i < MAX_RECURSION_DEPTH; i++) {
		const next = resolveMixinsOnce(result, registry);
		if (next == result) break;
		result = next;
	}
	return result;
}
function resolveMixinsOnce(code, registry) {
	const applyRegex = /@apply\s+(--[\w-]+)(?:\s*\(([^)]*)\))?\s*(?:\{([^}]*)\}\s*;?|;)/g;
	let result = "";
	let lastIdx = 0;
	for (const match of code.matchAll(applyRegex)) {
		const [fullMatch, mixinName, argsStr, contentsBlock] = match;
		const start = match.index;
		result += code.slice(lastIdx, start);
		const def = registry.get(mixinName);
		if (!def) {
			result += `/* Unknown mixin: ${mixinName} */`;
			lastIdx = start + fullMatch.length;
			continue;
		}
		const args = argsStr ? splitByComma(argsStr) : [];
		let substituted = substituteEnvVars(def.body, def.params, args);
		if (def.hasContents && contentsBlock !== void 0) substituted = substituted.replace(/@contents\s*(?:\{[^}]*\})?\s*;?/g, contentsBlock.trim());
		else if (def.hasContents) {
			substituted = substituted.replace(/@contents\s*\{([^}]*)\}\s*;?/g, "$1");
			substituted = substituted.replace(/@contents\s*;?/g, "");
		}
		result += substituted;
		lastIdx = start + fullMatch.length;
	}
	return result + code.slice(lastIdx);
}
function mergeRanges(ranges) {
	if (ranges.length === 0) return [];
	const sorted = [...ranges].sort((a, b) => a[0] - b[0]);
	const merged = [];
	let [curStart, curEnd] = sorted[0];
	for (let i = 1; i < sorted.length; i++) {
		const [s, e] = sorted[i];
		if (s <= curEnd) curEnd = Math.max(curEnd, e);
		else {
			merged.push([curStart, curEnd]);
			[curStart, curEnd] = [s, e];
		}
	}
	merged.push([curStart, curEnd]);
	return merged;
}
function transformExcludingRanges(code, exclude, mixinRegistry, functionRegistry, strip) {
	const ranges = mergeRanges(exclude);
	let result = "";
	let cursor = 0;
	for (const [start, end] of ranges) {
		let processedChunk = resolveMixinApplications(code.slice(cursor, start), mixinRegistry);
		processedChunk = resolveFunctionCalls(processedChunk, functionRegistry);
		result += processedChunk;
		if (strip) {
			const blank = code.slice(start, end).replace(/[^\n]/g, "");
			result += blank;
		} else result += code.slice(start, end);
		cursor = end;
	}
	let processedTail = resolveMixinApplications(code.slice(cursor), mixinRegistry);
	processedTail = resolveFunctionCalls(processedTail, functionRegistry);
	result += processedTail;
	return result;
}
async function* findStyleFiles(dir, skipNodeModules = true) {
	const entries = await readdir(dir, { withFileTypes: true });
	for (const entry of entries) {
		if (IGNORED_DIRS.has(entry.name)) continue;
		if (skipNodeModules && entry.name == "node_modules") continue;
		const fullPath = join(dir, entry.name);
		if (entry.isDirectory()) yield* findStyleFiles(fullPath, skipNodeModules);
		else if (SOURCE_EXTENSIONS.has(entry.name.split(".").at(-1))) yield fullPath;
	}
}
const functionsMixins = ({ deps = [], strip = true } = {}) => {
	const functionRegistry = /* @__PURE__ */ new Map();
	const mixinRegistry = /* @__PURE__ */ new Map();
	let root;
	const depPaths = deps.map((d) => join("node_modules", d));
	const depPathsTerminated = deps.map((d) => join("node_modules", d, ""));
	function processCode(code, strip$1) {
		const functionRanges = extractFunctions(code, functionRegistry);
		const mixinRanges = extractMixins(code, mixinRegistry);
		return transformExcludingRanges(code, [...functionRanges, ...mixinRanges], mixinRegistry, functionRegistry, strip$1);
	}
	const stylePreprocessor = ({ content, filename }) => {
		if (!depPathsTerminated.find((d) => filename.includes(d))) return;
		return { code: processCode(content, strip) };
	};
	return {
		name: "vite-plugin-functions-mixins",
		async buildStart() {
			const includeScans = [...depPaths, root].map(async (p) => {
				for await (const file of findStyleFiles(p)) {
					const content = await readFile(file, "utf-8");
					extractFunctions(content, functionRegistry);
					extractMixins(content, mixinRegistry);
				}
			});
			await Promise.all(includeScans);
		},
		configResolved(config) {
			root = config.root;
			const sveltePlugin = config.plugins.find((p) => p.name == "vite-plugin-svelte:config") || config.plugins.find((p) => p.name == "vite-plugin-svelte");
			if (!sveltePlugin?.api?.options) return;
			const opts = sveltePlugin.api.options;
			opts.preprocess = [...Array.isArray(opts.preprocess) ? opts.preprocess : opts.preprocess ? [opts.preprocess] : [], { style: stylePreprocessor }];
		},
		transform(code, id) {
			const ext = id.split("?")[1]?.split("lang.")[1] || id.split("?")[0].split(".").at(-1);
			if (SOURCE_EXTENSIONS.has(ext)) return {
				code: processCode(code, strip),
				map: null
			};
			else if (ext == "svelte" || ext == "vue") {
				const styleStart = code.indexOf("<style");
				if (styleStart == -1) return;
				return {
					code: code.slice(0, styleStart) + processCode(code.slice(styleStart), strip),
					map: null
				};
			}
		}
	};
};

//#endregion
export { functionsMixins };