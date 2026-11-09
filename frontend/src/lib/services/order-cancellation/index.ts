import { createClient, type Transport } from '@connectrpc/connect';
import { OrderService } from '$lib/gen/order/v1/order_pb';
import { CancellationStatus, type Order } from '$lib/gen/order/v1/shared_types_pb';

export interface CancellationSubmission {
	orderId: string;
	expectedRevision: string;
	reason: string;
	key: string;
}

export interface CancellationOutcome {
	order?: Order;
	operationId: string;
	status: CancellationStatus;
}

export interface ParticipantActionsApi {
	cancel(input: CancellationSubmission): Promise<CancellationOutcome>;
	pickup(id: string): Promise<void>;
	receipt(id: string): Promise<void>;
	recordLocation(id: string, location: string): Promise<void>;
}

export function createParticipantActionsApi(transport: Transport): ParticipantActionsApi {
	const client = createClient(OrderService, transport);
	return {
		async cancel(input) {
			const result = await client.cancelOrder({
				id: input.orderId, reason: input.reason, idempotencyKey: input.key,
				expectedRevision: BigInt(input.expectedRevision)
			});
			if (!result.cancellation?.operationId || ![
				CancellationStatus.PENDING, CancellationStatus.SUCCEEDED, CancellationStatus.SUPERSEDED
			].includes(result.cancellation.status)) throw new Error('Cancellation result is unknown. Retry the saved request.');
			return { order: result.order, operationId: result.cancellation.operationId, status: result.cancellation.status };
		},
		async pickup(orderId) { await client.confirmPickup({ orderId }); },
		async receipt(orderId) { await client.confirmReceipt({ orderId }); },
		async recordLocation(orderId, itemLocation) { await client.recordItemLocation({ orderId, itemLocation }); }
	};
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
export const cancellationStorageKey = (caller: string, order: string) => `order-cancellation:${caller}:${order}`;

/** Retain exactly one confirmed intent. Never refresh its revision on retry. */
export function loadCancellation(storage: Storage, caller: string, order: string): CancellationSubmission | null {
	const raw = storage.getItem(cancellationStorageKey(caller, order));
	if (!raw) return null;
	const value = JSON.parse(raw);
	if (!value || value.orderId !== order || typeof value.reason !== 'string' ||
		[...value.reason].length > 500 || typeof value.key !== 'string' || !uuid.test(value.key) ||
		typeof value.expectedRevision !== 'string' || !/^[1-9][0-9]*$/.test(value.expectedRevision) ||
		BigInt(value.expectedRevision) > 9223372036854775807n) throw new Error('Invalid saved cancellation');
	return { orderId: order, reason: value.reason, key: value.key, expectedRevision: value.expectedRevision };
}
