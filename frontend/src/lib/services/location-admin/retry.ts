// Supplier creation keys expire 24 hours after the server records them.
const retryLifetimeMs = 24 * 60 * 60 * 1000;

export class CreationAttemptError extends Error {}

/** Retains the exact create payload after ambiguous failures. Editing needs an explicit new attempt. */
export class CreationAttempt<T> {
	#attempt: { key: string; fingerprint: string; payload: T; startedAt: number } | undefined;
	get pending(): boolean {
		return !!this.#attempt;
	}
	prepare(payload: T): { key: string; payload: T } {
		// Never send an uncertain creation again after the server may have forgotten its key.
		if (this.#attempt && Date.now() - this.#attempt.startedAt >= retryLifetimeMs) {
			throw new CreationAttemptError(
				'This creation retry has expired after 24 hours. Check existing records before explicitly starting a new creation.'
			);
		}
		const fingerprint = JSON.stringify(payload);
		if (this.#attempt && this.#attempt.fingerprint !== fingerprint) {
			throw new CreationAttemptError(
				'A creation may already have succeeded. Retry the original details, or explicitly start a new creation after checking the list.'
			);
		}
		this.#attempt ??= {
			key: crypto.randomUUID(),
			fingerprint,
			payload: structuredClone(payload),
			startedAt: Date.now()
		};
		return this.#attempt;
	}
	clear(): void {
		this.#attempt = undefined;
	}
}
