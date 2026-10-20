#!/usr/bin/env bun
// Bun 1.4.3; no npm dependencies. Missing K8S_BOOTSTRAP_SUPERADMIN_EMAIL skips
// without making requests. Use a disposable, already bootstrapped test account.
// SMOKE_ORIGIN is the browser/Origin-header origin (e.g. https://localhost:5173).
// SMOKE_USER_ORIGIN is the reachable gateway (e.g. https://localhost:18443);
// SMOKE_SUPPLIER_ORIGIN defaults to that same gateway. Compose may override both.
// SMOKE_MAILPIT_URL defaults to http://127.0.0.1:8025. All endpoints must be local.
// Trust the local TLS CA through SMOKE_TLS_CA_FILE; TLS verification stays on.
// Exit 0: passed or explicitly SKIPped. Exit 1: configuration or smoke failure.
import { setTimeout as delay } from 'node:timers/promises';
import { pathToFileURL } from 'node:url';

const authPath = '/user.v1.AuthService/';
const profilePath = '/user.v1.ProfileService/GetMyProfile';
const supplierPath = '/supplier.location.v1.LocationDiscoveryService/ListLocations';
const cookieName = 'foc-refresh-token';
const connectCodes = new Set([
	'canceled', 'unknown', 'invalid_argument', 'deadline_exceeded', 'not_found',
	'already_exists', 'permission_denied', 'resource_exhausted', 'failed_precondition',
	'aborted', 'out_of_range', 'unimplemented', 'internal', 'unavailable',
	'data_loss', 'unauthenticated'
]);

class SmokeError extends Error {}

function requireCondition(condition, message) {
	if (!condition) throw new SmokeError(message);
}

function localOrigin(value, name) {
	let url;
	try {
		url = new URL(value);
	} catch {
		throw new SmokeError(`${name} must be an absolute HTTP(S) origin`);
	}
	requireCondition(
		['http:', 'https:'].includes(url.protocol) &&
		['localhost', '127.0.0.1', '[::1]'].includes(url.hostname) &&
		!url.username && !url.password && !url.search && !url.hash && url.pathname === '/',
		`${name} must be a loopback HTTP(S) origin without credentials, a path, or a query`
	);
	return url.origin;
}

// The application has one host-only cookie scoped to AuthService. Never send it
// to Supplier or profile RPCs. HTTP replay is supported only for local Compose;
// this is a transport smoke, not a browser cookie-policy test.
class AuthCookieJar {
	value = '';

	update(headers) {
		for (const header of headers.getSetCookie()) {
			const [pair, ...attributes] = header.split(';').map(part => part.trim());
			const separator = pair.indexOf('=');
			if (pair.slice(0, separator) !== cookieName) continue;
			const attrs = new Map(attributes.map(attribute => {
				const index = attribute.indexOf('=');
				return index < 0
					? [attribute.toLowerCase(), true]
					: [attribute.slice(0, index).toLowerCase(), attribute.slice(index + 1)];
			}));
			requireCondition(
				attrs.get('path') === authPath && !attrs.has('domain') &&
				attrs.has('secure') && attrs.has('httponly') &&
				String(attrs.get('samesite')).toLowerCase() === 'strict',
				'refresh cookie must be host-only, Secure, HttpOnly, SameSite=Strict and scoped to AuthService'
			);
			const value = pair.slice(separator + 1);
			const expired = attrs.has('max-age')
				? Number(attrs.get('max-age')) <= 0
				: attrs.has('expires') && Date.parse(attrs.get('expires')) <= Date.now();
			this.value = expired ? '' : value;
		}
	}

	header() {
		return this.value ? `${cookieName}=${this.value}` : '';
	}
}

function isRecipient(message, email) {
	return Array.isArray(message.To) && message.To.some(
		address => typeof address.Address === 'string' && address.Address.toLowerCase() === email
	);
}

function loginToken(message, origin) {
	const content = [message.Text, message.HTML].filter(value => typeof value === 'string').join('\n')
		.replace(/&amp;|&#38;|&#x26;/gi, '&');
	for (const match of content.matchAll(/https?:\/\/[^\s<>"']+/g)) {
		let link;
		try {
			link = new URL(match[0]);
		} catch {
			continue;
		}
		if (link.origin !== origin) continue;
		requireCondition(link.pathname !== '/register', 'mail contains a registration link; bootstrap the configured account first');
		if (link.pathname === '/login' && link.searchParams.get('token')) {
			return link.searchParams.get('token');
		}
	}
	throw new SmokeError('new Mailpit message has no login token for SMOKE_ORIGIN');
}

export async function runAuthSmoke(env = process.env, log = console.log) {
	const email = env.K8S_BOOTSTRAP_SUPERADMIN_EMAIL?.trim().toLowerCase();
	if (!email) {
		log('[auth-smoke] SKIP: set K8S_BOOTSTRAP_SUPERADMIN_EMAIL to an already bootstrapped test account');
		return { skipped: true };
	}
	const [bunMajor, bunMinor] = (process.versions.bun || '').split('.').map(Number);
	requireCondition(bunMajor > 1 || (bunMajor === 1 && bunMinor >= 4), 'Bun 1.4 or newer is required');
	requireCondition(env.NODE_TLS_REJECT_UNAUTHORIZED !== '0', 'do not disable TLS verification; set SMOKE_TLS_CA_FILE instead');
	requireCondition(/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email), 'configured bootstrap email is invalid');
	const origin = localOrigin(env.SMOKE_ORIGIN || 'https://localhost:5173', 'SMOKE_ORIGIN');
	const userOrigin = localOrigin(env.SMOKE_USER_ORIGIN || origin, 'SMOKE_USER_ORIGIN');
	const supplierOrigin = localOrigin(env.SMOKE_SUPPLIER_ORIGIN || userOrigin, 'SMOKE_SUPPLIER_ORIGIN');
	const mailpit = localOrigin(env.SMOKE_MAILPIT_URL || 'http://127.0.0.1:8025', 'SMOKE_MAILPIT_URL');
	const timeout = Number(env.SMOKE_TIMEOUT_MS || 90_000);
	requireCondition(Number.isSafeInteger(timeout) && timeout >= 1000 && timeout <= 300_000,
		'SMOKE_TIMEOUT_MS must be an integer between 1000 and 300000');
	const overall = AbortSignal.timeout(timeout);
	const jar = new AuthCookieJar();
	let stage = 'Mailpit preflight';
	let activeSession = false;

	const ca = env.SMOKE_TLS_CA_FILE ? await Bun.file(env.SMOKE_TLS_CA_FILE).text() : undefined;
	async function request(base, path, options = {}, cleanup = false) {
		let response;
		let data;
		try {
			const url = new URL(path, base);
			response = await fetch(url, {
				...options,
				...(url.protocol === 'https:' && ca ? { tls: { ca } } : {}),
				redirect: 'error',
				signal: cleanup ? AbortSignal.timeout(5000)
					: AbortSignal.any([overall, AbortSignal.timeout(10_000)])
			});
			// Read a bounded body; never include response bodies or raw errors in logs.
			const chunks = [];
			let bytes = 0;
			for await (const chunk of response.body) {
				bytes += chunk.length;
				requireCondition(bytes <= 2 * 1024 * 1024, 'response exceeds the smoke body limit');
				chunks.push(chunk);
			}
			data = JSON.parse(Buffer.concat(chunks).toString('utf8'));
		} catch (error) {
			if (error instanceof SmokeError) throw error;
			const tls = /CERT|SELF_SIGNED|ISSUER/.test(error.cause?.code || '');
			throw new SmokeError(tls
				? 'TLS validation failed; set SMOKE_TLS_CA_FILE to the local CA certificate'
				: 'request failed, timed out, redirected, or returned invalid JSON');
		}
		requireCondition(data && typeof data === 'object' && !Array.isArray(data), 'expected a JSON response object');
		return { response, data };
	}

	async function rpc(base, path, body = {}, options = {}) {
		const headers = {
			'Content-Type': 'application/json',
			'Connect-Protocol-Version': '1',
			Origin: origin
		};
		if (options.token) headers.Authorization = `Bearer ${options.token}`;
		const cookie = options.cookie ?? (options.session ? jar.header() : '');
		if (cookie) headers.Cookie = cookie;
		const { response, data } = await request(base, path, {
			method: 'POST', headers, body: JSON.stringify(body)
		}, options.cleanup);
		if (options.unauthenticated) {
			// Supplier's shared HTTP authenticator rejects before the Connect
			// handler and writes the repository's API envelope. Other auth RPCs
			// must still return the Connect unauthenticated error contract.
			const middlewareError = options.middlewareEnvelope && data.data === null &&
				Array.isArray(data.status) && data.status.length > 0 &&
				data.status.every(status => status?.severity === 'error') &&
				data.status.some(status => status.message === 'invalid or missing access token');
			requireCondition(response.status === 401 && (data.code === 'unauthenticated' || middlewareError),
				`expected HTTP 401 with ${options.middlewareEnvelope ? 'an authentication error envelope' : 'Connect unauthenticated'}; received HTTP ${response.status}`);
			return data;
		}
		const code = connectCodes.has(data.code) ? ` (${data.code})` : '';
		requireCondition(response.ok && !data.code, `RPC failed with HTTP ${response.status}${code}`);
		if (options.session) jar.update(response.headers);
		return data;
	}

	function checkUser(user) {
		requireCondition(user?.id && user.email?.toLowerCase() === email,
			'authenticated response does not identify the configured bootstrap account');
		requireCondition(user.role === 'USER_ROLE_SUPER_ADMIN' || user.role === 1,
			'configured bootstrap account is not a super administrator');
	}

	function checkAccessToken(value) {
		requireCondition(typeof value === 'string' && value.split('.').length === 3,
			'authentication response is missing an access JWT');
		return value;
	}

	try {
		// Verify Mailpit is reachable before the only operation that sends mail.
		const inbox = await request(mailpit, '/api/v1/messages?limit=100');
		requireCondition(inbox.response.ok && Array.isArray(inbox.data.messages), 'Mailpit message listing failed');
		const previous = new Set(inbox.data.messages.map(message => message.ID));
		const requestedAt = Date.now();

		stage = 'unauthenticated GetMyProfile';
		await rpc(userOrigin, profilePath, {}, { unauthenticated: true });
		stage = 'unauthenticated Supplier ListLocations';
		await rpc(supplierOrigin, supplierPath, {}, { unauthenticated: true, middlewareEnvelope: true });

		stage = 'RequestLink';
		await rpc(userOrigin, `${authPath}RequestLink`, { email });
		log('[auth-smoke] PASS: RequestLink accepted; waiting for the local Mailpit message');

		stage = 'Mailpit login-link delivery';
		let token;
		while (!token) {
			overall.throwIfAborted();
			const inbox = await request(mailpit, '/api/v1/messages?limit=100');
			requireCondition(inbox.response.ok && Array.isArray(inbox.data.messages), 'Mailpit message listing failed');
			for (const message of inbox.data.messages) {
				if (previous.has(message.ID) || !isRecipient(message, email)) continue;
				// Reject older messages that enter the first page after mailbox deletions.
				const created = Date.parse(message.Created);
				requireCondition(Number.isFinite(created), 'Mailpit message creation timestamp is missing or invalid');
				if (created < requestedAt - 30_000) continue;
				requireCondition(typeof message.ID === 'string' && message.ID, 'Mailpit message ID is missing');
				const detail = await request(mailpit, `/api/v1/message/${encodeURIComponent(message.ID)}`);
				requireCondition(detail.response.ok && isRecipient(detail.data, email), 'Mailpit message recipient did not match');
				token = loginToken(detail.data, origin);
				break;
			}
			if (!token) await delay(1000, undefined, { signal: overall });
		}

		stage = 'Login';
		const login = await rpc(userOrigin, `${authPath}Login`, { token }, { session: true });
		activeSession = Boolean(jar.header());
		requireCondition(activeSession, 'Login did not set a refresh cookie');
		checkUser(login.user);
		let accessToken = checkAccessToken(login.accessToken);
		const userID = login.user.id;
		log('[auth-smoke] PASS: Mailpit magic-link Login and refresh-cookie attributes');

		stage = 'JWT-protected GetMyProfile';
		const profile = await rpc(userOrigin, profilePath, {}, { token: accessToken });
		checkUser(profile.profile);
		requireCondition(profile.profile.id === userID, 'profile belongs to a different user');

		stage = 'JWT-protected Supplier ListLocations';
		const locations = await rpc(supplierOrigin, supplierPath, {
			suppliersOnly: true, page: 1, pageSize: 1
		}, { token: accessToken });
		requireCondition(locations.page === 1 && locations.pageSize === 1 &&
			(locations.locations === undefined || Array.isArray(locations.locations)),
			'ListLocations did not return the expected paginated response');
		log('[auth-smoke] PASS: protected profile and Supplier list via public RPC routes');

		if (env.SMOKE_SHARED_GATEWAY === '1') {
			stage = 'JWT-protected Order ListOpenOrders';
			await rpc(userOrigin, '/order.v1.OrderService/ListOpenOrders', { page: 1, pageSize: 1 }, { token: accessToken });
			stage = 'JWT-protected Communication GetTelegramLink';
			await rpc(userOrigin, '/communication.v1.TelegramLinkService/GetTelegramLink', {}, { token: accessToken });
			log('[auth-smoke] PASS: protected Order and Communication RPCs via the shared gateway');
		}

		stage = 'Refresh';
		const previousCookie = jar.header();
		const refreshed = await rpc(userOrigin, `${authPath}Refresh`, {}, { session: true });
		checkUser(refreshed.user);
		accessToken = checkAccessToken(refreshed.accessToken);
		requireCondition(jar.header() && jar.header() !== previousCookie, 'Refresh did not rotate its cookie');
		const refreshedProfile = await rpc(userOrigin, profilePath, {}, { token: accessToken });
		requireCondition(refreshedProfile.profile?.id === userID, 'refreshed JWT did not authorize the same profile');
		log('[auth-smoke] PASS: refresh-cookie rotation and refreshed JWT');

		stage = 'Logout';
		const loggedOutCookie = jar.header();
		await rpc(userOrigin, `${authPath}Logout`, {}, { session: true });
		activeSession = false;
		requireCondition(!jar.header(), 'Logout did not clear the refresh cookie');
		stage = 'logged-out session rejection';
		await rpc(userOrigin, `${authPath}Refresh`, {}, { cookie: loggedOutCookie, unauthenticated: true });
		await rpc(userOrigin, `${authPath}Refresh`, {}, { unauthenticated: true });
		log('[auth-smoke] PASS: Logout clears the cookie and revokes session refresh');
		return { skipped: false };
	} catch (error) {
		throw new SmokeError(`${stage}: ${error instanceof SmokeError ? error.message : 'operation failed or timed out (details redacted)'}`);
	} finally {
		if (activeSession && jar.header()) {
			try {
				await rpc(userOrigin, `${authPath}Logout`, {}, { session: true, cleanup: true });
			} catch {
				// Do not replace the original failure or print tokens from server errors.
				log('[auth-smoke] Session cleanup failed; the smoke session may remain active');
			}
		}
	}
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
	try {
		await runAuthSmoke();
	} catch (error) {
		console.error(`[auth-smoke] FAIL: ${error instanceof SmokeError ? error.message : 'unexpected error (details redacted)'}`);
		process.exitCode = 1;
	}
}
