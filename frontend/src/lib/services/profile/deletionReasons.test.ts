import { describe, expect, it } from 'vitest';
import { deletionReasons, reasonText } from './deletionReasons';

// Keep this list in sync with user-service/internal/accountdeletion/drain.go and
// worker.go: every reason code the worker can persist must render as human text.
const workerReasonCodes = [
	'order_outstanding',
	'offer_open',
	'pending_workflow',
	'pending_admission',
	'credit_reserved_balance',
	'credit_reservation',
	'credit_unsettled_assignment',
	'credit_pending_financial_work',
	'admission_grace',
	'check_unavailable'
];

describe('deletionReasons', () => {
	it('maps every worker reason code to human-readable text', () => {
		for (const code of workerReasonCodes) {
			expect(deletionReasons[code]).toBeTruthy();
			expect(reasonText(code)).toBe(deletionReasons[code]);
		}
	});

	it('falls back to a generic, non-empty line for an unknown code', () => {
		const text = reasonText('something_new');
		expect(text).toContain('something_new');
	});
});
