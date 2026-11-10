import { Code, ConnectError } from '@connectrpc/connect';
import { parseRpcError } from '$lib/services/rpc/errors';

// The backend names the problem for client errors (for example "warning is no
// longer active" or "appeal the retained review instead"), so those messages
// are shown as they are. Server and transport failures get a generic retry.
const clientCodes = new Set<Code>([
	Code.InvalidArgument,
	Code.NotFound,
	Code.AlreadyExists,
	Code.PermissionDenied,
	Code.FailedPrecondition,
	Code.Unimplemented
]);

/** Describes a report or appeal RPC failure; `action` reads as "submit the report". */
export function describeModerationError(cause: unknown, action: string): string {
	if (cause instanceof ConnectError) {
		const message = parseRpcError(cause)?.message?.trim();
		if (clientCodes.has(cause.code) && message) return `Could not ${action}: ${message}.`;
		if (cause.code === Code.Aborted) return `Could not ${action}: it changed in the meantime. Reload and try again.`;
		if (cause.code === Code.Unavailable) return `Could not ${action} right now. Please try again shortly.`;
	}
	return `Could not ${action}. Please try again.`;
}
