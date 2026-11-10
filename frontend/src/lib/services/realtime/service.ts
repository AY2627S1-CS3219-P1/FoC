import { Code, ConnectError } from '@connectrpc/connect';
import type { WatchChangesResponse } from '$lib/gen/communication/v1/realtime_pb';
import { ChangeScope } from '$lib/gen/communication/v1/realtime_pb';

export type Scope = 'credits' | 'orders' | 'openOrders';
export interface ChangeSource {
 subscribe(scopes: Scope[], read: () => Promise<unknown>): () => void;
}
export interface RealtimeApi {
 watch(signal: AbortSignal): AsyncIterable<WatchChangesResponse>;
}

/** Push, focus, and fallback all invalidate the same mounted reads. */
export class RealtimeService implements ChangeSource {
 readonly #reads = new Set<{ scopes: Scope[]; run: () => void }>();
 readonly #pending = new Set<Scope>();
 #flush: ReturnType<typeof setTimeout> | undefined;
 constructor(readonly api: RealtimeApi, readonly refreshAuth: () => Promise<void>) {}

 subscribe(scopes: Scope[], read: () => Promise<unknown>): () => void {
  let active = true, running = false, dirty = false;
  const run = () => {
   if (!active) return;
   if (running) { dirty = true; return; }
   running = true;
   void Promise.resolve().then(() => active && read()).catch((error) => {
    console.warn('Could not refresh current data', error);
   }).finally(() => {
    running = false;
    if (dirty) { dirty = false; run(); }
   });
  };
  const entry = { scopes, run };
  this.#reads.add(entry);
  run();
  return () => { active = false; this.#reads.delete(entry); };
 }

 invalidate(scopes: Scope[]): void {
  for (const scope of scopes) this.#pending.add(scope);
  if (this.#flush !== undefined) return;
  this.#flush = setTimeout(() => {
   this.#flush = undefined;
   if (typeof document !== 'undefined' && document.hidden) return;
   const dirty = new Set(this.#pending);
   this.#pending.clear();
   for (const read of this.#reads) if (read.scopes.some((scope) => dirty.has(scope))) read.run();
  }, 200);
 }

 refresh(): void {
  clearTimeout(this.#flush); this.#flush = undefined; this.#pending.clear();
  for (const read of this.#reads) read.run();
 }

 start(): () => void {
  const lifetime = new AbortController();
  let connection: AbortController | undefined;
  let lastMessage = Date.now();
  const visible = () => {
   if (!document.hidden) { this.refresh(); connection?.abort(); }
  };
  document.addEventListener('visibilitychange', visible);
  window.addEventListener('focus', visible);
  window.addEventListener('online', visible);
  const poll = setInterval(() => { if (!document.hidden) this.refresh(); }, 60_000);
  const watchdog = setInterval(() => {
   if (!document.hidden && Date.now() - lastMessage > 45_000) connection?.abort();
  }, 5_000);

  const wait = (ms: number) => new Promise<void>((resolve) => {
   const done = () => { clearTimeout(timer); lifetime.signal.removeEventListener('abort', done); resolve(); };
   const timer = setTimeout(done, ms);
   lifetime.signal.addEventListener('abort', done, { once: true });
   if (lifetime.signal.aborted) done();
  });
  void (async () => {
   let delay = 1000;
   while (!lifetime.signal.aborted) {
    connection = new AbortController();
    lastMessage = Date.now();
    try {
     for await (const message of this.api.watch(connection.signal)) {
      if (lifetime.signal.aborted || connection.signal.aborted) break;
      lastMessage = Date.now(); delay = 1000;
      if (message.ready) this.refresh();
      const scopes: Scope[] = [];
      if (message.scopes.includes(ChangeScope.CREDITS)) scopes.push('credits');
      if (message.scopes.includes(ChangeScope.ORDERS)) scopes.push('orders');
      if (message.scopes.includes(ChangeScope.OPEN_ORDERS)) scopes.push('openOrders');
      if (scopes.length) this.invalidate(scopes);
     }
    } catch (error) {
     if (!lifetime.signal.aborted && !connection.signal.aborted && ConnectError.from(error).code === Code.Unauthenticated) {
      try { await this.refreshAuth(); } catch { /* The shared auth service owns session state. */ }
     }
    }
    if (lifetime.signal.aborted) break;
    await wait(delay * (0.8 + Math.random() * 0.4));
    delay = Math.min(30_000, delay * 2);
   }
  })();

  return () => {
   lifetime.abort(); connection?.abort();
   clearInterval(poll); clearInterval(watchdog); clearTimeout(this.#flush);
   this.#flush = undefined; this.#pending.clear();
   document.removeEventListener('visibilitychange', visible);
   window.removeEventListener('focus', visible); window.removeEventListener('online', visible);
  };
 }
}
