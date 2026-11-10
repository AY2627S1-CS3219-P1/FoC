import test from 'node:test';
import assert from 'node:assert/strict';
import { category, validateResponse } from './workload.mjs';

test('every complete deterministic cycle has exactly 70/20/10 calls at any worker offset', () => {
  for (let offset = 0; offset < 100; offset++) {
    const counts = { read: 0, auth: 0, mutation: 0 };
    for (let slot = offset; slot < offset + 100; slot++) counts[category(slot)]++;
    assert.deepEqual(counts, { read: 70, auth: 20, mutation: 10 });
  }
});

test('HTTP 200 alone is insufficient: errors, wrong identity, and pending mutations fail', () => {
  assert.equal(validateResponse('GetMyProfile', { profile: { id: 'other' } }, 'me'), false);
  assert.equal(validateResponse('GetMyProfile', { code: 'permission_denied', profile: { id: 'me' } }, 'me'), false);
  assert.equal(validateResponse('CreateOrder', { order: { id: 'id', status: 'ORDER_STATUS_PENDING_RESERVATION' } }), false);
  assert.equal(validateResponse('CancelOrder', { order: { id: 'id', status: 'ORDER_STATUS_CANCELLED' } }, 'id'), true);
  assert.equal(validateResponse('ListOpenOrders', { pageInfo: { totalItems: '0' } }), true);
  assert.equal(validateResponse('Refresh', { accessToken: 'signed', user: { id: 'me' } }, 'me'), true);
});
