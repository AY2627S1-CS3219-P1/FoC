import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
import { OrderStatus, ParticipantRole, type Order } from '$lib/gen/order/v1/shared_types_pb';

export const orderStatuses: [OrderStatus, string][] = [
	[OrderStatus.PENDING_RESERVATION, 'Pending reservation'],
	[OrderStatus.AWAITING_COURIER, 'Awaiting courier'],
	[OrderStatus.AWAITING_PICKUP, 'Awaiting pickup'],
	[OrderStatus.DELIVERING, 'Delivering'], [OrderStatus.DELIVERED, 'Delivered'],
	[OrderStatus.COMPLETED, 'Completed'], [OrderStatus.CANCELLED, 'Cancelled'],
	[OrderStatus.EXPIRED, 'Expired'], [OrderStatus.FAILED, 'Failed'], [OrderStatus.REJECTED, 'Rejected']
];

export function statusLabel(status: OrderStatus): string {
	return orderStatuses.find(([value]) => value === status)?.[1] ?? 'Unknown status';
}

export function isTerminal(status: OrderStatus): boolean {
	return [OrderStatus.COMPLETED, OrderStatus.CANCELLED, OrderStatus.EXPIRED, OrderStatus.FAILED, OrderStatus.REJECTED].includes(status);
}

export function dateLabel(value?: Timestamp): string {
	return value ? timestampDate(value).toLocaleString('en-SG', { timeZone: 'Asia/Singapore' }) + ' SGT' : 'Not recorded';
}

/** Backend still decides whether reaching a deadline changes the Order status. */
export function orderDeadline(order: Order): number | undefined {
	if (order.status === OrderStatus.AWAITING_COURIER && order.expiresAt) return timestampDate(order.expiresAt).getTime();
	if (order.status === OrderStatus.DELIVERING && order.pickedUpAt && order.deliveryDuration) {
		return timestampDate(order.pickedUpAt).getTime() + Number(order.deliveryDuration.seconds) * 2000;
	}
	return undefined;
}

export function roleFromQuery(value: string | null): ParticipantRole {
	return value === 'requester' ? ParticipantRole.REQUESTER : value === 'courier' ? ParticipantRole.COURIER : ParticipantRole.UNSPECIFIED;
}
