import { Code, ConnectError } from '@connectrpc/connect';
import { ViolationsSchema } from '$lib/gen/buf/validate/validate_pb';

// The generated protovalidate message is named `Violations` (the field list is
// `violations`), not the historical `ValidationError`. `findDetails` matches on
// the message type name, so this is the schema the CMS sends for InvalidArgument
// validation failures.
interface ViolationMessage {
	message: string;
}
interface ViolationsDetail {
	violations: readonly ViolationMessage[];
}

/**
 * User-facing message for a moderation-config API error. Follows the repository
 * convention of mapping a Connect status code to a stable, non-leaky sentence:
 * no field names, provider internals or raw validation text are shown here.
 * Field-level detail, when the server provides it, is surfaced separately by
 * [extractValidationMessages].
 */
export function describeModerationConfigError(error: unknown): string {
	switch (ConnectError.from(error).code) {
		case Code.Unauthenticated:
			return 'Your session has expired. Please sign in again.';
		case Code.PermissionDenied:
			return 'You do not have permission to change the moderation configuration.';
		case Code.NotFound:
			return 'This configuration could not be found.';
		case Code.InvalidArgument:
			return 'One or more settings were rejected. Check the highlighted fields and try again.';
		case Code.Aborted:
		case Code.FailedPrecondition:
			return 'The active configuration changed while you were editing. Reload and try again.';
		default:
			return 'The operation could not be completed. Check your connection and try again.';
	}
}

// Best-effort extraction of the server's protovalidate messages for a
// validation failure, so the UI can show exactly which setting was wrong. It is
// deliberately non-authoritative: if the server omits the detail (or this
// transport does not decode it) it returns nothing and the caller falls back to
// describeModerationConfigError.
export function extractValidationMessages(error: unknown): string[] {
	if (!(error instanceof ConnectError) || error.code !== Code.InvalidArgument) {
		return [];
	}
	const details = error.findDetails(ViolationsSchema) as readonly ViolationsDetail[];
	const messages: string[] = [];
	for (const detail of details) {
		for (const violation of detail.violations ?? []) {
			if (violation.message) {
				messages.push(violation.message);
			}
		}
	}
	return messages;
}
