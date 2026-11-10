import { currentPosition, type Point } from './geo';

/** Page-lifetime location, shared by every list on that page. Never persisted. */
export class Proximity {
	origin = $state<Point | null>(null);
	locating = $state(false);
	error = $state('');
	attempted = $state(false);

	async locate() {
		if (this.locating) return;
		this.attempted = true;
		this.locating = true;
		this.error = '';
		try {
			this.origin = await currentPosition();
		} catch {
			this.error = 'Location unavailable.';
		} finally {
			this.locating = false;
		}
	}
}
