import { createClient, type Transport } from '@connectrpc/connect';
import { OrderAdminService } from '$lib/gen/order/v1/admin_pb';
import { ConnectOrderAdminApi } from './connectOrderAdminApi.ts';

export type * from './orderAdminApi.ts';

export function createOrderAdminApi(transport: Transport): ConnectOrderAdminApi {
	return new ConnectOrderAdminApi(createClient(OrderAdminService, transport));
}
