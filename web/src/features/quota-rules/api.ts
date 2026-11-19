/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export interface QuotaRule {
  id: number
  name: string
  description: string
  enabled: boolean
  quota_limit_enabled: boolean
  quota_period: 'daily' | 'weekly' | 'monthly'
  quota_limit: number
  time_window_enabled: boolean
  time_window_timezone: 'UTC' | 'Asia/Shanghai'
  allowed_weekdays: string
  bound_token_count: number
  valid_from?: number | null
  valid_until?: number | null
  validity_status?: 'disabled' | 'scheduled' | 'effective' | 'expired'
}

export type QuotaRuleTarget = { userId: number; appId: number }

function rulePath(target?: QuotaRuleTarget) {
  return target
    ? `/api/app-proxy/users/${target.userId}/quota-rules`
    : '/api/token/quota_rules'
}

export const quotaRuleQueryKey = ['token-quota-rules']

export async function getQuotaRules(
  params: {
    p: number
    size: number
    keyword?: string
    status?: string
    bind_status?: string
  },
  target?: QuotaRuleTarget
): Promise<{ items: QuotaRule[]; total: number; server_time: number }> {
  const response = await api.get(rulePath(target), {
    params: {
      ...params,
      page_size: params.size,
      ...(target && { app_id: target.appId }),
    },
  })
  return requireServerSuccess(response.data).data
}

export async function saveQuotaRule(
  rule: Omit<QuotaRule, 'bound_token_count'>,
  target?: QuotaRuleTarget
) {
  const { id, validity_status: _status, ...input } = rule
  const payload = target ? input : rule
  const config = target ? { params: { app_id: target.appId } } : undefined
  const response = rule.id
    ? await api.put(`${rulePath(target)}/${id}`, payload, config)
    : await api.post(rulePath(target), payload, config)
  return requireServerSuccess(response.data)
}

export async function deleteQuotaRule(id: number, target?: QuotaRuleTarget) {
  return requireServerSuccess(
    (
      await api.delete(
        `${rulePath(target)}/${id}`,
        target ? { params: { app_id: target.appId } } : undefined
      )
    ).data
  )
}

export async function bindQuotaRule(
  tokenId: number,
  ruleId: number,
  target?: QuotaRuleTarget
) {
  return requireServerSuccess(
    (
      await api.put(
        target
          ? `/api/app-proxy/users/${target.userId}/keys/${tokenId}/quota-rule`
          : `/api/token/${tokenId}/quota_rule`,
        { rule_id: ruleId },
        target ? { params: { app_id: target.appId } } : undefined
      )
    ).data
  )
}
