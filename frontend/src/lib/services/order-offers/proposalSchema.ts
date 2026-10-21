import { z } from 'zod';

// Read the current reward at validation time, including after an order refresh.
export function proposalSchema(currentReward: () => bigint) {
 return z.object({
  credits: z.string().refine(value => {
   if (!/^[1-9][0-9]{0,18}$/.test(value)) return false;
   const credits = BigInt(value);
   return credits <= 9223372036854775807n && credits > currentReward();
  }, 'Enter whole credits above the current reward and within the supported range.')
 });
}
