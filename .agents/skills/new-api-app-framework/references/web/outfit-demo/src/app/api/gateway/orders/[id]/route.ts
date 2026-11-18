import { fetchGatewayOrder } from '@/lib/gateway';
import { gatewayFailure, gatewaySuccess } from '@/lib/gateway-bff';

export async function GET(_request: Request, context: { params: Promise<{ id: string }> }) {
  try { return gatewaySuccess(await fetchGatewayOrder((await context.params).id)); }
  catch (error) { return gatewayFailure(error); }
}
