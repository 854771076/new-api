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
}

export const quotaRuleQueryKey = ['token-quota-rules']

export async function getQuotaRules(params: {
  p: number
  size: number
  keyword?: string
  status?: string
  bind_status?: string
}): Promise<{ items: QuotaRule[]; total: number }> {
  const response = await api.get('/api/token/quota_rules', { params })
  return requireServerSuccess(response.data).data
}

export async function saveQuotaRule(
  rule: Omit<QuotaRule, 'bound_token_count'>
) {
  const response = rule.id
    ? await api.put(`/api/token/quota_rules/${rule.id}`, rule)
    : await api.post('/api/token/quota_rules', rule)
  return requireServerSuccess(response.data)
}

export async function deleteQuotaRule(id: number) {
  return requireServerSuccess(
    (await api.delete(`/api/token/quota_rules/${id}`)).data
  )
}

export async function bindQuotaRule(tokenId: number, ruleId: number) {
  return requireServerSuccess(
    (await api.put(`/api/token/${tokenId}/quota_rule`, { rule_id: ruleId }))
      .data
  )
}
