import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import type { LocationAdditionRequestService } from '$lib/gen/supplier/location/v1/addition_request_pb';
import { ConnectAdditionRequestApi } from './connectRequestApi';
describe('addition request RPC boundary', () => {
	it('filters the paginated queue and trims the review reason', async () => {
		const request = { id: 'proposal' };
		const client = {
			listLocationAdditionRequests: vi.fn(async () => ({
				requests: [request],
				pageInfo: { page: 2 }
			})),
			getLocationAdditionRequest: vi.fn(async () => ({ request })),
			approveLocationAdditionRequest: vi.fn(async () => ({ request })),
			rejectLocationAdditionRequest: vi.fn(async () => ({ request }))
		};
		const api = new ConnectAdditionRequestApi(
			client as unknown as Client<typeof LocationAdditionRequestService>
		);
		await api.list(1, 2);
		await api.get('proposal');
		await api.approve('proposal', 9007199254740993n);
		await api.reject('proposal', ' Not on campus ', 3n);
		expect(client.listLocationAdditionRequests).toHaveBeenCalledWith({
			status: 1,
			page: 2,
			pageSize: 20
		});
		expect(client.approveLocationAdditionRequest).toHaveBeenCalledWith({
			id: 'proposal',
			expectedRevision: 9007199254740993n
		});
		expect(client.rejectLocationAdditionRequest).toHaveBeenCalledWith({
			id: 'proposal',
			reviewNote: 'Not on campus',
			expectedRevision: 3n
		});
	});
	it('rejects incomplete responses', async () => {
		const client = {
			getLocationAdditionRequest: async () => ({}),
			listLocationAdditionRequests: async () => ({ requests: [] })
		} as unknown as Client<typeof LocationAdditionRequestService>;
		const api = new ConnectAdditionRequestApi(client);
		await expect(api.get('id')).rejects.toThrow('incomplete');
		await expect(api.list(1, 1)).rejects.toThrow('incomplete');
	});
});
