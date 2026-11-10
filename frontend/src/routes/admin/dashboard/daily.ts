import type { CalendarDate } from '$lib/calendar-date';

/** One day's value in a daily series. */
export interface DailyPoint {
	date: CalendarDate;
	value: bigint;
}

export interface DailySummary {
	total: bigint;
	/** The mean per day over the whole range, including empty days. */
	average: number;
	/** The day with the largest value, the latest on ties; absent when every day is zero. */
	busiest?: DailyPoint;
	/** A round axis maximum at or above the largest value; zero when every day is zero. */
	scaleMax: bigint;
	/** Indices of the days to label on the time axis: first, middle and last. */
	ticks: number[];
}

export function summarizeDaily(points: DailyPoint[]): DailySummary {
	let total = 0n;
	let busiest: DailyPoint | undefined;
	for (const point of points) {
		total += point.value;
		if (point.value > 0n && (!busiest || point.value >= busiest.value)) busiest = point;
	}

	const average = points.length === 0 ? 0 : Number(total) / points.length;
	const last = points.length - 1;
	const ticks = last < 0 ? [] : [...new Set([0, Math.floor(last / 2), last])];
	return { total, average, busiest, scaleMax: niceCeiling(busiest?.value ?? 0n), ticks };
}

/** The smallest of 1, 2 or 5 times a power of ten that is at least `value`. */
export function niceCeiling(value: bigint): bigint {
	if (value <= 0n) return 0n;
	for (let magnitude = 1n; ; magnitude *= 10n) {
		for (const step of [1n, 2n, 5n]) {
			if (step * magnitude >= value) return step * magnitude;
		}
	}
}

/** Formats a per-day average with at most one decimal place. */
export function formatAverage(value: number): string {
	return value.toLocaleString('en-SG', { maximumFractionDigits: 1 });
}
