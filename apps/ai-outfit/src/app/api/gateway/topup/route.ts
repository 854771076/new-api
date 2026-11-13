import type { NextRequest } from 'next/server';
import { fetchGatewayTopup, GatewayError } from '@/lib/gateway';
import { gatewayTopupSchema } from '@/lib/gateway-contract';
import { gatewayFailure, gatewaySuccess, requireGatewayOrigin } from '@/lib/gateway-bff';

export const runtime = 'nodejs';
export async function POST(request: NextRequest) {
  try {
    requireGatewayOrigin(request);
    const parsed = gatewayTopupSchema.safeParse(await request.json().catch(() => null));
    if (!parsed.success) throw new GatewayError(400, 'Amount must be an integer from 1 to 10000 and payment_method must be a supported payment method.', 'BAD_REQUEST_INVALID_BODY');
    return gatewaySuccess(await fetchGatewayTopup(parsed.data));
  } catch (error) { return gatewayFailure(error); }
}
