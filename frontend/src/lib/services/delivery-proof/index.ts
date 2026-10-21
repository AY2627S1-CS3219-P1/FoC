import { createClient, Code, ConnectError, type Transport } from '@connectrpc/connect';
import { OrderService } from '$lib/gen/order/v1/order_pb';
import { ProofRejectedError, type DeliveryProofAPI } from './api';

export type SavedProofSubmission = Parameters<DeliveryProofAPI['confirmDelivery']>[0];
export type DeliveryProofRead = {
	photos: { id: string; url: string; contentType: string; expiresAt: number }[];
	outageReason: string;
	pending?: SavedProofSubmission;
};
export interface DeliveryProofService extends DeliveryProofAPI {
	read(orderId: string, signal: AbortSignal): Promise<DeliveryProofRead>;
}

// Authentication, cancellation, deadlines and transport failures can hide a
// committed result. Only explicit application rejection releases saved input.
function rethrow(cause: unknown): never {
	if (cause instanceof ConnectError && [
		Code.InvalidArgument, Code.FailedPrecondition, Code.NotFound,
		Code.AlreadyExists, Code.ResourceExhausted
	].includes(cause.code)) {
		throw new ProofRejectedError(cause.code === Code.ResourceExhausted
			? 'Photo uploads are temporarily limited. Keep this photo and try again later.'
			: 'This action could not be completed. Check the order, photos and delivery deadline, then try again.');
	}
	throw cause;
}

export function createDeliveryProofService(transport: Transport): DeliveryProofService {
	const client = createClient(OrderService, transport);
	return {
		async createUpload(input, signal) {
			try {
				const response = await client.createProofUpload({ ...input, sizeBytes: BigInt(input.sizeBytes) }, { signal, timeoutMs: 15000 });
				if (!response.proofId || !response.uploadUrl || !response.expiresAt) throw new Error('Proof grant result is unknown');
				return { proofId: response.proofId, uploadUrl: response.uploadUrl, uploadHeaders: response.uploadHeaders };
			} catch (cause) { rethrow(cause); }
		},
		async abandonUpload(orderId, uploadId, signal) {
			try { await client.abandonProofUpload({ orderId, uploadId }, { signal, timeoutMs: 15000 }); }
			catch (cause) { rethrow(cause); }
		},
		async verifyUpload(orderId, proofId, signal) {
			try { await client.verifyProofUpload({ orderId, proofId }, { signal, timeoutMs: 15000 }); }
			catch (cause) { rethrow(cause); }
		},
		async confirmDelivery(input, signal) {
			try {
				const response = await client.confirmDelivery(input, { signal, timeoutMs: 15000 });
				if (response.order?.id !== input.orderId || !response.order.status) throw new Error('Delivery confirmation result is unknown');
			} catch (cause) { rethrow(cause); }
		},
		async read(orderId, signal) {
			const response = await client.getDeliveryProof({ orderId }, { signal, timeoutMs: 15000 });
			return {
				photos: response.proofs.map((photo) => ({
					id: photo.proofId, url: photo.downloadUrl, contentType: photo.contentType,
					expiresAt: photo.expiresAt ? Number(photo.expiresAt.seconds) * 1000 + Math.floor(photo.expiresAt.nanos / 1000000) : 0
				})),
				outageReason: response.outageReason,
				pending: response.pending ? { orderId, proofIds: [...response.pending.proofIds], unattended: response.pending.unattended, withoutProof: response.pending.withoutProof } : undefined
			};
		}
	};
}
