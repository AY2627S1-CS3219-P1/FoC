import { Code, ConnectError } from '@connectrpc/connect';

/** Describes a failed dashboard read without refreshing the session (see CONVENTIONS.md, Connect RPC). */
export function describeDashboardError(cause: unknown, subject: string): string {
	if (cause instanceof ConnectError && cause.code === Code.PermissionDenied) {
		return `You no longer have permission to view ${subject}.`;
	}
	return `We could not load ${subject}. Check your connection and try again.`;
}

/** One card's read. Only the latest request may update the state, so a slow retry cannot overwrite a newer result. */
export class Loadable<T> {
	data = $state<T | undefined>();
	error = $state('');
	loading = $state(false);
	#generation = 0;

	constructor(
		private readonly read: () => Promise<T>,
		private readonly subject: string
	) {}

	async load(): Promise<void> {
		const request = ++this.#generation;
		this.data = undefined;
		this.loading = true;
		this.error = '';
		try {
			const value = await this.read();
			if (request === this.#generation) this.data = value;
		} catch (cause) {
			if (request === this.#generation) this.error = describeDashboardError(cause, this.subject);
		} finally {
			if (request === this.#generation) this.loading = false;
		}
	}
}
