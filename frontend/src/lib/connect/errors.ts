import { ConnectError } from '@connectrpc/connect';

const fallbackMessage = 'We could not complete the request. Please try again.';

/** Prefer the backend's public message; keep local exception details out of the UI. */
export function getApiErrorMessage(error: unknown): string {
	// Errors decoded from a backend response have no local cause. Connect also
	// wraps fetch failures, retaining the original exception as their cause.
	if (error instanceof ConnectError && error.cause == null && error.rawMessage.trim()) {
		return error.rawMessage;
	}
	return fallbackMessage;
}
