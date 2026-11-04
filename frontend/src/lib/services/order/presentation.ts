import { timestampDate, type Duration, type Timestamp } from '@bufbuild/protobuf/wkt';
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
		return timestampDate(order.pickedUpAt).getTime() + Number(order.deliveryDuration.seconds) * 1000;
	}
	return undefined;
}

/**
 * The caller's part in an order. ListMyOrders also returns orders a courier
 * withdrew from, where they are neither requester nor current courier.
 */
export function participationLabel(order: Order, callerId: string | undefined): string {
	if (order.requesterId === callerId) return 'Requested';
	if (order.courierId === callerId) return 'Courier';
	return 'Withdrew as courier';
}

export function roleFromQuery(value: string | null): ParticipantRole {
	return value === 'requester' ? ParticipantRole.REQUESTER : value === 'courier' ? ParticipantRole.COURIER : ParticipantRole.UNSPECIFIED;
}

/** Nanoseconds since the epoch, as Go's time arithmetic sees a protobuf Timestamp. */
export function timestampNanos(value: Timestamp): bigint {
	return value.seconds * 1_000_000_000n + BigInt(value.nanos);
}

/** Nanoseconds in a protobuf Duration. */
export function durationNanos(value: Duration): bigint {
	return value.seconds * 1_000_000_000n + BigInt(value.nanos);
}

/**
 * Mirrors Credit Service's lateness rule (credit-service/internal/reservation/lateness.go)
 * in the same integer nanoseconds, so the UI can show what a courier earns. Credit settles
 * the actual payout; replace this with a server value once Order exposes it (#206).
 * - elapsed <= allowed pays the full reward;
 * - allowed < elapsed < 2 * allowed pays floor(reward * (2 * allowed - elapsed) / allowed);
 * - elapsed >= 2 * allowed pays 0.
 */
export function payableReward(reward: bigint, elapsedNanos: bigint, allowedNanos: bigint): bigint {
	if (allowedNanos <= 0n || elapsedNanos <= allowedNanos) return reward;
	if (elapsedNanos >= 2n * allowedNanos) return 0n;
	return (reward * (2n * allowedNanos - elapsedNanos)) / allowedNanos;
}
