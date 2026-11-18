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
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { getOAuthApps } from '@/features/oauth-apps/api'
import { OAUTH_APP_STATUS } from '@/features/oauth-apps/types'
import { getSelfApps } from '@/features/wallet/app-wallet-api'
import { useActiveApp } from '@/hooks/use-active-app'
import { useIsAdmin } from '@/hooks/use-admin'
import { requireServerSuccess } from '@/lib/server-error-message'
import { PLATFORM_APP_ID } from '@/stores/active-app-store'
import { useAuthStore } from '@/stores/auth-store'

export function ActiveAppSwitcher(props: { includeProxy?: boolean } = {}) {
  const { t } = useTranslation()
  const isAdmin = useIsAdmin()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const { appId, setAppId } = useActiveApp()

  const { data: apps } = useQuery({
    queryKey: isAdmin ? ['oauth-apps'] : ['self-apps', userId],
    queryFn: async () => {
      const res = requireServerSuccess(
        await (isAdmin ? getOAuthApps() : getSelfApps())
      )
      return res.data ?? []
    },
    enabled: !!userId,
    refetchInterval: 30000,
    retry: false,
    meta: { errorToast: false },
  })

  useEffect(() => {
    if (!apps || appId === PLATFORM_APP_ID) return
    if (
      apps.some(
        (app) =>
          app.id === appId &&
          (props.includeProxy || app.oauth_enabled !== false)
      )
    ) {
      return
    }
    setAppId(PLATFORM_APP_ID)
  }, [appId, apps, isAdmin, setAppId, props.includeProxy])

  if (!userId) return null

  const items = [
    { value: PLATFORM_APP_ID, label: t('Platform') },
    ...(apps ?? [])
      .filter((app) => props.includeProxy || app.oauth_enabled !== false)
      .map((app) => ({
        value: app.id,
        label:
          app.status === OAUTH_APP_STATUS.ENABLED
            ? app.name
            : `${app.name} (${t('Disabled')})`,
      })),
  ]
  // Until the list can reconcile a missing app, show the actual financial
  // scope rather than presenting Platform while requests still use this ID.
  if (!items.some((item) => item.value === appId)) {
    items.push({ value: appId, label: `${t('App')} #${appId}` })
  }

  return (
    <div className='min-w-0 shrink-0'>
      <Select
        items={items}
        value={appId}
        onValueChange={(value) => {
          if (value !== null) setAppId(value)
        }}
      >
        <SelectTrigger
          className='w-24 sm:w-32 xl:w-44'
          aria-label={t(
            props.includeProxy ? 'Financial App filter' : 'Active App'
          )}
          title={t('Switch application')}
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent align='end' alignItemWithTrigger={false}>
          <SelectGroup>
            {items.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>
  )
}
