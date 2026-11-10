<script lang="ts" module>
	export type MarkerKind = 'supplier' | 'pickup' | 'destination';

	export interface MapMarker {
		id: string;
		latitude: number;
		longitude: number;
		label: string;
		kind: MarkerKind;
	}

	/** NUS Kent Ridge campus, used until markers give a better view. */
	export const campusCenter = { latitude: 1.2966, longitude: 103.7764 };
</script>

<script lang="ts">
	import 'leaflet/dist/leaflet.css';
	import { onMount } from 'svelte';
	import type * as Leaflet from 'leaflet';

	let {
		markers,
		selectedId,
		route,
		label = 'Campus map',
		onselect
	}: {
		markers: MapMarker[];
		selectedId?: string;
		/** Draws a line between two marker ids, e.g. pickup to destination. */
		route?: [string, string];
		label?: string;
		onselect?: (id: string) => void;
	} = $props();

	const colors: Record<MarkerKind, string> = {
		supplier: 'var(--m3c-primary)',
		pickup: 'var(--m3c-tertiary)',
		destination: 'var(--m3c-error)'
	};

	let container: HTMLDivElement;
	let L = $state<typeof Leaflet>();
	let map = $state<Leaflet.Map>();
	let layer: Leaflet.LayerGroup | undefined;
	// Bumped after the container resizes so the view is refitted to the new size.
	let resized = $state(0);

	// Leaflet touches `window`, so load it only in the browser.
	onMount(() => {
		let disposed = false;
		let observer: ResizeObserver | undefined;

		void import('leaflet').then((module) => {
			if (disposed) return;
			L = module.default ?? module;
			map = L.map(container, { zoomControl: true, attributionControl: true, zoomSnap: 0.25 }).setView(
				[campusCenter.latitude, campusCenter.longitude],
				16
			);
			L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
				maxZoom: 19,
				attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
			}).addTo(map);
			layer = L.layerGroup().addTo(map);
			// Exposes the settled zoom for browser checks.
			map.on('zoomend', () => { container.dataset.zoom = String(map?.getZoom()); });
			observer = new ResizeObserver(() => {
				map?.invalidateSize();
				resized++;
			});
			observer.observe(container);
		});

		return () => {
			disposed = true;
			observer?.disconnect();
			map?.remove();
		};
	});

	// Redraw markers and refit the view whenever the inputs change.
	$effect(() => {
		if (!L || !map || !layer) return;
		const current = markers;
		const selected = selectedId;
		const line = route;
		void resized;

		layer.clearLayers();
		const points: Leaflet.LatLngExpression[] = [];
		const byId = new Map<string, Leaflet.LatLngExpression>();
		const style = getComputedStyle(container);

		for (const marker of current) {
			const point: Leaflet.LatLngExpression = [marker.latitude, marker.longitude];
			const isSelected = marker.id === selected;
			const color = resolve(style, colors[marker.kind]);
			const circle = L.circleMarker(point, {
				radius: isSelected ? 11 : 7,
				color: '#fff',
				weight: 2,
				fillColor: color,
				fillOpacity: isSelected ? 1 : 0.85
			})
				.bindTooltip(textElement(marker.label), { direction: 'top', permanent: isSelected })
				.addTo(layer);
			if (onselect) circle.on('click', () => onselect(marker.id));
			points.push(point);
			byId.set(marker.id, point);
		}

		if (line) {
			const [from, to] = line.map((id) => byId.get(id));
			if (from && to) {
				L.polyline([from, to], {
					color: resolve(style, 'var(--m3c-tertiary)'),
					weight: 3,
					dashArray: '6 6'
				}).addTo(layer);
			}
		}

		const focus = line
			? line.map((id) => byId.get(id)).filter((point) => point !== undefined)
			: selected && byId.has(selected)
				? [byId.get(selected)!]
				: points;
		if (focus.length === 1) map.setView(focus[0], Math.max(map.getZoom(), 17), { animate: false });
		else if (focus.length > 1) map.fitBounds(L.latLngBounds(focus), { paddingTopLeft: [24, 72], paddingBottomRight: [24, 32], maxZoom: 17, animate: false });
	});

	/** Leaflet renders string tooltips as HTML; labels hold user text, so pass a text-only element. */
	function textElement(text: string): HTMLElement {
		const element = document.createElement('span');
		element.textContent = text;
		return element;
	}

	/** Leaflet draws SVG attributes, which can't read CSS variables directly. */
	function resolve(style: CSSStyleDeclaration, value: string): string {
		const match = /^var\((--[^)]+)\)$/.exec(value);
		return match ? style.getPropertyValue(match[1]).trim() || '#174a99' : value;
	}
</script>

<div class="campus-map" role="region" aria-label={label} bind:this={container}></div>

<style>
	.campus-map {
		/* Positioned so the z-index below applies: contains Leaflet's high panes inside this panel. */
		position: relative;
		width: 100%;
		height: 100%;
		min-height: 12rem;
		border-radius: var(--m3-shape-large);
		overflow: hidden;
		background: var(--m3c-surface-container);
		z-index: 0;
	}
</style>
