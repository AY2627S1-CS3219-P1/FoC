import { describe, expect, it, vi } from 'vitest';
import { Code, ConnectError, type Client } from '@connectrpc/connect';
import { AllowedDomainAdminService } from '$lib/gen/user/v1/admin_pb';
import { ConnectAllowedDomainApi } from './connectAllowedDomainApi.ts';

describe('ConnectAllowedDomainApi', () => {
	it('maps the domain list and sends mutation payloads', async () => {
		const client = {
			listAllowedDomains: vi.fn(async () => ({ allowedDomains: [{ $typeName: 'user.v1.AllowedDomain', id: 'domain-id', domain: 'example.com' }] })),
			allowDomain: vi.fn(async () => ({})),
			removeAllowedDomain: vi.fn(async () => ({}))
		};
		const api = new ConnectAllowedDomainApi(client as unknown as Client<typeof AllowedDomainAdminService>);

		expect(await api.listAllowedDomains()).toEqual([{ id: 'domain-id', domain: 'example.com' }]);
		expect(client.listAllowedDomains).toHaveBeenCalledWith({});
		await api.allowDomain(' example.com ');
		expect(client.allowDomain).toHaveBeenCalledWith({ domain: 'example.com' });
		await api.removeAllowedDomain('domain-id');
		expect(client.removeAllowedDomain).toHaveBeenCalledWith({ id: 'domain-id' });
	});

	it.each([
		['listAllowedDomains', Code.PermissionDenied],
		['allowDomain', Code.AlreadyExists],
		['removeAllowedDomain', Code.NotFound]
	] as const)('preserves %s errors for the caller', async (operation, code) => {
		const error = new ConnectError('rejected', code);
		const client = { [operation]: vi.fn(async () => { throw error; }) };
		const api = new ConnectAllowedDomainApi(client as unknown as Client<typeof AllowedDomainAdminService>);
		const result = operation === 'listAllowedDomains' ? api.listAllowedDomains()
			: operation === 'allowDomain' ? api.allowDomain('example.com') : api.removeAllowedDomain('domain-id');
		await expect(result).rejects.toBe(error);
	});
});
