import type { Client } from '@connectrpc/connect';
import type {
	LocationAdditionRequestService,
	LocationAdditionRequestStatus
} from '$lib/gen/supplier/location/v1/addition_request_pb';
import type { AdditionRequestApi } from './requestApi';
function required<T>(value: T | undefined): T {
	if (!value) throw new Error('Supplier service returned an incomplete request response.');
	return value;
}
export class ConnectAdditionRequestApi implements AdditionRequestApi {
	constructor(private readonly client: Client<typeof LocationAdditionRequestService>) {}
	async list(status: LocationAdditionRequestStatus, page: number) {
		const result = await this.client.listLocationAdditionRequests({ status, page, pageSize: 20 });
		return { requests: result.requests, pageInfo: required(result.pageInfo) };
	}
	async get(id: string) {
		return required((await this.client.getLocationAdditionRequest({ id })).request);
	}
	async approve(id: string, expectedRevision: bigint) {
		return required(
			(await this.client.approveLocationAdditionRequest({ id, expectedRevision })).request
		);
	}
	async reject(id: string, reviewNote: string, expectedRevision: bigint) {
		return required(
			(
				await this.client.rejectLocationAdditionRequest({
					id,
					reviewNote: reviewNote.trim(),
					expectedRevision
				})
			).request
		);
	}
}
