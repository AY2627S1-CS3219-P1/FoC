import { createClient, type Transport } from '@connectrpc/connect';
import { CreditAccountService } from '$lib/gen/credit/v1/account_pb';

/** Only the advisory account read needed by creation; reservation stays in Order. */
export function createCreditAccountApi(transport: Transport) {
	const client = createClient(CreditAccountService, transport);
	return {
		async getAvailableCredits(): Promise<bigint> {
			const { account } = await client.getMyAccount({});
			if (!account) throw new Error('Credit response did not contain an account');
			return account.available;
		}
	};
}
