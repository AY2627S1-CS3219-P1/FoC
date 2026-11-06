import type { WarningApi, WarningPage, WarningQuery } from './warningApi.ts';

export class WarningService {
	readonly #api: WarningApi;

	constructor(api: WarningApi) {
		this.#api = api;
	}

	listMyWarnings(query?: WarningQuery): Promise<WarningPage> {
		return this.#api.listMyWarnings(query);
	}
}
