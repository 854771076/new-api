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
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { buttonVariants } from '@/components/ui/button'
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardFooter,
} from '@/components/ui/card'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

interface MarketplaceApp {
  id: number
  name: string
  logo_url: string
  description: string
  url: string
}

export function AppMarketplace() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['app-marketplace'],
    queryFn: async () => {
      const response = await api.get<{
        success: boolean
        message?: string
        data: MarketplaceApp[]
      }>('/api/app-marketplace')
      return requireServerSuccess(response.data).data
    },
  })
  return (
    <PublicLayout>
      <div className='space-y-6 py-6'>
        <h1 className='text-2xl font-semibold'>{t('App marketplace')}</h1>
        {query.isPending && <LoadingState />}
        {query.isError && (
          <ErrorState
            title={t('Failed to load apps')}
            onRetry={() => void query.refetch()}
          />
        )}
        {query.isSuccess && query.data.length === 0 && (
          <EmptyState title={t('No apps available yet')} />
        )}
        {query.isSuccess && (
          <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
            {query.data.map((app) => (
              <Card key={app.id}>
                <CardHeader>
                  {app.logo_url && (
                    <img
                      src={app.logo_url}
                      alt=''
                      className='mb-2 size-12 rounded-md object-contain'
                      loading='lazy'
                      referrerPolicy='no-referrer'
                    />
                  )}
                  <CardTitle className='break-words'>{app.name}</CardTitle>
                  <CardDescription className='break-words whitespace-pre-wrap'>
                    {app.description}
                  </CardDescription>
                </CardHeader>
                <CardFooter className='mt-auto'>
                  <a
                    className={buttonVariants()}
                    href={app.url}
                    target='_blank'
                    rel='noopener noreferrer'
                    aria-label={t('Open {{app}}', { app: app.name })}
                  >
                    {t('Open App')}
                  </a>
                </CardFooter>
              </Card>
            ))}
          </div>
        )}
      </div>
    </PublicLayout>
  )
}
