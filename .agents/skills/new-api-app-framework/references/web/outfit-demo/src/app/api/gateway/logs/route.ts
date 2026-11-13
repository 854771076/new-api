import type { NextRequest } from 'next/server';
import { z } from 'zod';
import { fetchGatewayLogs, GatewayError } from '@/lib/gateway';
import { gatewayFailure, gatewaySuccess } from '@/lib/gateway-bff';

export const runtime = 'nodejs';
const querySchema = z.object({
  page: z.coerce.number().int().positive().max(1_000_000).default(1),
  page_size: z.coerce.number().int().positive().max(100).default(20),
});
export async function GET(request: NextRequest) {
  try {
    const parsed = querySchema.safeParse(Object.fromEntries(request.nextUrl.searchParams));
    if (!parsed.success) throw new GatewayError(400, 'Invalid pagination parameters.', 'BAD_REQUEST_INVALID_QUERY');
    return gatewaySuccess(await fetchGatewayLogs({ page: parsed.data.page, pageSize: parsed.data.page_size }));
  } catch (error) { return gatewayFailure(error); }
}
