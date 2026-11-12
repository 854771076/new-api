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
// key 必须与 relaykit/types/error.go 的 ErrorCode 常量一致（含冒号前缀的 channel:* 系列）。
const RELAY_CODE_MESSAGES: Record<string, string> = {
  // 请求参数 / 请求体
  invalid_request: '请求被网关拒绝，请检查素材后重试。',
  invalid_request_error: '请求被网关拒绝，请检查素材后重试。',
  bad_request_body: '请求内容有误，请检查后重试。',
  read_request_body_failed: '请求读取失败，请稍后重试。',
  convert_request_failed: '请求转换失败，请检查素材格式后重试。',
  // 鉴权 / 配额
  access_denied: '网关拒绝了本次请求，请确认授权后重试。',
  insufficient_user_quota: '网关额度不足，请先充值后再生成。',
  pre_consume_token_quota_failed: '额度预扣失败，请稍后重试或联系管理员。',
  // 模型 / 渠道
  model_not_found: '所选模型在网关暂无可用渠道，请联系管理员更换模型或配置渠道。',
  get_channel_failed: '网关暂无可用模型渠道，请联系管理员。',
  'channel:no_available_key': '模型渠道暂不可用，请联系管理员。',
  'channel:invalid_key': '模型渠道密钥无效，请联系管理员。',
  'channel:model_mapped_error': '模型渠道映射有误，请联系管理员。',
  'channel:param_override_invalid': '渠道参数覆盖无效，请联系管理员。',
  'channel:header_override_invalid': '渠道请求头覆盖无效，请联系管理员。',
  // 上游调用失败
  do_request_failed: '上游模型服务暂时不可用，请稍后重试。',
  'channel:response_time_exceeded': '模型响应超时，请稍后重试。',
  'channel:aws_client_error': '模型服务调用失败，请稍后重试。',
  aws_invoke_error: '模型服务调用失败，请稍后重试。',
  count_token_failed: '用量统计失败，请稍后重试或联系管理员。',
  model_price_error: '模型价格配置有误，请联系管理员。',
  // 上游响应异常
  bad_response_status_code: '模型返回异常状态，请稍后重试。',
  bad_response: '模型返回内容异常，请稍后重试。',
  bad_response_body: '模型返回内容异常，请稍后重试。',
  read_response_body_failed: '模型响应读取失败，请稍后重试。',
  empty_response: '模型未返回有效内容，请稍后重试。',
  // 内容安全
  sensitive_words_detected: '内容包含敏感信息，无法生成，请调整后重试。',
  prompt_blocked: '内容未通过安全审核，无法生成，请调整后重试。',
  // 数据层
  query_data_error: '数据查询失败，请稍后重试或联系管理员。',
  update_data_error: '数据保存失败，请稍后重试或联系管理员。',
  // 频率限制（上游透传）
  rate_limit_exceeded: '请求过于频繁或已达额度上限，请稍后再试。',
};

// 无法识别错误码/信封时的 HTTP 状态码兜底，让真实根因可被诊断且不泄漏正文。
const STATUS_MESSAGES: Record<number, string> = {
  400: '请求参数有误，请检查后重试。',
  401: '网关登录态已失效，请重新登录。',
  403: '网关拒绝了本次请求，请确认授权后重试。',
  404: '请求的服务或模型不存在，请检查后重试。',
  413: '上传的素材过大，请压缩或更换素材后重试。',
  429: '请求过于频繁或已达额度上限，请稍后再试。',
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
    return { message: '余额或额度不足，请先充值后再支付。', code: 'UPSTREAM_CHECKOUT_FAILED' };
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
  //    仅当 code 是非空字符串时才进入受控映射；code 为 null/空串（网关上游透传或
  //    鉴权中断时 code 缺省为空）会落入下方状态码兜底，避免误导性提示。
  const code = safeText(asRecord(raw.error)?.code).toLowerCase();
  if (code) {
    const message = RELAY_CODE_MESSAGES[code]
      ?? (response.status >= 500
        ? '模型服务暂时不可用，请稍后重试。'
        : '生成请求被网关拒绝，请检查素材或更换模型后重试。');
    return new GatewayError(response.status, message, `UPSTREAM_RELAY_${code.toUpperCase()}`);
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
  const fallback = STATUS_MESSAGES[response.status]
    ?? (response.status >= 500
      ? '网关服务暂时不可用，请稍后重试。'
      : `网关请求失败（HTTP ${response.status}），请稍后重试。`);
  return new GatewayError(response.status, fallback, oauthCode ? `UPSTREAM_${oauthCode.toUpperCase()}` : 'UPSTREAM_GATEWAY_ERROR');
}
