/** Formats a count or credit amount with grouping, without losing int64 precision. */
export function formatCount(value: bigint): string {
	return value.toLocaleString('en-SG');
}
