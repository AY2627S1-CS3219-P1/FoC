import type { Account } from '$lib/gen/credit/v1/shared_types_pb';
import type { ChangeSource } from '../realtime/service';

interface CreditReader { getAccount(): Promise<Account> }

export class CreditService {
 account = $state<Account | null>(null);
 failed = $state(false);
 #generation = 0;
 constructor(readonly api: CreditReader, readonly changes: ChangeSource) {}

 start(): () => void {
  const generation = ++this.#generation;
  this.account = null; this.failed = false;
  const stop = this.changes.subscribe(['credits'], async () => {
   try {
    const account = await this.api.getAccount();
    if (generation === this.#generation) { this.account = account; this.failed = false; }
   } catch {
    if (generation === this.#generation) this.failed = true;
   }
  });
  return () => { stop(); ++this.#generation; this.account = null; this.failed = false; };
 }
}
