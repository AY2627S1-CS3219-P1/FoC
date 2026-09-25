import { env } from '$env/dynamic/public';
import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { HealthService } from '$lib/gen/foc/supplier/v1/health_pb';

const supplierTransport = createConnectTransport({
  baseUrl: env.PUBLIC_SUPPLIER_SERVICE_URL ?? 'http://localhost:8082',
  useBinaryFormat: false
});

export const supplierHealthClient = createClient(
  HealthService,
  supplierTransport
);
