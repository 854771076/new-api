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

// TS mirror ↔ Go ParseEntries double-sided lock (02-13 threat T-02-24): both
// sides consume THIS ONE fixture table (service/concurrency/testdata/...), so
// a semantic change must be made in the fixture and both implementations or
// this suite fails. A missing fixture file fails the suite — never skipped.
const FIXTURE_PATH = resolve(
  import.meta.dirname,
  '../../../../../../service/concurrency/testdata/token_limit_parse_cases.json'
)

type FixtureSegment = {
  raw: string
  class: string
  model: string
  limit: number
  reason: string
}

type FixtureCase = {
  input: string
  mustReject: boolean
  segments: FixtureSegment[]
}

function loadFixture(): FixtureCase[] {
  return JSON.parse(readFileSync(FIXTURE_PATH, 'utf8')) as FixtureCase[]
}

let fixture: FixtureCase[] = []
let fixtureLoadError: unknown = null
try {
  fixture = loadFixture()
} catch (error) {
  fixtureLoadError = error
}

describe('parseConcurrencyLimitByModel shares the Go fixture table', () => {
  test('the shared fixture file loads and covers every planned parse case', () => {
    if (fixtureLoadError) {
      throw fixtureLoadError
    }
    const inputs = fixture.map((fixtureCase) => fixtureCase.input)
    expect(inputs).toEqual(
      expect.arrayContaining([
        'gpt-4o=3,claude-sonnet=2',
        'gpt-4o=3,',
        ' gpt-4o = 3 ',
        '',
        'gpt-4o',
        'gpt-4o=',
        '=3',
        'gpt-4o=x',
        'gpt-4o=0',
        'gpt-4o=-1',
        'gpt-4o=3,bad',
        'gpt-4o=3,gpt-4o=5',
      ])
    )
  })

  fixture.forEach((fixtureCase, index) => {
    const label = `case ${index + 1} "${fixtureCase.input}"`

    test(`${label} classifies each segment exactly like Go ParseEntries`, async () => {
      const { parseConcurrencyLimitByModel } =
        await import('../concurrency-limit-by-model')
      const { segments } = parseConcurrencyLimitByModel(fixtureCase.input)
      expect(segments).toEqual(fixtureCase.segments)
    })

    test(`${label} blocks saving exactly when the fixture says mustReject`, async () => {
      const { parseConcurrencyLimitByModel } =
        await import('../concurrency-limit-by-model')
      const { defects } = parseConcurrencyLimitByModel(fixtureCase.input)
      expect(defects.length > 0).toBe(fixtureCase.mustReject)
    })
  })

  test('accepted aggregates expose exactly the pairs the preview renders', async () => {
    const { parseConcurrencyLimitByModel } =
      await import('../concurrency-limit-by-model')
    const { accepted, defects } = parseConcurrencyLimitByModel(
      'gpt-4o=3,claude-sonnet=2, gpt-4o = 5 ,'
    )
    expect(accepted).toEqual([
      { model: 'gpt-4o', limit: 3 },
      { model: 'claude-sonnet', limit: 2 },
      { model: 'gpt-4o', limit: 5 },
    ])
    expect(defects).toEqual([])
  })

  test('defect aggregates expose the raw entry and its reason code', async () => {
    const { parseConcurrencyLimitByModel } =
      await import('../concurrency-limit-by-model')
    const { defects } = parseConcurrencyLimitByModel(
      'gpt-4o=3,bad,gpt-4o=x,gpt-4o=0,=3,gpt-4o='
    )
    expect(defects).toEqual([
      { raw: 'bad', reason: 'missing_separator' },
      { raw: 'gpt-4o=x', reason: 'invalid_limit' },
      { raw: 'gpt-4o=0', reason: 'non_positive_limit' },
      { raw: '=3', reason: 'missing_separator' },
      { raw: 'gpt-4o=', reason: 'missing_separator' },
    ])
  })
})
