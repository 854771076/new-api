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

// TypeScript mirror of Go service/concurrency.ParseEntries — the single
// parse source for the per-model CSV (TUI-02 BCD conclusion). Classification
// boundaries, classes and reason codes are identical on both sides and locked
// by the shared fixture table
// (service/concurrency/testdata/token_limit_parse_cases.json, consumed by both
// the Go tests and lib/__tests__/concurrency-limit-by-model.test.ts), so a
// semantic change must land in the fixture and both implementations.

export type ConcurrencyLimitEntryClass = 'accept' | 'empty' | 'defect'

export type ConcurrencyLimitReason =
  // Reason codes mirror the Go exported constants.
  | 'missing_separator'
  | 'empty_model'
  | 'invalid_limit'
  | 'non_positive_limit'
  // Accept / empty segments carry no reason.
  | ''

export type ConcurrencyLimitSegment = {
  raw: string
  class: ConcurrencyLimitEntryClass
  model: string
  limit: number
  reason: ConcurrencyLimitReason
}

export type AcceptedConcurrencyLimit = {
  model: string
  limit: number
}

export type DefectiveConcurrencyLimit = {
  raw: string
  reason: Exclude<ConcurrencyLimitReason, ''>
}

export type ParsedConcurrencyLimitByModel = {
  segments: ConcurrencyLimitSegment[]
  accepted: AcceptedConcurrencyLimit[]
  defects: DefectiveConcurrencyLimit[]
}

// Minimum/maximum of a Go `int` on a 64-bit platform — strconv.Atoi rejects
// anything outside this range (value out of range), so the mirror classifies
// those inputs as invalid_limit too.
const MIN_INT64 = -(2n ** 63n)
const MAX_INT64 = 2n ** 63n - 1n

/**
 * Split and classify every `model=limit` CSV segment exactly like Go
 * ParseEntries: accept (positive integer limit), empty (pure-whitespace
 * segment, tolerated), defect (with a reason code). The parser never drops a
 * defect — callers decide (the read side skips them, the form gate blocks
 * saving when any exists).
 */
export function parseConcurrencyLimitByModel(
  csv: string
): ParsedConcurrencyLimitByModel {
  const segments: ConcurrencyLimitSegment[] = []
  for (const part of csv.split(',')) {
    const entry = part.trim()
    if (entry === '') {
      segments.push({
        raw: '',
        class: 'empty',
        model: '',
        limit: 0,
        reason: '',
      })
      continue
    }
    const eq = entry.indexOf('=')
    if (eq <= 0 || eq >= entry.length - 1) {
      // Missing, leading or trailing '='.
      segments.push({
        raw: entry,
        class: 'defect',
        model: '',
        limit: 0,
        reason: 'missing_separator',
      })
      continue
    }
    const model = entry.slice(0, eq).trim()
    const value = entry.slice(eq + 1).trim()
    if (model === '') {
      // Defensive parity with the Go empty_model check (unreachable after the
      // outer trim, kept so both mirrors classify identically).
      segments.push({
        raw: entry,
        class: 'defect',
        model: '',
        limit: 0,
        reason: 'empty_model',
      })
      continue
    }
    // strconv.Atoi parity: optional sign plus digits only, int64 range.
    if (!/^[+-]?\d+$/.test(value)) {
      segments.push({
        raw: entry,
        class: 'defect',
        model: '',
        limit: 0,
        reason: 'invalid_limit',
      })
      continue
    }
    const integer = BigInt(value)
    if (integer < MIN_INT64 || integer > MAX_INT64) {
      segments.push({
        raw: entry,
        class: 'defect',
        model: '',
        limit: 0,
        reason: 'invalid_limit',
      })
      continue
    }
    const limit = Number(integer)
    if (limit <= 0) {
      // Zero/negative per-model caps mean "same as omitting the entry" at read
      // time; the write boundary and the UI gate reject them instead of
      // silently dropping (SC: no silent normalization).
      segments.push({
        raw: entry,
        class: 'defect',
        model: '',
        limit: 0,
        reason: 'non_positive_limit',
      })
      continue
    }
    segments.push({ raw: entry, class: 'accept', model, limit, reason: '' })
  }

  const accepted: AcceptedConcurrencyLimit[] = []
  const defects: DefectiveConcurrencyLimit[] = []
  for (const segment of segments) {
    if (segment.class === 'accept') {
      accepted.push({ model: segment.model, limit: segment.limit })
    } else if (segment.class === 'defect') {
      defects.push({
        raw: segment.raw,
        reason: segment.reason as DefectiveConcurrencyLimit['reason'],
      })
    }
  }
  return { segments, accepted, defects }
}
