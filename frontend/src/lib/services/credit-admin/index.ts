import { createClient, type Transport } from '@connectrpc/connect';
import { CreditAdminService } from '$lib/gen/credit/v1/admin_pb';
import { ConnectCreditAdminApi } from './connectCreditAdminApi.ts';

export type * from './creditAdminApi.ts';

export function createCreditAdminApi(transport: Transport): ConnectCreditAdminApi {
	return new ConnectCreditAdminApi(createClient(CreditAdminService, transport));
}
