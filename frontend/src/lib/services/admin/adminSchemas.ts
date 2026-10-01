import { z } from 'zod';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import { needsRoleReason } from './roles.ts';

export const userSearchSchema = z.object({
	email: z.email('Enter a valid email address.').trim()
});

export const roleChangeSchema = z.object({
	toRole: z.enum(['admin', 'user', 'suspended_user']),
	reason: z.string().trim().max(2000, 'Use 2,000 characters or fewer.'),
	userId: z.uuid(),
	fromRole: z.number().int()
}).superRefine((value, ctx) => {
	if (needsRoleReason(value.fromRole as UserRole, value.toRole) && !value.reason) {
		ctx.addIssue({ code: 'custom', path: ['reason'], message: 'Enter a reason for suspending or reinstating this user.' });
	}
});

export type UserSearchForm = z.infer<typeof userSearchSchema>;
export type RoleChangeForm = z.infer<typeof roleChangeSchema>;
