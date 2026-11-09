import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
export function formatTimestamp(value: Timestamp | undefined): string {
	return value
		? new Intl.DateTimeFormat('en-SG', {
				dateStyle: 'medium',
				timeStyle: 'short',
				timeZone: 'Asia/Singapore'
			}).format(timestampDate(value))
		: '—';
}
export const requestStatuses = [
	{ value: '1', text: 'Pending' },
	{ value: '2', text: 'Approved' },
	{ value: '3', text: 'Rejected' },
	{ value: '4', text: 'Withdrawn' },
	{ value: '0', text: 'All statuses' }
];
export function requestStatusLabel(value: number): string {
	return requestStatuses.find((s) => s.value === String(value))?.text ?? 'Unknown';
}
export function pageFromParams(params: URLSearchParams): number {
	const value = Number(params.get('page'));
	return Number.isSafeInteger(value) && value > 0 && value <= 2147483647 ? value : 1;
}
