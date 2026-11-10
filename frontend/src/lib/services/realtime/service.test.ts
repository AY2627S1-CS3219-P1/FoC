import { afterEach, describe, expect, it, vi } from 'vitest';
import { Code, ConnectError } from '@connectrpc/connect';
import { create } from '@bufbuild/protobuf';
import { WatchChangesResponseSchema, ChangeScope } from '$lib/gen/communication/v1/realtime_pb';
import { RealtimeService } from './service';

function source() {
 return new RealtimeService({ async *watch() { /* disconnected */ } }, async () => {});
}
async function settle() { for (let i = 0; i < 8; i++) await Promise.resolve(); }
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

describe('shared read invalidation', () => {
 it('coalesces bursts and performs one trailing read for an in-flight invalidation', async () => {
  vi.useFakeTimers();
  const service = source();
  let release!: () => void;
  const read = vi.fn().mockImplementationOnce(() => new Promise<void>((resolve) => { release = resolve; })).mockResolvedValue(undefined);
  const stop = service.subscribe(['credits'], read);
  await settle();
  for (let i = 0; i < 50; i++) service.invalidate(['credits']);
  await vi.advanceTimersByTimeAsync(200);
  expect(read).toHaveBeenCalledTimes(1);
  release(); await settle();
  expect(read).toHaveBeenCalledTimes(2);
  service.invalidate(['orders']); await vi.advanceTimersByTimeAsync(200);
  expect(read).toHaveBeenCalledTimes(2);
  stop(); service.refresh(); await settle();
  expect(read).toHaveBeenCalledTimes(2);
 });

 it('does not start queued work after the view is disposed', async () => {
  const read = vi.fn(); const service = source();
  const stop = service.subscribe(['orders'], read); stop();
  await settle(); expect(read).not.toHaveBeenCalled();
 });

 it('uses ready, focus, visibility and the slow fallback to recover missed hints', async () => {
  vi.useFakeTimers();
  const document = Object.assign(new EventTarget(), { hidden: false });
  const window = new EventTarget();
  vi.stubGlobal('document', document); vi.stubGlobal('window', window);
  let deliver!: (value: ReturnType<typeof create<typeof WatchChangesResponseSchema>>) => void;
  const api = { async *watch(signal: AbortSignal) {
   yield create(WatchChangesResponseSchema, { ready: true });
   const message = await new Promise<ReturnType<typeof create<typeof WatchChangesResponseSchema>> | null>((resolve) => {
    deliver = resolve; signal.addEventListener('abort', () => resolve(null), { once: true });
   });
   if (message) yield message;
   if (!signal.aborted) await new Promise<void>((resolve) => signal.addEventListener('abort', () => resolve(), { once: true }));
  } };
  const service = new RealtimeService(api, async () => {});
  const read = vi.fn().mockResolvedValue(undefined);
  const dispose = service.subscribe(['credits'], read);
  await settle(); const stop = service.start(); await settle();
  expect(read).toHaveBeenCalledTimes(2);
  deliver(create(WatchChangesResponseSchema, { scopes: [ChangeScope.CREDITS] }));
  await settle(); await vi.advanceTimersByTimeAsync(200);
  expect(read.mock.calls.length).toBeGreaterThanOrEqual(3);
  document.hidden = true;
  const before = read.mock.calls.length;
  await vi.advanceTimersByTimeAsync(60_000); expect(read).toHaveBeenCalledTimes(before);
  document.hidden = false; document.dispatchEvent(new Event('visibilitychange'));
  await settle(); expect(read.mock.calls.length).toBeGreaterThan(before);
  const visible = read.mock.calls.length;
  await vi.advanceTimersByTimeAsync(60_000); expect(read.mock.calls.length).toBeGreaterThan(visible);
  stop(); dispose();
 });

 it('refreshes authentication centrally after a mid-stream expiry', async () => {
  vi.useFakeTimers();
  vi.stubGlobal('document', Object.assign(new EventTarget(), { hidden: false }));
  vi.stubGlobal('window', new EventTarget());
  const refresh = vi.fn().mockResolvedValue(undefined);
  const service = new RealtimeService({ async *watch() { throw new ConnectError('expired', Code.Unauthenticated); } }, refresh);
  const stop = service.start(); await settle();
  expect(refresh).toHaveBeenCalledOnce();
  stop(); await vi.advanceTimersByTimeAsync(60_000);
  expect(refresh).toHaveBeenCalledOnce();
 });
});
