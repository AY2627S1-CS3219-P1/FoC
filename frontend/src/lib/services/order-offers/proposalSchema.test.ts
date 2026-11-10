import { describe, expect, it } from 'vitest';
import { superValidate } from 'sveltekit-superforms';
import { zod4 } from 'sveltekit-superforms/adapters';
import { proposalSchema } from './proposalSchema';

describe('counteroffer Superforms validation', () => {
 const schema = proposalSchema(() => 20n);
 it.each(['', '0', '-1', '20', '19', '01', '21.5', '1e3', ' 30', '30 ', 'abc', '9223372036854775808', '10000000000000000000'])('rejects invalid credits %j with a field error', async credits => {
  const form = await superValidate({ credits }, zod4(schema));
  expect(form.valid).toBe(false);
  expect(form.errors.credits).toEqual(['Enter whole credits above the current reward and within the supported range.']);
 });
 it.each(['21', '9007199254740993', '9223372036854775807'])('preserves exact int64 input %s', async credits => {
  const form = await superValidate({ credits }, zod4(schema));
  expect(form.valid).toBe(true);
  expect(form.data.credits).toBe(credits);
 });
 it('validates against the refreshed reward without recreating the form', async () => {
  let reward = 20n;
  const adapter = zod4(proposalSchema(() => reward));
  expect((await superValidate({ credits: '30' }, adapter)).valid).toBe(true);
  reward = 30n;
  expect((await superValidate({ credits: '30' }, adapter)).valid).toBe(false);
  expect((await superValidate({ credits: '31' }, adapter)).valid).toBe(true);
 });
});
