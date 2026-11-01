import type { AccountWarning, WarningApi } from './warningApi.ts';

export class WarningService {
	readonly #api: WarningApi;

	constructor(api: WarningApi) {
		this.#api = api;
	}

	getMyWarnings(): Promise<AccountWarning[]> {
		return this.#api.getMyWarnings();
	}
}
