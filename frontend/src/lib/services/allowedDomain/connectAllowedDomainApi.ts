import type { Client } from '@connectrpc/connect';
import { AllowedDomainAdminService } from '$lib/gen/user/v1/admin_pb';
import type { AllowedDomain, AllowedDomainApi } from './allowedDomainApi.ts';

export class ConnectAllowedDomainApi implements AllowedDomainApi {
	readonly #client: Client<typeof AllowedDomainAdminService>;

	constructor(client: Client<typeof AllowedDomainAdminService>) {
		this.#client = client;
	}

	async listAllowedDomains(): Promise<AllowedDomain[]> {
		const response = await this.#client.listAllowedDomains({});
		return response.allowedDomains.map(({ id, domain }) => ({ id, domain }));
	}

	async allowDomain(domain: string): Promise<void> {
		await this.#client.allowDomain({ domain: domain.trim() });
	}

	async removeAllowedDomain(id: string): Promise<void> {
		await this.#client.removeAllowedDomain({ id });
	}
}
