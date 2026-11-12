import { fetchGatewayPaymentMethods } from '@/lib/gateway';
import { gatewayFailure, gatewaySuccess } from '@/lib/gateway-bff';

export const runtime = 'nodejs';
export async function GET() {
  try { return gatewaySuccess({ payment_methods: await fetchGatewayPaymentMethods() }); }
  catch (error) { return gatewayFailure(error); }
}
