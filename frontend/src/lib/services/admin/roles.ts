import { UserRole } from '$lib/gen/user/v1/auth_pb';

export type EditableRole = 'admin' | 'user' | 'suspended_user';
const editableRoles: EditableRole[] = ['admin', 'user', 'suspended_user'];

export function roleLabel(role: UserRole): string {
	switch (role) {
		case UserRole.SUPER_ADMIN: return 'Super admin';
		case UserRole.ADMIN: return 'Admin';
		case UserRole.USER: return 'User';
		case UserRole.SUSPENDED_USER: return 'Suspended';
		default: return 'Unknown';
	}
}

export function roleValue(role: EditableRole): UserRole {
	switch (role) {
		case 'admin': return UserRole.ADMIN;
		case 'user': return UserRole.USER;
		case 'suspended_user': return UserRole.SUSPENDED_USER;
	}
}

export function allowedRoleTargets(actorRole: UserRole, targetRole: UserRole, isSelf: boolean): EditableRole[] {
	if (isSelf || targetRole === UserRole.SUPER_ADMIN) return [];
	if (actorRole === UserRole.ADMIN) {
		return targetRole === UserRole.USER ? ['suspended_user'] :
			targetRole === UserRole.SUSPENDED_USER ? ['user'] : [];
	}
	if (actorRole !== UserRole.SUPER_ADMIN) return [];
	return editableRoles.filter((role) => roleValue(role) !== targetRole);
}

export function needsRoleReason(from: UserRole, to: EditableRole): boolean {
	return from === UserRole.SUSPENDED_USER || to === 'suspended_user';
}
