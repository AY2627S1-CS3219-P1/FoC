import type { Client } from '@connectrpc/connect';
import type { LocationDisablementService } from '$lib/gen/supplier/location/v1/disablement_pb';
import type { DisablementState } from '$lib/gen/supplier/location/v1/shared_types_pb';
import type { ClosureInput, DisablementApi } from './disablementApi';
function required<T>(value: T | undefined): T {
	if (!value) throw new Error('Supplier service returned an incomplete closure response.');
	return value;
}
export class ConnectDisablementApi implements DisablementApi {
	constructor(private readonly client: Client<typeof LocationDisablementService>) {}
	async list(locationId: string, state: DisablementState, page: number) {
		const result = await this.client.listDisablements({ locationId, state, page, pageSize: 20 });
		return { disablements: result.disablements, pageInfo: required(result.pageInfo) };
	}
	async create(locationId: string, idempotencyKey: string, input: ClosureInput) {
		return required(
			(await this.client.createDisablement({ locationId, idempotencyKey, ...input })).disablement
		);
	}
	async update(id: string, expectedRevision: bigint, input: ClosureInput, paths: string[]) {
		return required(
			(
				await this.client.updateDisablement({
					id,
					expectedRevision,
					...input,
					updateMask: { paths }
				})
			).disablement
		);
	}
	async end(id: string) {
		return required((await this.client.endDisablement({ id })).disablement);
	}
	async cancel(id: string) {
		return required((await this.client.cancelDisablement({ id })).disablement);
	}
}
