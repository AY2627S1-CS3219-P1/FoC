/** A calendar day without a time or timezone. `month` runs from 1 to 12. */
export interface CalendarDate {
	year: number;
	month: number;
	day: number;
}

/** An inclusive range of calendar days. */
export interface DateRange {
	start: CalendarDate;
	end: CalendarDate;
}

// Admin daily series bucket by Singapore days, matching the Order and Credit services.
const singaporeParts = new Intl.DateTimeFormat('en-CA', {
	timeZone: 'Asia/Singapore',
	year: 'numeric',
	month: '2-digit',
	day: '2-digit'
});

/** Today's date in Singapore. */
export function singaporeToday(now: Date = new Date()): CalendarDate {
	const parts = Object.fromEntries(
		singaporeParts.formatToParts(now).map((part) => [part.type, part.value])
	);
	return { year: Number(parts.year), month: Number(parts.month), day: Number(parts.day) };
}

/** The date `days` after `date`; negative values go back. */
export function addDays(date: CalendarDate, days: number): CalendarDate {
	const shifted = new Date(Date.UTC(date.year, date.month - 1, date.day + days));
	return {
		year: shifted.getUTCFullYear(),
		month: shifted.getUTCMonth() + 1,
		day: shifted.getUTCDate()
	};
}

/** The `days` calendar days ending on `end`, inclusive. */
export function lastDays(days: number, end: CalendarDate): DateRange {
	return { start: addDays(end, 1 - days), end };
}

/** A stable key for maps and keyed `#each` blocks, such as `2026-11-09`. */
export function dateKey({ year, month, day }: CalendarDate): string {
	return `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`;
}

const shortFormat = new Intl.DateTimeFormat('en-SG', {
	day: 'numeric',
	month: 'short',
	timeZone: 'UTC'
});
const longFormat = new Intl.DateTimeFormat('en-SG', {
	weekday: 'short',
	day: 'numeric',
	month: 'short',
	year: 'numeric',
	timeZone: 'UTC'
});

/** For example `9 Nov`. */
export function formatShortDate(date: CalendarDate): string {
	return shortFormat.format(asUTC(date));
}

/** For example `Mon, 9 Nov 2026`. */
export function formatLongDate(date: CalendarDate): string {
	return longFormat.format(asUTC(date));
}

function asUTC({ year, month, day }: CalendarDate): Date {
	return new Date(Date.UTC(year, month - 1, day));
}
