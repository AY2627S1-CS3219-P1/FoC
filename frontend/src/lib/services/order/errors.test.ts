import { describe, expect, it } from 'vitest';
import { Code, ConnectError } from '@connectrpc/connect';
import { isDefinitiveRejection } from './errors.ts';

// An earlier uncertain dispatch must retain its identity when a later retry
// is denied by suspension before the retained creation is looked up.
describe('creation rejection certainty', () => {
	it('does not treat suspension denial as resolution of an earlier submission', () => {
		const denial = new ConnectError('Suspended after earlier admission', Code.PermissionDenied);
		expect(isDefinitiveRejection(denial)).toBe(false);
	});
});
