import { Code, ConnectError } from '@connectrpc/connect';

export function describeOrderError(error: unknown, action = 'load orders'): string {
	switch (ConnectError.from(error).code) {
		case Code.Unauthenticated: return 'Your session expired. Sign in again to ' + action + '.';
		case Code.PermissionDenied: return 'You cannot ' + action + '. If your account is suspended, you can still view My Orders.';
		case Code.NotFound: return 'The order or selected location is unavailable. Refresh and check your selections.';
		case Code.InvalidArgument: return 'Check your details and the limits shown for each field.';
		case Code.FailedPrecondition: return 'Check your images, locations and available credits, then try again.';
		case Code.ResourceExhausted: return 'Too many attempts. Wait one minute and try again.';
		default: return 'Could not ' + action + '. Check your connection and try again.';
	}
}

/** Only explicit business rejection permits changing a previously submitted payload. */
export function isDefinitiveRejection(error: unknown): boolean {
	// Suspension can deny a retry before the server looks up an earlier creation.
	// It cannot establish that the original uncertain submission was rejected.
	return [Code.InvalidArgument, Code.FailedPrecondition, Code.ResourceExhausted, Code.NotFound].includes(ConnectError.from(error).code);
}
