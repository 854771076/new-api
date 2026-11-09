import { createFileRoute, redirect } from '@tanstack/react-router'

import { OAuthAppsPage } from '@/features/oauth-apps'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/oauth-apps/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: OAuthAppsPage,
})
