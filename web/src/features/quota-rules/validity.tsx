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
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'

import type { QuotaRule } from './api'

export function QuotaRuleValidity(props: { rule: QuotaRule }) {
  const { t, i18n } = useTranslation()
  const rule = props.rule
  const labels = {
    disabled: t('Disabled'),
    scheduled: t('Scheduled'),
    effective: t('Effective'),
    expired: t('Expired'),
  }
  const formatter = new Intl.DateTimeFormat(
    toIntlLocale(i18n.resolvedLanguage || i18n.language),
    {
      timeZone: rule.time_window_timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    }
  )
  return (
    <div className='text-muted-foreground text-xs'>
      <div>
        {rule.validity_status ? labels[rule.validity_status] : t('Enabled')}
      </div>
      <div>
        {rule.valid_from
          ? formatter.format(new Date(rule.valid_from * 1000))
          : t('Unbounded')}{' '}
        →{' '}
        {rule.valid_until
          ? formatter.format(new Date(rule.valid_until * 1000))
          : t('Unbounded')}
      </div>
      <div>{rule.time_window_timezone}</div>
    </div>
  )
}
