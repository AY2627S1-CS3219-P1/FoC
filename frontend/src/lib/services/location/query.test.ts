import { describe, expect, it } from 'vitest';
import { paramsFromQuery, queryFromParams } from './query.ts';

describe('location query params', () => {
	it('round-trips every field', () => {
		const query = {
			search: 'coffee',
			buildingId: 'b1',
			categoryId: 'c1',
			suppliersOnly: true,
			archive: 'all' as const,
			sort: 'building' as const,
			direction: 'desc' as const,
			page: 3
		};
		expect(queryFromParams(paramsFromQuery(query))).toEqual(query);
	});

	it('omits defaults from the URL', () => {
		expect(
			paramsFromQuery({ archive: 'active', sort: 'name', direction: 'asc', page: 1 }).toString()
		).toBe('');
	});

	it('ignores invalid values', () => {
		const query = queryFromParams(new URLSearchParams('sort=rating&archive=deleted&page=-2'));
		expect(query.sort).toBeUndefined();
		expect(query.archive).toBeUndefined();
		expect(query.page).toBeUndefined();
	});
});
