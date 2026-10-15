import { describe, expect, it, vi } from 'vitest';
import { Code, ConnectError } from '@connectrpc/connect';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import { SupplierAdminAccess, isAdministrator } from './access.svelte';
describe('administrative access', () => {
 it('allows both administrator roles', () => {
  expect(isAdministrator(UserRole.ADMIN)).toBe(true);
  expect(isAdministrator(UserRole.SUPER_ADMIN)).toBe(true);
  expect(isAdministrator(UserRole.USER)).toBe(false);
  expect(isAdministrator(undefined)).toBe(false);
 });
 it('blocks calls before transport for non-administrators', async () => {
  const access = new SupplierAdminAccess({ user: { role: UserRole.USER }, refresh: async () => {} });
  const next = vi.fn();
  await expect(access.interceptor(next)({} as never)).rejects.toMatchObject({ code: Code.PermissionDenied });
  expect(next).not.toHaveBeenCalled();
 });
 it('hides stale data after denial and requires a successful session refresh', async () => {
  const refresh = vi.fn(async () => {});
  const access = new SupplierAdminAccess({ user: { role: UserRole.ADMIN }, refresh });
  const next = vi.fn(async () => { throw new ConnectError('Denied', Code.PermissionDenied); });
  await expect(access.interceptor(next)({} as never)).rejects.toMatchObject({ code: Code.PermissionDenied });
  expect(access.denied).toBe(true);
  refresh.mockRejectedValueOnce(new Error('offline'));
  await expect(access.recover()).rejects.toThrow('offline');
  expect(access.denied).toBe(true);
  await access.recover();
  expect(access.denied).toBe(false);
 });
});
