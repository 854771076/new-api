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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useSearch } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PermissionMatrix } from '@/components/permission-matrix'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { permissionMatrixToScopes } from '@/lib/admin-permissions'
import { handleServerError } from '@/lib/handle-server-error'

import { getAccessTokenCatalog } from '../security/api'
import { accessTokenPermissionGroups } from '../security/lib/access-token-catalog'
import { issueCLIAuthCode, type CLIAuthCode } from './api'
import {
  cliAuthorizeFormSchema,
  defaultPermissions,
  summarizeGrant,
  type CLIAuthorizeFormValues,
} from './lib/form'

export function CLIAuthorizePage() {
  const { t } = useTranslation()
  const [issued, setIssued] = useState<CLIAuthCode | null>(null)
  const catalog = useQuery({
    queryKey: ['cli-auth', 'catalog'],
    queryFn: getAccessTokenCatalog,
    retry: false,
  })
  // The CLI passes its --name flag through the authorize URL.
  const search = useSearch({ from: '/_authenticated/cli/authorize' })
  const defaultName = search.name?.trim() || 'newapi-cli'
  const form = useForm<CLIAuthorizeFormValues>({
    resolver: zodResolver(cliAuthorizeFormSchema),
    defaultValues: { name: defaultName, permissions: {} },
  })
  const permissions = form.watch('permissions')
  const verification = useSecureVerification()
  const requestVerification = verification.requestVerification
  const operation = useRef<AbortController | null>(null)

  useEffect(
    () => () => {
      operation.current?.abort()
      operation.current = null
    },
    []
  )

  // Preselect the recommended grant once the catalog arrives, without
  // overwriting choices the user has already made.
  useEffect(() => {
    if (!catalog.data) return
    if (permissionMatrixToScopes(form.getValues('permissions')).length > 0) {
      return
    }
    form.setValue('permissions', defaultPermissions(catalog.data), {
      shouldDirty: false,
    })
  }, [catalog.data, form])

  const issue = useMutation({
    mutationFn: async (values: CLIAuthorizeFormValues) => {
      const input = {
        name: values.name.trim(),
        scopes: permissionMatrixToScopes(values.permissions),
      }
      const controller = new AbortController()
      operation.current = controller
      try {
        // A proof is single use and bound to these exact scopes, so every
        // attempt starts a new verification. Cancelling it aborts the issue.
        const proof = await requestVerification({
          scope: 'access_token.generate',
          context: { scopes: input.scopes, expires_at: 0 },
        })
        if (!proof) return null
        return await issueCLIAuthCode(
          input,
          proof.proof_token,
          controller.signal
        )
      } finally {
        if (operation.current === controller) operation.current = null
      }
    },
    onSuccess: (code) => {
      if (code) setIssued(code)
    },
    onError: (error) => handleServerError(error),
  })

  const submit = (values: CLIAuthorizeFormValues) => {
    issue.mutate(values)
  }

  const startOver = () => {
    setIssued(null)
    form.reset({ name: defaultName, permissions: {} })
  }

  if (catalog.isLoading) return <LoadingState />
  if (catalog.isError || !catalog.data) {
    return <ErrorState title={t('Failed to load access tokens')} />
  }
  const groups = accessTokenPermissionGroups(catalog.data)
  const summary = summarizeGrant(
    catalog.data,
    permissionMatrixToScopes(permissions)
  )

  return (
    <div className='mx-auto w-full max-w-2xl p-4 sm:p-6'>
      <h1 className='mb-2 text-xl font-bold'>{t('Authorize CLI')}</h1>
      <p className='text-muted-foreground mb-6 text-sm'>
        {t(
          'The CLI will receive a personal access token with the permissions you select below.'
        )}
      </p>
      {issued ? (
        <div className='space-y-4'>
          <div className='space-y-2'>
            <span className='text-sm font-medium'>
              {t('Authorization code')}
            </span>
            <div className='flex items-center gap-2'>
              <code className='bg-muted flex-1 rounded-md px-3 py-2 font-mono text-sm break-all'>
                {issued.code}
              </code>
              <CopyButton value={issued.code} variant='outline' />
            </div>
            <p className='text-warning text-sm'>
              {t(
                'Paste this code into the CLI prompt to finish signing in. It is shown only once, expires in 10 minutes, and can be used only once.'
              )}
            </p>
          </div>
          <Button variant='outline' onClick={startOver}>
            {t('Create another code')}
          </Button>
        </div>
      ) : (
        <Form {...form}>
          <form onSubmit={form.handleSubmit(submit)} className='space-y-5'>
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Token name')}</FormLabel>
                  <FormControl>
                    <Input {...field} autoComplete='off' />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='permissions'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Permissions')}</FormLabel>
                  <PermissionMatrix
                    groups={groups}
                    value={field.value}
                    disabled={issue.isPending}
                    onChange={field.onChange}
                  />
                  <FormMessage />
                </FormItem>
              )}
            />
            <div className='bg-muted/40 space-y-2 rounded-md border p-3 text-sm'>
              <p className='font-medium'>{t('Selected permissions')}</p>
              {summary.items.length === 0 ? (
                <p className='text-muted-foreground'>
                  {t('No permissions selected')}
                </p>
              ) : (
                <div className='flex flex-wrap gap-1.5'>
                  {summary.items.map((item) => (
                    <Badge key={item.scope} variant='secondary'>
                      {t(item.resourceLabelKey)} / {t(item.actionLabelKey)}
                    </Badge>
                  ))}
                </div>
              )}
              {summary.highRiskCount > 0 && (
                <p className='text-warning'>
                  {t('This grant includes {{count}} high-risk permissions.', {
                    count: summary.highRiskCount,
                  })}
                </p>
              )}
            </div>
            <Button type='submit' disabled={issue.isPending}>
              {issue.isPending
                ? t('Generating...')
                : t('Generate authorization code')}
            </Button>
          </form>
        </Form>
      )}
      <SecureVerificationDialog {...verification.dialogProps} />
    </div>
  )
}
