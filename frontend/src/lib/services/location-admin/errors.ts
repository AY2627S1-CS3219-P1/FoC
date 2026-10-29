import { Code, ConnectError } from '@connectrpc/connect';
export function adminError(
	error: unknown,
	domain: 'location' | 'request' | 'closure' = 'location'
): string {
	switch (ConnectError.from(error).code) {
		case Code.Unauthenticated:
			return 'Your session has expired. Please sign in again.';
		case Code.PermissionDenied:
			return 'Administrator access is no longer available. Refresh your session to continue.';
		case Code.NotFound:
			return 'This record could not be found. It may have been removed.';
		case Code.InvalidArgument:
			return 'Check the highlighted fields and the supplied values, then try again.';
		case Code.Aborted:
			return 'This record changed while you were editing. Reload its latest version and reconcile your changes before saving.';
		case Code.AlreadyExists:
			return domain === 'closure'
				? 'This closure overlaps another interval, or a retry key was used with different details. Refresh closure history and check the dates.'
				: 'This creation was already attempted with different details. Retry the original submission or start a new creation.';
		case Code.FailedPrecondition:
			return domain === 'request'
				? 'This request may already have been reviewed, or its building/categories are no longer valid. Reload before continuing.'
				: domain === 'closure'
					? 'The closure state changed, or the location is archived. Reload before continuing.'
					: 'Check the building and categories. Suppliers need categories; facilities must have none.';
		default:
			return 'The operation could not be confirmed. Check your connection and retry the same submission.';
	}
}
export function isConflict(error: unknown): boolean {
	const code = ConnectError.from(error).code;
	return code === Code.Aborted || code === Code.FailedPrecondition;
}
