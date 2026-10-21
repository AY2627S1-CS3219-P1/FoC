import type { MessageInitShape } from '@bufbuild/protobuf';
import type { Location, LocationInputSchema } from '$lib/gen/supplier/location/v1/shared_types_pb';

export type LocationValues = MessageInitShape<typeof LocationInputSchema>;
export interface LocationAdminApi {
	create(key: string, location: LocationValues): Promise<Location>;
	update(
		id: string,
		revision: bigint,
		location: LocationValues,
		paths: string[]
	): Promise<Location>;
	archive(id: string): Promise<Location>;
	restore(id: string): Promise<Location>;
}
