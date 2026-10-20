export type OfferAction = {
 kind: 'accept' | 'proposal' | 'counteroffer';
 key: string;
 credits?: string;
 offerId?: string;
 outcome: 'uncertain' | 'pending' | 'compensating' | 'succeeded' | 'rejected';
};
export type RetryStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
function storageKey(caller: string, order: string): string { return `order-offers:v1:${caller}:${order}`; }

/** Persist immutable request values before dispatch. Never store credentials or delivery data. */
export function saveOfferAction(storage: RetryStorage, caller: string, order: string, action: OfferAction): void {
 storage.setItem(storageKey(caller, order), JSON.stringify(action));
}
export function clearOfferAction(storage: RetryStorage, caller: string, order: string): void {
 storage.removeItem(storageKey(caller, order));
}
export function loadOfferAction(storage: RetryStorage, caller: string, order: string): OfferAction | null {
 const raw = storage.getItem(storageKey(caller, order));
 if (!raw) return null;
 if (raw.length > 4096) throw new Error('Stored retry data is invalid. Refresh current state before starting a new action.');
 let value: OfferAction;
 try { value = JSON.parse(raw); } catch { throw new Error('Stored retry data is unreadable. No new action was sent.'); }
 if (!value || !['accept','proposal','counteroffer'].includes(value.kind) || !uuid.test(value.key) ||
  !['uncertain','pending','compensating','succeeded','rejected'].includes(value.outcome) ||
  (value.kind === 'proposal' && (!value.credits || !/^[1-9][0-9]{0,18}$/.test(value.credits) || BigInt(value.credits) > 9223372036854775807n)) ||
  (value.kind === 'counteroffer' && (!value.offerId || !uuid.test(value.offerId)))) {
  throw new Error('Stored retry data is invalid. No new action was sent.');
 }
 return value;
}
