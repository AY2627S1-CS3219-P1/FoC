import { createClient, type Transport } from '@connectrpc/connect';
import { ProfileService as ProfileServiceDefinition } from '$lib/gen/user/v1/profile_pb';
import { ConnectProfileApi } from './connectProfileApi.ts';
import { ProfileService, type ProfileIdentity } from './service.ts';

export function createProfileService(transport: Transport, auth: ProfileIdentity): ProfileService {
	return new ProfileService(
		new ConnectProfileApi(createClient(ProfileServiceDefinition, transport)),
		auth
	);
}
