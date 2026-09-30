import { describe, expect, it } from 'vitest';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import { allowedRoleTargets, needsRoleReason, roleLabel, roleValue } from './roles.ts';

describe('allowedRoleTargets', () => {
	it('lets admins suspend or reinstate ordinary users only', () => {
		expect(allowedRoleTargets(UserRole.ADMIN, UserRole.USER, false)).toEqual(['suspended_user']);
		expect(allowedRoleTargets(UserRole.ADMIN, UserRole.SUSPENDED_USER, false)).toEqual(['user']);
		expect(allowedRoleTargets(UserRole.ADMIN, UserRole.ADMIN, false)).toEqual([]);
	});

	it('lets super admins assign any different supported role to non-super-admins', () => {
		expect(allowedRoleTargets(UserRole.SUPER_ADMIN, UserRole.USER, false)).toEqual(['admin', 'suspended_user']);
		expect(allowedRoleTargets(UserRole.SUPER_ADMIN, UserRole.ADMIN, false)).toEqual(['user', 'suspended_user']);
		expect(allowedRoleTargets(UserRole.SUPER_ADMIN, UserRole.SUSPENDED_USER, false)).toEqual(['admin', 'user']);
	});

	it('never offers self or super-admin role changes', () => {
		expect(allowedRoleTargets(UserRole.SUPER_ADMIN, UserRole.USER, true)).toEqual([]);
		expect(allowedRoleTargets(UserRole.SUPER_ADMIN, UserRole.SUPER_ADMIN, false)).toEqual([]);
	});
});

describe('role labels and suspension reasons', () => {
	it('maps editable roles to protobuf values and readable labels', () => {
		expect(roleValue('admin')).toBe(UserRole.ADMIN);
		expect(roleLabel(UserRole.SUSPENDED_USER)).toBe('Suspended');
	});

	it('requires a reason when suspending or reinstating', () => {
		expect(needsRoleReason(UserRole.USER, 'suspended_user')).toBe(true);
		expect(needsRoleReason(UserRole.SUSPENDED_USER, 'user')).toBe(true);
		expect(needsRoleReason(UserRole.USER, 'admin')).toBe(false);
	});
});
