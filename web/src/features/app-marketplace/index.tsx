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
import { ArrowUpRight, LayoutGrid } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
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
      <div className='mx-auto max-w-6xl space-y-8 py-6 sm:py-10'>
        <header className='bg-muted/30 flex items-start gap-4 rounded-2xl border p-6 sm:gap-6 sm:p-10'>
          <div className='bg-primary/10 text-primary flex size-12 shrink-0 items-center justify-center rounded-xl sm:size-14'>
            <LayoutGrid className='size-6' aria-hidden='true' />
          </div>
          <div className='min-w-0 space-y-3'>
            <h1 className='text-2xl font-semibold tracking-tight sm:text-3xl'>
              {t('App marketplace')}
            </h1>
            <p className='text-muted-foreground max-w-xl text-sm leading-relaxed sm:text-base'>
              {t(
                'Discover AI apps for your next idea. Choose an app to get started.'
              )}
            </p>
          </div>
        </header>
        {query.isPending && <LoadingState />}
        {query.isError && (
          <ErrorState
            title={t('Failed to load apps')}
            onRetry={() => void query.refetch()}
          />
        )}
        {query.isSuccess && query.data.length === 0 && (
          <EmptyState
            icon={LayoutGrid}
            title={t('No apps available yet')}
            description={t(
              'Published apps will appear here. Check back soon to discover something new.'
            )}
            bordered
            className='bg-muted/10 min-h-80 rounded-2xl sm:min-h-96'
          />
        )}
        {query.isSuccess && (
          <div className='grid gap-5 sm:grid-cols-2 lg:grid-cols-3'>
            {query.data.map((app) => (
              <Card
                key={app.id}
                className='min-w-0 gap-6 pt-6 transition-shadow hover:shadow-md motion-reduce:transition-none'
              >
                <CardHeader className='gap-4 px-6'>
                  <Avatar className='size-14 rounded-xl after:rounded-xl'>
                    <AvatarImage
                      src={app.logo_url}
                      alt=''
                      className='rounded-xl object-contain'
                      loading='lazy'
                      referrerPolicy='no-referrer'
                    />
                    <AvatarFallback className='bg-primary/10 text-primary rounded-xl text-lg font-semibold'>
                      {app.name.slice(0, 1).toUpperCase()}
                    </AvatarFallback>
                  </Avatar>
                  <CardTitle>
                    <h2
                      className='line-clamp-2 text-lg font-semibold [overflow-wrap:anywhere]'
                      title={app.name}
                    >
                      {app.name}
                    </h2>
                  </CardTitle>
                  <CardDescription
                    className='line-clamp-3 min-h-16 leading-relaxed [overflow-wrap:anywhere] whitespace-pre-wrap'
                    title={app.description}
                  >
                    {app.description}
                  </CardDescription>
                </CardHeader>
                <CardFooter className='bg-muted/20 mt-auto border-t px-6 py-4'>
                  <a
                    className={buttonVariants({
                      variant: 'outline',
                      className: 'w-full justify-between',
                    })}
                    href={app.url}
                    target='_blank'
                    rel='noopener noreferrer'
                    aria-label={t('Open {{app}}', { app: app.name })}
                  >
                    {t('Open App')}
                    <ArrowUpRight className='size-4' aria-hidden='true' />
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
