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
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import { channelFormSchema } from '../../lib'
import { ModelMappingEditor } from '../model-mapping-editor'

test('external mapping changes update the editor without emitting an edit', () => {
  const onChange = vi.fn()
  const view = render(
    <ModelMappingEditor value='{"client-a":"upstream-a"}' onChange={onChange} />
  )
  expect(screen.getByDisplayValue('client-a')).toBeVisible()
  expect(screen.getByDisplayValue('upstream-a')).toBeVisible()

  view.rerender(
    <ModelMappingEditor value='{"client-b":"upstream-b"}' onChange={onChange} />
  )
  expect(screen.getByDisplayValue('client-b')).toBeVisible()
  expect(screen.getByDisplayValue('upstream-b')).toBeVisible()
  expect(screen.queryByDisplayValue('client-a')).not.toBeInTheDocument()
  expect(onChange).not.toHaveBeenCalled()
})

test('language changes preserve draft mappings and explain the same direction in JSON mode', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en, zh },
    keySeparator: false,
    interpolation: { escapeValue: false },
  })
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(
    <I18nextProvider i18n={i18n}>
      <ModelMappingEditor value='' onChange={onChange} />
    </I18nextProvider>
  )
  await user.click(screen.getByRole('button', { name: 'Add Mapping' }))
  await user.type(screen.getByPlaceholderText('gpt-3.5-turbo'), 'client-alias')
  await user.type(
    screen.getByPlaceholderText('gpt-3.5-turbo-0125'),
    'provider-model'
  )

  await act(() => i18n.changeLanguage('zh'))
  expect(screen.getByText('请求模型名称')).toBeVisible()
  expect(screen.getByText('上游模型名称')).toBeVisible()
  expect(screen.getByDisplayValue('client-alias')).toBeVisible()
  expect(screen.getByDisplayValue('provider-model')).toBeVisible()

  await user.click(screen.getByRole('tab', { name: 'JSON' }))
  expect(
    screen.getByText('JSON 的键是请求模型名称，值是上游模型名称。')
  ).toBeVisible()
  expect(screen.getByRole('textbox', { name: '模型映射' })).toHaveValue(
    '{\n  "client-alias": "provider-model"\n}'
  )
  expect(onChange).toHaveBeenLastCalledWith(
    '{\n  "client-alias": "provider-model"\n}'
  )
})

test('a new row focuses its request field and both fields offer searchable model options', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(
    <ModelMappingEditor
      value=''
      onChange={onChange}
      sourceModelOptions={['alpha-model', 'beta-model']}
      targetModelOptions={['upstream-a']}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Add Mapping' }))
  const source = screen.getByRole('combobox', { name: 'Request Model Name' })
  expect(source).toHaveFocus()
  expect(source).toHaveAttribute('aria-expanded', 'true')
  await user.click(screen.getByRole('option', { name: 'alpha-model' }))
  expect(source).toHaveValue('alpha-model')
  expect(onChange).toHaveBeenLastCalledWith('{\n  "alpha-model": ""\n}')

  await user.click(
    screen.getByRole('combobox', { name: 'Upstream Model Name' })
  )
  await user.click(screen.getByRole('option', { name: 'upstream-a' }))
  expect(onChange).toHaveBeenLastCalledWith(
    '{\n  "alpha-model": "upstream-a"\n}'
  )
})

test('the row filter appears from six mappings and narrows the visible rows by either name', async () => {
  const user = userEvent.setup()
  const five = JSON.stringify(
    Object.fromEntries([1, 2, 3, 4, 5].map((n) => [`alpha-${n}`, `up-${n}`]))
  )
  const view = render(<ModelMappingEditor value={five} onChange={vi.fn()} />)
  expect(
    screen.queryByRole('textbox', { name: 'Filter mappings' })
  ).not.toBeInTheDocument()

  const six = JSON.stringify({ ...JSON.parse(five), 'beta-6': 'up-6' })
  view.rerender(<ModelMappingEditor value={six} onChange={vi.fn()} />)
  await user.type(
    screen.getByRole('textbox', { name: 'Filter mappings' }),
    'beta'
  )
  expect(
    screen.getAllByRole('combobox', { name: 'Request Model Name' })
  ).toHaveLength(1)
  expect(screen.getByDisplayValue('beta-6')).toBeVisible()
  expect(screen.getByText('Showing 1 of 6 mappings')).toBeVisible()

  await user.clear(screen.getByRole('textbox', { name: 'Filter mappings' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Filter mappings' }),
    'up-2'
  )
  expect(screen.getByDisplayValue('alpha-2')).toBeVisible()
  expect(screen.getByText('Showing 1 of 6 mappings')).toBeVisible()
})

test('the batch button is only offered when the caller handles it', async () => {
  const user = userEvent.setup()
  const onBatchAdd = vi.fn()
  const view = render(<ModelMappingEditor value='' onChange={vi.fn()} />)
  expect(
    screen.queryByRole('button', { name: 'Batch Add' })
  ).not.toBeInTheDocument()

  view.rerender(
    <ModelMappingEditor value='' onChange={vi.fn()} onBatchAdd={onBatchAdd} />
  )
  await user.click(screen.getByRole('button', { name: 'Batch Add' }))
  expect(onBatchAdd).toHaveBeenCalledTimes(1)
})

test('a draft request with a known request name appends a row, focuses the upstream field, and stays unsaved until completed', async () => {
  const onChange = vi.fn()
  const handled = vi.fn()
  render(
    <ModelMappingEditor
      value=''
      onChange={onChange}
      draftRequest={{ from: 'alias', to: '', token: 1 }}
      onDraftRequestHandled={handled}
    />
  )
  await waitFor(() =>
    expect(
      screen.getByRole('combobox', { name: 'Upstream Model Name' })
    ).toHaveFocus()
  )
  expect(
    screen.getByRole('combobox', { name: 'Request Model Name' })
  ).toHaveValue('alias')
  expect(handled).toHaveBeenCalledTimes(1)
  expect(onChange).not.toHaveBeenCalled()
})

test('a draft request with only the upstream name focuses the request field, and a known request name reuses its existing row', async () => {
  const view = render(
    <ModelMappingEditor
      value='{"alias":"old"}'
      onChange={vi.fn()}
      draftRequest={{ from: '', to: 'up-1', token: 1 }}
    />
  )
  // The row is committed before its passive effect moves focus.
  await waitFor(() => {
    const requestFields = screen.getAllByRole('combobox', {
      name: 'Request Model Name',
    })
    expect(requestFields).toHaveLength(2)
    expect(requestFields[1]).toHaveFocus()
  })
  expect(screen.getByDisplayValue('up-1')).toBeVisible()

  view.rerender(
    <ModelMappingEditor
      value='{"alias":"old"}'
      onChange={vi.fn()}
      draftRequest={{ from: 'alias', to: '', token: 2 }}
    />
  )
  await waitFor(() =>
    expect(
      screen.getAllByRole('combobox', { name: 'Upstream Model Name' })[0]
    ).toHaveFocus()
  )
  expect(
    screen.getAllByRole('combobox', { name: 'Request Model Name' })
  ).toHaveLength(2)
})

test('a draft request can ask for the request field of an existing row', async () => {
  render(
    <ModelMappingEditor
      value='{"alias":"old"}'
      onChange={vi.fn()}
      draftRequest={{ from: 'alias', to: 'old', focus: 'from', token: 1 }}
    />
  )
  await waitFor(() =>
    expect(
      screen.getByRole('combobox', { name: 'Request Model Name' })
    ).toHaveFocus()
  )
  expect(
    screen.getAllByRole('combobox', { name: 'Request Model Name' })
  ).toHaveLength(1)
})

test('adding, editing and deleting a mapping row emits the resulting mapping', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<ModelMappingEditor value='' onChange={onChange} />)

  await user.click(screen.getByRole('button', { name: 'Add Mapping' }))
  const request = screen.getByRole('combobox', { name: 'Request Model Name' })
  const upstream = screen.getByRole('combobox', { name: 'Upstream Model Name' })
  await user.type(request, 'client-alias')
  await user.type(upstream, 'upstream-model')
  expect(onChange).toHaveBeenLastCalledWith(
    '{\n  "client-alias": "upstream-model"\n}'
  )

  await user.clear(upstream)
  await user.type(upstream, 'other-upstream')
  expect(onChange).toHaveBeenLastCalledWith(
    '{\n  "client-alias": "other-upstream"\n}'
  )

  await user.click(screen.getByRole('button', { name: 'Delete mapping' }))
  expect(onChange).toHaveBeenLastCalledWith('')
  expect(
    screen.getByText(
      'No model mappings configured. Click "Add Mapping" to get started.'
    )
  ).toBeVisible()
})

test('an empty upstream name is reported inline and emits a mapping the channel form refuses to save', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<ModelMappingEditor value='' onChange={onChange} />)

  await user.click(screen.getByRole('button', { name: 'Add Mapping' }))
  await user.type(
    screen.getByRole('combobox', { name: 'Request Model Name' }),
    'client-alias'
  )

  expect(
    await screen.findByText('Missing upstream model name for: client-alias')
  ).toBeVisible()
  // The relay drops blank upstream models, so the saved value must be refused.
  const emitted = onChange.mock.lastCall?.[0] as string
  expect(emitted).toBe('{\n  "client-alias": ""\n}')
  expect(channelFormSchema.shape.model_mapping.safeParse(emitted).success).toBe(
    false
  )
})

test('an upstream name equal to the request name is serialized without a validation error', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<ModelMappingEditor value='' onChange={onChange} />)

  await user.click(screen.getByRole('button', { name: 'Add Mapping' }))
  await user.type(
    screen.getByRole('combobox', { name: 'Request Model Name' }),
    'same-model'
  )
  await user.type(
    screen.getByRole('combobox', { name: 'Upstream Model Name' }),
    'same-model'
  )

  const emitted = onChange.mock.lastCall?.[0] as string
  expect(emitted).toBe('{\n  "same-model": "same-model"\n}')
  expect(
    screen.queryByText('Missing upstream model name for: same-model')
  ).not.toBeInTheDocument()
  expect(channelFormSchema.shape.model_mapping.safeParse(emitted).success).toBe(
    true
  )
})

test('invalid JSON in the JSON editor is reported and the raw text stays in the emitted value', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<ModelMappingEditor value='' onChange={onChange} />)

  await user.click(screen.getByRole('tab', { name: 'JSON' }))
  const editor = await screen.findByRole('textbox', { name: 'Model Mapping' })
  fireEvent.input(editor, { target: { value: '{"client-alias": ' } })

  expect(
    await screen.findByText('Model mapping must be valid JSON format')
  ).toBeVisible()
  expect(editor).toHaveAttribute('aria-invalid', 'true')
  expect(onChange).toHaveBeenLastCalledWith('{"client-alias": ')
})

test.each([
  ['["client-alias"]', 'Model mapping must be a valid JSON object'],
  ['{"client-alias": 1}', 'Model mapping values must be strings'],
])('the JSON editor reports %s as %s', async (json, message) => {
  const user = userEvent.setup()
  render(<ModelMappingEditor value='' onChange={vi.fn()} />)

  await user.click(screen.getByRole('tab', { name: 'JSON' }))
  const editor = await screen.findByRole('textbox', { name: 'Model Mapping' })
  fireEvent.input(editor, { target: { value: json } })

  expect(await screen.findByText(message)).toBeVisible()
  expect(editor).toHaveAttribute('aria-invalid', 'true')
})

test('two rows with the same request name are rejected with a visible error', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(
    <ModelMappingEditor
      value='{"client-alias":"upstream-model"}'
      onChange={onChange}
    />
  )

  await user.click(screen.getByRole('button', { name: 'Add Mapping' }))
  await user.type(
    screen.getAllByRole('combobox', { name: 'Request Model Name' })[1],
    'client-alias'
  )

  expect(
    await screen.findByText('Duplicate source model mappings are not allowed')
  ).toBeVisible()
  expect(
    screen.getByText('Duplicate source model(s): client-alias')
  ).toBeVisible()
  expect(onChange).toHaveBeenLastCalledWith('{ "duplicate_source_models": ')
})

test('commit fires once an edit settles on Enter or when focus leaves, not per keystroke', async () => {
  const user = userEvent.setup()
  const onCommit = vi.fn()
  render(
    <>
      <ModelMappingEditor
        value='{"alias":""}'
        onChange={vi.fn()}
        onCommit={onCommit}
      />
      <button type='button'>outside</button>
    </>
  )
  const upstream = screen.getByRole('combobox', { name: 'Upstream Model Name' })
  await user.type(upstream, 'up-1')
  expect(onCommit).not.toHaveBeenCalled()
  await user.keyboard('{Enter}')
  expect(onCommit).toHaveBeenCalledTimes(1)
  expect(onCommit).toHaveBeenLastCalledWith('{\n  "alias": "up-1"\n}')

  await user.click(screen.getByRole('combobox', { name: 'Request Model Name' }))
  expect(onCommit).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole('button', { name: 'outside' }))
  expect(onCommit).toHaveBeenCalledTimes(2)
})
