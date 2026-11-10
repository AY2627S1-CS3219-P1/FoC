import { createClient, type Transport } from '@connectrpc/connect';
import { ReviewService } from '$lib/gen/user/review/v1/review_pb';
import { ConnectReviewApi } from './connectReviewApi.ts';

export type * from './reviewApi.ts';

export function createReviewApi(transport: Transport): ConnectReviewApi {
	return new ConnectReviewApi(createClient(ReviewService, transport));
}
