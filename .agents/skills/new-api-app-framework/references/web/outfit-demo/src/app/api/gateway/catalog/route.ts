import { fetchGatewayCatalog } from '@/lib/gateway';
import { gatewayFailure, gatewaySuccess } from '@/lib/gateway-bff';

export const runtime = 'nodejs';
export async function GET() {
  try { return gatewaySuccess(await fetchGatewayCatalog()); }
  catch (error) { return gatewayFailure(error); }
}
