/** The parts of google.type.TimeOfDay the UI uses. */
export interface Clock {
	hours: number;
	minutes: number;
}

const pad = (n: number) => String(n).padStart(2, '0');

/** Formats a time as 24-hour "HH:MM", e.g. 09:05. */
export function formatTimeOfDay(time: Clock): string {
	return `${pad(time.hours)}:${pad(time.minutes)}`;
}

/**
 * Formats opening hours as "HH:MM–HH:MM". Adds " (overnight)" when closing is
 * before opening, and returns "Hours not listed" when either time is missing.
 */
export function formatOpeningHours(opensAt: Clock | undefined, closesAt: Clock | undefined): string {
	if (!opensAt || !closesAt) {
		return 'Hours not listed';
	}
	const range = `${formatTimeOfDay(opensAt)}–${formatTimeOfDay(closesAt)}`;
	const overnight = closesAt.hours * 60 + closesAt.minutes < opensAt.hours * 60 + opensAt.minutes;
	return overnight ? `${range} (overnight)` : range;
}
