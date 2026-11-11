import 'server-only';

export class GatewayError extends Error {
  constructor(public status: number, message: string, public code = 'UPSTREAM_GATEWAY_ERROR') {
    super(message);
    this.name = 'GatewayError';
  }
}

// OAuth 管理面（/oauth/*）错误信封：扁平的 { error, error_description }。
const OAUTH_MESSAGES: Record<string, string> = {
  invalid_token: '网关登录态已失效，请重新登录。',
  insufficient_scope: '当前授权不允许该操作，请重新登录授权。',
  invalid_client: '网关应用暂不可用，请联系管理员。',
  invalid_request: '请求参数无效，请检查后重试。',
  unsupported_payment_method: '暂不支持该支付方式。',
  access_denied: '该应用无权进行此购买。',
  not_found: '请求的项目不存在。',
  server_error: '网关服务暂时不可用，请稍后重试。',
};

// AI relay（/v1/*）错误信封：嵌套的 { error: { code, message, type } }。
// 只按受控 code 给中文提示，绝不回传任意上游 message（可能含内部地址/请求号）。
const RELAY_CODE_MESSAGES: Record<string, string> = {
  model_not_found: '所选模型在网关暂无可用渠道，请联系管理员更换模型或配置渠道。',
  invalid_request_error: '请求被网关拒绝，请检查素材后重试。',
  invalid_request: '请求被网关拒绝，请检查素材后重试。',
  bad_request_body: '请求内容有误，请检查后重试。',
  rate_limit_exceeded: '请求过于频繁或已达额度上限，请稍后再试。',
  insufficient_quota: '网关额度不足，请先充值后再生成。',
  access_denied: '网关拒绝了本次请求，请确认授权后重试。',
  sensitive_words_detected: '内容包含敏感信息，无法生成，请调整后重试。',
  do_request_failed: '上游模型服务暂时不可用，请稍后重试。',
  get_channel_failed: '网关暂无可用模型渠道，请联系管理员。',
  channel_no_available_key: '模型渠道暂不可用，请联系管理员。',
  bad_response: '模型返回内容异常，请稍后重试。',
  bad_response_body: '模型返回内容异常，请稍后重试。',
  read_response_body_failed: '模型响应读取失败，请稍后重试。',
  response_time_exceeded: '模型响应超时，请稍后重试。',
};

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' ? value as Record<string, unknown> : null;
}

function safeText(value: unknown, max = 300): string {
  return typeof value === 'string' ? value.trim().slice(0, max) : '';
}

// checkout_failed 的具体原因只放行可识别的业务语义，其余一律用通用支付提示，
// 避免把任意上游描述（可能携带内部信息）直接展示给用户。
function checkoutMessage(description: string): { message: string; code: string } {
  const d = description.trim().toLowerCase();
  if (/^insufficient (balance|quota)[.!]?$/.test(d)) {
    return { message: '余额或积分不足，请先充值后再支付。', code: 'UPSTREAM_CHECKOUT_FAILED' };
  }
  if (d.includes('compliance')) {
    return { message: '支付功能尚未由管理员开通，请联系管理员。', code: 'UPSTREAM_PAYMENT_DISABLED' };
  }
  if (d.includes('waffo') || d.includes('configuration is incomplete') || d.includes('not fully configured') || d.includes('credentials are not configured')) {
    return { message: '在线支付通道尚未配置完成，请联系管理员或改用余额支付。', code: 'UPSTREAM_PAYMENT_NOT_CONFIGURED' };
  }
  if (d.includes('cannot be less than') || d.includes('amount is too low') || d.includes('top-up amount')) {
    return { message: '支付金额不满足最低限额，请调整金额后重试。', code: 'UPSTREAM_CHECKOUT_AMOUNT_INVALID' };
  }
  if (d.includes('not enabled') || d.includes('purchase limit') || d.includes('does not exist')) {
    return { message: '该商品或套餐当前不可购买，请刷新后重试或联系管理员。', code: 'UPSTREAM_CHECKOUT_UNAVAILABLE' };
  }
  return { message: '下单失败，请检查余额与支付配置后重试，或联系管理员。', code: 'UPSTREAM_CHECKOUT_FAILED' };
}

export async function readGatewayError(response: Response): Promise<GatewayError> {
  const payload: unknown = await response.json().catch(() => null);
  const raw = asRecord(payload) ?? {};

  // 1) AI relay 嵌套错误 { error: { code, message, type } }（图像/对话生成走 /v1/*）。
  const nested = asRecord(raw.error);
  if (nested && typeof nested.code !== 'undefined') {
    const code = safeText(nested.code).toLowerCase();
    const message = RELAY_CODE_MESSAGES[code]
      ?? (response.status >= 500
        ? '模型服务暂时不可用，请稍后重试。'
        : '生成请求被网关拒绝，请检查素材或更换模型后重试。');
    return new GatewayError(response.status, message, code ? `UPSTREAM_RELAY_${code.toUpperCase()}` : 'UPSTREAM_RELAY_ERROR');
  }

  // 2) OAuth 扁平错误 { error, error_description }。
  const oauthCode = typeof raw.error === 'string' ? raw.error : '';
  if (oauthCode === 'checkout_failed') {
    const picked = checkoutMessage(safeText(raw.error_description));
    return new GatewayError(response.status, picked.message, picked.code);
  }
  if (Object.hasOwn(OAUTH_MESSAGES, oauthCode)) {
    return new GatewayError(response.status, OAUTH_MESSAGES[oauthCode], `UPSTREAM_${oauthCode.toUpperCase()}`);
  }

  // 3) 无法识别的信封（含 HTML 网关错误页、空响应）：用状态码给安全兜底，不回传正文。
  const fallback = response.status >= 500
    ? '网关服务暂时不可用，请稍后重试。'
    : '网关请求失败，请稍后重试。';
  return new GatewayError(response.status, fallback, oauthCode ? `UPSTREAM_${oauthCode.toUpperCase()}` : 'UPSTREAM_GATEWAY_ERROR');
}
