import { createClient, type Transport } from '@connectrpc/connect';
import { OrderService } from '$lib/gen/order/v1/order_pb';
import { ConnectOrderApi } from './connectOrderApi.ts';

export type * from './orderApi.ts';

export function createOrderApi(transport: Transport): ConnectOrderApi {
	return new ConnectOrderApi(createClient(OrderService, transport));
}
