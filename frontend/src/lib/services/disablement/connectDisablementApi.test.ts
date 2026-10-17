import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import type { LocationDisablementService } from '$lib/gen/supplier/location/v1/disablement_pb';
import { ConnectDisablementApi } from './connectDisablementApi';
describe('closure RPC boundary', () => {
	it('preserves omitted starts/ends, retry keys and revision masks', async () => {
		const disablement = { id: 'closure' };
		const client = {
			createDisablement: vi.fn(async () => ({ disablement })),
			updateDisablement: vi.fn(async () => ({ disablement })),
			listDisablements: vi.fn(async () => ({ disablements: [], pageInfo: { page: 3 } })),
			endDisablement: vi.fn(async () => ({ disablement })),
			cancelDisablement: vi.fn(async () => ({ disablement }))
		};
		const api = new ConnectDisablementApi(
			client as unknown as Client<typeof LocationDisablementService>
		);
		await api.create('location', 'key', { reason: 'Renovation' });
		expect(client.createDisablement).toHaveBeenCalledWith({
			locationId: 'location',
			idempotencyKey: 'key',
			reason: 'Renovation'
		});
		await api.update('closure', 3n, { reason: 'Renovation', endsAt: undefined }, ['ends_at']);
		expect(client.updateDisablement).toHaveBeenCalledWith({
			id: 'closure',
			expectedRevision: 3n,
			reason: 'Renovation',
			endsAt: undefined,
			updateMask: { paths: ['ends_at'] }
		});
		await api.list('location', 1, 3);
		await api.end('closure');
		await api.cancel('closure');
		expect(client.listDisablements).toHaveBeenCalledWith({
			locationId: 'location',
			state: 1,
			page: 3,
			pageSize: 20
		});
		expect(client.endDisablement).toHaveBeenCalledWith({ id: 'closure' });
		expect(client.cancelDisablement).toHaveBeenCalledWith({ id: 'closure' });
	});
	it('rejects incomplete responses', async () => {
		const api = new ConnectDisablementApi({ endDisablement: async () => ({}) } as unknown as Client<
			typeof LocationDisablementService
		>);
		await expect(api.end('id')).rejects.toThrow('incomplete');
	});
});
