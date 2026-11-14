/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or any later version.
*/

import { render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { describe, expect, test } from 'vitest'

import type { ConcurrencyDegradedStatus } from '../../lib/concurrency-transform'
import { DegradedBadge } from '../degraded-badge'

// Empty `en` resources make t() fall back to the English source key itself,
// so assertions locate elements by translation-key semantics (web/AGENTS
// 3.14) instead of pinning one language's rendered sentence.
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const TITLE_KEY =
  'Redis concurrency backend unavailable. Requests are being counted on local fallback buckets.'
const FORMATTED_TIME = /\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/

const ACTIVE: ConcurrencyDegradedStatus = {
  active: true,
  lastDegradedAt: 1_767_225_845_000,
  consecutiveFailures: 3,
}
const RECOVERED: ConcurrencyDegradedStatus = {
  active: false,
  lastDegradedAt: 1_767_225_845_000,
  consecutiveFailures: 0,
}
const NEVER: ConcurrencyDegradedStatus = {
  active: false,
  lastDegradedAt: 0,
  consecutiveFailures: 0,
}

function renderBadge(degraded?: ConcurrencyDegradedStatus) {
  return render(
    <I18nextProvider i18n={i18n}>
      <DegradedBadge degraded={degraded} />
    </I18nextProvider>
  )
}

describe('DegradedBadge three states (D-14/D-16)', () => {
  test('active degraded snapshot renders a warning badge with the explanation title', () => {
    renderBadge(ACTIVE)
    const badge = screen.getByText('Redis degraded')
    expect(badge).toBeInTheDocument()
    expect(badge).toHaveAttribute('title', TITLE_KEY)
  })

  test('recovered snapshot drops the badge but keeps the formatted last-degraded timestamp', () => {
    renderBadge(RECOVERED)
    expect(screen.queryByText('Redis degraded')).not.toBeInTheDocument()
    expect(
      screen.getByText(new RegExp(`Last degraded at ${FORMATTED_TIME.source}`))
    ).toBeInTheDocument()
  })

  test('never-degraded snapshot renders nothing', () => {
    const { container } = renderBadge(NEVER)
    expect(container).toBeEmptyDOMElement()
  })

  test('missing degraded envelope renders nothing on an old backend', () => {
    const { container } = renderBadge(undefined)
    expect(container).toBeEmptyDOMElement()
  })
})
