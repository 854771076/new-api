import type { NextRequest } from 'next/server';
import { logoutGatewaySession } from '@/lib/gateway-session';
import { gatewayFailure, gatewaySuccess, requireGatewayOrigin } from '@/lib/gateway-bff';

export const runtime = 'nodejs';
export async function POST(request: NextRequest) {
  try {
    requireGatewayOrigin(request);
    await logoutGatewaySession();
    return gatewaySuccess(null);
  } catch (error) { return gatewayFailure(error); }
}
