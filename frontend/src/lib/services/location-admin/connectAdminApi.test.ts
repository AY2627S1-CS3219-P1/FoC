import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { create } from '@bufbuild/protobuf';
import type { LocationAdminService } from '$lib/gen/supplier/location/v1/admin_pb';
import { LocationSchema } from '$lib/gen/supplier/location/v1/shared_types_pb';
import { ConnectLocationAdminApi } from './connectAdminApi';

describe('location administration RPC boundary', () => {
 it('preserves creation identity, bigint revision and clearing masks', async () => {
  const location = create(LocationSchema, { id: 'location', revision: 9007199254740993n });
  const client = { createLocation: vi.fn(async () => ({ location })), updateLocation: vi.fn(async () => ({ location })), archiveLocation: vi.fn(async () => ({ location })), unarchiveLocation: vi.fn(async () => ({ location })) };
  const api = new ConnectLocationAdminApi(client as unknown as Client<typeof LocationAdminService>);
  await api.create('retry-key', { name: 'Shop', isSupplier: true });
  expect(client.createLocation).toHaveBeenCalledWith({ idempotencyKey: 'retry-key', location: { name: 'Shop', isSupplier: true } });
  expect(await api.update('location', location.revision, { contact: undefined }, ['contact'])).toBe(location);
  expect(client.updateLocation).toHaveBeenCalledWith({ id: 'location', expectedRevision: 9007199254740993n, location: { contact: undefined }, updateMask: { paths: ['contact'] } });
  await api.archive('location'); await api.restore('location');
  expect(client.archiveLocation).toHaveBeenCalledWith({ id: 'location' });
  expect(client.unarchiveLocation).toHaveBeenCalledWith({ id: 'location' });
 });
 it('does not report success for a missing location', async () => {
  const api = new ConnectLocationAdminApi({ createLocation: async () => ({}) } as unknown as Client<typeof LocationAdminService>);
  await expect(api.create('key', {})).rejects.toThrow('no location');
 });
});
