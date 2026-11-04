import type { AllowedDomain, AllowedDomainApi } from './allowedDomainApi.ts';

export class AllowedDomainService {
	readonly #api: AllowedDomainApi;
	#domains = $state<AllowedDomain[]>([]);
	#isLoading = $state(false);
	#hasLoaded = $state(false);
	#loadError = $state<unknown>(null);

	constructor(api: AllowedDomainApi) {
		this.#api = api;
	}

	get domains(): AllowedDomain[] {
		return this.#domains;
	}

	get isLoading(): boolean {
		return this.#isLoading;
	}

	get hasLoaded(): boolean {
		return this.#hasLoaded;
	}

	get loadError(): unknown {
		return this.#loadError;
	}

	async listAllowedDomains(): Promise<AllowedDomain[]> {
		this.#isLoading = true;
		this.#loadError = null;
		try {
			const domains = await this.#api.listAllowedDomains();
			this.#domains = domains;
			this.#hasLoaded = true;
			return domains;
		} catch (error) {
			this.#loadError = error;
			throw error;
		} finally {
			this.#isLoading = false;
		}
	}

	allowDomain(domain: string): Promise<void> {
		return this.#api.allowDomain(domain);
	}

	async removeAllowedDomain(id: string): Promise<void> {
		await this.#api.removeAllowedDomain(id);
		this.#domains = this.#domains.filter((domain) => domain.id !== id);
	}
}
