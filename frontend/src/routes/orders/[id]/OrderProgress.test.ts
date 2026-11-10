import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { render } from 'svelte/server';
import { describe, expect, it } from 'vitest';
import { OrderSchema, OrderStatus } from '$lib/gen/order/v1/shared_types_pb';
import OrderProgress from './OrderProgress.svelte';

describe('completion progress', () => {
 it.each([false, true])('shows completion provenance (automatic=%s)', (automaticallyCompleted) => {
  const order = create(OrderSchema, {
   status: OrderStatus.COMPLETED,
   completedAt: timestampFromDate(new Date('2026-11-10T11:00:00Z')),
   automaticallyCompleted
  });
  const body = render(OrderProgress, { props: { order } }).body;
  expect(body).toContain(automaticallyCompleted ? 'Completed automatically' : 'Receipt confirmed');
  expect(body).not.toContain(automaticallyCompleted ? 'Receipt confirmed' : 'Completed automatically');
 });
});
