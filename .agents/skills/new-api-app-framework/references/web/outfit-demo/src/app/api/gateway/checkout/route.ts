import type { NextRequest } from 'next/server';
import { fetchGatewayCheckout, GatewayError } from '@/lib/gateway';
import { gatewayCheckoutSchema } from '@/lib/gateway-contract';
import { gatewayFailure, gatewaySuccess, requireGatewayOrigin } from '@/lib/gateway-bff';

export const runtime = 'nodejs';
export async function POST(request: NextRequest) {
  try {
    requireGatewayOrigin(request);
    const parsed = gatewayCheckoutSchema.safeParse(await request.json().catch(() => null));
    if (!parsed.success) throw new GatewayError(400, 'Invalid checkout request.', 'BAD_REQUEST_INVALID_BODY');
    return gatewaySuccess(await fetchGatewayCheckout(parsed.data));
  } catch (error) { return gatewayFailure(error); }
}
