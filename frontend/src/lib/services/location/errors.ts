import { Code, ConnectError } from '@connectrpc/connect';

/** Returns a message suitable for showing to users for a Location API error. */
export function describeLocationError(error: unknown): string {
	const code = ConnectError.from(error).code;
	switch (code) {
		case Code.Unauthenticated:
			return 'Please log in to see campus locations.';
		case Code.PermissionDenied:
			return 'You do not have access to this view.';
		case Code.NotFound:
			return 'This location does not exist.';
		case Code.InvalidArgument:
			return 'Some search options are invalid. Try clearing the filters.';
		default:
			return 'Could not load locations. Please try again.';
	}
}
