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
import { Check, ChevronsUpDown } from 'lucide-react'
import * as React from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

export type ComboboxInputOption = {
  value: string
  label: string
  icon?: React.ReactNode
  disabled?: boolean
  description?: string
}

interface ComboboxInputProps {
  options: readonly ComboboxInputOption[]
  value?: string
  onValueChange: (value: string) => void
  placeholder?: string
  emptyText?: string
  className?: string
  popupClassName?: string
  id?: string
  allowCustomValue?: boolean
  openOnFocus?: boolean
  disabled?: boolean
  onKeyDown?: React.KeyboardEventHandler<HTMLInputElement>
  'aria-label'?: string
  'aria-labelledby'?: string
  'aria-invalid'?: React.AriaAttributes['aria-invalid']
}

export function ComboboxInput({
  options,
  value = '',
  onValueChange,
  placeholder = 'Select or type...',
  emptyText = 'No option found.',
  className,
  popupClassName,
  id,
  allowCustomValue = false,
  openOnFocus = true,
  disabled = false,
  onKeyDown,
  'aria-label': ariaLabel,
  'aria-labelledby': ariaLabelledBy,
  'aria-invalid': ariaInvalid,
}: ComboboxInputProps) {
  const { t } = useTranslation()
  const listId = React.useId()
  const [open, setOpen] = React.useState(false)
  const [searchValue, setSearchValue] = React.useState('')
  const [searchChanged, setSearchChanged] = React.useState(false)
  const [highlightedIndex, setHighlightedIndex] = React.useState(-1)
  const containerRef = React.useRef<HTMLDivElement>(null)
  const inputRef = React.useRef<HTMLInputElement>(null)
  const listRef = React.useRef<HTMLUListElement>(null)
  const dropdownRef = React.useRef<HTMLDivElement>(null)
  // The dropdown is portaled so scrolling ancestors cannot clip it. It goes
  // into the enclosing dialog when there is one, which keeps a modal parent
  // from treating option clicks as outside presses.
  const [dropdown, setDropdown] = React.useState<{
    container: HTMLElement
    top: number
    left: number
    width: number
  } | null>(null)
  const pointerFocusRef = React.useRef(false)
  const selectedOption = React.useMemo(
    () => options.find((option) => option.value === value),
    [options, value]
  )
  const displayValue =
    open && searchValue !== '' ? searchValue : (selectedOption?.label ?? value)

  const filteredOptions = React.useMemo(() => {
    if (!searchChanged || !searchValue.trim()) return options
    const search = searchValue.toLowerCase().trim()
    return options.filter(
      (option) =>
        option.label.toLowerCase().includes(search) ||
        option.value.toLowerCase().includes(search)
    )
  }, [options, searchValue, searchChanged])

  // Reset highlight when filtered options change
  React.useEffect(() => {
    setHighlightedIndex(-1)
  }, [filteredOptions])

  // Handle click outside to close
  React.useEffect(() => {
    if (!open) return

    const handleClickOutside = (e: MouseEvent) => {
      const target = e.target as Node
      if (
        containerRef.current &&
        !containerRef.current.contains(target) &&
        !dropdownRef.current?.contains(target)
      ) {
        setOpen(false)
        setSearchValue('')
      }
    }

    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [open])

  // Position the portaled dropdown under the input and follow scroll/resize.
  React.useEffect(() => {
    if (!open) {
      setDropdown(null)
      return
    }
    const input = inputRef.current
    if (!input) return
    const container =
      input.closest<HTMLElement>('[role="dialog"]') ?? document.body
    // 弹层是 position:fixed 的视口坐标层。容器（对话框）有 overflow 裁剪且
    // 内容可滚动：向下空间不足时向上翻转，且打开期间持续跟随输入框位移，
    // 否则弹层会落到对话框/视口之外，DOM 存在但屏幕上不可见。
    const popupGap = 4
    const popupMaxHeight = 208
    const measure = () => {
      const rect = input.getBoundingClientRect()
      const containerRect =
        container === document.body ? null : container.getBoundingClientRect()
      const minTop = containerRect ? containerRect.top + popupGap : 0
      const maxBottom = containerRect
        ? containerRect.bottom - popupGap
        : window.innerHeight - popupGap
      const spaceBelow = maxBottom - rect.bottom - popupGap
      const spaceAbove = rect.top - popupGap - minTop
      const placeBelow =
        spaceBelow >= popupMaxHeight || spaceBelow >= spaceAbove
      let top = placeBelow
        ? rect.bottom + popupGap
        : rect.top - popupGap - popupMaxHeight
      top = Math.max(top, minTop)
      top = Math.min(top, maxBottom - popupMaxHeight)
      top = Math.min(top, window.innerHeight - popupMaxHeight - popupGap)
      // 弹层 DOM 挂载在容器内；容器若成为 fixed 的包含块（transform/contain 等），
      // style 坐标是容器局部坐标，与上面的视口坐标差一个包含块偏移（实测对话框
      // 偏移137px，弹层落到框外被裁）。用「实际渲染位置 - 当前 style 值」测出
      // 偏移并换算，偏移恒定时逐帧幂等收敛；无布局环境（jsdom）下 rect 全为 0，
      // 跳过换算以保持视口坐标语义。
      let left = rect.left
      const node = dropdownRef.current
      if (node && node.isConnected) {
        const actual = node.getBoundingClientRect()
        const hasLayout =
          actual.width !== 0 ||
          actual.height !== 0 ||
          actual.top !== 0 ||
          actual.left !== 0
        if (hasLayout) {
          const currentTop = Number.parseFloat(node.style.top)
          if (Number.isFinite(currentTop)) {
            top -= actual.top - currentTop
          }
          const currentLeft = Number.parseFloat(node.style.left)
          if (Number.isFinite(currentLeft)) {
            left -= actual.left - currentLeft
          }
        }
      }
      const next = { container, top, left, width: rect.width }
      setDropdown((prev) =>
        prev &&
        prev.top === next.top &&
        prev.left === next.left &&
        prev.width === next.width &&
        prev.container === next.container
          ? prev
          : next
      )
    }
    measure()
    // rAF 持续跟随：容器内滚动/动画不一定派发 scroll 事件到 window
    let followId = requestAnimationFrame(function follow() {
      measure()
      followId = requestAnimationFrame(follow)
    })
    window.addEventListener('scroll', measure, true)
    window.addEventListener('resize', measure)
    return () => {
      cancelAnimationFrame(followId)
      window.removeEventListener('scroll', measure, true)
      window.removeEventListener('resize', measure)
    }
  }, [open])

  const handleSelect = (selectedValue: string) => {
    onValueChange(selectedValue)
    setOpen(false)
    setSearchValue('')
    inputRef.current?.focus()
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!open && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      e.preventDefault()
      setSearchValue(allowCustomValue ? value : '')
      setSearchChanged(false)
      setOpen(true)
      return
    }

    if (!open) return

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setHighlightedIndex((prev) =>
          prev < filteredOptions.length - 1 ? prev + 1 : 0
        )
        break
      case 'ArrowUp':
        e.preventDefault()
        setHighlightedIndex((prev) =>
          prev > 0 ? prev - 1 : filteredOptions.length - 1
        )
        break
      case 'Enter':
        if (highlightedIndex >= 0 && filteredOptions[highlightedIndex]) {
          e.preventDefault()
          handleSelect(filteredOptions[highlightedIndex].value)
        } else if (allowCustomValue && searchValue.trim()) {
          e.preventDefault()
          handleSelect(searchValue.trim())
        } else {
          if (!onKeyDown) e.preventDefault()
          // No highlighted option, just close the dropdown and keep current value
          setOpen(false)
          setSearchValue('')
        }
        break
      case 'Escape':
        e.preventDefault()
        e.stopPropagation()
        setOpen(false)
        setSearchValue('')
        break
    }
  }

  // Scroll highlighted item into view
  React.useEffect(() => {
    if (highlightedIndex < 0 || !listRef.current) return
    const item = listRef.current.children[highlightedIndex] as HTMLElement
    item?.scrollIntoView({ block: 'nearest' })
  }, [highlightedIndex])

  const showDropdown =
    open &&
    !disabled &&
    (filteredOptions.length > 0 || (allowCustomValue && searchValue.trim()))

  return (
    <div ref={containerRef} className='relative'>
      <Input
        ref={inputRef}
        id={id}
        type='text'
        role='combobox'
        disabled={disabled}
        aria-label={ariaLabel}
        aria-labelledby={ariaLabelledBy}
        aria-invalid={ariaInvalid}
        aria-expanded={!!showDropdown}
        aria-controls={
          showDropdown && filteredOptions.length > 0 ? listId : undefined
        }
        aria-activedescendant={
          showDropdown && highlightedIndex >= 0
            ? `${listId}-${highlightedIndex}`
            : undefined
        }
        aria-haspopup='listbox'
        aria-autocomplete='list'
        autoComplete='off'
        placeholder={placeholder}
        value={displayValue}
        onChange={(e) => {
          const nextValue = e.target.value
          setSearchValue(nextValue)
          setSearchChanged(true)
          // allowCustomValue consumers (log group filter, billing timezone,
          // model mapping rows, custom icon keys, dashboard token key) treat
          // the parent value as a draft that mirrors the keystrokes: they
          // commit on blur, on Search, or on their own validation, so the
          // typed text must reach onValueChange as it is typed. The
          // displayValue fallback below keeps the selected label visible
          // while the dropdown opens without search text, which is what
          // stops the input from looking "stuck" on reopen.
          if (allowCustomValue) {
            onValueChange(nextValue)
          }
          if (!open) setOpen(true)
        }}
        onPointerDown={() => {
          pointerFocusRef.current = true
          if (document.activeElement === inputRef.current && !open) {
            setSearchValue(allowCustomValue ? value : '')
            setSearchChanged(false)
            setOpen(true)
          }
        }}
        onFocus={() => {
          setSearchValue(allowCustomValue ? value : '')
          setSearchChanged(false)
          if (openOnFocus || pointerFocusRef.current) {
            setOpen(true)
          }
          pointerFocusRef.current = false
        }}
        onBlur={() => {
          setOpen(false)
          setSearchValue('')
        }}
        onKeyDown={(event) => {
          handleKeyDown(event)
          if (!event.defaultPrevented) onKeyDown?.(event)
        }}
        className={cn('pr-9', className)}
      />
      <ChevronsUpDown className='pointer-events-none absolute top-1/2 right-3 size-4 shrink-0 -translate-y-1/2 opacity-50' />

      {showDropdown &&
        dropdown &&
        createPortal(
          <div
            ref={dropdownRef}
            style={{
              position: 'fixed',
              top: dropdown.top,
              left: dropdown.left,
              width: dropdown.width,
            }}
            className={cn(
              'bg-popover text-popover-foreground z-100 rounded-md border shadow-md',
              popupClassName
            )}
          >
            {filteredOptions.length > 0 ? (
              <ul
                ref={listRef}
                id={listId}
                role='listbox'
                className='max-h-[200px] overflow-y-auto p-1'
              >
                {filteredOptions.map((option, index) => (
                  <li
                    key={option.value}
                    id={`${listId}-${index}`}
                    role='option'
                    aria-selected={value === option.value}
                    data-highlighted={index === highlightedIndex}
                    className={cn(
                      'relative flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm select-none',
                      index === highlightedIndex &&
                        'bg-accent text-accent-foreground',
                      value === option.value && 'font-medium'
                    )}
                    onMouseEnter={() => setHighlightedIndex(index)}
                    onMouseDown={(e) => {
                      e.preventDefault() // Prevent blur
                      handleSelect(option.value)
                    }}
                  >
                    <Check
                      className={cn(
                        'size-4 shrink-0',
                        value === option.value ? 'opacity-100' : 'opacity-0'
                      )}
                    />
                    {option.icon && <span aria-hidden>{option.icon}</span>}
                    <span className='truncate'>{option.label}</span>
                  </li>
                ))}
              </ul>
            ) : (
              <div className='px-2 py-6 text-center text-sm'>
                {t(emptyText)}
                {allowCustomValue && searchValue.trim() && (
                  <div className='text-muted-foreground mt-1 text-xs'>
                    {t('Press Enter to use "{{value}}"', {
                      value: searchValue.trim(),
                    })}
                  </div>
                )}
              </div>
            )}
          </div>,
          dropdown.container
        )}
    </div>
  )
}
