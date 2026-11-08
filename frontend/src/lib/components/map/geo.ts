export interface Point {
	latitude: number;
	longitude: number;
}

/** Great-circle distance in metres. */
export function distanceMetres(a: Point, b: Point): number {
	const radius = 6_371_000;
	const toRadians = (degrees: number) => (degrees * Math.PI) / 180;
	const dLat = toRadians(b.latitude - a.latitude);
	const dLng = toRadians(b.longitude - a.longitude);
	const h =
		Math.sin(dLat / 2) ** 2 +
		Math.cos(toRadians(a.latitude)) * Math.cos(toRadians(b.latitude)) * Math.sin(dLng / 2) ** 2;
	return 2 * radius * Math.asin(Math.sqrt(h));
}

export function distanceLabel(metres: number): string {
	return metres < 1000 ? `${Math.round(metres / 10) * 10} m` : `${(metres / 1000).toFixed(1)} km`;
}

/** Resolves with the browser's position, or rejects if unavailable or denied. */
export function currentPosition(): Promise<Point> {
	return new Promise((resolve, reject) => {
		if (!('geolocation' in navigator)) {
			reject(new Error('Location is not available in this browser.'));
			return;
		}
		navigator.geolocation.getCurrentPosition(
			(position) => resolve({ latitude: position.coords.latitude, longitude: position.coords.longitude }),
			() => reject(new Error('Allow location access to sort by distance.')),
			{ timeout: 10_000, maximumAge: 60_000 }
		);
	});
}
