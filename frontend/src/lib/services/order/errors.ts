import { Code, ConnectError } from '@connectrpc/connect';

export function describeOrderError(error: unknown, action = 'load orders'): string {
	switch (ConnectError.from(error).code) {
		case Code.Unauthenticated: return 'Your session expired. Sign in again to ' + action + '.';
		case Code.PermissionDenied: return 'Your account cannot ' + action + '. You can still check My Orders when suspended.';
		case Code.NotFound: return 'The order or selected location is unavailable. Refresh and check your selections.';
		case Code.InvalidArgument: return 'The submitted values were rejected. Check the fields and applicable limits.';
		case Code.FailedPrecondition: return 'The action was rejected. Check image uploads, location availability and available credits before retrying.';
		case Code.ResourceExhausted: return 'Too many submissions. Wait one minute before trying again.';
		default: return 'Could not ' + action + '. Check your connection and retry. Your input has been retained.';
	}
}

/** Only explicit business rejection permits changing a previously submitted payload. */
export function isDefinitiveRejection(error: unknown): boolean {
	// Suspension can deny a retry before the server looks up an earlier creation.
	// It cannot establish that the original uncertain submission was rejected.
	return [Code.InvalidArgument, Code.FailedPrecondition, Code.ResourceExhausted, Code.NotFound].includes(ConnectError.from(error).code);
}
