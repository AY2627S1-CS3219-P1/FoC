export interface SnackbarOptions {
	actions?: Record<string, () => void>;
	/** Duration in milliseconds; use -1 to keep the snackbar open until dismissed. */
	timeout?: number;
}

type SnackbarTrigger = (
	message: string,
	actions: Record<string, () => void> | undefined,
	closable: boolean,
	timeout: number
) => void;

export class SnackbarService {
	readonly #trigger: SnackbarTrigger;

	constructor(trigger: SnackbarTrigger) {
		this.#trigger = trigger;
	}

	show(message: string, options: SnackbarOptions = {}): void {
		this.#trigger(message, options.actions, true, options.timeout ?? 5000);
	}
}
