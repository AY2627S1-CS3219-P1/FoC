import { writable } from 'svelte/store';

export type SnackbarSeverity = 'info' | 'success' | 'error' | 'warning';

export interface SnackbarOptions {
	severity?: SnackbarSeverity;
	actions?: Record<string, () => void>;
	/** Duration in milliseconds; use -1 to keep the snackbar open until dismissed. */
	timeout?: number;
}

export interface SnackbarNotice {
	id: number;
	message: string;
	severity: SnackbarSeverity;
	actions: Record<string, () => void>;
}

export class SnackbarService {
	readonly notice = writable<SnackbarNotice | null>(null);
	#nextID = 0;
	#timeout: ReturnType<typeof setTimeout> | undefined;

	show(message: string, options: SnackbarOptions = {}): void {
		if (this.#timeout) clearTimeout(this.#timeout);
		this.notice.set({
			id: ++this.#nextID,
			message,
			severity: options.severity ?? 'info',
			actions: options.actions ?? {}
		});

		const timeout = options.timeout ?? 5000;
		if (timeout > 0) this.#timeout = setTimeout(() => this.dismiss(), timeout);
	}

	dismiss(): void {
		if (this.#timeout) clearTimeout(this.#timeout);
		this.#timeout = undefined;
		this.notice.set(null);
	}
}
