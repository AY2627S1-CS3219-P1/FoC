import { Code, ConnectError, type Interceptor } from '@connectrpc/connect';
import type { AuthRpc, AuthSession, AuthUser } from './auth-rpc';

export class AuthService {
	#user = $state<AuthUser | null>(null);
	#accessToken = $state<string | null>(null);
	#isLoading = $state(false);
	#sessionRevision = 0;
	#refreshInFlight: Promise<void> | undefined;
	#pendingLogouts = new Set<Promise<void>>();
	#sessionOperations = new Set<Promise<void>>();

	readonly interceptor: Interceptor;

	constructor(private readonly rpc: AuthRpc) {
		this.interceptor = (next) => async (req) => {
			if (this.#accessToken) {
				req.header.set('Authorization', `Bearer ${this.#accessToken}`);
			}

			try {
				return await next(req);
			} catch (error) {
				if (!(error instanceof ConnectError) || error.code !== Code.Unauthenticated) {
					throw error;
				}

				try {
					await this.refresh();
				} catch {
					throw error;
				}

				if (req.stream) {
					throw error;
				}

				const token = this.#accessToken;
				if (!token) {
					this.#clearSession();
					throw error;
				}

				req.header.set('Authorization', `Bearer ${token}`);
				try {
					return await next(req);
				} catch (retryError) {
					if (
						retryError instanceof ConnectError &&
						retryError.code === Code.Unauthenticated
					) {
						this.#clearSession();
					}
					throw retryError;
				}
			}
		};
	}

	get user(): AuthUser | null {
		return this.#user;
	}

	get accessToken(): string | null {
		return this.#accessToken;
	}

	get isAuthenticated(): boolean {
		return this.#accessToken !== null;
	}

	get isLoading(): boolean {
		return this.#isLoading;
	}

	requestLink(email: string): Promise<void> {
		return this.rpc.requestLink(email);
	}

	login(token: string): Promise<void> {
		return this.#runSessionOperation(() => this.rpc.login(token));
	}

	register(token: string, displayName: string): Promise<void> {
		return this.#runSessionOperation(() => this.rpc.register(token, displayName));
	}

	refresh(): Promise<void> {
		if (this.#pendingLogouts.size > 0) {
			return Promise.all([...this.#pendingLogouts]).then(() => undefined);
		}
		if (this.#refreshInFlight) {
			return this.#refreshInFlight;
		}

		this.#setLoading(true);
		const refresh = this.#runSessionOperation(() => this.rpc.refresh(), true).finally(() => {
			if (this.#refreshInFlight === refresh) {
				this.#refreshInFlight = undefined;
			}
			this.#setLoading(false);
		});
		this.#refreshInFlight = refresh;
		return refresh;
	}

	async restoreSession(): Promise<void> {
		try {
			await this.refresh();
		} catch {}
	}

	logout(): Promise<void> {
		this.#sessionRevision += 1;
		let logout: Promise<void>;
		logout = Promise.resolve()
			.then(() => Promise.allSettled([...this.#sessionOperations]))
			.then(() => this.rpc.logout())
			.then(() => this.#clearSession())
			.finally(() => this.#pendingLogouts.delete(logout));
		this.#pendingLogouts.add(logout);
		return logout;
	}

	#runSessionOperation(
		operation: () => Promise<AuthSession>,
		clearOnFailure = false
	): Promise<void> {
		if (this.#pendingLogouts.size > 0) {
			return Promise.allSettled([...this.#pendingLogouts]).then(() =>
				this.#runSessionOperation(operation, clearOnFailure)
			);
		}

		const revision = this.#sessionRevision;
		const request = Promise.resolve()
			.then(operation)
			.then((session) => {
				if (revision === this.#sessionRevision) {
					this.#setSession(session);
				}
			})
			.catch((error: unknown) => {
				if (clearOnFailure && revision === this.#sessionRevision) {
					this.#clearSession();
				}
				throw error;
			});

		this.#sessionOperations.add(request);
		void request.then(
			() => this.#sessionOperations.delete(request),
			() => this.#sessionOperations.delete(request)
		);
		return request;
	}

	#setSession(session: AuthSession): void {
		this.#sessionRevision += 1;
		this.#accessToken = session.accessToken;
		this.#user = session.user;
	}

	#clearSession(): void {
		this.#sessionRevision += 1;
		this.#user = null;
		this.#accessToken = null;
	}

	#setLoading(loading: boolean): void {
		this.#isLoading = loading;
	}
}
