import { describe, expect, it, vi } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import type { Client } from '@connectrpc/connect';
import { PageInfoSchema } from '$lib/gen/pagination/v1/pagination_pb';
import {
	AppealSchema,
	AppealService,
	AppealStatus,
	DecisionOutcome,
	DecisionSchema,
	type Appeal
} from '$lib/gen/user/appeal/v1/appeal_pb';
import { ConnectAppealApi } from './connectAppealApi.ts';
import { counterparts, isAppealParticipant } from './appealApi.ts';

const createdAt = new Date('2026-11-01T02:00:00Z');
const appellant = '00000000-0000-4000-8000-000000000001';
const other = '00000000-0000-4000-8000-000000000002';

function protoAppeal(overrides: Partial<Appeal> = {}): Appeal {
	const message = create(AppealSchema, {
		id: 'appeal-id',
		appellantId: appellant,
		appellantName: 'Ann',
		reason: 'Unfair',
		status: AppealStatus.UNDER_REVIEW,
		decisions: [
			create(DecisionSchema, {
				id: 'd1',
				target: { case: 'reportId', value: 'report-id' },
				counterpartId: other,
				counterpartName: 'Oli'
			}),
			create(DecisionSchema, { id: 'd2', target: { case: 'warningId', value: 'warning-id' } }),
			create(DecisionSchema, {
				id: 'd3',
				target: { case: 'removedReviewId', value: 'review-id' },
				counterpartId: other,
				counterpartName: 'Oli'
			})
		],
		createdAt: timestampFromDate(createdAt),
		updatedAt: timestampFromDate(createdAt),
	});
	return Object.assign(message, overrides);
}

function clientWith(methods: Record<string, unknown>) {
	return methods as unknown as Client<typeof AppealService>;
}

describe('ConnectAppealApi', () => {
	it('maps decisions by target kind and leaves outcomes unset while open', async () => {
		const client = clientWith({ getAppeal: vi.fn(async () => ({ appeal: protoAppeal(), responses: [] })) });
		const { appeal } = await new ConnectAppealApi(client).getAppeal('appeal-id');

		expect(appeal.status).toBe('under_review');
		expect(appeal.requestId).toBeNull();
		expect(appeal.decisions.map((decision) => [decision.target, decision.outcome])).toEqual([
			[{ kind: 'report', id: 'report-id' }, null],
			[{ kind: 'warning', id: 'warning-id' }, null],
			[{ kind: 'removed_review', id: 'review-id' }, null]
		]);
	});

	it('maps decision outcomes once closed', async () => {
		const closed = protoAppeal({
			status: AppealStatus.CLOSED,
			decisions: [
				create(DecisionSchema, {
					id: 'd1',
					target: { case: 'retainedReviewId', value: 'review-id' },
					outcome: DecisionOutcome.OVERTURNED,
					outcomeReason: 'Abusive after all'
				})
			]
		});
		const client = clientWith({ getAppeal: vi.fn(async () => ({ appeal: closed, responses: [] })) });
		const { appeal } = await new ConnectAppealApi(client).getAppeal('appeal-id');

		expect(appeal.decisions[0]).toMatchObject({
			target: { kind: 'retained_review', id: 'review-id' },
			outcome: 'overturned',
			outcomeReason: 'Abusive after all'
		});
	});

	it('submits every decision kind with the idempotency key', async () => {
		const submitAppeal = vi.fn(async () => ({ appeal: protoAppeal() }));
		await new ConnectAppealApi(clientWith({ submitAppeal })).submitAppeal({
			idempotencyKey: 'key',
			reason: 'Please reconsider',
			decisions: [
				{ kind: 'report', id: 'r' },
				{ kind: 'warning', id: 'w' },
				{ kind: 'removed_review', id: 'rr' },
				{ kind: 'retained_review', id: 'kr' }
			],
			evidenceIds: ['e1']
		});

		expect(submitAppeal).toHaveBeenCalledWith({
			idempotencyKey: 'key',
			reason: 'Please reconsider',
			decisions: [
				{ target: { case: 'reportId', value: 'r' } },
				{ target: { case: 'warningId', value: 'w' } },
				{ target: { case: 'removedReviewId', value: 'rr' } },
				{ target: { case: 'retainedReviewId', value: 'kr' } }
			],
			evidenceIds: ['e1']
		});
	});

	it('closes an appeal with one resolution per decision', async () => {
		const closeAppeal = vi.fn(async () => ({ appeal: protoAppeal() }));
		await new ConnectAppealApi(clientWith({ closeAppeal })).closeAppeal('appeal-id', [
			{ decisionId: 'd1', outcome: 'upheld', reason: 'Stands' },
			{ decisionId: 'd2', outcome: 'overturned', reason: 'Wrong' }
		]);

		expect(closeAppeal).toHaveBeenCalledWith({
			id: 'appeal-id',
			resolutions: [
				{ decisionId: 'd1', outcome: DecisionOutcome.UPHELD, reason: 'Stands' },
				{ decisionId: 'd2', outcome: DecisionOutcome.OVERTURNED, reason: 'Wrong' }
			]
		});
	});

	it('lists the admin queue by status', async () => {
		const listAppeals = vi.fn(async () => ({
			appeals: [],
			pagination: create(PageInfoSchema, { page: 1n, pageSize: 20n, totalItems: 0n, totalPages: 0n })
		}));
		const page = await new ConnectAppealApi(clientWith({ listAppeals })).listAppeals({ status: 'open' });

		expect(listAppeals).toHaveBeenCalledWith({
			status: AppealStatus.OPEN,
			appellantId: undefined,
			pagination: { page: 1n, pageSize: 20n }
		});
		expect(page).toEqual({ appeals: [], page: 1, pageSize: 20, totalItems: 0, totalPages: 0 });
	});

	it('rejects incomplete responses', async () => {
		const client = clientWith({ requestResponse: vi.fn(async () => ({})) });
		await expect(new ConnectAppealApi(client).requestResponse('appeal-id', other)).rejects.toThrow(
			'did not include the appeal'
		);
	});
});

describe('appeal helpers', () => {
	it('lists each counterpart once and recognises participants', async () => {
		const client = clientWith({ getAppeal: vi.fn(async () => ({ appeal: protoAppeal(), responses: [] })) });
		const { appeal } = await new ConnectAppealApi(client).getAppeal('appeal-id');

		expect(counterparts(appeal)).toEqual([{ id: other, name: 'Oli' }]);
		expect(isAppealParticipant(appeal, appellant)).toBe(true);
		expect(isAppealParticipant(appeal, other)).toBe(true);
		expect(isAppealParticipant(appeal, 'someone-else')).toBe(false);
	});
});
