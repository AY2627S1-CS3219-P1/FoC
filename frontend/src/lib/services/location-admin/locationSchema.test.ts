import { describe, expect, it } from 'vitest';
import {
	locationSchema,
	locationForm,
	locationInput,
	changedLocationPaths
} from './locationSchema';
const id = '11111111-1111-4111-8111-111111111111';
function valid() {
	return {
		...locationForm(),
		name: 'Shop',
		buildingId: id,
		categoryIds: [id],
		latitude: '1.29',
		longitude: '103.77'
	};
}
describe('location validation and updates', () => {
	it('requires supplier categories and clears them explicitly for facilities', () => {
		expect(locationSchema.safeParse({ ...valid(), categoryIds: [] }).success).toBe(false);
		expect(locationSchema.safeParse({ ...valid(), isSupplier: false }).success).toBe(false);
		const facility = { ...valid(), isSupplier: false, categoryIds: [] };
		expect(locationSchema.safeParse(facility).success).toBe(true);
		expect(changedLocationPaths(valid(), facility).sort()).toEqual(['category_ids', 'is_supplier']);
	});
	it('rejects blank, nonfinite and out-of-range coordinates', () => {
		for (const latitude of ['', ' ', 'NaN', 'Infinity', '91', '-91'])
			expect(locationSchema.safeParse({ ...valid(), latitude }).success).toBe(false);
		expect(locationSchema.safeParse({ ...valid(), latitude: '0', longitude: '0' }).success).toBe(
			true
		);
	});
	it('validates Unicode lengths after trimming', () => {
		expect(
			locationSchema.safeParse({ ...valid(), name: '  ' + '😀'.repeat(200) + '  ' }).success
		).toBe(true);
		expect(locationSchema.safeParse({ ...valid(), name: '😀'.repeat(201) }).success).toBe(false);
	});
	it('allows overnight hours, rejects partial/equal hours and masks both clocks', () => {
		const before = { ...valid(), opensAt: '22:00', closesAt: '06:00' };
		expect(locationSchema.safeParse(before).success).toBe(true);
		expect(locationSchema.safeParse({ ...before, closesAt: '22:00' }).success).toBe(false);
		expect(locationSchema.safeParse({ ...before, closesAt: '' }).success).toBe(false);
		expect(changedLocationPaths(before, { ...before, closesAt: '07:00' }).sort()).toEqual([
			'closes_at',
			'opens_at'
		]);
		const cleared = { ...before, opensAt: '', closesAt: '' };
		expect(locationInput(cleared).opensAt).toBeUndefined();
		expect(changedLocationPaths(before, cleared).sort()).toEqual(['closes_at', 'opens_at']);
	});
	it('clears optional fields and ignores normalization-only changes', () => {
		const before = { ...valid(), contact: ' 123 ', floor: '2' };
		expect(changedLocationPaths(before, { ...before, contact: '', floor: '' }).sort()).toEqual([
			'contact',
			'floor'
		]);
		expect(locationInput({ ...before, contact: '' }).contact).toBeUndefined();
		expect(changedLocationPaths(valid(), { ...valid(), name: ' Shop ' })).toEqual([]);
	});
});
