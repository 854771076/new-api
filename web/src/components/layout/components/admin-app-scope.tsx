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
import { useState, type ReactNode } from 'react'

import { ActiveAppContext, useActiveApp } from '@/hooks/use-active-app'
import { useAuthStore } from '@/stores/auth-store'

export function AdminAppScope(props: { children: ReactNode }) {
  const userId = useAuthStore((s) => s.auth.user?.id)
  return <AccountScope key={userId}>{props.children}</AccountScope>
}
function AccountScope(props: { children: ReactNode }) {
  const active = useActiveApp()
  const [selection, setSelection] = useState<number | null>(null)
  const appId = selection ?? active.appId
  return (
    <ActiveAppContext.Provider
      value={{ appId, setAppId: setSelection, isPlatform: appId === 0 }}
    >
      {props.children}
    </ActiveAppContext.Provider>
  )
}
