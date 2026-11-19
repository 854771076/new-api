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
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { QuotaRules } from '@/features/quota-rules'
import {
  getQuotaRules,
  quotaRuleQueryKey,
  type QuotaRule,
} from '@/features/quota-rules/api'
import { QuotaRuleBindingDialog } from '@/features/quota-rules/binding-dialog'
import { QuotaRuleValidity } from '@/features/quota-rules/validity'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { User } from '../../types'

type AppKey = {
  id: number
  user_id: number
  app_id: number
  name: string
  key: string
  status: number
  remain_quota: number
  unlimited_quota: boolean
  quota_rule_id?: number
  quota_rule?: QuotaRule
  rotation_status?: 'pending' | 'committed' | 'cancelled'
}

export function AppProxyKeysDialog(props: {
  user: User
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [name, setName] = useState('')
  const [quota, setQuota] = useState('100000')
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState<AppKey | null>(null)
  const [issued, setIssued] = useState<AppKey | null>(null)
  const [rotation, setRotation] = useState(false)
  const [disable, setDisable] = useState<AppKey | null>(null)
  const [binding, setBinding] = useState<AppKey | null>(null)
  const [rulesOpen, setRulesOpen] = useState(false)
  const [cancelling, setCancelling] = useState<AppKey | null>(null)
  const [ruleId, setRuleId] = useState(0)
  const operation = useRef({ input: '', key: '' })
  const path = `/api/app-proxy/users/${props.user.id}/keys`
  const params = { app_id: props.user.owner_app_id }
  const target = { userId: props.user.id, appId: props.user.owner_app_id ?? 0 }
  const rules = useInfiniteQuery({
    queryKey: [...quotaRuleQueryKey, target, 'creation'],
    enabled: props.open,
    initialPageParam: 1,
    queryFn: ({ pageParam }) =>
      getQuotaRules({ p: pageParam, size: 100 }, target),
    getNextPageParam: (last, pages) =>
      pages.length * 100 < last.total ? pages.length + 1 : undefined,
  })
  const keys = useQuery({
    queryKey: [
      'admin-app-keys',
      props.user.id,
      props.user.owner_app_id,
      page,
      status,
    ],
    enabled: props.open,
    refetchInterval: 30_000,
    queryFn: async () =>
      requireServerSuccess(
        (
          await api.get(path, {
            params: {
              ...params,
              p: page,
              page_size: 10,
              status: status || undefined,
            },
          })
        ).data
      ).data as { items: AppKey[]; total: number },
  })
  async function savePolicy() {
    if (!editing || busy) return
    const remaining = Number(quota)
    if (!name.trim() || !Number.isSafeInteger(remaining) || remaining < 0) {
      return
    }
    setBusy(true)
    try {
      requireServerSuccess(
        (
          await api.put(
            `${path}/${editing.id}`,
            {
              status: editing.status,
              name: name.trim(),
              ...(!editing.unlimited_quota ? { remain_quota: remaining } : {}),
            },
            { params }
          )
        ).data
      )
      setEditing(null)
      await keys.refetch()
    } catch (error) {
      handleServerError(error)
    } finally {
      setBusy(false)
    }
  }
  async function issue(old?: AppKey) {
    if (busy || issued) return
    const remaining = Number(quota)
    if (
      !old &&
      (!name.trim() || !Number.isSafeInteger(remaining) || remaining <= 0)
    ) {
      return
    }
    const body = old
      ? { old_token_id: old.id }
      : {
          name: name.trim(),
          expired_time: -1,
          remain_quota: remaining,
          unlimited_quota: false,
          quota_rule_id: ruleId,
        }
    const input = JSON.stringify(body)
    const storageKey = `app-key-operation:${props.user.id}:${props.user.owner_app_id}:${input}`
    if (operation.current.input !== input) {
      const key = sessionStorage.getItem(storageKey) ?? crypto.randomUUID()
      sessionStorage.setItem(storageKey, key)
      operation.current = { input, key }
    }
    setBusy(true)
    try {
      const result = requireServerSuccess(
        (
          await api.post(path, body, {
            params,
            headers: { 'Idempotency-Key': operation.current.key },
          })
        ).data
      ).data as AppKey
      if (
        result.app_id !== props.user.owner_app_id ||
        result.user_id !== props.user.id
      ) {
        throw new Error('Invalid key ownership')
      }
      if (old) {
        sessionStorage.setItem(
          `app-key-rotation:${props.user.id}:${props.user.owner_app_id}:${result.id}`,
          input
        )
      }
      setIssued(result)
      setRotation(!!old)
      await keys.refetch()
    } catch (error) {
      handleServerError(error)
    } finally {
      setBusy(false)
    }
  }
  async function commit() {
    if (!issued || busy) return
    setBusy(true)
    try {
      requireServerSuccess(
        (
          await api.post(
            `${path}/${issued.id}/commit-rotation`,
            { replacement_key: issued.key },
            { params }
          )
        ).data
      )
      setRotation(false)
      await keys.refetch()
    } catch (error) {
      handleServerError(error)
    } finally {
      setBusy(false)
    }
  }
  async function setKeyStatus(key: AppKey, next: number) {
    setBusy(true)
    try {
      requireServerSuccess(
        (await api.put(`${path}/${key.id}`, { status: next }, { params })).data
      )
      setDisable(null)
      await keys.refetch()
    } catch (error) {
      handleServerError(error)
    } finally {
      setBusy(false)
    }
  }
  async function cancelRotation() {
    if (!cancelling || busy) return
    setBusy(true)
    try {
      requireServerSuccess(
        (
          await api.post(
            `${path}/${cancelling.id}/cancel-rotation`,
            {},
            { params }
          )
        ).data
      )
      const rotationKey = `app-key-rotation:${props.user.id}:${props.user.owner_app_id}:${cancelling.id}`
      const input =
        sessionStorage.getItem(rotationKey) ??
        (issued?.id === cancelling.id ? operation.current.input : '')
      if (input) {
        sessionStorage.removeItem(
          `app-key-operation:${props.user.id}:${props.user.owner_app_id}:${input}`
        )
        if (operation.current.input === input) {
          operation.current = { input: '', key: '' }
        }
      }
      sessionStorage.removeItem(rotationKey)
      if (issued?.id === cancelling.id) {
        setIssued(null)
        setRotation(false)
      }
      setCancelling(null)
      await keys.refetch()
    } catch (error) {
      handleServerError(error)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!busy) {
          if (!open) setIssued(null)
          props.onOpenChange(open)
        }
      }}
      title={t('App AI Keys')}
      description={`${t('User')} #${props.user.id} · ${t('App')} #${props.user.owner_app_id}`}
    >
      <div className='space-y-4'>
        <Button variant='outline' onClick={() => setRulesOpen(true)}>
          {t('Quota management')}
        </Button>
        <p>
          {t('Limited keys cannot rotate without a shared spending budget.')}
        </p>
        <div className='flex flex-wrap gap-2'>
          {[
            ['', 'All'],
            ['1', 'Enabled'],
            ['2', 'Disabled'],
          ].map(([value, label]) => (
            <Button
              key={value}
              variant={status === value ? 'default' : 'outline'}
              onClick={() => {
                setStatus(value)
                setPage(1)
              }}
            >
              {t(label)}
            </Button>
          ))}
        </div>
        {keys.isError ? (
          <ErrorState onRetry={() => void keys.refetch()} />
        ) : (
          <>
            {keys.isPending && <p role='status'>{t('Loading...')}</p>}
            {keys.data?.items.length === 0 && <EmptyState />}
            {keys.data?.items.map((key) => (
              <div key={key.id} className='space-y-2 rounded-md border p-3'>
                <p>
                  {key.name} · #{key.id} ·{' '}
                  {key.status === 1 ? t('Enabled') : t('Disabled')}
                </p>
                <code>{key.key}</code>
                {key.quota_rule && <QuotaRuleValidity rule={key.quota_rule} />}
                {key.rotation_status === 'cancelled' && (
                  <p>{t('Rotation cancelled')}</p>
                )}
                <p>
                  {key.unlimited_quota
                    ? t('Unlimited')
                    : formatNumber(key.remain_quota)}
                </p>
                <div className='flex flex-wrap gap-2'>
                  <Button
                    variant='outline'
                    disabled={busy}
                    onClick={() => setBinding(key)}
                  >
                    {t('Bind quota rule')}
                  </Button>
                  {key.rotation_status === 'pending' && (
                    <Button
                      variant='outline'
                      disabled={busy}
                      onClick={() => setCancelling(key)}
                    >
                      {t('Cancel rotation')}
                    </Button>
                  )}
                  <Button
                    disabled={busy || !!issued}
                    variant='outline'
                    onClick={() => {
                      setEditing(key)
                      setName(key.name)
                      setQuota(String(key.remain_quota))
                    }}
                  >
                    {t('Edit')}
                  </Button>
                  <Button
                    disabled={busy || key.rotation_status === 'cancelled'}
                    variant='outline'
                    onClick={() => {
                      if (key.status === 1) setDisable(key)
                      else void setKeyStatus(key, 1)
                    }}
                  >
                    {key.status === 1 ? t('Disable') : t('Enable')}
                  </Button>
                  <Button
                    disabled={
                      busy ||
                      !!issued ||
                      key.status !== 1 ||
                      !key.unlimited_quota ||
                      !!key.quota_rule_id
                    }
                    variant='outline'
                    onClick={() => void issue(key)}
                  >
                    {t('Stage replacement key')}
                  </Button>
                </div>
              </div>
            ))}
            <div className='flex items-center gap-2'>
              <Button
                disabled={page === 1 || keys.isFetching}
                onClick={() => setPage(page - 1)}
              >
                {t('Previous')}
              </Button>
              <span>{formatNumber(page)}</span>
              <Button
                disabled={
                  keys.isFetching || page * 10 >= (keys.data?.total ?? 0)
                }
                onClick={() => setPage(page + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          </>
        )}
        {issued ? (
          <div className='space-y-3 rounded-md border p-3'>
            <p>
              {t('Save this key in the App backend. Never expose it to users.')}
            </p>
            <Input
              aria-label={t('API Key')}
              value={`sk-${issued.key}`}
              readOnly
            />
            <CopyButton value={`sk-${issued.key}`} />
            {rotation && (
              <Button disabled={busy} onClick={() => void commit()}>
                {t('Saved and verified: switch and revoke old key')}
              </Button>
            )}
            {!rotation && (
              <Button
                onClick={() => {
                  sessionStorage.removeItem(
                    `app-key-operation:${props.user.id}:${props.user.owner_app_id}:${operation.current.input}`
                  )
                  setIssued(null)
                  operation.current = { input: '', key: '' }
                }}
              >
                {t('New Key')}
              </Button>
            )}
          </div>
        ) : (
          <form
            className='space-y-3'
            onSubmit={(event) => {
              event.preventDefault()
              if (editing) void savePolicy()
              else void issue()
            }}
          >
            {!editing && (
              <label className='block'>
                {t('Quota rule')}
                <NativeSelect
                  value={ruleId}
                  onChange={(event) => setRuleId(Number(event.target.value))}
                  disabled={rules.isPending || rules.isError}
                >
                  <NativeSelectOption value={0}>
                    {t('No quota rule')}
                  </NativeSelectOption>
                  {rules.data?.pages
                    .flatMap((page) => page.items)
                    .map((rule) => (
                      <NativeSelectOption key={rule.id} value={rule.id}>
                        {rule.name}
                      </NativeSelectOption>
                    ))}
                </NativeSelect>
                {rules.hasNextPage && (
                  <Button
                    type='button'
                    variant='outline'
                    disabled={rules.isFetchingNextPage}
                    onClick={() => void rules.fetchNextPage()}
                  >
                    {t('Load more')}
                  </Button>
                )}
                {rules.isError && (
                  <ErrorState onRetry={() => void rules.refetch()} />
                )}
              </label>
            )}
            <label className='block'>
              {t('Name')}
              <Input
                required
                maxLength={100}
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            </label>
            <label className='block'>
              {t('Quota')}
              <Input
                type='number'
                min={1}
                max={Number.MAX_SAFE_INTEGER}
                step={1}
                required
                value={quota}
                onChange={(event) => setQuota(event.target.value)}
              />
            </label>
            <Button
              disabled={
                busy || (!editing && (rules.isPending || rules.isError))
              }
              type='submit'
            >
              {t(editing ? 'Save' : 'Create')}
            </Button>
            {editing && (
              <Button
                type='button'
                variant='outline'
                onClick={() => setEditing(null)}
              >
                {t('Cancel')}
              </Button>
            )}
          </form>
        )}
      </div>
      <ConfirmDialog
        open={!!cancelling}
        onOpenChange={(open) => !open && !busy && setCancelling(null)}
        title={t('Cancel rotation')}
        desc={t(
          'The replacement key will be revoked. The original key and its usage remain unchanged.'
        )}
        isLoading={busy}
        handleConfirm={() => void cancelRotation()}
      />
      <Dialog
        open={rulesOpen}
        onOpenChange={setRulesOpen}
        title={t('Quota management')}
      >
        {rulesOpen && (
          <QuotaRules
            key={`${target.userId}:${target.appId}`}
            target={target}
          />
        )}
      </Dialog>
      {binding && (
        <QuotaRuleBindingDialog
          key={`${target.userId}:${binding.id}`}
          apiKey={binding}
          target={target}
          onClose={() => setBinding(null)}
          onSuccess={() => void keys.refetch()}
        />
      )}
      <ConfirmDialog
        open={!!disable}
        onOpenChange={(open) => {
          if (!open) setDisable(null)
        }}
        title={t('Disable API Key')}
        desc={t('New requests with this key will be rejected.')}
        isLoading={busy}
        handleConfirm={() => {
          if (disable) void setKeyStatus(disable, 2)
        }}
      />
    </Dialog>
  )
}
