import { describe, expect, it } from 'vitest';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { OrderService } from '$lib/gen/order/v1/order_pb';
import { AssignmentAttemptResultSchema, AssignmentAttemptStatus, OrderStatus } from '$lib/gen/order/v1/shared_types_pb';
import { AcceptanceRejected, OrderActionRejected, createOrderOffersApi } from './index';
import { clearOfferAction, loadOfferAction, saveOfferAction, type RetryStorage } from './retry';
const key='c55ce32c-2af7-46d1-a088-6a976d3bdbaf';
const attempt='41dbf670-f32c-4cca-9473-5861c274e789';

// Source: required caller/method-scoped retry UUID, historical outcome and F0's
// acknowledged current-state projection. These are in-memory adapter tests.
describe('acceptance contract adapter',()=>{
 it('retains the request key and does not turn historical success into current assignment',async()=>{
  const requests:string[]=[];
  const api=createOrderOffersApi(createRouterTransport(router=>router.service(OrderService,{
   acceptOrder:async(req)=>{requests.push(req.attemptId);return {attempt:{attemptId:attempt,status:AssignmentAttemptStatus.SUCCEEDED},order:{id:req.id,status:OrderStatus.AWAITING_COURIER}};}
  })));
  const first=await api.acceptOrder('order',key);
  const replay=await api.acceptOrder('order',key);
  expect(requests).toEqual([key,key]);
  expect(first.attempt.status).toBe(AssignmentAttemptStatus.SUCCEEDED);
  expect(replay.order?.status).toBe(OrderStatus.AWAITING_COURIER);
  expect(replay.order?.courierId).toBeUndefined();
 });
 it.each([AssignmentAttemptStatus.PENDING,AssignmentAttemptStatus.COMPENSATING])('does not treat status %s as assignment success',async(status)=>{
  const api=createOrderOffersApi(createRouterTransport(router=>router.service(OrderService,{
   acceptOrder:async()=>({attempt:{attemptId:attempt,status}})
  })));
  const result=await api.acceptOrder('order',key);
  expect(result.attempt.status).toBe(status);
  expect(result.order).toBeUndefined();
 });
 it('decodes typed failed_precondition as failure rather than success',async()=>{
  const api=createOrderOffersApi(createRouterTransport(router=>router.service(OrderService,{
   acceptOrder:async()=>{throw new ConnectError('rejected',Code.FailedPrecondition,undefined,[{desc:AssignmentAttemptResultSchema,value:{attemptId:attempt,status:AssignmentAttemptStatus.REJECTED,rejection:'insufficient_credits'}}]);}
  })));
  try {await api.acceptOrder('order',key);throw new Error('expected rejection');}
  catch(error){expect(error).toBeInstanceOf(AcceptanceRejected);expect((error as AcceptanceRejected).attempt.attemptId).toBe(attempt);}
 });
 // Review F1 counterexample: a permanently void offer returned an untyped precondition
 // and locked every control into the same failing retry. Unknown results differ.
 it('resolves definitive admission rejection without fabricating an attempt outcome',async()=>{
  const api=createOrderOffersApi(createRouterTransport(router=>router.service(OrderService,{
   acceptOrder:async()=>{throw new ConnectError('unknown precondition',Code.FailedPrecondition);},
   acceptOffer:async()=>({})
  })));
  await expect(api.acceptOrder('order',key)).rejects.toBeInstanceOf(OrderActionRejected);
  await expect(api.acceptOrder('order',key)).rejects.not.toBeInstanceOf(AcceptanceRejected);
  await expect(api.acceptOffer('offer')).rejects.toThrow('valid attempt outcome');
 });
 // Source: durable recovery keeps unavailable/deadline/aborted outcomes uncertain.
 // Follow-up review: suspension may deny replay after the original was admitted.
 it.each([Code.Unavailable,Code.DeadlineExceeded,Code.Aborted,Code.PermissionDenied])('retains uncertainty for transport or processing code %s',async(code)=>{
  const api=createOrderOffersApi(createRouterTransport(router=>router.service(OrderService,{
   acceptOrder:async()=>{throw new ConnectError('unknown result',code);}
  })));
  await expect(api.acceptOrder('order',key)).rejects.not.toBeInstanceOf(OrderActionRejected);
 });
 // Source: MakeOffer validates price before admission and has no financial call.
 it('resolves a rejected proposal so its immutable input can be replaced deliberately',async()=>{
  const api=createOrderOffersApi(createRouterTransport(router=>router.service(OrderService,{
   makeOffer:async()=>{throw new ConnectError('bad price',Code.InvalidArgument);}
  })));
  await expect(api.makeOffer('order',1n,key)).rejects.toBeInstanceOf(OrderActionRejected);
 });
 it('preserves int64 proposal values and the original retry key',async()=>{
  const seen:unknown[]=[];
  const api=createOrderOffersApi(createRouterTransport(router=>router.service(OrderService,{
   makeOffer:async(req)=>{seen.push([req.orderId,req.credits,req.idempotencyKey]);return {offer:{id:attempt,orderId:req.orderId,credits:req.credits}};}
  })));
  await api.makeOffer('order',9007199254740993n,key);
  await api.makeOffer('order',9007199254740993n,key);
  expect(seen).toEqual([['order',9007199254740993n,key],['order',9007199254740993n,key]]);
 });
});

// Source: retry identity must survive refresh/uncertain outcomes without leaking
// across caller or Order boundaries. Corrupt state must not invent a new key.
it('recovers immutable saved input only for the same caller and order',()=>{
 const rows=new Map<string,string>();
 const storage:RetryStorage={getItem:k=>rows.get(k)??null,setItem:(k,v)=>{rows.set(k,v);},removeItem:k=>{rows.delete(k);}};
 const action={kind:'proposal' as const,key,credits:'50',outcome:'uncertain' as const};
 saveOfferAction(storage,'courier-a','order-a',action);
 expect(loadOfferAction(storage,'courier-a','order-a')).toEqual(action);
 expect(loadOfferAction(storage,'courier-b','order-a')).toBeNull();
 expect(loadOfferAction(storage,'courier-a','order-b')).toBeNull();
 rows.set([...rows.keys()][0],'{corrupt');
 expect(()=>loadOfferAction(storage,'courier-a','order-a')).toThrow('unreadable');
 clearOfferAction(storage,'courier-a','order-a');
 expect(loadOfferAction(storage,'courier-a','order-a')).toBeNull();
});
