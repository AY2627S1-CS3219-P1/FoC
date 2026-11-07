// Parses the common.v1.ErrorDetail the backend attaches to review RPC errors.
import { ConnectError, type Code } from '@connectrpc/connect';
import { ErrorDetailSchema } from '$lib/gen/common/v1/error_pb';

export type ParsedRpcError = {
	code: Code;
	reason?: string;
	message: string;
	/** Photo ids the backend named as the offending input, if any. */
	photoIds: string[];
};

/** Extracts the leading photo id from a `"<uuid>: <why>"` violation. */
function photoIdOf(description: string): string | undefined {
	return /^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}):/i.exec(description)?.[1];
}

export function parseRpcError(cause: unknown): ParsedRpcError | undefined {
	if (!(cause instanceof ConnectError)) return undefined;
	const detail = cause.findDetails(ErrorDetailSchema)[0];
	const photoIds = (detail?.fieldViolations ?? [])
		.map((violation) => photoIdOf(violation.description ?? ''))
		.filter((id): id is string => !!id);
	return {
		code: cause.code,
		reason: detail?.reason || undefined,
		message: detail?.message || cause.rawMessage,
		photoIds
	};
}
