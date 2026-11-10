import http from 'k6/http';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { Counter, Gauge, Rate, Trend } from 'k6/metrics';
import { category, validateResponse } from './workload.mjs';

const base = (__ENV.PERF_BASE_URL || '').replace(/\/$/, '');
if (!/^https:\/\/[^/?#@]+$/.test(base)) throw new Error('PERF_BASE_URL must be a public HTTPS origin');
const vus = Number(__ENV.PERF_VUS || 20000);
const accountsPerVU = Number(__ENV.PERF_ACCOUNTS_PER_VU || 1);
const warmup = Number(__ENV.PERF_WARMUP_SECONDS || 120);
const measurement = Number(__ENV.PERF_MEASURE_SECONDS || 600);
const fixturePath = __ENV.PERF_FIXTURES || '../../.local/perf/fixtures.json';
const sessions = new SharedArray('sessions', () => JSON.parse(open(__ENV.PERF_SESSIONS || '../../.local/perf/sessions.json')));
const locations = new SharedArray('locations', () => JSON.parse(open(fixturePath)).locationIds);
const orders = new SharedArray('orders', () => JSON.parse(open(fixturePath)).openIds);
if (sessions.length < vus * accountsPerVU) throw new Error('Need exclusive sessions for every worker/account slot');
if (locations.length < 2 || !orders.length || vus < 1 || accountsPerVU < 1 || warmup < 1 || measurement < 1) throw new Error('Invalid workload configuration');

const fullCall = new Trend('full_call_ms', true);
const timely = new Rate('successful_within_500ms');
const failed = new Rate('attempt_failed');
const attempts = new Counter('attempts');
// Raw Gauge points are signed call boundaries, not a Gauge of aggregate VUs.
// report.py integrates these across each measurement second.
const boundary = new Gauge('call_boundary');
const window = new Gauge('measurement_window_ms');

export const options = {
  scenarios: { api: { executor: 'constant-vus', vus, duration: `${warmup + measurement}s`, gracefulStop: '30s' } },
  setupTimeout: '30s',
  noCookiesReset: true,
  throw: false,
  maxRedirects: 0,
  summaryTrendStats: ['med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  // Endpoint names carry no record IDs, query strings or bearer tokens.
  systemTags: ['status', 'method', 'name', 'scenario', 'error_code'],
  thresholds: {
    'full_call_ms{phase:measured}': ['p(95)<=500'],
    'successful_within_500ms{phase:measured}': ['rate>=0.95'],
    'attempt_failed{phase:measured}': ['rate<0.01'],
  },
};

export function setup() {
  // Validate pool uniqueness once, outside the measured scenario.
  const identities = new Set();
  for (const s of sessions) {
    if (!s.userId || !s.accessToken || !s.refreshCookie || identities.has(s.userId)) throw new Error('Invalid or shared session identity');
    identities.add(s.userId);
  }
  return {};
}

let states;
let selected = -1;
let count = 0;

function uuid() {
  // UUIDs identify independent API attempts; fixture records use the fixed seed.
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = Math.floor(Math.random() * 16);
    return (c === 'x' ? r : (r & 3) | 8).toString(16);
  });
}

export default function () {
  const timing = { measurementStart: exec.scenario.startTime + warmup * 1000,
    end: exec.scenario.startTime + (warmup + measurement) * 1000 };
  if (!states) states = Array.from({ length: accountsPerVU }, (_, i) => ({
    ...sessions[(exec.vu.idInTest - 1) * accountsPerVU + i], pending: null, creating: null,
  }));
  if (exec.vu.idInTest === 1 && count === 0) {
    window.add(exec.scenario.startTime, { edge: 'warmupStart' });
    window.add(timing.measurementStart, { edge: 'start' });
    window.add(timing.end, { edge: 'end' });
  }
  const accountIndex = Math.floor(count / 100) % accountsPerVU;
  const account = states[accountIndex];
  const jar = http.cookieJar();
  if (selected !== accountIndex) {
    jar.set(`${base}/user.v1.AuthService/`, 'foc-refresh-token', account.refreshCookie,
      { path: '/user.v1.AuthService/', secure: true });
    selected = accountIndex;
  }
  const slot = (count + exec.vu.idInTest * 37) % 100;
  const group = category(slot);
  const index = count + exec.vu.idInTest;
  const page = 1 + (index % 50);
  let service, method, body = {}, expected = account.userId;
  if (group === 'read') {
    switch (index % 6) {
      case 0: service = 'order.v1.OrderService'; method = 'ListOpenOrders'; body = { page, pageSize: 20, ...(index % 3 === 0 ? { pickupLocationId: locations[index % locations.length] } : {}) }; break;
      case 1: service = 'order.v1.OrderService'; method = 'GetOrder'; expected = orders[index % orders.length]; body = { id: expected }; break;
      case 2: service = 'order.v1.OrderService'; method = 'ListMyOrders'; body = { page: 1 + index % 5, pageSize: 20 }; break;
      case 3: service = 'supplier.location.v1.LocationDiscoveryService'; method = 'ListLocations'; body = { search: `supplier ${index % 100}`, suppliersOnly: true, page: 1 + index % 3, pageSize: 20, sortDirection: index % 2 ? 'SORT_DIRECTION_ASCENDING' : 'SORT_DIRECTION_DESCENDING' }; break;
      case 4: service = 'credit.v1.CreditAccountService'; method = 'GetMyAccount'; break;
      default: service = 'credit.v1.CreditAccountService'; method = 'ListMyLedgerEntries'; body = { page: 1 + index % 3, pageSize: 20 }; break;
    }
  } else if (group === 'auth') {
    if (slot % 10 === 7) { service = 'user.v1.AuthService'; method = 'Refresh'; }
    else {
      service = 'user.v1.ProfileService';
      method = index % 2 ? 'GetMyProfile' : 'UpdateMyProfile';
      if (method === 'UpdateMyProfile') body = { displayName: 'Performance user', description: `Private load fixture ${index % 100}`, phoneNumber: '+6591234567' };
    }
  } else {
    service = 'order.v1.OrderService';
    if (account.pending) {
      method = 'CancelOrder'; expected = account.pending.id;
      body = { id: expected, expectedRevision: account.pending.revision, idempotencyKey: account.pending.cancelKey, reason: 'Private performance run' };
    } else {
      method = 'CreateOrder';
      const location = index % locations.length;
      body = account.creating || { pickupLocationId: locations[location], delivery: { locationId: locations[(location + 1) % locations.length] }, itemDescription: 'Private performance run', rewardCredits: '1', deliveryDuration: '1800s', expiresAt: new Date(Date.now() + 3600000).toISOString(), idempotencyKey: uuid() };
      account.creating = body;
    }
  }
  const start = Date.now();
  // The executor stops at scenario end. Never start calls in the drain.
  if (start >= timing.end) return;
  const phase = start >= timing.measurementStart ? 'measured' : 'warmup';
  const tags = { phase, category: group, name: `${service}/${method}` };
  boundary.add(1, { phase });
  attempts.add(1, tags);
  let response, data, ended, success = false, cause = 'transport';
  try {
    response = http.post(`${base}/${service}/${method}`, JSON.stringify(body), {
      timeout: '25s', tags,
      headers: { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1', Origin: base, Authorization: `Bearer ${account.accessToken}` },
    });
    ended = Date.now();
    boundary.add(-1, { phase });
    try { data = response.json(); } catch { cause = 'invalid_json'; }
    if (data) {
      success = response.status === 200 && validateResponse(method, data, expected);
      cause = success ? 'none' : response.status === 429 ? 'throttled' : data.code || `invalid_response_${response.status}`;
      if (success && method === 'Refresh') {
        const cookie = response.cookies['foc-refresh-token']?.[0]?.value;
        if (!cookie || cookie === account.refreshCookie) {
          success = false;
          cause = 'invalid_refresh_cookie';
        } else {
          account.accessToken = data.accessToken;
          account.refreshCookie = cookie;
        }
      }
    }
  } finally {
    // Includes DNS, connect, TLS, send and complete response receipt. Parsing
    // and assertions do not inflate measured HTTP concurrency or call latency.
    if (ended === undefined) { ended = Date.now(); boundary.add(-1, { phase }); }
    const elapsed = ended - start;
    fullCall.add(elapsed, tags);
    timely.add(success && elapsed <= 500, tags);
    failed.add(!success, { ...tags, cause });
  }
  if (success && method === 'CreateOrder') {
    account.pending = { id: data.order.id, revision: data.order.revision, cancelKey: uuid() };
    account.creating = null;
  }
  if (success && method === 'CancelOrder') account.pending = null;
  count++;
}

export function handleSummary(data) {
  return { [__ENV.PERF_SUMMARY || '../../.local/perf/k6-summary.json']: JSON.stringify(data, null, 2) };
}

export function teardown() {
  window.add(Date.now(), { edge: 'finished' });
}
