import { useEffect, useRef, useState } from 'react'
import { ApiError, isDesktop } from '../api/client'
import type { AttachmentView, ChannelDTO } from '../api/types'
import { attachFromClipboard, pickAttachments, removeAttachment, retryAttachment, uploadAttachments } from '../chat'
import { errorMessage } from '../errors'
import { t } from '../i18n'
import { useStore } from '../store'
import { AttachmentsTray } from './AttachmentsTray'

const DRAFT_DELAY = 500

interface Props {
  channel: ChannelDTO
  serverId: number
  attachments: AttachmentView[]
  onSend(message: string, attachmentIds: string[]): Promise<void>
  onDraft(text: string): void
  onEditLast(): void
}

// Composer must be keyed by channel id: its draft belongs to one channel.
export function Composer({ channel, serverId, attachments, onSend, onDraft, onEditLast }: Props) {
  const [text, setText] = useState(channel.draft)
  const [error, setError] = useState<string | null>(null)
  const attachError = useStore((s) => s.attachError)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
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
    const attachmentIds = attachments.map((a) => a.id)
    if (!msg.trim() && attachmentIds.length === 0) return
    setText('')
    latest.current = ''
    flush()
    setError(null)
    useStore.getState().setAttachError(null)
    try {
      await onSend(msg, attachmentIds)
    } catch (e) {
      setText(msg)
      latest.current = msg
      // The pre-send flush already persisted '' as the draft; without this,
      // the restored text would only reach the server on the next edit (500ms
      // debounce) or on leaving the channel — losing it to a crash/reload in
      // between even though it's still visible on screen.
      saved.current = msg
      onDraft(msg)
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

  // showAttachError: a limit or a refused paste/pick/drop/upload, shown
  // like a send error — but quietly for no_paste_gesture (spec: paste from
  // a context menu, or Ctrl+V not seen natively yet, is not an error the
  // user did anything wrong to cause).
  const showAttachError = (e: unknown) => {
    if (e instanceof ApiError && e.code === 'no_paste_gesture') return
    useStore.getState().setAttachError(errorMessage(e))
  }

  const attachAction = async (run: () => Promise<unknown>) => {
    useStore.getState().setAttachError(null)
    try {
      await run()
    } catch (e) {
      showAttachError(e)
    }
  }

  // onAttachClick: desktop asks Go to read the clipboard/open the dialog
  // (the UI never sees a path — AGENTS.md "Вложения"); browser mode has no
  // such source, so 📎 opens a plain file input instead.
  const onAttachClick = () => {
    if (isDesktop()) void attachAction(() => pickAttachments(serverId, channel.id))
    else fileInputRef.current?.click()
  }

  const onFilesSelected = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? [])
    e.target.value = '' // selecting the same file again must still fire onChange
    if (files.length > 0) void attachAction(() => uploadAttachments(serverId, channel.id, files))
  }

  // onPaste: desktop never gets pasted file contents from the page (spike
  // §2.1/§3) — a copied file shows as a hidden text/uri-list (its default
  // action would insert paths as text) and a pasted picture as an empty
  // paste, so Go reads the clipboard itself instead. attachFromClipboard is
  // called synchronously here (Go only honours a paste gesture within 1.5s
  // of the native Ctrl+V). Browser mode gets real File objects.
  const onPaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const cd = e.clipboardData
    if (!cd) return
    if (isDesktop()) {
      const types = Array.from(cd.types)
      const hiddenFileList = types.includes('text/uri-list') && cd.getData('text/uri-list') === ''
      if (hiddenFileList) {
        e.preventDefault()
        void attachAction(() => attachFromClipboard(serverId, channel.id))
      } else if (!types.includes('text/plain')) {
        // A bare image, or HTML with an image: let any default paste (the
        // HTML) proceed too — the image attaches alongside it.
        void attachAction(() => attachFromClipboard(serverId, channel.id))
      }
      return
    }
    if (cd.files && cd.files.length > 0) {
      e.preventDefault()
      void attachAction(() => uploadAttachments(serverId, channel.id, Array.from(cd.files)))
    }
  }

  return (
    <div className="border-t border-line bg-panel px-4 py-3">
      {error && (
        <p role="alert" className="pb-1 text-xs text-danger">
          {error}
        </p>
      )}
      {attachError && (
        <p role="alert" className="pb-1 text-xs text-danger">
          {attachError}
        </p>
      )}
      <AttachmentsTray
        serverId={serverId}
        items={attachments}
        onRemove={(id) => removeAttachment(serverId, id)}
        onRetry={(id) => retryAttachment(serverId, id)}
        onFocusTextarea={() => textareaRef.current?.focus()}
      />
      <div className="flex items-end gap-2">
        <button
          type="button"
          aria-label={t('composer.attach')}
          title={t('composer.attach')}
          onClick={onAttachClick}
          className="shrink-0 rounded px-2 py-2 text-fg-muted hover:bg-hover hover:text-fg"
        >
          📎
        </button>
        <textarea
          ref={textareaRef}
          aria-label={t('composer.label')}
          placeholder={t('composer.placeholder', { name: channel.name })}
          value={text}
          rows={Math.min(10, text.split('\n').length)}
          onChange={(e) => change(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          autoFocus
          className="w-full flex-1 resize-none rounded border border-line bg-app px-3 py-2 text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
        />
      </div>
      {!isDesktop() && <input ref={fileInputRef} type="file" multiple onChange={onFilesSelected} className="hidden" />}
    </div>
  )
}
