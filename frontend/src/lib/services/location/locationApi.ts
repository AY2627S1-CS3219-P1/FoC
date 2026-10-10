import type { Building, Category, Location } from '$lib/gen/supplier/location/v1/shared_types_pb';

export type SortField = 'name' | 'building';
export type SortDirection = 'asc' | 'desc';
export type ArchiveView = 'active' | 'archived' | 'all';

/** A Location list query. Omitted fields use the service defaults. */
export interface LocationQuery {
	search?: string;
	buildingId?: string;
	categoryId?: string;
	suppliersOnly?: boolean;
	archive?: ArchiveView;
	sort?: SortField;
	direction?: SortDirection;
	page?: number;
	pageSize?: number;
}

export interface LocationPage {
	locations: Location[];
	page: number;
	pageSize: number;
	totalItems: number;
	totalPages: number;
}

/** Operations the Location pages need from Supplier Service. */
export interface LocationApi {
	getLocation(id: string): Promise<Location>;
	listLocations(query: LocationQuery): Promise<LocationPage>;
	listBuildings(): Promise<Building[]>;
	listCategories(): Promise<Category[]>;
}
