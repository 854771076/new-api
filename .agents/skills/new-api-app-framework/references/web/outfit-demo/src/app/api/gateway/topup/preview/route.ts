import type { NextRequest } from 'next/server';
import { fetchGatewayTopupPreview, GatewayError } from '@/lib/gateway';
import { gatewayTopupSchema } from '@/lib/gateway-contract';
import { gatewayFailure, gatewaySuccess, requireGatewayOrigin } from '@/lib/gateway-bff';

export const runtime = 'nodejs';
export async function POST(request: NextRequest) {
  try {
    requireGatewayOrigin(request);
    const parsed = gatewayTopupSchema.safeParse(await request.json().catch(() => null));
    if (!parsed.success) throw new GatewayError(400, 'Invalid top-up preview.', 'BAD_REQUEST_INVALID_BODY');
    return gatewaySuccess(await fetchGatewayTopupPreview(parsed.data));
  } catch (error) { return gatewayFailure(error); }
}
