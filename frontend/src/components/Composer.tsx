import { useEffect, useRef, useState } from 'react'
import { ApiError, isDesktop } from '../api/client'
import type { AttachmentView } from '../api/types'
import { attachFromClipboard, pickAttachments, removeAttachment, retryAttachment, uploadAttachments } from '../chat'
import { errorMessage } from '../errors'
import { t } from '../i18n'
import { useStore } from '../store'
import { AttachmentsTray } from './AttachmentsTray'
import { IconAttach } from './icons'

const DRAFT_DELAY = 500

interface Props {
  channelId: string
  channelName: string
  draft: string
  // rootId: a reply composer (thread panel), keyed by (channel, root) on the
  // Go side — absent/'' is the channel's own composer. Its attachments/
  // errors live in the store's thread* fields instead of the channel's.
  rootId?: string
  // disabled: the thread's root was deleted — the panel stays open with a
  // banner, the composer shown but inert (Task 6 brief).
  disabled?: boolean
  serverId: number
  attachments: AttachmentView[]
  onSend(message: string, attachmentIds: string[]): Promise<void>
  onDraft(text: string): void
  onEditLast(): void
}

// Composer must be keyed by (channel, root): its draft belongs to one
// channel's or one thread's composer.
export function Composer({ channelId, channelName, draft, rootId = '', disabled = false, serverId, attachments, onSend, onDraft, onEditLast }: Props) {
  const [text, setText] = useState(draft)
  const [error, setError] = useState<string | null>(null)
  const attachError = useStore((s) => (rootId ? s.threadAttachError : s.attachError))
  const setAttachError = (msg: string | null) => {
    const s = useStore.getState()
    if (rootId) s.setThreadAttachError(msg)
    else s.setAttachError(msg)
  }
  const fileInputRef = useRef<HTMLInputElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const latest = useRef(draft)
  const saved = useRef(draft)
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

  // pendingSendIds: attachment ids handed to onSend but not yet confirmed
  // gone from `attachments` — the confirming attachments_changed event is
  // coalesced up to ~100ms (AGENTS.md), so a second Enter inside that
  // window still sees the just-sent chips in props. Without this, that
  // second call would resend already-taken ids and Go's Take rejects the
  // *whole* call with not_found, bouncing the new text back too.
  const pendingSendIds = useRef<Set<string>>(new Set())
  useEffect(() => {
    const ids = new Set(attachments.map((a) => a.id))
    for (const id of pendingSendIds.current) {
      if (!ids.has(id)) pendingSendIds.current.delete(id) // confirmed gone: sent, removed, or replaced
    }
  }, [attachments])

  const change = (v: string) => {
    setText(v)
    latest.current = v
    clearTimeout(timer.current)
    timer.current = setTimeout(flush, DRAFT_DELAY)
  }

  const send = async () => {
    if (disabled) return
    const msg = text
    const attachmentIds = attachments.filter((a) => !pendingSendIds.current.has(a.id)).map((a) => a.id)
    if (!msg.trim() && attachmentIds.length === 0) return
    setText('')
    latest.current = ''
    flush()
    setError(null)
    setAttachError(null)
    attachmentIds.forEach((id) => pendingSendIds.current.add(id))
    try {
      await onSend(msg, attachmentIds)
    } catch (e) {
      // Chips are never removed from `attachments` here (only tracked as
      // in-flight above), so a failed send needs no visual restore — it
      // only needs to make these ids sendable again on the next Enter.
      attachmentIds.forEach((id) => pendingSendIds.current.delete(id))
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
    setAttachError(errorMessage(e))
  }

  const attachAction = async (run: () => Promise<unknown>) => {
    setAttachError(null)
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
    if (disabled) return
    if (isDesktop()) void attachAction(() => pickAttachments(serverId, channelId, rootId))
    else fileInputRef.current?.click()
  }

  const onFilesSelected = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? [])
    e.target.value = '' // selecting the same file again must still fire onChange
    if (files.length > 0) void attachAction(() => uploadAttachments(serverId, channelId, files, rootId))
  }

  // onPaste: desktop never gets pasted file contents from the page (spike
  // §2.1/§3) — a copied file shows as a hidden text/uri-list (its default
  // action would insert paths as text) and a pasted picture as an empty
  // paste, so Go reads the clipboard itself instead. attachFromClipboard is
  // called synchronously here (Go only honours a paste gesture within 1.5s
  // of the native Ctrl+V). Browser mode gets real File objects.
  const onPaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    if (disabled) return
    const cd = e.clipboardData
    if (!cd) return
    if (isDesktop()) {
      const types = Array.from(cd.types)
      const hiddenFileList = types.includes('text/uri-list') && cd.getData('text/uri-list') === ''
      if (hiddenFileList) {
        e.preventDefault()
        void attachAction(() => attachFromClipboard(serverId, channelId, rootId))
      } else if (!types.includes('text/plain')) {
        // A bare image, or HTML with an image: let any default paste (the
        // HTML) proceed too — the image attaches alongside it.
        void attachAction(() => attachFromClipboard(serverId, channelId, rootId))
      }
      return
    }
    if (cd.files && cd.files.length > 0) {
      e.preventDefault()
      void attachAction(() => uploadAttachments(serverId, channelId, Array.from(cd.files), rootId))
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
          disabled={disabled}
          className="flex shrink-0 items-center justify-center rounded px-2 py-2 text-fg-muted hover:bg-hover hover:text-fg disabled:opacity-50"
        >
          <IconAttach />
        </button>
        <textarea
          ref={textareaRef}
          aria-label={t('composer.label')}
          placeholder={rootId ? t('composer.replyPlaceholder') : t('composer.placeholder', { name: channelName })}
          value={text}
          rows={Math.min(10, text.split('\n').length)}
          onChange={(e) => change(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          disabled={disabled}
          autoFocus
          className="w-full flex-1 resize-none rounded border border-line bg-app px-3 py-2 text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none disabled:opacity-50"
        />
      </div>
      {!isDesktop() && <input ref={fileInputRef} type="file" multiple onChange={onFilesSelected} className="hidden" />}
    </div>
  )
}
