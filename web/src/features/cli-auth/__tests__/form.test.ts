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
import { describe, expect, test } from 'vitest'

import { permissionMatrixToScopes } from '@/lib/admin-permissions'

import type { AccessTokenCatalog } from '../../security/api'
import {
  CLI_HIGH_RISK_SCOPES,
  CLI_RECOMMENDED_SCOPES,
  cliAuthorizeFormSchema,
  defaultPermissions,
  grantableScopes,
  summarizeGrant,
} from '../lib/form'

function catalogWith(resources: string[]): AccessTokenCatalog {
  return {
    max_tokens: 20,
    default_expiry_days: 30,
    groups: [
      {
        group: 'personal',
        resources: resources.map((resource) => ({
          resource,
          label_key: resource,
          actions: [
            { action: 'read', label_key: 'View', description_key: '' },
            { action: 'write', label_key: 'Edit', description_key: '' },
          ],
        })),
      },
    ],
  }
}

describe('cli authorize form', () => {
  test('collects every catalog scope as resource:action', () => {
    const scopes = grantableScopes(catalogWith(['usage', 'user']))
    expect(scopes).toEqual(
      new Set(['usage:read', 'usage:write', 'user:read', 'user:write'])
    )
  })

  test('preselects only recommended scopes the user can grant', () => {
    const matrix = defaultPermissions(catalogWith(['usage', 'user']))
    expect(permissionMatrixToScopes(matrix)).toEqual([
      'usage:read',
      'user:read',
    ])
  })

  test('never preselects high-risk write scopes', () => {
    const matrix = defaultPermissions(
      catalogWith([
        'api_key',
        'user',
        'option',
        'model',
        'deployment',
        'channel',
      ])
    )
    expect(permissionMatrixToScopes(matrix)).toEqual([
      'api_key:read',
      'api_key:write',
      'channel:read',
      'deployment:read',
      'model:read',
      'option:read',
      'user:read',
    ])
    expect(CLI_HIGH_RISK_SCOPES).toEqual(
      expect.arrayContaining([
        'user:write',
        'option:write',
        'model:write',
        'deployment:write',
      ])
    )
  })

  test('summarizes the selected scopes with catalog labels', () => {
    const summary = summarizeGrant(catalogWith(['usage', 'user']), [
      'usage:read',
      'user:write',
      'unknown:read',
    ])
    expect(summary.items).toEqual([
      {
        scope: 'usage:read',
        resourceLabelKey: 'usage',
        actionLabelKey: 'View',
      },
      { scope: 'user:write', resourceLabelKey: 'user', actionLabelKey: 'Edit' },
    ])
    expect(summary.highRiskCount).toBe(1)
  })

  test('reports an empty summary when nothing is selected', () => {
    const summary = summarizeGrant(catalogWith(['usage']), [])
    expect(summary).toEqual({ items: [], highRiskCount: 0 })
  })

  test('preselects nothing when the catalog grants no recommended scope', () => {
    const matrix = defaultPermissions(catalogWith(['wallet']))
    expect(permissionMatrixToScopes(matrix)).toEqual([])
    expect(CLI_RECOMMENDED_SCOPES).not.toContain('wallet:read')
  })

  test('rejects an empty permission grant', () => {
    const result = cliAuthorizeFormSchema.safeParse({
      name: 'newapi-cli',
      permissions: {},
    })
    expect(result.success).toBe(false)
    if (!result.success) {
      const paths = result.error.issues.map((issue) => issue.path.join('.'))
      expect(paths).toContain('permissions')
    }
  })

  test('rejects names longer than 64 characters', () => {
    const result = cliAuthorizeFormSchema.safeParse({
      name: 'x'.repeat(65),
      permissions: { usage: { read: true } },
    })
    expect(result.success).toBe(false)
  })

  test('accepts a trimmed name with a partial grant', () => {
    const result = cliAuthorizeFormSchema.safeParse({
      name: '  my cli  ',
      permissions: { usage: { read: true, write: false } },
    })
    expect(result.success).toBe(true)
  })
})
