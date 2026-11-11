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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'

import { DEFAULT_GROUP } from '../constants'
import type { ApiKey, ApiKeyFormData } from '../types'
import { parseConcurrencyLimitByModel } from './concurrency-limit-by-model'

// ============================================================================
// Form Schema
// ============================================================================

export function getApiKeyFormSchema(t: TFunction, maxAutoGroups = 5) {
  const autoGroupLimit =
    Number.isInteger(maxAutoGroups) && maxAutoGroups > 0 ? maxAutoGroups : 5

  return z
    .object({
      name: z.string().min(1, t('Please enter a name')),
      remain_quota_dollars: z.number().optional(),
      expired_time: z.date().nullable(),
      unlimited_quota: z.boolean(),
      model_limits: z.array(z.string()),
      allow_ips: z.string().optional(),
      allow_user_quota_query: z.boolean(),
      group: z.string().optional(),
      auto_groups_mode: z.enum(['inherit', 'custom']),
      auto_groups: z.array(z.string()),
      cross_group_retry: z.boolean().optional(),
      concurrency_limit: z.number().min(0).optional(),
      concurrency_limit_by_model: z.string().optional(),
      tokenCount: z.number().min(1).optional(),
    })
    .superRefine((data, ctx) => {
      // Per-model CSV gate (TUI-02 UI main gate): any defect segment blocks
      // saving before the payload is built — the user sees the parsed preview
      // with raw entry + reason instead of the server silently dropping it.
      if (data.concurrency_limit_by_model) {
        const { defects } = parseConcurrencyLimitByModel(
          data.concurrency_limit_by_model
        )
        if (defects.length > 0) {
          ctx.addIssue({
            code: 'custom',
            path: ['concurrency_limit_by_model'],
            message: t('Fix invalid entries before saving'),
          })
        }
      }

      if (data.group === 'auto') {
        if (
          data.auto_groups_mode === 'custom' &&
          data.auto_groups.length === 0
        ) {
          ctx.addIssue({
            code: 'custom',
            path: ['auto_groups'],
            message: t(
              'Select at least one Auto group or restore global Auto.'
            ),
          })
        }

        if (data.auto_groups.length > autoGroupLimit) {
          ctx.addIssue({
            code: 'custom',
            path: ['auto_groups'],
            message: t('Select at most {{max}} Auto groups', {
              max: autoGroupLimit,
            }),
          })
        }

        if (new Set(data.auto_groups).size !== data.auto_groups.length) {
          ctx.addIssue({
            code: 'custom',
            path: ['auto_groups'],
            message: t('Auto groups must not contain duplicates'),
          })
        }
      }

      if (data.unlimited_quota) {
        return
      }

      if (
        data.remain_quota_dollars === undefined ||
        data.remain_quota_dollars < 0
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['remain_quota_dollars'],
          message: t('Quota must be zero or greater'),
        })
      }
    })
}

export type ApiKeyFormValues = z.infer<ReturnType<typeof getApiKeyFormSchema>>

// ============================================================================
// Form Defaults
// ============================================================================

export const API_KEY_FORM_DEFAULT_VALUES: ApiKeyFormValues = {
  name: '',
  remain_quota_dollars: 10,
  expired_time: null,
  unlimited_quota: true,
  model_limits: [],
  allow_ips: '',
  allow_user_quota_query: true,
  group: DEFAULT_GROUP,
  auto_groups_mode: 'inherit',
  auto_groups: [],
  cross_group_retry: true,
  concurrency_limit: 0,
  concurrency_limit_by_model: '',
  tokenCount: 1,
}

export function getApiKeyFormDefaultValues(
  defaultUseAutoGroup: boolean
): ApiKeyFormValues {
  return {
    ...API_KEY_FORM_DEFAULT_VALUES,
    group: defaultUseAutoGroup ? 'auto' : DEFAULT_GROUP,
    auto_groups_mode: 'inherit',
    auto_groups: [],
    cross_group_retry: defaultUseAutoGroup,
  }
}

// ============================================================================
// Form Data Transformation
// ============================================================================

/**
 * Transform form data to API payload
 */
export function transformFormDataToPayload(
  data: ApiKeyFormValues
): ApiKeyFormData {
  return {
    name: data.name,
    remain_quota: data.unlimited_quota
      ? 0
      : parseQuotaFromDollars(data.remain_quota_dollars || 0),
    expired_time: data.expired_time
      ? Math.floor(data.expired_time.getTime() / 1000)
      : -1,
    unlimited_quota: data.unlimited_quota,
    model_limits_enabled: data.model_limits.length > 0,
    model_limits: data.model_limits.join(','),
    allow_ips: data.allow_ips || '',
    allow_user_quota_query: data.allow_user_quota_query,
    group: data.group || '',
    auto_groups:
      data.group === 'auto' && data.auto_groups_mode === 'custom'
        ? data.auto_groups
        : [],
    cross_group_retry: data.group === 'auto' ? !!data.cross_group_retry : false,
    // `?? 0` keeps an explicit "unlimited" (0) on the payload instead of
    // dropping it — the backend admin-only block persists explicit zero and
    // preserves stored values only when the key is omitted, so always send it.
    concurrency_limit: data.concurrency_limit ?? 0,
    // Explicit CSV on every payload: '' clears the stored per-model caps while
    // an omitted key would preserve them (the backend only writes keys the
    // admin actually sent), so a cleared field can never be silently kept —
    // and a backfilled value is never silently dropped.
    concurrency_limit_by_model: data.concurrency_limit_by_model ?? '',
  }
}

/**
 * Transform API key data to form defaults
 */
export function transformApiKeyToFormDefaults(
  apiKey: ApiKey,
  availableAutoGroups: string[] = [],
  maxAutoGroups = 5
): ApiKeyFormValues {
  const availableSet = new Set(availableAutoGroups)
  const storedAutoGroups = apiKey.auto_groups ?? []
  const autoGroups = storedAutoGroups
    .filter((group) => availableSet.has(group))
    .slice(0, Math.max(0, maxAutoGroups))
  const autoGroupsMode = storedAutoGroups.length > 0 ? 'custom' : 'inherit'

  return {
    name: apiKey.name,
    remain_quota_dollars: apiKey.unlimited_quota
      ? 0
      : quotaUnitsToDollars(apiKey.remain_quota),
    expired_time:
      apiKey.expired_time > 0 ? new Date(apiKey.expired_time * 1000) : null,
    unlimited_quota: apiKey.unlimited_quota,
    model_limits: apiKey.model_limits
      ? apiKey.model_limits.split(',').filter(Boolean)
      : [],
    allow_ips: apiKey.allow_ips || '',
    allow_user_quota_query: !!apiKey.allow_user_quota_query,
    group: apiKey.group || DEFAULT_GROUP,
    auto_groups_mode: autoGroupsMode,
    auto_groups: autoGroups,
    cross_group_retry: !!apiKey.cross_group_retry,
    // Backfill the stored cap so reopening the drawer never silently zeroes an
    // existing limit on the next save (`?? 0` for tokens without the field).
    concurrency_limit: apiKey.concurrency_limit ?? 0,
    // Same round-trip contract for the per-model CSV: missing field becomes '',
    // a stored CSV is backfilled verbatim so saving never clears it silently.
    concurrency_limit_by_model: apiKey.concurrency_limit_by_model ?? '',
    tokenCount: 1,
  }
}
