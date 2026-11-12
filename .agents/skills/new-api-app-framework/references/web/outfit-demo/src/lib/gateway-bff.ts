import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { config } from '@/lib/config';
import { GatewayError } from '@/lib/gateway-errors';

const headers = { 'Cache-Control': 'no-store' };
export function gatewaySuccess<T>(data: T) {
  return NextResponse.json({ success: true, message: '', data, error_code: null }, { headers });
}
export function gatewayFailure(error: unknown) {
  const safe = error instanceof GatewayError
    ? error
    : new GatewayError(500, 'Service is temporarily unavailable. Please try again.', 'UPSTREAM_INTERNAL_ERROR');
  return NextResponse.json({ success: false, message: safe.message, data: null, error_code: safe.code }, { status: safe.status, headers });
}
export function requireGatewayOrigin(request: NextRequest): void {
  const origin = request.headers.get('origin');
  const target = request.nextUrl.origin;
  let configured = '';
  try { if (config.appOrigin) configured = new URL(config.appOrigin).origin; } catch { /* fail closed */ }
  // Do not trust client-supplied Host / X-Forwarded-Host as the production origin.
  if (!origin || origin !== target ||
      (process.env.NODE_ENV === 'production' && (!configured || target !== configured))) {
    throw new GatewayError(403, 'Request origin is not allowed.', 'AUTH_CSRF_REJECTED');
  }
}
