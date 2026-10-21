import type { Timestamp } from '@bufbuild/protobuf/wkt';
import type {
	Disablement,
	DisablementState,
	PageInfo
} from '$lib/gen/supplier/location/v1/shared_types_pb';
export interface ClosureInput {
	startsAt?: Timestamp;
	endsAt?: Timestamp;
	reason: string;
}
export interface ClosurePage {
	disablements: Disablement[];
	pageInfo: PageInfo;
}
export interface DisablementApi {
	list(locationId: string, state: DisablementState, page: number): Promise<ClosurePage>;
	create(locationId: string, key: string, input: ClosureInput): Promise<Disablement>;
	update(id: string, revision: bigint, input: ClosureInput, paths: string[]): Promise<Disablement>;
	end(id: string): Promise<Disablement>;
	cancel(id: string): Promise<Disablement>;
}
