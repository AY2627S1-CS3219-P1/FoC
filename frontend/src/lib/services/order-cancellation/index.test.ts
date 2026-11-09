import { describe, expect, it } from 'vitest';
import { createRouterTransport } from '@connectrpc/connect';
import { OrderService } from '$lib/gen/order/v1/order_pb';
import { CancellationStatus } from '$lib/gen/order/v1/shared_types_pb';
import { cancellationStorageKey, createParticipantActionsApi, loadCancellation } from './index';
import { cancellationSchema, dispositionSchema } from './forms';

const caller = '11111111-1111-4111-8111-111111111111';
const orderId = '22222222-2222-4222-8222-222222222222';
const key = '33333333-3333-4333-8333-333333333333';
const input = { orderId, key, expectedRevision: '7', reason: 'Cannot continue' };
function storage(value: string | null): Storage {
	return { getItem: () => value } as unknown as Storage;
}

// Q5/Q15: the confirmed revision and retry identity must survive lost responses.
describe('cancellation contract', () => {
	it('matches the RPC contract for trimmed Unicode lengths and required final location', () => {
		expect(cancellationSchema.safeParse({ reason: ' ' }).success).toBe(true);
		expect(dispositionSchema.safeParse({ location: ' ' }).success).toBe(false);
		expect(dispositionSchema.parse({ location: '  Library desk  ' }).location).toBe('Library desk');
		expect(dispositionSchema.safeParse({ location: '😀'.repeat(500) }).success).toBe(true);
		expect(dispositionSchema.safeParse({ location: '界'.repeat(501) }).success).toBe(false);
	});
	it('sends the same confirmed payload on retry, including revision and key', async () => {
		const calls: unknown[] = [];
		const api = createParticipantActionsApi(createRouterTransport(router => router.service(OrderService, {
			cancelOrder(request) {
				calls.push({ id: request.id, key: request.idempotencyKey, revision: request.expectedRevision, reason: request.reason });
				return { cancellation: { operationId: key, status: CancellationStatus.SUCCEEDED } };
			}
		})));
		await api.cancel(input);
		await api.cancel(loadCancellation(storage(JSON.stringify(input)), caller, orderId)!);
		expect(calls).toEqual([
			{ id: orderId, key, revision: 7n, reason: 'Cannot continue' },
			{ id: orderId, key, revision: 7n, reason: 'Cannot continue' }
		]);
	});

	it('does not treat a missing operation outcome as success', async () => {
		const api = createParticipantActionsApi(createRouterTransport(router => router.service(OrderService, {
			cancelOrder() { return {}; }
		})));
		await expect(api.cancel(input)).rejects.toThrow('unknown');
	});

	it('retains large bigint revisions exactly and scopes storage by account and order', () => {
		const value = { ...input, expectedRevision: '9007199254740993' };
		expect(loadCancellation(storage(JSON.stringify(value)), caller, orderId)).toEqual(value);
		expect(cancellationStorageKey(caller, orderId)).not.toEqual(cancellationStorageKey(key, orderId));
	});

	it.each([
		'{', JSON.stringify({ ...input, orderId: key }), JSON.stringify({ ...input, expectedRevision: '0' }),
		JSON.stringify({ ...input, expectedRevision: '9223372036854775808' }), JSON.stringify({ ...input, key: 'bad' }),
		JSON.stringify({ ...input, reason: '界'.repeat(501) })
	])('fails closed on damaged or mismatched retained input', raw => {
		expect(() => loadCancellation(storage(raw), caller, orderId)).toThrow();
	});
});
