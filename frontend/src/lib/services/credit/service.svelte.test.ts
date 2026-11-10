import { describe, expect, it } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { AccountSchema } from '$lib/gen/credit/v1/shared_types_pb';
import { CreditService } from './service.svelte';

describe('shared credit state', () => {
 it('ignores an old account response after switching identity', async () => {
  const callbacks: (() => Promise<unknown>)[] = [];
  let release!: (value: ReturnType<typeof create<typeof AccountSchema>>) => void;
  const api = { getAccount: () => new Promise<ReturnType<typeof create<typeof AccountSchema>>>((resolve) => { release = resolve; }) };
  const service = new CreditService(api, { subscribe: (_scopes, read) => { callbacks.push(read); return () => {}; } });
  const stop = service.start(); const old = callbacks[0](); stop();
  service.start(); release(create(AccountSchema, { userId: 'old', available: 100n }));
  await old; expect(service.account).toBeNull();
  const current = callbacks[1](); release(create(AccountSchema, { userId: 'new', available: 80n }));
  await current; expect(service.account?.available).toBe(80n);
 });
});
