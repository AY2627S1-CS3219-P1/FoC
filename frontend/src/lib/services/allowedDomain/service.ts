import type { AllowedDomain, AllowedDomainApi } from './allowedDomainApi.ts';

export class AllowedDomainService {
	readonly #api: AllowedDomainApi;

	constructor(api: AllowedDomainApi) {
		this.#api = api;
	}

	listAllowedDomains(): Promise<AllowedDomain[]> {
		return this.#api.listAllowedDomains();
	}

	allowDomain(domain: string): Promise<void> {
		return this.#api.allowDomain(domain);
	}

	removeAllowedDomain(id: string): Promise<void> {
		return this.#api.removeAllowedDomain(id);
	}
}
