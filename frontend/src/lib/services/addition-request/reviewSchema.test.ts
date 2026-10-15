import { describe, expect, it } from 'vitest';
import { rejectionSchema } from './reviewSchema';
describe('rejection reasons', () => {
 it('requires a nonblank reason and applies Unicode limits after trimming', () => {
  expect(rejectionSchema.safeParse({ reason: '   ' }).success).toBe(false);
  expect(rejectionSchema.safeParse({ reason: '😀'.repeat(2000) }).success).toBe(true);
  expect(rejectionSchema.safeParse({ reason: '😀'.repeat(2001) }).success).toBe(false);
  expect(rejectionSchema.parse({ reason: ' Duplicate ' }).reason).toBe('Duplicate');
 });
});
