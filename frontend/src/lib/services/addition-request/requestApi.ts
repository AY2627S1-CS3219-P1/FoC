import type { LocationAdditionRequest, LocationAdditionRequestStatus } from '$lib/gen/supplier/location/v1/addition_request_pb';
import type { PageInfo } from '$lib/gen/supplier/location/v1/shared_types_pb';
export interface RequestPage { requests: LocationAdditionRequest[]; pageInfo: PageInfo }
export interface AdditionRequestApi {
 list(status: LocationAdditionRequestStatus, page: number): Promise<RequestPage>;
 get(id: string): Promise<LocationAdditionRequest>;
 approve(id: string): Promise<LocationAdditionRequest>;
 reject(id: string, reviewNote: string): Promise<LocationAdditionRequest>;
}
