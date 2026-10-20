import { describe, expect, it } from 'vitest';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { OrderService } from '$lib/gen/order/v1/order_pb';
import { OrderStatus } from '$lib/gen/order/v1/shared_types_pb';
import { ProofRejectedError } from '$lib/features/delivery-proof/controller';
import { createDeliveryProofService } from './index';

const orderId = '11111111-1111-4111-8111-111111111111';
const proofId = '22222222-2222-4222-8222-222222222222';
const submission = { orderId, proofIds: [proofId], unattended: true, withoutProof: false };
const signal = () => new AbortController().signal;

// Sources: published protobuf fields, approved durable pending/replay behavior,
// and the requirement to retain unknown transport/authentication results.
describe('delivery proof Connect adapter', () => {
	it('preserves exact upload requirements and immutable confirmation input', async () => {
		const uploads: unknown[] = [], confirmations: unknown[] = [];
		const api = createDeliveryProofService(createRouterTransport(router => router.service(OrderService, {
			createProofUpload(request) {
				uploads.push({ orderId: request.orderId, uploadId: request.uploadId, sizeBytes: request.sizeBytes, contentType: request.contentType });
				return { proofId, uploadUrl: 'https://storage.example.test/put', uploadHeaders: { 'If-None-Match': '*' }, expiresAt: { seconds: 100n } };
			},
			confirmDelivery(request) {
				confirmations.push({ orderId: request.orderId, proofIds: [...request.proofIds], unattended: request.unattended, withoutProof: request.withoutProof });
				return { order: { id: orderId, status: OrderStatus.COMPLETED } };
			}
		})));
		const upload = { orderId, uploadId: proofId, sizeBytes: 8000000, contentType: 'image/heic' };
		const grant = await api.createUpload(upload, signal());
		expect(uploads).toEqual([{ ...upload, sizeBytes: 8000000n }]);
		expect(grant.uploadHeaders).toEqual({ 'If-None-Match': '*' });
		await api.confirmDelivery(submission, signal());
		await api.confirmDelivery(submission, signal());
		expect(confirmations).toEqual([submission, submission]);
	});

	it('reads server-owned pending input without needing browser File objects', async () => {
		const api = createDeliveryProofService(createRouterTransport(router => router.service(OrderService, {
			getDeliveryProof() { return { pending: { proofIds: [proofId], unattended: true, withoutProof: false, receivedAt: { seconds: 100n } } }; }
		})));
		expect((await api.read(orderId, signal())).pending).toEqual(submission);
	});

	it.each([Code.Unavailable, Code.DeadlineExceeded, Code.Canceled, Code.Unauthenticated, Code.PermissionDenied, Code.Internal])('does not call ambiguous/auth code %s an authoritative rejection', async (code) => {
		const api = createDeliveryProofService(createRouterTransport(router => router.service(OrderService, {
			confirmDelivery() { throw new ConnectError('unknown result', code); }
		})));
		try { await api.confirmDelivery(submission, signal()); expect.fail('request should fail'); }
		catch (cause) { expect(cause).not.toBeInstanceOf(ProofRejectedError); }
	});

	it('releases an explicitly rejected submission but not an empty success envelope', async () => {
		const rejected = createDeliveryProofService(createRouterTransport(router => router.service(OrderService, {
			confirmDelivery() { throw new ConnectError('proof rejected', Code.FailedPrecondition); }
		})));
		await expect(rejected.confirmDelivery(submission, signal())).rejects.toBeInstanceOf(ProofRejectedError);
		const unknown = createDeliveryProofService(createRouterTransport(router => router.service(OrderService, { confirmDelivery() { return {}; } })));
		await expect(unknown.confirmDelivery(submission, signal())).rejects.not.toBeInstanceOf(ProofRejectedError);
	});
});
