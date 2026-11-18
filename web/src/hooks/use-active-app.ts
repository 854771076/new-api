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
import { createContext, useContext } from 'react'

import { PLATFORM_APP_ID, useActiveAppStore } from '@/stores/active-app-store'
import { useAuthStore } from '@/stores/auth-store'

export type ActiveAppSelection = {
  appId: number
  setAppId: (appId: number) => void
  isPlatform: boolean
}

/**
 * Reads the globally selected OAuth app. The app list is owned by the
 * switcher component, so this hook only exposes the stored selection.
 */
export const ActiveAppContext = createContext<ActiveAppSelection | null>(null)

export function useActiveApp(): ActiveAppSelection {
  const scoped = useContext(ActiveAppContext)
  const userId = useAuthStore((state) => state.auth.user?.id)
  const appId = useActiveAppStore((state) =>
    userId && state.ownerId === userId ? state.appId : PLATFORM_APP_ID
  )
  const setAppId = useActiveAppStore((state) => state.setAppId)
  return scoped ?? { appId, setAppId, isPlatform: appId === PLATFORM_APP_ID }
}
