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

import { CHANNEL_FORM_DEFAULT_VALUES, channelFormSchema } from '../channel-form'

const validForm = {
  ...CHANNEL_FORM_DEFAULT_VALUES,
  name: 'OpenAI upstream',
  key: 'sk-test',
  models: 'gpt-4o',
}

describe('channel form schema', () => {
  test('rejects a model mapping whose source key is blank', () => {
    const result = channelFormSchema.safeParse({
      ...validForm,
      model_mapping: '{"": "gpt-4o"}',
    })

    expect(result.success).toBe(false)
    if (result.success) return
    expect(result.error.issues.map((issue) => issue.message)).toContain(
      'Model mapping keys must not be empty'
    )
  })

  test('accepts a model mapping with named source keys', () => {
    const result = channelFormSchema.safeParse({
      ...validForm,
      model_mapping: '{"gpt-4o": "gpt-4o-2024"}',
    })

    expect(result.success).toBe(true)
  })

  test('rejects a negative channel concurrency limit', () => {
    const result = channelFormSchema.safeParse({
      ...validForm,
      concurrency_limit: -1,
    })

    expect(result.success).toBe(false)
  })

  test('accepts zero (unlimited) and a positive channel concurrency limit', () => {
    expect(
      channelFormSchema.safeParse({ ...validForm, concurrency_limit: 0 })
        .success
    ).toBe(true)
    expect(
      channelFormSchema.safeParse({ ...validForm, concurrency_limit: 5 })
        .success
    ).toBe(true)
  })
})
