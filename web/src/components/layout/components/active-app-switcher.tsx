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
import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { getOAuthApps } from '@/features/oauth-apps/api'
import { OAUTH_APP_STATUS } from '@/features/oauth-apps/types'
import { useActiveApp } from '@/hooks/use-active-app'
import { useIsAdmin } from '@/hooks/use-admin'
import { PLATFORM_APP_ID } from '@/stores/active-app-store'

export function ActiveAppSwitcher() {
  const { t } = useTranslation()
  const isAdmin = useIsAdmin()
  const { appId, setAppId } = useActiveApp()

  const { data: apps } = useQuery({
    queryKey: ['oauth-apps'],
    queryFn: async () => {
      const res = await getOAuthApps()
      return res.data ?? []
    },
    enabled: isAdmin,
    retry: false,
    meta: { errorToast: false },
  })

  useEffect(() => {
    if (!isAdmin || !apps || appId === PLATFORM_APP_ID) return
    if (apps.some((app) => app.id === appId)) return
    setAppId(PLATFORM_APP_ID)
  }, [appId, apps, isAdmin, setAppId])

  if (!isAdmin) return null

  const items = [
    { value: PLATFORM_APP_ID, label: t('Platform') },
    ...(apps ?? []).map((app) => ({
      value: app.id,
      label:
        app.status === OAUTH_APP_STATUS.ENABLED
          ? app.name
          : `${app.name} (${t('Disabled')})`,
    })),
  ]
  // Fall back to the platform entry while apps load, when loading fails, or
  // when the persisted app no longer exists.
  const selectedValue = items.some((item) => item.value === appId)
    ? appId
    : PLATFORM_APP_ID

  return (
    <div className='hidden lg:block'>
      <Select
        items={items}
        value={selectedValue}
        onValueChange={(value) => {
          if (value !== null) setAppId(value)
        }}
      >
        <SelectTrigger
          className='w-32 xl:w-44'
          aria-label={t('Active App')}
          title={t('Switch application')}
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent align='end' alignItemWithTrigger={false}>
          {items.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}
