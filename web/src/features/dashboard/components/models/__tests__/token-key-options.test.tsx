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
along with this program.  If not, see <http://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import {
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type {
  DashboardChartPreferences,
  DashboardFilters,
} from '../../../types'
import { ModelsFilter } from '../models-filter-dialog'

// 令牌 Key 下拉的 label 由前端 i18n 模板拼装；测试里显式登记模板，
// 保证断言的是"名称 + 调用次数 + tokens"这一用户可见契约本身。
const TOKEN_KEY_LABEL_KEYS = [
  '{{name}} · {{count}} calls · {{tokens}} tokens',
  '{{name}} ({{user}}) · {{count}} calls · {{tokens}} tokens',
  '{{key}} · {{count}} calls · {{tokens}} tokens',
  '{{key}} (deleted) · {{count}} calls · {{tokens}} tokens',
]

const PREFERENCES: DashboardChartPreferences = {
  consumptionDistributionChart: 'bar',
  modelAnalyticsChart: 'trend',
  defaultTimeRangeDays: 14,
  defaultTimeGranularity: 'day',
}

// 后端已按调用次数降序返回；前端必须原样展示，不重排
const TOKEN_KEY_ENTRIES = [
  {
    label: 'Alpha Key (admin-user) · 12',
    value: 'sk-alpha-0001',
    token_name: 'Alpha Key',
    user_name: 'admin-user',
    usage: 12,
    token_used: 48000,
  },
  {
    label: 'Beta Key · 5',
    value: 'sk-beta-0002',
    token_name: 'Beta Key',
    usage: 5,
    token_used: 1500,
  },
  {
    label: 'sk-b***-0003 (deleted) · 3',
    value: 'sk-gamma-0003',
    usage: 3,
    token_used: 900,
    deleted: true,
  },
]

type TokenKeysApiParams = {
  limit?: number
  start_timestamp?: number
  end_timestamp?: number
}

const TOKEN_KEYS_URL = '/api/data/token-keys'

let client: QueryClient
let apiGetSpy: ReturnType<typeof vi.spyOn>

// jsdom 未实现 Web Animations API，而对话框里的 Base UI ScrollArea 会在清理
// 定时器里调用 viewport.getAnimations()，测试结束后抛未捕获 TypeError。
// 只在本测试文件内补空实现（vitest 按文件隔离环境，不会泄漏到其它文件）；
// 全局补丁会改变 Base UI 其它组件的动画判定，已验证会破坏 keys 抽屉等
// 依赖"无 getAnimations"jsdom 行为的用例。
beforeAll(() => {
  if (!Element.prototype.getAnimations) {
    Object.defineProperty(Element.prototype, 'getAnimations', {
      configurable: true,
      writable: true,
      value: () => [],
    })
  }
})

function lastTokenKeysParams(): TokenKeysApiParams {
  const calls = apiGetSpy.mock.calls as unknown as Array<
    [string, { params?: TokenKeysApiParams }?]
  >
  let lastParams: TokenKeysApiParams = {}
  for (const [url, config] of calls) {
    if (url === TOKEN_KEYS_URL) lastParams = config?.params ?? {}
  }
  return lastParams
}

beforeEach(() => {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin-user',
    role: 10,
    quota: 1000000,
    used_quota: 1000,
    request_count: 1,
  })
  for (const key of TOKEN_KEY_LABEL_KEYS) {
    i18next.addResource('en', 'translation', key, key)
  }
  client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  apiGetSpy = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/data/token-keys') {
      return { data: { success: true, message: '', data: TOKEN_KEY_ENTRIES } }
    }
    throw new Error(`Unexpected request: ${url}`)
  })
})

afterEach(() => {
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
})

function renderFilter(currentFilters: DashboardFilters) {
  return render(
    <QueryClientProvider client={client}>
      <ModelsFilter
        preferences={PREFERENCES}
        currentFilters={currentFilters}
        onFilterChange={vi.fn()}
        onReset={vi.fn()}
      />
    </QueryClientProvider>
  )
}

describe('models filter token key options', () => {
  it('requests stats for the dialog time range and lists options by calls desc with tokens', async () => {
    const start = new Date('2026-01-01T00:00:00Z')
    const end = new Date('2026-01-15T00:00:00Z')
    renderFilter({
      start_timestamp: start,
      end_timestamp: end,
      time_granularity: 'day',
    })

    await waitFor(() => {
      expect(lastTokenKeysParams()).toEqual({
        limit: 200,
        start_timestamp: Math.floor(start.getTime() / 1000),
        end_timestamp: Math.floor(end.getTime() / 1000),
      })
    })

    await userEvent.click(screen.getByRole('button', { name: 'Filter' }))
    const combobox = await screen.findByPlaceholderText('Filter by token key')
    await userEvent.click(combobox)

    const options = await screen.findAllByRole('option')
    expect(options).toHaveLength(3)
    expect(options[0]).toHaveTextContent(
      'Alpha Key (admin-user) · 12 calls · 48000 tokens'
    )
    expect(options[1]).toHaveTextContent('Beta Key · 5 calls · 1500 tokens')
    expect(options[2]).toHaveTextContent(
      'sk-g***0003 (deleted) · 3 calls · 900 tokens'
    )
  })

  it('omits the time range params when the filters have no range', async () => {
    renderFilter({})

    await waitFor(() => {
      expect(apiGetSpy).toHaveBeenCalledWith(
        '/api/data/token-keys',
        expect.anything()
      )
    })
    const params = lastTokenKeysParams()
    expect(params.limit).toBe(200)
    expect(params.start_timestamp).toBeUndefined()
    expect(params.end_timestamp).toBeUndefined()
  })
})
