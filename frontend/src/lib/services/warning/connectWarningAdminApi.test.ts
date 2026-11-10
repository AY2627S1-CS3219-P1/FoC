import { describe, expect, it, vi } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import type { Client } from '@connectrpc/connect';
import { PageInfoSchema } from '$lib/gen/pagination/v1/pagination_pb';
import { AdminAccountWarningSchema, WarningAdminService } from '$lib/gen/user/warning/v1/admin_pb';
import { WarningStatus } from '$lib/gen/user/warning/v1/warning_pb';
import { ConnectWarningAdminApi } from './connectWarningAdminApi.ts';

describe('ConnectWarningAdminApi', () => {
	it("lists a user's warnings with the total", async () => {
		const createdAt = new Date('2026-11-01T02:00:00Z');
		const listWarningsForUser = vi.fn(async () => ({
			warnings: [
				create(AdminAccountWarningSchema, {
					id: 'w1',
					requestId: 'order-1',
					reason: 'Late',
					status: WarningStatus.ACTIVE,
					createdAt: timestampFromDate(createdAt),
					updatedByName: 'Admin'
				})
			],
			pagination: create(PageInfoSchema, { page: 1n, pageSize: 100n, totalItems: 3n, totalPages: 1n })
		}));
		const api = new ConnectWarningAdminApi({ listWarningsForUser } as unknown as Client<typeof WarningAdminService>);

		const page = await api.listWarningsForUser('user-1', { pageSize: 100 });

		expect(listWarningsForUser).toHaveBeenCalledWith({ userId: 'user-1', pagination: { page: 1n, pageSize: 100n } });
		expect(page).toMatchObject({ totalItems: 3, warnings: [{ id: 'w1', reason: 'Late', status: 'active', createdAt }] });
	});
});
