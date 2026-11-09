import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { CreditAdminService } from '$lib/gen/credit/v1/admin_pb';
import { ConnectCreditAdminApi } from './connectCreditAdminApi.ts';

describe('ConnectCreditAdminApi', () => {
	it('returns the credit totals', async () => {
		const client = {
			getCreditTotals: vi.fn(async () => ({ reserved: 40n, returned: 15n, transferred: 120n }))
		} as unknown as Client<typeof CreditAdminService>;

		expect(await new ConnectCreditAdminApi(client).getCreditTotals()).toEqual({
			reserved: 40n,
			returned: 15n,
			transferred: 120n
		});
		expect(client.getCreditTotals).toHaveBeenCalledWith({});
	});
});
