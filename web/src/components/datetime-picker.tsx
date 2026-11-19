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
import timezone from 'dayjs/plugin/timezone'
import utc from 'dayjs/plugin/utc'
import { ChevronDownIcon } from 'lucide-react'
import * as React from 'react'
import { enUS, fr, ja, ru, vi, zhCN, zhTW } from 'react-day-picker/locale'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import dayjs from '@/lib/dayjs'
import { cn } from '@/lib/utils'

dayjs.extend(utc)
dayjs.extend(timezone)

const calendarLocales = {
  en: enUS,
  zhCN,
  zhTW,
  fr,
  ru,
  ja,
  vi,
} as const

interface DateTimePickerProps extends React.AriaAttributes {
  /** Explicit zones use a seconds-precision native editor instead of browser-local dates. */
  timeZone?: 'UTC' | 'Asia/Shanghai'
  id?: string
  value?: Date
  onChange?: (date: Date | undefined) => void
  placeholder?: string
  className?: string
}

export function DateTimePicker({
  value,
  onChange,
  placeholder,
  className,
  timeZone,
  id,
  ...controlProps
}: DateTimePickerProps) {
  const { t, i18n } = useTranslation()
  const placeholderText = placeholder ?? t('Select date')
  const calendarLocale =
    calendarLocales[i18n.language as keyof typeof calendarLocales] ?? enUS
  const currentYear = new Date().getFullYear()
  const [open, setOpen] = React.useState(false)
  const [date, setDate] = React.useState<Date | undefined>(value)
  const [month, setMonth] = React.useState<Date | undefined>(value)
  const [time, setTime] = React.useState<string>('00:00:00')

  React.useEffect(() => {
    setDate(value)
    setMonth(value)
    if (value) {
      setTime(dayjs(value).format('HH:mm:ss'))
    }
  }, [value])

  const handleDateSelect = (selectedDate: Date | undefined) => {
    if (selectedDate) {
      const [hours, minutes, seconds = 0] = time.split(':').map(Number)
      const newDate = new Date(selectedDate)
      newDate.setHours(hours, minutes, seconds, 0)
      setDate(newDate)
      setMonth(newDate)
      onChange?.(newDate)
      setOpen(false)
    } else {
      setDate(undefined)
      setMonth(undefined)
      onChange?.(undefined)
    }
  }

  const handleTimeChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newTime = e.target.value
    // The browser reports an emptied or half-cleared field as ''. Keep the
    // current time: the field shows it again and no invalid date goes out.
    if (!newTime) return
    setTime(newTime)

    if (date) {
      // A browser may omit :00 seconds from the value.
      const [hours, minutes, seconds = 0] = newTime.split(':').map(Number)
      const newDate = new Date(date)
      newDate.setHours(hours, minutes, seconds, 0)
      setDate(newDate)
      onChange?.(newDate)
    }
  }

  const handleClear = () => {
    setDate(undefined)
    setMonth(undefined)
    setTime('00:00:00')
    onChange?.(undefined)
  }

  if (timeZone) {
    const offset = timeZone === 'Asia/Shanghai' ? 8 * 3600_000 : 0
    let text = value
      ? new Date(value.getTime() + offset)
          .toISOString()
          .split('.')[0]
          .replace(/^\+0/, '')
      : ''
    if (value && value.getUTCFullYear() < 9999) {
      text = dayjs(value).tz(timeZone).format('YYYY-MM-DDTHH:mm:ss')
    }
    return (
      <Input
        {...controlProps}
        id={id}
        type='datetime-local'
        step={1}
        className={className}
        min={new Date(1000 + offset).toISOString().split('.')[0]}
        max={new Date(253402300799000 + offset)
          .toISOString()
          .split('.')[0]
          .replace(/^\+0/, '')}
        value={text}
        onChange={(event) => {
          if (!event.target.value) {
            onChange?.(undefined)
            return
          }
          // shortcut: Day.js cannot parse extended years, use fixed future offsets until it supports them.
          if (
            /^\d{4}-/.test(event.target.value) &&
            !event.target.value.startsWith('9999-')
          ) {
            const date = dayjs.tz(event.target.value, timeZone)
            if (date.isValid()) onChange?.(date.toDate())
            return
          }
          const milliseconds =
            Date.parse(
              `${event.target.value.replace(/^(\d{5,})-/, (_, year: string) => `+${year.padStart(6, '0')}-`)}Z`
            ) - offset
          if (Number.isFinite(milliseconds)) onChange?.(new Date(milliseconds))
        }}
      />
    )
  }

  return (
    <div className={cn('flex gap-2', className)}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          render={
            <Button
              variant='outline'
              className={cn(
                'flex-1 justify-between font-normal',
                !date && 'text-muted-foreground'
              )}
            />
          }
        >
          {date ? dayjs(date).format('YYYY-MM-DD') : placeholderText}
          <ChevronDownIcon className='h-4 w-4 opacity-50' />
        </PopoverTrigger>
        <PopoverContent className='w-auto overflow-hidden p-0' align='start'>
          <Calendar
            mode='single'
            selected={date}
            month={month}
            onMonthChange={setMonth}
            captionLayout='dropdown'
            onSelect={handleDateSelect}
            locale={calendarLocale}
            startMonth={new Date(currentYear - 100, 0)}
            endMonth={new Date(currentYear + 100, 11)}
          />
        </PopoverContent>
      </Popover>
      <Input
        type='time'
        step={1}
        value={time}
        aria-label={t('Time')}
        onChange={handleTimeChange}
        className='w-32 appearance-none [&::-webkit-calendar-picker-indicator]:hidden [&::-webkit-calendar-picker-indicator]:appearance-none'
        disabled={!date}
      />
      {date && (
        <Button
          type='button'
          variant='outline'
          size='icon'
          onClick={handleClear}
          className='shrink-0'
          aria-label='Clear'
        >
          <span aria-hidden='true'>✕</span>
        </Button>
      )}
    </div>
  )
}
