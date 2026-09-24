import { useEffect, useRef, useState } from 'react'
import type { ChannelDTO } from '../api/types'
import { errorMessage } from '../errors'
import { t } from '../i18n'

const DRAFT_DELAY = 500

interface Props {
  channel: ChannelDTO
  onSend(message: string): Promise<void>
  onDraft(text: string): void
  onEditLast(): void
}

// Composer must be keyed by channel id: its draft belongs to one channel.
export function Composer({ channel, onSend, onDraft, onEditLast }: Props) {
  const [text, setText] = useState(channel.draft)
  const [error, setError] = useState<string | null>(null)
  const latest = useRef(channel.draft)
  const saved = useRef(channel.draft)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const flush = () => {
    clearTimeout(timer.current)
    timer.current = undefined
    if (latest.current !== saved.current) {
      saved.current = latest.current
      onDraft(latest.current)
    }
  }
  const flushRef = useRef(flush)
  flushRef.current = flush
  useEffect(() => () => flushRef.current(), []) // leaving the channel saves at once

  const change = (v: string) => {
    setText(v)
    latest.current = v
    clearTimeout(timer.current)
    timer.current = setTimeout(flush, DRAFT_DELAY)
  }

  const send = async () => {
    const msg = text
    if (!msg.trim()) return
    setText('')
    latest.current = ''
    flush()
    setError(null)
    try {
      await onSend(msg)
    } catch (e) {
      setText(msg)
      latest.current = msg
      setError(errorMessage(e))
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      void send()
    } else if (e.key === 'ArrowUp' && text === '') {
      e.preventDefault()
      onEditLast()
    }
  }

  return (
    <div className="border-t border-neutral-200 px-4 py-3">
      {error && (
        <p role="alert" className="pb-1 text-xs text-red-600">
          {error}
        </p>
      )}
      <textarea
        aria-label={t('composer.label')}
        placeholder={t('composer.placeholder', { name: channel.name })}
        value={text}
        rows={Math.min(10, text.split('\n').length)}
        onChange={(e) => change(e.target.value)}
        onKeyDown={onKeyDown}
        autoFocus
        className="w-full resize-none rounded border border-neutral-300 px-3 py-2 focus:border-blue-500 focus:outline-none"
      />
    </div>
  )
}
