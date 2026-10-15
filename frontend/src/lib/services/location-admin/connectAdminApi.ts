import type { Client } from '@connectrpc/connect';
import type { LocationAdminService } from '$lib/gen/supplier/location/v1/admin_pb';
import type { LocationAdminApi, LocationValues } from './adminApi';

function required<T>(value: T | undefined): T {
 if (!value) throw new Error('Supplier service returned no location.');
 return value;
}
export class ConnectLocationAdminApi implements LocationAdminApi {
 constructor(private readonly client: Client<typeof LocationAdminService>) {}
 async create(idempotencyKey: string, location: LocationValues) {
  return required((await this.client.createLocation({ idempotencyKey, location })).location);
 }
 async update(id: string, expectedRevision: bigint, location: LocationValues, paths: string[]) {
  return required((await this.client.updateLocation({ id, expectedRevision, location, updateMask: { paths } })).location);
 }
 async archive(id: string) { return required((await this.client.archiveLocation({ id })).location); }
 async restore(id: string) { return required((await this.client.unarchiveLocation({ id })).location); }
}
