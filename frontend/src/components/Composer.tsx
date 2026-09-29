import { lazy, Suspense, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ApiError, client, isDesktop } from '../api/client'
import type { AttachmentView, EmojiDTO } from '../api/types'
import { attachFromClipboard, pickAttachments, removeAttachment, retryAttachment, uploadAttachments } from '../chat'
import type { MarkdownMode } from '../composerFormatting'
import { applyMarkdown, insertAtCaret, replaceTextareaValue } from '../composerFormatting'
import { fitCount, TOOLBAR_BUTTON_SIZE, TOOLBAR_GAP } from '../composerToolbarFit'
import { errorMessage } from '../errors'
import { t } from '../i18n'
import { isShortcut } from '../keyboard'
import { useStore } from '../store'
import { AttachmentsTray } from './AttachmentsTray'
import { IconBold, IconCode, IconHeading, IconItalic, IconListBulleted, IconListNumbered, IconMood, IconQuote, IconSend, IconStrikethrough } from './composerIcons'
import { FormattingMenu } from './FormattingMenu'
import { IconAttach, IconLink, IconMore } from './icons'

const EmojiPicker = lazy(() => import('./EmojiPicker'))

const DRAFT_DELAY = 500

// PANE_HEIGHT_RATIO: the textarea auto-grows up to this share of the pane
// (its own parent's) height, then scrolls internally — brief's "~40% of
// the pane height". MIN_MAX_HEIGHT keeps that floor sane before the
// ResizeObserver has measured anything (or in a very short pane).
const PANE_HEIGHT_RATIO = 0.4
const MIN_MAX_HEIGHT = 88

// formattingBarFetchStarted: a module-level guard, not per-instance state —
// the channel composer and the thread composer can be mounted at the same
// time and would otherwise both fire GetFormattingBarHidden on their first
// render. The Aa toggle is app-wide (one ui_prefs row), so one fetch is enough.
let formattingBarFetchStarted = false

// FORMAT_BUTTONS: left group of the toolbar, brief's exact order and
// grouping ("B, I, S, H | link, code, quote | bulleted, numbered").
// groupBreak marks a button that starts a new group (a separator goes
// before it).
const FORMAT_BUTTONS: { mode: MarkdownMode; label: Parameters<typeof t>[0]; Icon: typeof IconBold; groupBreak?: boolean }[] = [
  { mode: 'bold', label: 'composer.bold', Icon: IconBold },
  { mode: 'italic', label: 'composer.italic', Icon: IconItalic },
  { mode: 'strike', label: 'composer.strike', Icon: IconStrikethrough },
  { mode: 'heading', label: 'composer.heading', Icon: IconHeading },
  { mode: 'link', label: 'composer.linkFormat', Icon: IconLink, groupBreak: true },
  { mode: 'code', label: 'composer.code', Icon: IconCode },
  { mode: 'quote', label: 'composer.quote', Icon: IconQuote },
  { mode: 'ul', label: 'composer.bulletList', Icon: IconListBulleted, groupBreak: true },
  { mode: 'ol', label: 'composer.numberedList', Icon: IconListNumbered },
]

// GROUP_BREAK_INDICES: FORMAT_BUTTONS' own separator positions, precomputed
// once — fed straight to composerToolbarFit's fitCount/widthForCount.
const GROUP_BREAK_INDICES = FORMAT_BUTTONS.reduce<number[]>((acc, b, i) => {
  if (b.groupBreak) acc.push(i)
  return acc
}, [])

// RIGHT_GROUP_WIDTH: the toolbar's right-hand group (Aa, attach, emoji,
// send) is always exactly these 4 fixed-size buttons — a constant is exact
// and needs no extra ResizeObserver/ref of its own. 4 buttons have only 3
// gaps *between* them (review fix round 1, Minor: counting a 4th, trailing
// one here as well as the row's own px-1.5 padding — 6px each side — plus
// the one gap *before* this group double-counted about 2px). TOOLBAR_RESERVED
// is exactly those three things: the right group itself, the row's padding,
// and that one gap before it.
const RIGHT_GROUP_BUTTON_COUNT = 4
const RIGHT_GROUP_WIDTH = RIGHT_GROUP_BUTTON_COUNT * TOOLBAR_BUTTON_SIZE + (RIGHT_GROUP_BUTTON_COUNT - 1) * TOOLBAR_GAP
const TOOLBAR_ROW_PADDING = 12 // px-1.5, both sides
const TOOLBAR_RESERVED = RIGHT_GROUP_WIDTH + TOOLBAR_ROW_PADDING + TOOLBAR_GAP

function ToolbarButton({
  label, onClick, children, pressed, disabled, haspopup, expanded, className = 'text-fg-muted',
}: {
  label: string
  onClick(e: React.MouseEvent<HTMLButtonElement>): void
  children: React.ReactNode
  pressed?: boolean
  disabled?: boolean
  haspopup?: 'menu'
  expanded?: boolean
  className?: string
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={pressed}
      aria-haspopup={haspopup}
      aria-expanded={haspopup ? expanded : undefined}
      onClick={onClick}
      disabled={disabled}
      className={`flex h-7 w-7 shrink-0 items-center justify-center rounded hover:bg-hover hover:text-fg disabled:opacity-50 disabled:hover:bg-transparent ${pressed ? 'bg-hover text-fg' : className}`}
    >
      {children}
    </button>
  )
}

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
  // emojiInfo: optional so the many pre-existing Composer tests that don't
  // exercise the emoji picker need no changes — ChannelPane/ThreadPane
  // always pass the real one.
  emojiInfo?(): Promise<EmojiDTO>
  onSend(message: string, attachmentIds: string[]): Promise<void>
  onDraft(text: string): void
  onEditLast(): void
}

const emptyEmojiInfo = (): Promise<EmojiDTO> => Promise.resolve({ recent: [], custom: [], custom_enabled: false })

// Composer must be keyed by (channel, root): its draft belongs to one
// channel's or one thread's composer.
export function Composer({
  channelId, channelName, draft, rootId = '', disabled = false, serverId, attachments,
  emojiInfo = emptyEmojiInfo, onSend, onDraft, onEditLast,
}: Props) {
  const [text, setText] = useState(draft)
  const [error, setError] = useState<string | null>(null)
  const attachError = useStore((s) => (rootId ? s.threadAttachError : s.attachError))
  const setAttachError = (msg: string | null) => {
    const s = useStore.getState()
    if (rootId) s.setThreadAttachError(msg)
    else s.setAttachError(msg)
  }
  const formattingBarHidden = useStore((s) => s.formattingBarHidden)
  const formattingBarLoaded = useStore((s) => s.formattingBarLoaded)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const boxRef = useRef<HTMLDivElement>(null)
  const latest = useRef(draft)
  const saved = useRef(draft)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  // The Aa toggle is app-wide (one ui_prefs row) — fetched once per app
  // session, not once per composer instance (see formattingBarFetchStarted).
  useEffect(() => {
    if (formattingBarLoaded || formattingBarFetchStarted) return
    formattingBarFetchStarted = true
    client.getFormattingBarHidden().then(
      (hidden) => useStore.getState().setFormattingBarHidden(hidden),
      () => useStore.getState().setFormattingBarHidden(false),
    )
  }, [formattingBarLoaded])

  const toggleFormattingBar = () => {
    const next = !formattingBarHidden
    useStore.getState().setFormattingBarHidden(next)
    void client.setFormattingBarHidden(next).catch(() => {})
  }

  // Responsive formatting buttons (coordinator ruling 2026-09-29, composer
  // brief follow-up): the thread panel can be as narrow as 320px, too
  // narrow for all 9 buttons plus the always-visible right group — they
  // collapse into a "more formatting" popover (FormattingMenu) instead of
  // a horizontal scrollbar. visibleCount is an integer (0..9), not the raw
  // pixel width, and the setter only fires when it actually changes — a
  // continuous drag-resize does not churn a React render for every pixel,
  // only for the (much rarer) frame where a button's fit flips.
  // Fix round 1, item (c): the callback below used to run its measurement
  // (and any resulting setState) synchronously inside the ResizeObserver
  // notification itself. That could still change layout (the toolbar row's
  // own content) within the same delivery cycle the virtualizer's scroller
  // observer was *also* being notified in — Chromium occasionally reported
  // "ResizeObserver loop completed with undelivered notifications" while
  // typing in the composer (bisected: 0/16 with this observer's callback
  // disabled). Deferring the actual measurement to the next animation
  // frame moves it out of that notification cycle entirely; coalescing
  // repeated notifications into a single pending rAF (rather than one per
  // callback) is what actually avoids the loop, not just the existing
  // "only setState when the count changes" guard on its own. It was not the
  // source the e2e kept catching, though: that was the virtualizer's rect
  // observer (Feed.tsx, observeElementRect; round 1, item b follow-up).
  const toolbarRef = useRef<HTMLDivElement>(null)
  const [visibleCount, setVisibleCount] = useState(FORMAT_BUTTONS.length)
  useEffect(() => {
    const row = toolbarRef.current
    if (!row || typeof ResizeObserver === 'undefined') return
    let raf = 0
    const measure = () => {
      raf = 0
      const available = row.offsetWidth - TOOLBAR_RESERVED
      const next = fitCount(available, FORMAT_BUTTONS.length, GROUP_BREAK_INDICES)
      setVisibleCount((cur) => (cur === next ? cur : next))
    }
    const schedule = () => {
      if (raf) return
      raf = requestAnimationFrame(measure)
    }
    schedule()
    const ro = new ResizeObserver(schedule)
    ro.observe(row)
    return () => {
      ro.disconnect()
      cancelAnimationFrame(raf)
    }
  }, [])
  const shownButtons = FORMAT_BUTTONS.slice(0, visibleCount)
  const hiddenButtons = FORMAT_BUTTONS.slice(visibleCount)
  const [moreAnchor, setMoreAnchor] = useState<HTMLButtonElement | null>(null)

  // maxTextareaHeight: ~40% of the pane's height — ChannelPane's/
  // ThreadPane's own flex column, whose height does not itself change as
  // the composer grows. boxRef is the bordered box; its parentElement is
  // *this component's own root* (the `border-t … px-3 py-2` wrapper below),
  // which does grow with the composer — that was the bug (fix round 1,
  // item a): the cap chased its own box and topped out at ~3-4 lines
  // instead of ~40% of the real pane. The pane is one level further up:
  // boxRef -> composer root -> pane. typeof ResizeObserver check: jsdom has
  // none (Feed.tsx uses the same guard) — tests fall back to no cap, which
  // is harmless (no real layout to measure there anyway).
  const [maxTextareaHeight, setMaxTextareaHeight] = useState<number | undefined>(undefined)
  useEffect(() => {
    const pane = boxRef.current?.parentElement?.parentElement
    if (!pane || typeof ResizeObserver === 'undefined') return
    const update = () => setMaxTextareaHeight(Math.max(MIN_MAX_HEIGHT, Math.round(pane.clientHeight * PANE_HEIGHT_RATIO)))
    update()
    const ro = new ResizeObserver(update)
    ro.observe(pane)
    return () => ro.disconnect()
  }, [])

  // Auto-grow: measure the real content height and apply it up to the cap,
  // then let overflow-y-auto scroll. jsdom never computes layout
  // (scrollHeight is always 0 there, same gotcha as Feed.test.tsx) — left
  // at its CSS default height in tests, which is fine since nothing there
  // asserts on pixel height. The measuring layout (height 'auto' collapses
  // the textarea to one row) runs with the composer box's height held: had
  // the box shrunk for it, the feed above would have grown for that layout
  // and the browser would have clamped its scrollTop — a feed at its bottom
  // was pulled up by every new line, past the bottom-stick's reach
  // (Feed.tsx; stick/download fix round 1).
  useLayoutEffect(() => {
    const el = textareaRef.current
    const box = el?.parentElement
    if (!el || !box || el.scrollHeight === 0) return
    box.style.minHeight = `${box.offsetHeight}px`
    el.style.height = 'auto'
    const next = maxTextareaHeight ? Math.min(el.scrollHeight, maxTextareaHeight) : el.scrollHeight
    el.style.height = `${next}px`
    box.style.minHeight = ''
  }, [text, maxTextareaHeight])

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

  const canSend = text.trim().length > 0 || attachments.length > 0

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

  // runFormat: applies a formatting button/shortcut to the current
  // selection through the real textarea (see composerFormatting.ts) — its
  // 'input' event drives `change` the same way typing does, so the draft
  // debounce and React state stay in sync without a separate setText call.
  const runFormat = (mode: MarkdownMode) => {
    if (disabled) return
    const el = textareaRef.current
    if (!el) return
    const start = el.selectionStart ?? el.value.length
    const end = el.selectionEnd ?? el.value.length
    const result = applyMarkdown(mode, el.value, start, end)
    replaceTextareaValue(el, result.message, result.selectionStart, result.selectionEnd)
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      void send()
      return
    }
    if (e.key === 'ArrowUp' && text === '') {
      e.preventDefault()
      onEditLast()
      return
    }
    if (disabled) return
    // Shortcuts match the webapp (physical key, so they work on a Russian
    // layout too — see keyboard.ts): Ctrl+B bold, Ctrl+I italic,
    // Ctrl+Alt+K link.
    if (isShortcut(e.nativeEvent, 'KeyB', { ctrl: true }) && !e.altKey) {
      e.preventDefault()
      runFormat('bold')
    } else if (isShortcut(e.nativeEvent, 'KeyI', { ctrl: true }) && !e.altKey) {
      e.preventDefault()
      runFormat('italic')
    } else if (isShortcut(e.nativeEvent, 'KeyK', { ctrl: true }) && e.altKey) {
      e.preventDefault()
      runFormat('link')
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

  // Emoji picker: anchored to the 🙂 button, reusing the same component and
  // recent/quick-emoji data as the post toolbar (PostItem.tsx) — see
  // EmojiPicker.tsx. Every close path (pick, Escape, outside click) returns
  // focus to the textarea (brief: "Esc returns focus to the textarea").
  const [picker, setPicker] = useState<{ anchor: DOMRect; info: EmojiDTO | null } | null>(null)
  const openPicker = (e: React.MouseEvent<HTMLButtonElement>) => {
    if (disabled) return
    setPicker({ anchor: e.currentTarget.getBoundingClientRect(), info: null })
    emojiInfo().then(
      (info) => setPicker((p) => (p ? { ...p, info } : p)),
      () => {},
    )
  }
  const closePicker = () => {
    setPicker(null)
    textareaRef.current?.focus()
  }
  const pickEmoji = (name: string) => {
    closePicker()
    if (textareaRef.current) insertAtCaret(textareaRef.current, `:${name}: `)
  }

  return (
    <div className="border-t border-line bg-panel px-3 py-2">
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
      <div ref={boxRef} className="flex flex-col rounded-lg border border-line bg-app focus-within:border-accent">
        <textarea
          ref={textareaRef}
          aria-label={t('composer.label')}
          placeholder={rootId ? t('composer.replyPlaceholder') : t('composer.placeholder', { name: channelName })}
          value={text}
          rows={1}
          onChange={(e) => change(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          disabled={disabled}
          autoFocus
          style={{ maxHeight: maxTextareaHeight ? `${maxTextareaHeight}px` : undefined }}
          className="w-full resize-none overflow-y-auto rounded-t-lg bg-transparent px-3 py-2 text-fg placeholder:text-fg-subtle focus:outline-none disabled:opacity-50"
        />
        <div ref={toolbarRef} role="toolbar" aria-label={t('composer.toolbar')} className="flex items-center gap-0.5 px-1.5 py-1">
          {/* No horizontal scrollbar here (coordinator ruling 2026-09-29):
              the thread panel (as narrow as 320px) cannot fit all 9
              formatting buttons plus the right-hand group, so the ones
              that don't fit collapse behind "More formatting options"
              (FormattingMenu) instead — shrink-0 below keeps the right
              group always fully visible. */}
          {!formattingBarHidden && (
            <>
              {shownButtons.map(({ mode, label, Icon, groupBreak }) => (
                <span key={mode} className="flex shrink-0 items-center gap-0.5">
                  {groupBreak && <span aria-hidden className="mx-1 h-4 w-px shrink-0 bg-line" />}
                  <ToolbarButton label={t(label)} onClick={() => runFormat(mode)} disabled={disabled}>
                    <Icon size={18} />
                  </ToolbarButton>
                </span>
              ))}
              {hiddenButtons.length > 0 && (
                <ToolbarButton
                  label={t('composer.moreFormatting')}
                  haspopup="menu"
                  expanded={moreAnchor !== null}
                  disabled={disabled}
                  onClick={(e) => {
                    // Capture currentTarget synchronously: React nulls it out
                    // on the pooled event by the time a functional setState
                    // updater actually runs, which is not guaranteed to be
                    // within this same synchronous dispatch.
                    const btn = e.currentTarget
                    setMoreAnchor((cur) => (cur ? null : btn))
                  }}
                >
                  <IconMore size={18} />
                </ToolbarButton>
              )}
            </>
          )}
          <div className="ml-auto flex shrink-0 items-center gap-0.5">
            <ToolbarButton
              label={formattingBarHidden ? t('composer.formatShow') : t('composer.formatHide')}
              pressed={!formattingBarHidden}
              onClick={toggleFormattingBar}
            >
              <span className="text-xs font-semibold leading-none">Aa</span>
            </ToolbarButton>
            <ToolbarButton label={t('composer.attach')} onClick={onAttachClick} disabled={disabled}>
              <IconAttach size={18} />
            </ToolbarButton>
            <ToolbarButton label={t('composer.emoji')} onClick={openPicker} disabled={disabled}>
              <IconMood size={18} />
            </ToolbarButton>
            <button
              type="button"
              aria-label={t('composer.send')}
              title={t('composer.send')}
              onClick={() => void send()}
              disabled={disabled || !canSend}
              className={`flex h-7 w-7 shrink-0 items-center justify-center rounded ${
                canSend && !disabled ? 'bg-accent text-accent-fg hover:opacity-90' : 'text-fg-muted opacity-50'
              }`}
            >
              <IconSend size={16} />
            </button>
          </div>
        </div>
      </div>
      {!isDesktop() && <input ref={fileInputRef} type="file" multiple onChange={onFilesSelected} className="hidden" />}
      {picker &&
        createPortal(
          <Suspense fallback={null}>
            <EmojiPicker
              serverId={serverId}
              anchor={picker.anchor}
              info={picker.info}
              onPick={pickEmoji}
              onClose={closePicker}
            />
          </Suspense>,
          document.body,
        )}
      {moreAnchor &&
        hiddenButtons.length > 0 &&
        createPortal(
          <FormattingMenu
            anchorEl={moreAnchor}
            items={hiddenButtons.map(({ mode, label, Icon }) => ({ mode, label: t(label), Icon }))}
            onPick={(mode) => {
              setMoreAnchor(null)
              runFormat(mode)
            }}
            onClose={() => setMoreAnchor(null)}
          />,
          document.body,
        )}
    </div>
  )
}
