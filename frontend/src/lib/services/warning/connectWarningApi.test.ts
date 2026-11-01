import { create } from '@bufbuild/protobuf';
import type { Client } from '@connectrpc/connect';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { describe, expect, it, vi } from 'vitest';
import {
	AccountWarningSchema,
	WarningService as WarningServiceDefinition,
	WarningStatus
} from '$lib/gen/user/v1/warning_pb';
import { ConnectWarningApi } from './connectWarningApi.ts';

describe('ConnectWarningApi', () => {
	it('maps the current user warning list', async () => {
		const createdAt = new Date('2026-10-19T10:00:00.000Z');
		const removedAt = new Date('2026-10-20T10:00:00.000Z');
		const warning = create(AccountWarningSchema, {
			id: 'warning-id',
			requestId: 'request-id',
			reason: 'late cancellation',
			status: WarningStatus.REMOVED,
			createdAt: timestampFromDate(createdAt),
			removedAt: timestampFromDate(removedAt),
			removedReason: 'appeal overturned'
		});
		const client = {
			getMyWarnings: vi.fn(async () => ({ warnings: [warning] }))
		} as unknown as Client<typeof WarningServiceDefinition>;
		const api = new ConnectWarningApi(client);

		expect(await api.getMyWarnings()).toEqual([{
			id: 'warning-id',
			requestId: 'request-id',
			reason: 'late cancellation',
			status: 'removed',
			createdAt,
			removedAt,
			removedReason: 'appeal overturned'
		}]);
		expect(client.getMyWarnings).toHaveBeenCalledWith({});
	});
});
