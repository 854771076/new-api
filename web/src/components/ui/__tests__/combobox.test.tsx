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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { Dialog } from '@/components/dialog'

import { Combobox } from '../combobox'
import { Sheet, SheetContent, SheetTitle } from '../sheet'

const options = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Google' },
  { value: 'disabled', label: 'Unavailable provider', disabled: true },
]

function Fixture() {
  const [value, setValue] = useState('openai')
  return (
    <>
      <Combobox
        options={options}
        value={value}
        onValueChange={(next) => setValue(next ?? '')}
        aria-label='Provider'
        emptyText='No matching provider'
      />
      <output>{value}</output>
    </>
  )
}

describe('searchable single selection', () => {
  it.each(['dialog', 'sheet'] as const)(
    'keeps the options closed when a %s autofocuses the input, then opens on click',
    async (container) => {
      const field = <Fixture />
      render(
        container === 'dialog' ? (
          <Dialog open title='Choose a provider'>
            {field}
          </Dialog>
        ) : (
          <Sheet open>
            <SheetContent>
              <SheetTitle>Choose a provider</SheetTitle>
              {field}
            </SheetContent>
          </Sheet>
        )
      )
      const user = userEvent.setup()
      const input = screen.getByRole('combobox', { name: 'Provider' })
      await waitFor(() => expect(input).toHaveFocus())
      expect(input).toHaveAttribute('aria-expanded', 'false')
      expect(input).toHaveValue('OpenAI')
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument()

      await user.click(input)
      expect(screen.getByRole('option', { name: 'Google' })).toBeVisible()
      await user.click(screen.getByRole('option', { name: 'Google' }))
      await waitFor(() => expect(input).toHaveValue('Google'))
      expect(input).toHaveAttribute('aria-expanded', 'false')
      expect(screen.getByRole('dialog')).toBeVisible()
    }
  )

  it('keeps options closed on Tab focus and opens them with the arrow key', async () => {
    render(<Fixture />)
    const user = userEvent.setup()
    const input = screen.getByRole('combobox', { name: 'Provider' })
    await user.tab()
    expect(input).toHaveFocus()
    expect(input).toHaveAttribute('aria-expanded', 'false')
    await user.keyboard('{ArrowDown}')
    expect(screen.getByRole('option', { name: 'Google' })).toBeVisible()
    await user.keyboard('{Escape}')
    expect(input).toHaveAttribute('aria-expanded', 'false')
    expect(input).toHaveValue('OpenAI')
  })

  it('opens and filters when typing into a focused, closed input', async () => {
    render(<Fixture />)
    const user = userEvent.setup()
    const input = screen.getByRole('combobox', { name: 'Provider' })
    await user.tab()
    await user.keyboard('g')
    expect(input).toHaveValue('g')
    expect(screen.getByRole('option', { name: 'Google' })).toBeVisible()
    expect(
      screen.queryByRole('option', { name: 'OpenAI' })
    ).not.toBeInTheDocument()
  })

  it.each([true, false])(
    'honors explicit openOnFocus=%s',
    async (openOnFocus) => {
      render(
        <Combobox
          options={options}
          value='openai'
          aria-label='Provider'
          openOnFocus={openOnFocus}
        />
      )
      const user = userEvent.setup()
      const input = screen.getByRole('combobox', { name: 'Provider' })
      await user.tab()
      expect(input).toHaveFocus()
      expect(input).toHaveAttribute('aria-expanded', String(openOnFocus))
      if (!openOnFocus) {
        await user.click(screen.getByRole('button', { name: 'Provider' }))
        expect(screen.getByRole('option', { name: 'Google' })).toBeVisible()
      }
    }
  )

  it('searches labels and values without committing text, shows empty results, and restores the selection on Escape', async () => {
    render(<Fixture />)
    const user = userEvent.setup()
    const input = screen.getByRole('combobox', { name: 'Provider' })
    expect(input).toHaveValue('OpenAI')
    await user.click(input)
    await user.type(input, 'missing')
    expect(screen.getByText('No matching provider')).toBeVisible()
    expect(screen.getByText('openai')).toHaveTextContent('openai')
    await user.keyboard('{Escape}')
    expect(input).toHaveValue('OpenAI')
    await user.click(input)
    await user.type(input, 'gemini')
    expect(screen.getByRole('option', { name: 'Google' })).toBeVisible()
    await user.keyboard('{ArrowDown}{Enter}')
    await waitFor(() => expect(input).toHaveValue('Google'))
    expect(screen.getByText('gemini')).toHaveTextContent('gemini')
  })

  it('respects disabled controls and options', async () => {
    const change = vi.fn()
    const view = render(
      <Combobox
        options={options}
        value='openai'
        onValueChange={change}
        aria-label='Provider'
        disabled
      />
    )
    const user = userEvent.setup()
    expect(screen.getByRole('combobox', { name: 'Provider' })).toBeDisabled()
    view.rerender(
      <Combobox
        options={options}
        value='openai'
        onValueChange={change}
        aria-label='Provider'
      />
    )
    await user.click(screen.getByRole('combobox', { name: 'Provider' }))
    expect(
      screen.getByRole('option', { name: 'Unavailable provider' })
    ).toHaveAttribute('aria-disabled', 'true')
    await user.click(
      screen.getByRole('option', { name: 'Unavailable provider' })
    )
    expect(change).not.toHaveBeenCalled()
  })
})

const pluginOptions = [
  {
    value: 'alpha',
    label: 'Alpha plugin',
    icon: <img src='/api/plugin/task/alpha/icon' alt='' />,
  },
  {
    value: 'beta',
    label: 'Beta plugin',
    icon: <img src='/api/plugin/task/beta/icon' alt='' />,
  },
]

function PluginSelectionFixture() {
  const [value, setValue] = useState<string | null>('alpha')
  return (
    <Combobox
      options={pluginOptions}
      value={value}
      onValueChange={setValue}
      showSelectedIcon
      aria-label='Task plugin'
    />
  )
}

describe('selected option icons', () => {
  it('shows the selected plugin logo and updates it when choosing another plugin', async () => {
    render(<PluginSelectionFixture />)
    const user = userEvent.setup()
    const input = screen.getByRole('combobox', { name: 'Task plugin' })
    expect(screen.getByAltText('')).toHaveAttribute(
      'src',
      '/api/plugin/task/alpha/icon'
    )
    await user.click(input)
    const nextOption = screen.getByRole('option', { name: 'Beta plugin' })
    expect(nextOption.querySelector('img')).toHaveAttribute(
      'src',
      '/api/plugin/task/beta/icon'
    )
    await user.click(nextOption)
    await waitFor(() => expect(input).toHaveValue('Beta plugin'))
    expect(screen.getByAltText('')).toHaveAttribute(
      'src',
      '/api/plugin/task/beta/icon'
    )
  })

  it('removes the logo when the selection is cleared or no longer has an icon', () => {
    const view = render(
      <Combobox
        options={pluginOptions}
        value='alpha'
        showSelectedIcon
        aria-label='Task plugin'
      />
    )
    expect(screen.getByAltText('')).toBeInTheDocument()
    view.rerender(
      <Combobox
        options={pluginOptions}
        value={null}
        showSelectedIcon
        aria-label='Task plugin'
      />
    )
    expect(screen.queryByAltText('')).not.toBeInTheDocument()
    view.rerender(
      <Combobox
        options={[{ value: 'alpha', label: 'Alpha plugin' }]}
        value='alpha'
        showSelectedIcon
        aria-label='Task plugin'
      />
    )
    expect(screen.queryByAltText('')).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Task plugin' })).toHaveValue(
      'Alpha plugin'
    )
  })

  it('preserves the existing text-only selected state unless icon display is requested', () => {
    render(
      <Combobox
        options={pluginOptions}
        value='alpha'
        aria-label='Task plugin'
      />
    )
    expect(screen.queryByAltText('')).not.toBeInTheDocument()
  })
})

type MockRect = {
  left: number
  top: number
  width: number
  height: number
  right: number
  bottom: number
}

function rect(left: number, top: number, width: number, height: number): MockRect {
  return { left, top, width, height, right: left + width, bottom: top + height }
}

function mockRectFor(el: Element, value: MockRect) {
  vi.spyOn(el, 'getBoundingClientRect').mockReturnValue(
    value as unknown as DOMRect
  )
}

describe('popup placement inside a dialog', () => {
  // 对话框(100,50)-(600,550)，内容可滚动；弹层 max 高度按 208(200+边框/间距)。
  // allowCustomValue 走 LegacyComboboxInput 分支——看板筛选对话框的令牌 Key
  // 筛选用的就是它（Base UI 分支自带锚定，不受此定位逻辑影响）。
  const dialogRect = rect(100, 50, 500, 500)

  function PlacementFixture() {
    return (
      <Combobox
        options={options}
        value=''
        onValueChange={() => undefined}
        aria-label='Provider'
        allowCustomValue
        emptyText='No matching provider'
      />
    )
  }

  async function openInDialog() {
    render(
      <Dialog open title='Choose a provider'>
        <PlacementFixture />
      </Dialog>
    )
    const input = screen.getByRole('combobox', { name: 'Provider' })
    const dialog = document.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()
    mockRectFor(dialog as Element, dialogRect)
    await userEvent.setup().click(input)
    await waitFor(() => expect(screen.getByRole('listbox')).toBeVisible())
    return input
  }

  function popupTop() {
    const style = screen.getByRole('listbox').parentElement?.style
    return Number.parseFloat(style?.top ?? '')
  }

  it('drops the popup right below the input when the dialog has room below', async () => {
    const input = await openInDialog()
    mockRectFor(input, rect(120, 100, 400, 32))
    window.dispatchEvent(new Event('scroll'))
    await waitFor(() => expect(popupTop()).toBe(136))
  })

  it('flips the popup above the input when space below is too short', async () => {
    // 输入框贴近对话框底部：下方仅 114px，不够 208 的弹层 → 向上翻转
    const input = await openInDialog()
    mockRectFor(input, rect(120, 400, 400, 32))
    window.dispatchEvent(new Event('scroll'))
    await waitFor(() => expect(popupTop()).toBe(188)) // 400 - 4 - 208
  })

  it('keeps the popup following the input when the dialog content scrolls', async () => {
    const input = await openInDialog()
    mockRectFor(input, rect(120, 100, 400, 32))
    window.dispatchEvent(new Event('scroll'))
    await waitFor(() => expect(popupTop()).toBe(136))

    // 模拟容器内滚动把输入框上移 60px
    mockRectFor(input, rect(120, 40, 400, 32))
    window.dispatchEvent(new Event('scroll'))
    await waitFor(() => expect(popupTop()).toBe(76)) // 40 + 32 + 4
  })

  it('self-corrects when the dialog becomes the fixed-position containing block', async () => {
    // 对话框若带 transform/contain 等成为 fixed 的包含块，同样的 top 值会整体
    // 偏移（实测偏移137px，弹层落到对话框外被裁掉）。弹层应按"实际渲染位置
    // 与期望视口位置的差值"逐帧纠偏。
    const input = await openInDialog()
    mockRectFor(input, rect(120, 100, 400, 32))
    window.dispatchEvent(new Event('scroll'))
    await waitFor(() => expect(popupTop()).toBe(136))

    const popup = screen.getByRole('listbox').parentElement as HTMLElement
    const containerOffset = 137
    // 模拟包含块偏移：渲染位置 = style.top + offset（跟随 style 变化）
    vi.spyOn(popup, 'getBoundingClientRect').mockImplementation(() => {
      const styleTop = Number.parseFloat(popup.style.top || '0')
      const styleLeft = Number.parseFloat(popup.style.left || '0')
      return rect(styleLeft + containerOffset, styleTop + containerOffset, 430, 202) as unknown as DOMRect
    })
    window.dispatchEvent(new Event('scroll'))
    // 期望视觉位置仍是 136：style.top 会被纠偏到 136 - 137 = -1
    await waitFor(() =>
      expect(Number.parseFloat(popup.style.top) + containerOffset).toBe(136)
    )
    // 稳定性：纠偏值必须跨帧收敛。若逐帧重算+阈值跳变，style.top 会在
    // 纠偏值与视口值之间来回振荡（真实浏览器里表现为弹层高频闪烁）。
    const seen = new Set<number>()
    for (let i = 0; i < 6; i += 1) {
      seen.add(Number.parseFloat(popup.style.top))
      await new Promise((resolve) => requestAnimationFrame(() => resolve(null)))
    }
    expect(seen.size).toBe(1)
    expect([...seen][0] + containerOffset).toBe(136)
  })
})
