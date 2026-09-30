import type { Client } from '@connectrpc/connect';
import {
	LocationDiscoveryService,
	LocationSortField,
	LocationStatusView,
	SortDirection as ProtoSortDirection
} from '$lib/gen/supplier/location/v1/discovery_pb';
import type { Building, Category, Location } from '$lib/gen/supplier/location/v1/shared_types_pb';
import type { ArchiveView, LocationApi, LocationPage, LocationQuery } from './locationApi.ts';

type LocationClient = Client<typeof LocationDiscoveryService>;

const archiveViews: Record<ArchiveView, LocationStatusView> = {
	active: LocationStatusView.ACTIVE,
	archived: LocationStatusView.ARCHIVED,
	all: LocationStatusView.ALL
};

/** Connect-backed implementation of the Location API. */
export class ConnectLocationApi implements LocationApi {
	readonly #client: LocationClient;

	constructor(client: LocationClient) {
		this.#client = client;
	}

	async getLocation(id: string): Promise<Location> {
		const { location } = await this.#client.getLocation({ id });
		if (!location) {
			throw new Error('GetLocation response did not include a location');
		}
		return location;
	}

	async listLocations(query: LocationQuery): Promise<LocationPage> {
		const response = await this.#client.listLocations({
			search: query.search ?? '',
			buildingId: query.buildingId,
			categoryId: query.categoryId,
			suppliersOnly: query.suppliersOnly ?? false,
			statusView: query.archive ? archiveViews[query.archive] : LocationStatusView.UNSPECIFIED,
			sortField: query.sort === 'building' ? LocationSortField.BUILDING : LocationSortField.NAME,
			sortDirection:
				query.direction === 'desc' ? ProtoSortDirection.DESCENDING : ProtoSortDirection.ASCENDING,
			page: query.page ?? 0,
			pageSize: query.pageSize ?? 0
		});
		return {
			locations: response.locations,
			page: response.page,
			pageSize: response.pageSize,
			totalItems: Number(response.totalItems),
			totalPages: response.totalPages
		};
	}

	async listBuildings(): Promise<Building[]> {
		return (await this.#client.listBuildings({})).buildings;
	}

	async listCategories(): Promise<Category[]> {
		return (await this.#client.listCategories({})).categories;
	}
}
