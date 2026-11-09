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
import { z } from 'zod'

import {
  permissionMatrixToScopes,
  scopesToPermissionMatrix,
  type AdminPermissionMatrix,
} from '@/lib/admin-permissions'

import type { AccessTokenCatalog } from '../../security/api'

// CLI_HIGH_RISK_SCOPES are administrative writes that can change other users,
// billing, or system-wide behavior. They are never preselected; the user must
// opt in explicitly.
export const CLI_HIGH_RISK_SCOPES = [
  'user:write',
  'option:write',
  'model:write',
  'deployment:write',
  'channel:operate',
  'channel:write',
  'channel:sensitive_write',
  'billing:write',
  'redemption:write',
  'group:write',
  'plugin:write',
  'ops:write',
  'task_plugin:bind',
  'account_security:write',
  'wallet:write',
]

// CLI_RECOMMENDED_SCOPES is the grant the CLI needs for day-to-day
// administration. It stays read-only apart from API key management, which is
// the CLI's basic operation; the user can widen or narrow it before
// confirming.
export const CLI_RECOMMENDED_SCOPES = [
  'profile:read',
  'usage:read',
  'api_key:read',
  'api_key:write',
  'user:read',
  'channel:read',
  'log:read',
  'model:read',
  'deployment:read',
  'option:read',
]

export const cliAuthorizeFormSchema = z
  .object({
    name: z
      .string()
      .trim()
      .min(1, 'Please enter a name')
      // The server counts characters, not UTF-16 code units.
      .refine((name) => [...name].length <= 64, {
        message: 'Name must be 64 characters or fewer',
      }),
    permissions: z.record(z.string(), z.record(z.string(), z.boolean())),
  })
  .superRefine((values, ctx) => {
    if (permissionMatrixToScopes(values.permissions).length === 0) {
      ctx.addIssue({
        code: 'custom',
        path: ['permissions'],
        message: 'Select at least one permission',
      })
    }
  })

export type CLIAuthorizeFormValues = z.infer<typeof cliAuthorizeFormSchema>

export function grantableScopes(catalog: AccessTokenCatalog): Set<string> {
  const scopes = new Set<string>()
  for (const group of catalog.groups) {
    for (const resource of group.resources) {
      for (const action of resource.actions) {
        scopes.add(`${resource.resource}:${action.action}`)
      }
    }
  }
  return scopes
}

// defaultPermissions preselects the recommended grant the user's role can
// actually grant, minus high-risk writes. Scopes outside the catalog stay
// unchecked.
export function defaultPermissions(
  catalog: AccessTokenCatalog
): AdminPermissionMatrix {
  const available = grantableScopes(catalog)
  const recommended = CLI_RECOMMENDED_SCOPES.filter(
    (scope) => available.has(scope) && !CLI_HIGH_RISK_SCOPES.includes(scope)
  )
  return scopesToPermissionMatrix(recommended)
}

export interface CLIGrantSummaryItem {
  scope: string
  resourceLabelKey: string
  actionLabelKey: string
}

export interface CLIGrantSummary {
  items: CLIGrantSummaryItem[]
  highRiskCount: number
}

// summarizeGrant resolves the selected scopes to the catalog labels shown to
// the user before they confirm, and counts the high-risk ones still included.
export function summarizeGrant(
  catalog: AccessTokenCatalog,
  scopes: readonly string[]
): CLIGrantSummary {
  const labels = new Map<string, Omit<CLIGrantSummaryItem, 'scope'>>()
  for (const group of catalog.groups) {
    for (const resource of group.resources) {
      for (const action of resource.actions) {
        labels.set(`${resource.resource}:${action.action}`, {
          resourceLabelKey: resource.label_key,
          actionLabelKey: action.label_key,
        })
      }
    }
  }
  const items = scopes.flatMap((scope) => {
    const label = labels.get(scope)
    return label ? [{ scope, ...label }] : []
  })
  return {
    items,
    highRiskCount: scopes.filter((scope) =>
      CLI_HIGH_RISK_SCOPES.includes(scope)
    ).length,
  }
}
