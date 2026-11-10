// The adapter marks only authoritative server rejections as Rejected.
// Transport failures and ambiguous responses remain Failed/unknown.
export class ProofRejectedError extends Error {}

export type ProofGrant = {
	proofId: string;
	uploadUrl: string;
	uploadHeaders: Record<string, string>;
};

// The service forwards abort signals and distinguishes authoritative rejection
// from unknown results.
export interface DeliveryProofAPI {
	createUpload(input: {
		orderId: string;
		uploadId: string;
		contentType: string;
		sizeBytes: number;
	}, signal: AbortSignal): Promise<ProofGrant>;
	abandonUpload(orderId: string, uploadId: string, signal: AbortSignal): Promise<void>;
	verifyUpload(orderId: string, proofId: string, signal: AbortSignal): Promise<void>;
	confirmDelivery(input: {
		orderId: string;
		proofIds: string[];
		unattended: boolean;
		withoutProof: boolean;
	}, signal: AbortSignal): Promise<void>;
}

