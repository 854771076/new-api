// GET /api/admin/gateway-models — 网关 /v1/models 可用模型目录，供后台模型选择器使用。
import 'server-only';
import { type NextRequest, NextResponse } from 'next/server';
import { requireAdmin } from '@/lib/admin/requireAdmin';
import { GatewayError, listGatewayModels } from '@/lib/gateway';

const json = (body: unknown, status = 200) => NextResponse.json(body, { status, headers: { 'Cache-Control': 'no-store' } });

export async function GET(req: NextRequest) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;
  try {
    return json({ data: await listGatewayModels() });
  } catch (error) {
    if (error instanceof GatewayError) return json({ error: error.code }, error.status);
    return json({ error: 'UPSTREAM_GATEWAY_ERROR' }, 503);
  }
}
