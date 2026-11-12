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
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, test } from 'vitest'

import {
  CHANNEL_PROVIDER_PRESENTATION,
  CHANNEL_TYPE_OPTIONS,
  CHANNEL_TYPES,
  MODEL_FETCHABLE_TYPES,
} from '../../constants'

const CHANNEL_TYPE_APIMART = 64
const BRAND_KEY = 'APIMart'
const DESCRIPTION_KEY = 'Connect to APIMart model services'
const LOCALES = ['en', 'zh', 'zh-TW', 'fr', 'ru', 'ja', 'vi'] as const

function readLocale(locale: string): Record<string, string> {
  const raw = readFileSync(
    resolve(import.meta.dirname, '../../../../i18n/locales', `${locale}.json`),
    'utf8'
  )
  const parsed = JSON.parse(raw) as { translation?: Record<string, string> }
  return parsed.translation ?? {}
}

describe('APIMart channel type', () => {
  test('registers channel type 64 with its provider presentation', () => {
    expect(CHANNEL_TYPES[CHANNEL_TYPE_APIMART]).toBe('APIMart')
    expect(CHANNEL_PROVIDER_PRESENTATION[CHANNEL_TYPE_APIMART]).toBeDefined()
  })

  test('allows fetching upstream models for channel type 64', () => {
    expect(MODEL_FETCHABLE_TYPES.has(CHANNEL_TYPE_APIMART)).toBe(true)
  })

  test('offers channel type 64 in the channel type dropdown', () => {
    expect(
      CHANNEL_TYPE_OPTIONS.some(
        (option) => option.value === CHANNEL_TYPE_APIMART
      )
    ).toBe(true)
  })

  test('ships the APIMart label and description in every locale', () => {
    for (const locale of LOCALES) {
      const translation = readLocale(locale)

      expect(translation[BRAND_KEY]).toBe('APIMart')
      expect(translation[DESCRIPTION_KEY]).toBeTruthy()
    }
  })

  test('translates the APIMart description in every non-English locale', () => {
    const englishDescription = readLocale('en')[DESCRIPTION_KEY]

    for (const locale of LOCALES.filter((locale) => locale !== 'en')) {
      expect(readLocale(locale)[DESCRIPTION_KEY]).not.toBe(englishDescription)
    }
  })
})
