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
import { fireEvent, render, screen } from '@testing-library/react'
import { expect, test, vi } from 'vitest'

import { DateTimePicker } from '../datetime-picker'

test('shows the seconds of the selected time', () => {
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 8, 30, 45)}
      onChange={() => undefined}
    />
  )
  expect(screen.getByLabelText('Time')).toHaveValue('08:30:45')
})

test('keeps the seconds typed into the time field', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 8, 0, 0)}
      onChange={onChange}
    />
  )
  fireEvent.change(screen.getByLabelText('Time'), {
    target: { value: '08:30:45' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date(2026, 9, 11, 8, 30, 45))
})

test('reads a time without seconds as zero seconds', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 8, 0, 30)}
      onChange={onChange}
    />
  )
  fireEvent.change(screen.getByLabelText('Time'), {
    target: { value: '09:15' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date(2026, 9, 11, 9, 15, 0))
})

test('keeps the current time when the time field is emptied', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 20, 30, 45)}
      onChange={onChange}
    />
  )
  const field = screen.getByLabelText('Time')
  fireEvent.change(field, { target: { value: '' } })
  expect(onChange).not.toHaveBeenCalled()
  expect(field).toHaveValue('20:30:45')
})

test('explicit timezone preserves the maximum accepted instant beyond local year 9999', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      aria-label='Validity'
      timeZone='Asia/Shanghai'
      value={new Date(253402300799000)}
      onChange={onChange}
    />
  )
  expect(screen.getByLabelText('Validity')).toHaveValue(
    '10000-01-01T07:59:59.000'
  )
  fireEvent.change(screen.getByLabelText('Validity'), {
    target: { value: '10000-01-01T07:59:58' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date(253402300798000))
})

test('Shanghai historical daylight saving dates use their real UTC offset', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      aria-label='Validity'
      timeZone='Asia/Shanghai'
      value={new Date('1988-07-01T00:00:00Z')}
      onChange={onChange}
    />
  )
  expect(screen.getByLabelText('Validity')).toHaveValue('1988-07-01T09:00')
  fireEvent.change(screen.getByLabelText('Validity'), {
    target: { value: '1988-07-01T10:00' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date('1988-07-01T01:00:00Z'))
})
