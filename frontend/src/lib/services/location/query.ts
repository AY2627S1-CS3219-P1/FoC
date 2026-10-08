import type { ArchiveView, LocationQuery, SortDirection, SortField } from './locationApi.ts';

const sortFields: SortField[] = ['name', 'building'];
const directions: SortDirection[] = ['asc', 'desc'];
const archiveViews: ArchiveView[] = ['active', 'archived', 'all'];

function pick<T extends string>(value: string | null, allowed: T[]): T | undefined {
	return allowed.find((option) => option === value);
}

/** Reads a LocationQuery from URL search params, ignoring invalid values. */
export function queryFromParams(params: URLSearchParams): LocationQuery {
	const page = Number(params.get('page'));
	return {
		search: params.get('search') ?? undefined,
		buildingId: params.get('building') ?? undefined,
		categoryId: params.get('category') ?? undefined,
		suppliersOnly: params.get('suppliers') === '1',
		archive: pick(params.get('archive'), archiveViews),
		sort: pick(params.get('sort'), sortFields),
		direction: pick(params.get('direction'), directions),
		page: Number.isInteger(page) && page > 1 ? page : undefined
	};
}

/** Writes a LocationQuery to URL search params, omitting defaults. */
export function paramsFromQuery(query: LocationQuery): URLSearchParams {
	const params = new URLSearchParams();
	if (query.search) params.set('search', query.search);
	if (query.buildingId) params.set('building', query.buildingId);
	if (query.categoryId) params.set('category', query.categoryId);
	if (query.suppliersOnly) params.set('suppliers', '1');
	if (query.archive && query.archive !== 'active') params.set('archive', query.archive);
	if (query.sort && query.sort !== 'name') params.set('sort', query.sort);
	if (query.direction && query.direction !== 'asc') params.set('direction', query.direction);
	if (query.page && query.page > 1) params.set('page', String(query.page));
	return params;
}
