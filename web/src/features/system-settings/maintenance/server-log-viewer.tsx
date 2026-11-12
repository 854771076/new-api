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
import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

type LogFile = {
  name: string
  size: number
  mod_time: string
}

type LogContent = {
  file: string
  line_count: number
  truncated: boolean
  lines: string[]
}

type LogMatch = {
  file: string
  line: number
  text: string
}

type LogSearchResult = {
  keyword: string
  since: string
  files_scanned: number
  matched: number
  truncated: boolean
  lines: LogMatch[]
}

/**
 * Inspects server log files: view the tail of a file or search recent entries
 * by keyword. Complements {@link ServerLogFiles}, which only manages/cleans
 * the files.
 */
export function ServerLogViewer() {
  const { t } = useTranslation()
  const titleId = useId()
  const [files, setFiles] = useState<LogFile[]>([])
  const [selectedFile, setSelectedFile] = useState<string | null>(null)
  const [tab, setTab] = useState('tail')

  const [tailLines, setTailLines] = useState(200)
  const [tailLoading, setTailLoading] = useState(false)
  const [tailContent, setTailContent] = useState<LogContent | null>(null)

  const [keyword, setKeyword] = useState('')
  const [since, setSince] = useState('24h')
  const [limit, setLimit] = useState(200)
  const [searchLoading, setSearchLoading] = useState(false)
  const [searchResult, setSearchResult] = useState<LogSearchResult | null>(null)

  const fetchFiles = useCallback(async () => {
    try {
      const res = await api.get('/api/performance/logs')
      requireServerSuccess(res.data)
      if (res.data.success) {
        const list = (res.data.data.files ?? []) as LogFile[]
        setFiles(list)
        setSelectedFile((current) => current ?? list[0]?.name ?? null)
      }
    } catch (error) {
      handleServerError(error)
    }
  }, [])

  useEffect(() => {
    fetchFiles()
  }, [fetchFiles])

  const loadTail = async () => {
    if (!selectedFile) return
    setTailLoading(true)
    try {
      const res = await api.get('/api/performance/logs/content', {
        params: { file: selectedFile, lines: tailLines },
      })
      requireServerSuccess(res.data)
      if (res.data.success) setTailContent(res.data.data)
    } catch (error) {
      handleServerError(error)
    } finally {
      setTailLoading(false)
    }
  }

  const runSearch = async () => {
    if (!keyword.trim()) return
    setSearchLoading(true)
    try {
      const res = await api.get('/api/performance/logs/search', {
        params: { keyword: keyword.trim(), since, limit },
      })
      requireServerSuccess(res.data)
      if (res.data.success) setSearchResult(res.data.data)
    } catch (error) {
      handleServerError(error)
    } finally {
      setSearchLoading(false)
    }
  }

  return (
    <div
      role='group'
      aria-labelledby={titleId}
      className='flex min-w-0 flex-col gap-4'
    >
      <div>
        <h4 id={titleId} className='font-medium'>
          {t('View Server Logs')}
        </h4>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t(
            'Inspect server log files: view the tail of a file or search recent entries by keyword.'
          )}
        </p>
      </div>

      {files.length === 0 ? (
        <Alert>
          <AlertDescription>{t('No log files available')}</AlertDescription>
        </Alert>
      ) : (
        <Tabs value={tab} onValueChange={(value) => value && setTab(value)}>
          <TabsList>
            <TabsTrigger value='tail'>{t('Tail')}</TabsTrigger>
            <TabsTrigger value='search'>{t('Search')}</TabsTrigger>
          </TabsList>

          <TabsContent value='tail' className='flex flex-col gap-4'>
            <div className='flex flex-wrap items-end gap-3'>
              <div className='grid gap-1.5'>
                <Label className='text-xs'>{t('Log File')}</Label>
                <Select
                  items={files.map((file) => ({
                    value: file.name,
                    label: file.name,
                  }))}
                  value={selectedFile}
                  onValueChange={(value) => value !== null && setSelectedFile(value)}
                >
                  <SelectTrigger className='w-[260px]'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {files.map((file) => (
                        <SelectItem key={file.name} value={file.name}>
                          {file.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
              <div className='grid gap-1.5'>
                <Label className='text-xs'>{t('Lines')}</Label>
                <Input
                  type='number'
                  min={1}
                  max={5000}
                  value={tailLines}
                  onChange={(event) =>
                    setTailLines(Number(event.target.value))
                  }
                  className='w-[120px]'
                />
              </div>
              <Button
                type='button'
                size='sm'
                disabled={tailLoading || !selectedFile}
                onClick={loadTail}
              >
                {tailLoading ? t('Loading...') : t('Load')}
              </Button>
            </div>

            {tailContent !== null && (
              <div className='flex flex-col gap-2'>
                {tailContent.truncated && (
                  <p className='text-muted-foreground text-xs'>
                    {t('Log content truncated')}
                  </p>
                )}
                <pre className='max-h-96 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs whitespace-pre-wrap'>
                  {tailContent.lines.join('\n')}
                </pre>
              </div>
            )}
          </TabsContent>

          <TabsContent value='search' className='flex flex-col gap-4'>
            <div className='flex flex-wrap items-end gap-3'>
              <div className='grid gap-1.5'>
                <Label className='text-xs'>{t('Keyword')}</Label>
                <Input
                  value={keyword}
                  onChange={(event) => setKeyword(event.target.value)}
                  className='w-[200px]'
                />
              </div>
              <div className='grid gap-1.5'>
                <Label className='text-xs'>{t('Since')}</Label>
                <Input
                  value={since}
                  placeholder='24h'
                  onChange={(event) => setSince(event.target.value)}
                  className='w-[120px]'
                />
              </div>
              <div className='grid gap-1.5'>
                <Label className='text-xs'>{t('Limit')}</Label>
                <Input
                  type='number'
                  min={1}
                  max={1000}
                  value={limit}
                  onChange={(event) => setLimit(Number(event.target.value))}
                  className='w-[120px]'
                />
              </div>
              <Button
                type='button'
                size='sm'
                disabled={searchLoading || !keyword.trim()}
                onClick={runSearch}
              >
                {searchLoading ? t('Loading...') : t('Search')}
              </Button>
            </div>

            {searchResult !== null && (
              <div className='flex flex-col gap-2'>
                <p className='text-xs text-muted-foreground'>
                  {t('{{count}} matches', { count: searchResult.matched })}
                </p>
                {searchResult.matched === 0 ? (
                  <p className='text-muted-foreground text-xs'>
                    {t('No matches found')}
                  </p>
                ) : (
                  <div className='max-h-96 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs'>
                    {searchResult.lines.map((match) => (
                      <div
                        key={`${match.file}:${match.line}`}
                        className='whitespace-pre-wrap'
                      >
                        <span className='text-muted-foreground'>
                          {match.file}:{match.line}:{' '}
                        </span>
                        {match.text}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </TabsContent>
        </Tabs>
      )}
    </div>
  )
}
