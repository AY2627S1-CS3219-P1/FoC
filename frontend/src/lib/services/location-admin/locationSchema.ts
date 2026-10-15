import { z } from 'zod';
import type { Location, LocationInput } from '$lib/gen/supplier/location/v1/shared_types_pb';
import type { LocationValues } from './adminApi';
import { formatTimeOfDay } from '../location/format';
const text = (maximum: number, minimum = 0) =>
	z
		.string()
		.trim()
		.refine(
			(value) => Array.from(value).length >= minimum && Array.from(value).length <= maximum,
			`Use ${minimum ? `${minimum}–` : 'at most '}${maximum} characters.`
		);
const coordinate = (min: number, max: number) =>
	z
		.string()
		.trim()
		.min(1, 'Enter a coordinate.')
		.refine(
			(value) => Number.isFinite(Number(value)) && Number(value) >= min && Number(value) <= max,
			`Enter a number from ${min} to ${max}.`
		);
const clock = z
	.string()
	.refine((v) => !v || /^([01]\d|2[0-3]):[0-5]\d$/.test(v), 'Use HH:MM (24-hour time).');
export const locationSchema = z
	.object({
		name: text(200, 1),
		isSupplier: z.boolean(),
		categoryIds: z.array(z.uuid()),
		buildingId: z.uuid('Select a building.'),
		floor: text(50),
		latitude: coordinate(-90, 90),
		longitude: coordinate(-180, 180),
		opensAt: clock,
		closesAt: clock,
		contact: text(500),
		details: text(2000)
	})
	.superRefine((v, ctx) => {
		if (v.isSupplier ? v.categoryIds.length === 0 : v.categoryIds.length > 0)
			ctx.addIssue({
				code: 'custom',
				path: ['categoryIds'],
				message: v.isSupplier
					? 'Select at least one category.'
					: 'Facilities must have no categories.'
			});
		if (new Set(v.categoryIds).size !== v.categoryIds.length)
			ctx.addIssue({
				code: 'custom',
				path: ['categoryIds'],
				message: 'Select each category once.'
			});
		if (!!v.opensAt !== !!v.closesAt || (v.opensAt && v.opensAt === v.closesAt))
			ctx.addIssue({
				code: 'custom',
				path: ['closesAt'],
				message: 'Set both times to different values, or leave both empty.'
			});
	});
export type LocationForm = z.infer<typeof locationSchema>;
export function locationForm(value?: Location | LocationInput): LocationForm {
	const location = value && 'id' in value ? (value as Location) : undefined;
	const input = value && !('id' in value) ? (value as LocationInput) : undefined;
	return {
		name: value?.name ?? '',
		isSupplier: value?.isSupplier ?? true,
		categoryIds: location?.categories.map((c) => c.id) ?? input?.categoryIds.slice() ?? [],
		buildingId: location?.building?.id ?? input?.buildingId ?? '',
		floor: value?.floor ?? '',
		latitude: value?.coordinates ? String(value.coordinates.latitude) : '',
		longitude: value?.coordinates ? String(value.coordinates.longitude) : '',
		opensAt: value?.opensAt ? formatTimeOfDay(value.opensAt) : '',
		closesAt: value?.closesAt ? formatTimeOfDay(value.closesAt) : '',
		contact: value?.contact ?? '',
		details: value?.details ?? ''
	};
}
function clockValue(value: string) {
	if (!value) return undefined;
	const [hours, minutes] = value.split(':').map(Number);
	return { hours, minutes };
}
export function locationInput(value: LocationForm): LocationValues {
	return {
		name: value.name.trim(),
		isSupplier: value.isSupplier,
		categoryIds: [...value.categoryIds].sort(),
		buildingId: value.buildingId,
		floor: value.floor.trim() || undefined,
		coordinates: { latitude: Number(value.latitude), longitude: Number(value.longitude) },
		opensAt: clockValue(value.opensAt),
		closesAt: clockValue(value.closesAt),
		contact: value.contact.trim() || undefined,
		details: value.details.trim()
	};
}
export function changedLocationPaths(before: LocationForm, after: LocationForm): string[] {
	const a = locationInput(before),
		b = locationInput(after);
	const fields = {
		name: 'name',
		isSupplier: 'is_supplier',
		categoryIds: 'category_ids',
		buildingId: 'building_id',
		floor: 'floor',
		coordinates: 'coordinates',
		opensAt: 'opens_at',
		closesAt: 'closes_at',
		contact: 'contact',
		details: 'details'
	} as const;
	const paths = Object.entries(fields)
		.filter(
			([key]) =>
				JSON.stringify(a[key as keyof typeof fields]) !==
				JSON.stringify(b[key as keyof typeof fields])
		)
		.map(([, path]) => String(path));
	if (paths.includes('opens_at') || paths.includes('closes_at'))
		return [...new Set([...paths, 'opens_at', 'closes_at'])];
	return paths;
}
