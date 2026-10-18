import { createClient, Code, ConnectError, type Transport } from '@connectrpc/connect';
import { OrderService } from '$lib/gen/order/v1/order_pb';
import {
 AssignmentAttemptResultSchema, AssignmentAttemptStatus,
 type AssignmentAttemptResult, type Offer, type Order
} from '$lib/gen/order/v1/shared_types_pb';

export interface AcceptanceResult { attempt: AssignmentAttemptResult; order?: Order; offer?: Offer }
export interface OrderOffersApi {
 listOffers(orderId: string): Promise<Offer[]>;
 makeOffer(orderId: string, credits: bigint, key: string): Promise<Offer>;
 acceptOrder(orderId: string, attemptId: string): Promise<AcceptanceResult>;
 acceptOffer(offerId: string): Promise<AcceptanceResult>;
}

/** A rejected attempt is still failure, not a successful mutation response. */
export class AcceptanceRejected extends Error {
 constructor(readonly attempt: AssignmentAttemptResult) {
  super(attempt.rejection === 'insufficient_credits'
   ? 'Insufficient credits. The proposal remains open after compensation is confirmed.'
   : 'This acceptance attempt was rejected. Refresh the current order.');
  this.name = 'AcceptanceRejected';
 }
}

function requiredAttempt(attempt: AssignmentAttemptResult | undefined): AssignmentAttemptResult {
 if (!attempt?.attemptId || ![
  AssignmentAttemptStatus.PENDING, AssignmentAttemptStatus.COMPENSATING,
  AssignmentAttemptStatus.SUCCEEDED, AssignmentAttemptStatus.REJECTED
 ].includes(attempt.status)) throw new Error('Acceptance response did not contain a valid attempt outcome');
 return attempt;
}

/** Keep transport uncertainty distinct from an acknowledged business rejection. */
function throwAcceptanceError(cause: unknown): never {
 const error = ConnectError.from(cause);
 if (error.code === Code.FailedPrecondition) {
  const attempt = error.findDetails(AssignmentAttemptResultSchema)
   .find(detail => detail.status === AssignmentAttemptStatus.REJECTED && detail.attemptId);
  if (attempt) throw new AcceptanceRejected(attempt);
 }
 throw cause;
}

export function createOrderOffersApi(transport: Transport): OrderOffersApi {
 const client = createClient(OrderService, transport);
 return {
  async listOffers(orderId) { return (await client.listOffers({ orderId })).offers; },
  async makeOffer(orderId, credits, idempotencyKey) {
   const result = (await client.makeOffer({ orderId, credits, idempotencyKey })).offer;
   if (!result) throw new Error('Proposal response did not contain an offer');
   return result;
  },
  async acceptOrder(id, attemptId) {
   try {
    const result = await client.acceptOrder({ id, attemptId });
    return { attempt: requiredAttempt(result.attempt), order: result.order };
   } catch (cause) { return throwAcceptanceError(cause); }
  },
  async acceptOffer(id) {
   try {
    const result = await client.acceptOffer({ id });
    return { attempt: requiredAttempt(result.attempt), order: result.order, offer: result.offer };
   } catch (cause) { return throwAcceptanceError(cause); }
  }
 };
}
