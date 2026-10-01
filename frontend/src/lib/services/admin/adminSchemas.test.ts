import { describe, expect, it } from 'vitest';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import { roleChangeSchema, userSearchSchema } from './adminSchemas.ts';

describe('admin form schemas', () => {
	it('requires a valid email for exact user lookup', () => {
		expect(userSearchSchema.safeParse({ email: 'admin@example.com' }).success).toBe(true);
		expect(userSearchSchema.safeParse({ email: 'not-an-email' }).success).toBe(false);
	});

	it('requires a reason for a suspension transition and allows an ordinary promotion', () => {
		const base = { userId: '00000000-0000-4000-8000-000000000001', fromRole: UserRole.USER };
		expect(roleChangeSchema.safeParse({ ...base, toRole: 'suspended_user', reason: '' }).success).toBe(false);
		expect(roleChangeSchema.safeParse({ ...base, toRole: 'suspended_user', reason: 'Policy violation' }).success).toBe(true);
		expect(roleChangeSchema.safeParse({ ...base, toRole: 'admin', reason: '' }).success).toBe(true);
	});

	it('requires a reason to reinstate a suspended account', () => {
		expect(roleChangeSchema.safeParse({
			userId: '00000000-0000-4000-8000-000000000001', fromRole: UserRole.SUSPENDED_USER, toRole: 'user', reason: ''
		}).success).toBe(false);
	});
});
