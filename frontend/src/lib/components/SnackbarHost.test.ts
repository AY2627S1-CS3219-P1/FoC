import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import { snackbarService } from '$lib/services';
import SnackbarHost from './SnackbarHost.svelte';

function renderHost() {
	return render(SnackbarHost).body;
}

function expectPersistentRegions(body: string) {
	expect(body).toContain('role="status" aria-live="polite" aria-atomic="true"');
	expect(body).toContain('role="alert" aria-live="assertive" aria-atomic="true"');
}

describe('SnackbarHost', () => {
	afterEach(() => snackbarService.dismiss());

	it('keeps empty polite and assertive live regions mounted', () => {
		const body = renderHost();

		expectPersistentRegions(body);
		expect(body).not.toContain('data-snackbar-item');
	});

	it.each(['info', 'success', 'warning'] as const)(
		'renders %s notices in the polite live region',
		(severity) => {
			snackbarService.show(`${severity} notice`, { severity, timeout: -1 });
			const body = renderHost();
			const statusIndex = body.indexOf('role="status"');
			const messageIndex = body.indexOf(`${severity} notice`);
			const alertIndex = body.indexOf('role="alert"');

			expectPersistentRegions(body);
			expect(statusIndex).toBeLessThan(messageIndex);
			expect(messageIndex).toBeLessThan(alertIndex);
			expect(body.match(new RegExp(`${severity} notice`, 'g'))).toHaveLength(1);
		}
	);

	it('renders error notices in the assertive live region', () => {
		snackbarService.show('error notice', { severity: 'error', timeout: -1 });
		const body = renderHost();
		const statusIndex = body.indexOf('role="status"');
		const alertIndex = body.indexOf('role="alert"');
		const messageIndex = body.indexOf('error notice');

		expectPersistentRegions(body);
		expect(statusIndex).toBeLessThan(alertIndex);
		expect(alertIndex).toBeLessThan(messageIndex);
		expect(body.match(/error notice/g)).toHaveLength(1);
	});

	it('leaves both live regions mounted after dismissal', () => {
		snackbarService.show('temporary notice', { timeout: -1 });
		snackbarService.dismiss();
		const body = renderHost();

		expectPersistentRegions(body);
		expect(body).not.toContain('temporary notice');
		expect(body).not.toContain('data-snackbar-item');
	});
});
