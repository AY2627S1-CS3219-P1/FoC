// Shared with the offline acceptance tests. One slot is exactly one HTTP call.
export function category(slot) {
  const position = slot % 10;
  return position < 7 ? 'read' : position < 9 ? 'auth' : 'mutation';
}

export function validateResponse(method, body, expected = '') {
  if (!body || typeof body !== 'object' || Array.isArray(body) || body.code) return false;
  switch (method) {
    case 'Refresh': return typeof body.accessToken === 'string' && body.accessToken.length > 0 && body.user?.id === expected;
    case 'GetMyProfile':
    case 'UpdateMyProfile': return body.profile?.id === expected;
    case 'GetMyAccount': return body.account?.userId === expected && body.account?.balance !== undefined;
    case 'ListMyLedgerEntries':
    case 'ListMyOrders':
    case 'ListOpenOrders': return !!body.pageInfo;
    case 'ListLocations': return Number(body.page) > 0 && Number(body.pageSize) > 0;
    case 'GetOrder': return body.order?.id === expected && Number(body.order?.revision) > 0;
    case 'CreateOrder': return !!body.order?.id && body.order.status === 'ORDER_STATUS_AWAITING_COURIER';
    case 'CancelOrder': return body.order?.id === expected && body.order.status === 'ORDER_STATUS_CANCELLED';
    default: return false;
  }
}
