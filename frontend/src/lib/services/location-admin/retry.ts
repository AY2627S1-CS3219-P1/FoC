/** Retains the exact create payload after ambiguous failures. Editing needs an explicit new attempt. */
export class CreationAttempt<T> {
	#attempt: { key: string; fingerprint: string; payload: T } | undefined;
	get pending(): boolean {
		return !!this.#attempt;
	}
	prepare(payload: T): { key: string; payload: T } {
		const fingerprint = JSON.stringify(payload);
		if (this.#attempt && this.#attempt.fingerprint !== fingerprint) {
			throw new Error(
				'A creation may already have succeeded. Retry the original details, or explicitly start a new creation after checking the list.'
			);
		}
		this.#attempt ??= { key: crypto.randomUUID(), fingerprint, payload: structuredClone(payload) };
		return this.#attempt;
	}
	clear(): void {
		this.#attempt = undefined;
	}
}
