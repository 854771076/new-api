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
  CHANNEL_TYPE_APIMART,
  CHANNEL_TYPE_DISPLAY_ORDER,
  CHANNEL_TYPE_OPTIONS,
  CHANNEL_TYPE_VLLM,
  CHANNEL_TYPES,
  MODEL_FETCHABLE_TYPES,
} from '../../constants'

// The English description the provider picker renders for APIMart. The same
// string is the i18n key the locale files translate.
const APIMART_DESCRIPTION_KEY = 'Connect to APIMart model services'
const LOCALES = ['en', 'zh', 'zh-TW', 'fr', 'ru', 'ja', 'vi'] as const

function readLocale(locale: string): Record<string, string> {
  const raw = readFileSync(
    resolve(import.meta.dirname, '../../../../i18n/locales', `${locale}.json`),
    'utf8'
  )
  const parsed = JSON.parse(raw) as { translation?: Record<string, string> }
  return parsed.translation ?? {}
}

// The APIMart brand is the channel label named in the English description, so
// the type id is looked up in CHANNEL_TYPES instead of being hardcoded.
const APIMART_BRAND_ENTRIES = Object.entries(CHANNEL_TYPES).filter(([, label]) =>
  APIMART_DESCRIPTION_KEY.split(/\s+/).includes(label)
)
const APIMART_TYPE_ID: number = Number(APIMART_BRAND_ENTRIES[0]?.[0])
const APIMART_BRAND: string =
  CHANNEL_TYPES[APIMART_TYPE_ID as keyof typeof CHANNEL_TYPES]
const APIMART_DESCRIPTION_I18N_KEY: string =
  CHANNEL_PROVIDER_PRESENTATION[CHANNEL_TYPE_APIMART]?.descriptionKey ?? ''

describe('APIMart channel type', () => {
  test('registers exactly one channel type labelled APIMart', () => {
    expect(APIMART_BRAND_ENTRIES).toHaveLength(1)
    expect(APIMART_BRAND).toBeTruthy()
    expect(APIMART_TYPE_ID).toBe(CHANNEL_TYPE_APIMART)
    expect(CHANNEL_PROVIDER_PRESENTATION[CHANNEL_TYPE_APIMART]).toBeDefined()
  })

  test('exports the APIMart channel type id as 64', () => {
    expect(CHANNEL_TYPE_APIMART).toBe(64)
  })

  test('exposes the APIMart description key that the locale files translate', () => {
    expect(APIMART_DESCRIPTION_I18N_KEY).toBe(APIMART_DESCRIPTION_KEY)
    expect(APIMART_DESCRIPTION_I18N_KEY).toContain(APIMART_BRAND)
  })

  test('allows fetching upstream models for the APIMart channel type', () => {
    expect(MODEL_FETCHABLE_TYPES.has(CHANNEL_TYPE_APIMART)).toBe(true)
  })

  test('offers the APIMart channel type in the channel type dropdown', () => {
    expect(
      CHANNEL_TYPE_OPTIONS.some(
        (option) =>
          option.value === CHANNEL_TYPE_APIMART && option.label === APIMART_BRAND
      )
    ).toBe(true)
  })

  test('keeps the APIMart channel type in the channel type display order', () => {
    expect(CHANNEL_TYPE_DISPLAY_ORDER).toContain(CHANNEL_TYPE_APIMART)
  })

  test('keeps the display order inside the built channel type options', () => {
    const positions = CHANNEL_TYPE_DISPLAY_ORDER.slice(0, 3).map((id) =>
      CHANNEL_TYPE_OPTIONS.findIndex((option) => option.value === id)
    )

    expect(positions.every((position) => position >= 0)).toBe(true)
    expect(positions).toEqual([...positions].sort((a, b) => a - b))
  })

  test('lists the APIMart channel type beside its neighbouring channel types', () => {
    const apimartIndex = CHANNEL_TYPE_OPTIONS.findIndex(
      (option) => option.value === CHANNEL_TYPE_APIMART
    )
    const vllmIndex = CHANNEL_TYPE_OPTIONS.findIndex(
      (option) => option.value === CHANNEL_TYPE_VLLM
    )

    expect(apimartIndex).toBe(vllmIndex + 1)
  })

  test('ships the APIMart label and description in every locale', () => {
    for (const locale of LOCALES) {
      const translation = readLocale(locale)

      expect(translation[APIMART_BRAND]).toBe(APIMART_BRAND)
      expect(translation[APIMART_DESCRIPTION_I18N_KEY]).toBeTruthy()
    }
  })

  test('translates the APIMart description in every non-English locale', () => {
    const englishDescription = readLocale('en')[APIMART_DESCRIPTION_I18N_KEY]

    for (const locale of LOCALES.filter((locale) => locale !== 'en')) {
      expect(readLocale(locale)[APIMART_DESCRIPTION_I18N_KEY]).not.toBe(
        englishDescription
      )
    }
  })
})
