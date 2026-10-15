import { z } from 'zod';
import { timestampDate, timestampFromDate, type Timestamp } from '@bufbuild/protobuf/wkt';
import type { Disablement } from '$lib/gen/supplier/location/v1/shared_types_pb';
import type { ClosureInput } from './disablementApi';
/** Parse Singapore wall-clock input independently of the browser timezone. */
export function singaporeDate(value: string): Date | undefined {
 if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(value)) return undefined;
 const date = new Date(`${value}+08:00`);
 if (!Number.isFinite(date.getTime())) return undefined;
 const wall = new Date(date.getTime() + 8 * 3600000).toISOString().slice(0, value.length);
 return wall === value ? date : undefined;
}
export function singaporeInput(value?: Timestamp): string {
 if (!value) return '';
 return new Date(timestampDate(value).getTime() + 8 * 3600000).toISOString().slice(0, 19);
}
export const closureSchema = z.object({
 startsAt: z.string().refine((v) => !v || !!singaporeDate(v), 'Enter a valid Singapore date and time.'),
 endsAt: z.string().refine((v) => !v || !!singaporeDate(v), 'Enter a valid Singapore date and time.'),
 reason: z.string().trim().refine((v) => Array.from(v).length >= 1 && Array.from(v).length <= 500, 'Enter a reason of 1–500 characters.')
}).superRefine((v, ctx) => {
 const start = v.startsAt ? singaporeDate(v.startsAt) : new Date();
 const end = v.endsAt ? singaporeDate(v.endsAt) : undefined;
 if (start && end && end <= start) ctx.addIssue({ code: 'custom', path: ['endsAt'], message: 'End must be later than the start.' });
});
export type ClosureForm = z.infer<typeof closureSchema>;
export function closureForm(value?: Disablement): ClosureForm {
 return { startsAt: singaporeInput(value?.startsAt), endsAt: singaporeInput(value?.endsAt), reason: value?.reason ?? '' };
}
export function closureInput(value: ClosureForm): ClosureInput {
 return { startsAt: value.startsAt ? timestampFromDate(singaporeDate(value.startsAt)!) : undefined,
  endsAt: value.endsAt ? timestampFromDate(singaporeDate(value.endsAt)!) : undefined, reason: value.reason.trim() };
}
export function changedClosurePaths(before: ClosureForm, after: ClosureForm): string[] {
 return (['startsAt', 'endsAt', 'reason'] as const).filter((key) => before[key] !== after[key]).map((key) => ({ startsAt: 'starts_at', endsAt: 'ends_at', reason: 'reason' })[key]);
}
