import 'server-only';

export class GatewayError extends Error {
  constructor(public status: number, message: string, public code = 'UPSTREAM_GATEWAY_ERROR') {
    super(message);
    this.name = 'GatewayError';
  }
}

export async function readGatewayError(response: Response): Promise<GatewayError> {
  const payload: unknown = await response.json().catch(() => null);
  const raw = payload && typeof payload === 'object' ? payload as Record<string, unknown> : {};
  const messages: Record<string, string> = {
    invalid_token: 'Gateway session expired. Please sign in again.',
    insufficient_scope: 'Gateway authorization does not allow this action.',
    invalid_client: 'Gateway application is unavailable.',
    invalid_request: 'Invalid gateway request.',
    unsupported_payment_method: 'This payment method is not supported.',
    checkout_failed: 'Checkout failed. Check your balance and payment configuration.',
    access_denied: 'This purchase is not available to this application.',
    not_found: 'The requested item was not found.',
    server_error: 'Gateway service is temporarily unavailable.',
  };
  const code = typeof raw.error === 'string' && Object.hasOwn(messages, raw.error) ? raw.error : '';
  // Read the OAuth description, but expose only recognized non-secret business messages.
  // Never return arbitrary descriptions, raw bodies, HTML, or transport errors.
  if (code === 'checkout_failed' && typeof raw.error_description === 'string' &&
      /^insufficient (balance|quota)[.!]?$/i.test(raw.error_description.trim())) {
    return new GatewayError(response.status, 'Insufficient balance.', 'UPSTREAM_CHECKOUT_FAILED');
  }
  return new GatewayError(
    response.status,
    messages[code] ?? 'Gateway request failed. Please try again.',
    code ? `UPSTREAM_${code.toUpperCase()}` : 'UPSTREAM_GATEWAY_ERROR',
  );
}
