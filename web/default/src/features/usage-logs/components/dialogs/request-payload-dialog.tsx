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
import { useCallback, useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Download, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import {
  CodeBlock,
  CodeBlockCopyButton,
} from '@/components/ai-elements/code-block'
import { getRequestDetail, getRequestMediaBlob } from '../../api'
import type { RequestDetailData, RequestMediaMeta } from '../../types'

interface RequestPayloadDialogProps {
  requestId: string | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(value >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`
}

// prettyJson tries to format a JSON string with indentation; falls back to the
// raw string if it is not valid JSON (e.g. SSE streams).
function prettyJson(raw: string): { text: string; isJson: boolean } {
  const trimmed = (raw || '').trim()
  if (!trimmed) return { text: '', isJson: false }
  try {
    return { text: JSON.stringify(JSON.parse(trimmed), null, 2), isJson: true }
  } catch {
    return { text: raw, isJson: false }
  }
}

function PayloadView(props: { body: string; truncated: boolean }) {
  const { t } = useTranslation()
  if (!props.body) {
    return (
      <div className='text-muted-foreground py-8 text-center text-sm'>
        {t('No content')}
      </div>
    )
  }
  const { text, isJson } = prettyJson(props.body)
  return (
    <div className='space-y-2'>
      {props.truncated && (
        <StatusBadge
          label={t('Truncated')}
          variant='orange'
          size='sm'
          copyable={false}
        />
      )}
      {isJson ? (
        <CodeBlock
          code={text}
          language='json'
          className='max-w-full [&_code]:break-words [&_code]:whitespace-pre-wrap [&_pre]:break-words [&_pre]:whitespace-pre-wrap'
        >
          <CodeBlockCopyButton
            onCopy={() => toast.success(t('Copied to clipboard'))}
          />
        </CodeBlock>
      ) : (
        <pre className='bg-muted/30 max-w-full overflow-x-auto rounded-md border p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap'>
          {text}
        </pre>
      )}
    </div>
  )
}

function MediaItem(props: { media: RequestMediaMeta; requestId: string }) {
  const { t } = useTranslation()
  const isImage = props.media.mime_type.startsWith('image/')
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    return () => {
      if (previewUrl) URL.revokeObjectURL(previewUrl)
    }
  }, [previewUrl])

  const load = useCallback(
    async (forDownload: boolean) => {
      setLoading(true)
      try {
        const blob = await getRequestMediaBlob(
          props.requestId,
          props.media.media_id
        )
        const url = URL.createObjectURL(blob)
        if (forDownload) {
          const a = document.createElement('a')
          a.href = url
          a.download = `${props.media.media_id}`
          document.body.appendChild(a)
          a.click()
          a.remove()
          URL.revokeObjectURL(url)
        } else {
          setPreviewUrl(url)
        }
      } catch {
        toast.error(t('Failed to load media'))
      } finally {
        setLoading(false)
      }
    },
    [props.media.media_id, props.requestId, t]
  )

  const toggleLabel = previewUrl ? t('Hide') : t('Preview')

  return (
    <div className='bg-muted/30 flex flex-col gap-2 rounded-md border p-2.5'>
      <div className='flex flex-wrap items-center gap-2 text-xs'>
        <StatusBadge
          label={props.media.role || 'media'}
          variant='neutral'
          size='sm'
          copyable={false}
        />
        <span className='font-mono'>{props.media.mime_type}</span>
        <span className='text-muted-foreground'>
          {formatBytes(props.media.size)}
        </span>
        <span className='text-muted-foreground font-mono'>
          {props.media.media_id.slice(0, 12)}
        </span>
        <div className='ml-auto flex items-center gap-1.5'>
          {isImage && (
            <Button
              variant='outline'
              size='sm'
              className='h-6 px-2 text-xs'
              disabled={loading}
              onClick={() => {
                if (previewUrl) {
                  setPreviewUrl(null)
                } else {
                  load(false)
                }
              }}
            >
              {loading ? (
                <Loader2 className='size-3 animate-spin' />
              ) : (
                toggleLabel
              )}
            </Button>
          )}
          <Button
            variant='outline'
            size='sm'
            className='h-6 px-2 text-xs'
            disabled={loading}
            onClick={() => load(true)}
          >
            <Download className='size-3' />
          </Button>
        </div>
      </div>
      {previewUrl && (
        <img
          src={previewUrl}
          alt={props.media.media_id}
          className='max-h-64 max-w-full rounded border object-contain'
        />
      )}
    </div>
  )
}

function resolveErrorMessage(
  isError: boolean,
  data: { success: boolean; message?: string } | undefined,
  t: (key: string) => string
): string | null {
  if (isError) return t('Failed to load request detail')
  if (data && !data.success) {
    return data.message || t('Request detail not found')
  }
  return null
}

export function RequestPayloadDialog(props: RequestPayloadDialogProps) {
  const { t } = useTranslation()

  const { data, isLoading, isError } = useQuery({
    queryKey: ['request-detail', props.requestId],
    queryFn: () => getRequestDetail(props.requestId as string),
    enabled: props.open && !!props.requestId,
  })

  const detail: RequestDetailData | null = data?.success
    ? (data.data ?? null)
    : null
  const errorMessage = resolveErrorMessage(isError, data, t)

  const renderContent = () => {
    if (isLoading) {
      return (
        <div className='flex items-center justify-center py-12'>
          <Loader2 className='text-muted-foreground size-6 animate-spin' />
        </div>
      )
    }
    if (errorMessage) {
      return (
        <div className='text-muted-foreground py-12 text-center text-sm'>
          {errorMessage}
        </div>
      )
    }
    if (!detail) {
      return (
        <div className='text-muted-foreground py-12 text-center text-sm'>
          {t('No content')}
        </div>
      )
    }

    const reqMedia = (detail.media || []).filter((m) => m.role === 'request')
    const respMedia = (detail.media || []).filter((m) => m.role === 'response')

    return (
      <Tabs defaultValue='request' className='min-w-0'>
        <TabsList className='sticky top-0 z-10'>
          <TabsTrigger value='request'>{t('Request')}</TabsTrigger>
          <TabsTrigger value='response'>{t('Response')}</TabsTrigger>
        </TabsList>
        <div className='min-w-0'>
          <TabsContent value='request' className='space-y-3'>
            <PayloadView
              body={detail.request_body}
              truncated={detail.request_truncated}
            />
            {reqMedia.length > 0 && (
              <div className='space-y-1.5'>
                <Label className='text-xs font-semibold'>
                  {t('Media')} ({reqMedia.length})
                </Label>
                {reqMedia.map((m) => (
                  <MediaItem
                    key={m.id}
                    media={m}
                    requestId={detail.request_id}
                  />
                ))}
              </div>
            )}
          </TabsContent>
          <TabsContent value='response' className='space-y-3'>
            <PayloadView
              body={detail.response_body}
              truncated={detail.response_truncated}
            />
            {respMedia.length > 0 && (
              <div className='space-y-1.5'>
                <Label className='text-xs font-semibold'>
                  {t('Media')} ({respMedia.length})
                </Label>
                {respMedia.map((m) => (
                  <MediaItem
                    key={m.id}
                    media={m}
                    requestId={detail.request_id}
                  />
                ))}
              </div>
            )}
          </TabsContent>
        </div>
      </Tabs>
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Full Request Log')}
      description={t(
        'View the complete request and response payload for this call'
      )}
      contentClassName={cn(
        'min-w-0 overflow-hidden',
        'max-sm:max-h-[calc(100dvh-1.5rem)] max-sm:w-[calc(100vw-1.5rem)] max-sm:max-w-[calc(100vw-1.5rem)] max-sm:p-4',
        'sm:max-w-3xl lg:max-w-4xl'
      )}
      titleClassName='text-base'
      descriptionClassName='sr-only'
      contentHeight='min(80vh, 820px)'
      bodyClassName='space-y-3'
    >
      {renderContent()}
    </Dialog>
  )
}
