import { lazy, memo, Suspense, useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent } from 'react'
import { createPortal } from 'react-dom'
import type { Attachment, EmojiDTO, FileView, GifPreviewView, PermalinkPreviewView, PostView, ReactionUsersDTO } from '../api/types'
import { invalidateRecent, useQuickReactions } from '../emoji/recent'
import { errorMessage } from '../errors'
import { formatTime } from '../format'
import { t } from '../i18n'
import { mediaURL, useLoadFailure } from '../media'
import { Attachments } from './Attachments'
import { Avatar } from './Avatar'
import { EmojiGlyph } from './EmojiGlyph'
import { boxStyle, fitBox } from './files'
import { IconAddReaction, IconAttach, IconBookmark, IconBookmarkFilled, IconMore, IconReply } from './icons'
import { Markdown } from './Markdown'
import { PostAvatar } from './PostAvatar'
import PostMenu from './PostMenu'
import { Reactions } from './Reactions'

const EmojiPicker = lazy(() => import('./EmojiPicker'))

export interface PostActions {
  link(href: string): void
  retry(post: PostView): void
  discard(post: PostView): void
  edit(post: PostView): void
  saveEdit(post: PostView, message: string): Promise<void>
  cancelEdit(): void
  remove(post: PostView): void
  markUnread(post: PostView): void
  save(post: PostView, saved: boolean): void
  copyLink(post: PostView): void
  view(post: PostView, fileId: string): void
  download(file: FileView): void
  open(file: FileView): void
  react(post: PostView, emoji: string, add: boolean): Promise<void>
  emojiInfo(): Promise<EmojiDTO>
  // reactionUsers: who reacted with emoji on this post — for the reaction
  // chip's hover/focus tooltip and its "and N others" modal.
  reactionUsers(post: PostView, emoji: string): Promise<ReactionUsersDTO>
  // openThread: opens post's thread — its own if post is a root, else its
  // root_id's. Used by the "N replies" link, the ↩ reply button and the
  // reply-context line's click (Task 6).
  openThread(post: PostView): void
  // openPermalink: navigates to a quoted permalink card's target message.
  openPermalink?(pl: PermalinkPreviewView): void
  // viewGif: opens an external/markdown GIF preview in GifLightbox.
  viewGif?(postId: string, gif: GifPreviewView): void
}

interface Props {
  serverId: number
  post: PostView
  head: boolean
  me: { id: string; username: string; avatar?: string }
  locale: string
  crt: boolean
  actions: PostActions
  editing: boolean
  // variant: 'channel' (the feed — shows the ↩ reply button and, for a
  // root, the "N replies" link) or 'thread' (the panel — neither; see
  // AGENTS.md/Task 6 brief). Defaults to 'channel' so existing callers that
  // never render a thread panel need no change.
  variant?: 'channel' | 'thread'
  // replyContext: this reply's context line ("reply to <author>: <snippet>")
  // for the first reply of a series without CRT — computed by feedRows.
  // undefined/null: no context line.
  replyContext?: { author: string; snippet: string } | null
  // isInlineReply: this post is a reply rendered inline in the channel feed
  // (variant 'channel', no CRT) — computed by feedRows for EVERY such reply,
  // not only the first of a series. Wraps the content column (message,
  // attachments, files, reactions, pending/failed state — not the avatar or
  // the header name/time) in a left border bar (reply-style brief,
  // 2026-09-28).
  isInlineReply?: boolean
}

const NAMED_COLORS: Record<string, string> = { good: '#2eb886', warning: '#daa038', danger: '#a30200' }
const barColor = (c?: string) => (c && (NAMED_COLORS[c] ?? (/^#[0-9a-f]{3,8}$/i.test(c) ? c : undefined))) || '#d4d4d4'

// Toolbar placement (density-brief 2026-09-29 addendum + "smart placement"
// update): default is INSIDE the hovered post's own box (top-right, a few
// px inset — 'inside-top'; vertically centred instead — 'inside-center' —
// when the row itself is shorter than the toolbar, e.g. a one-line
// continuation post, so the unavoidable overflow is split evenly above and
// below rather than dumped entirely into the next row). If that inside
// position would cover the post's own rendered content (any top-level
// content child's rect intersects the toolbar's band), it overhangs
// upward instead (the pre-addendum look) — unless doing so would push it
// above the feed scroller's own top edge (the first post / right after a
// date separator has no room above), in which case it stays inside
// despite the overlap: covering the post's own text beats being clipped
// by the scroller.
export type ToolbarPlacement = 'inside-top' | 'inside-center' | 'overhang'

interface Rect {
  top: number
  left: number
  right: number
  bottom: number
}

// decideToolbarPlacement is a pure function of already-measured rects —
// PostItem's useLayoutEffect does the one real layout read (getBoundingClientRect
// ×N) and hands the plain numbers here, so this rule itself is unit-testable
// with stubbed rects, no DOM/mounting required.
export function decideToolbarPlacement(input: {
  article: { top: number; height: number }
  toolbarInside: Rect & { height: number }
  // contentChildren: the content column's rendered rects — real
  // glyph-level line rects for text (see the caller: a Range per text node,
  // not the column's top-level element boxes, which stretch to the full
  // column width regardless of how little of it their text actually uses)
  // plus the bounding box of self-sized visual blocks (attachment cards,
  // images, buttons).
  contentChildren: Rect[]
  // scrollerTop: the feed scroller's own getBoundingClientRect().top, or
  // null if no [role="log"] ancestor was found (e.g. in isolation/tests) —
  // treated as "no clamp" (matches there being nothing to clip against).
  scrollerTop: number | null
}): ToolbarPlacement {
  const { article, toolbarInside, contentChildren, scrollerTop } = input
  const base: ToolbarPlacement = article.height < toolbarInside.height + 8 ? 'inside-center' : 'inside-top'
  const overlapsContent = contentChildren.some(
    (r) => r.right > toolbarInside.left && r.left < toolbarInside.right && r.bottom > toolbarInside.top && r.top < toolbarInside.bottom,
  )
  if (!overlapsContent) return base
  const OVERHANG_OFFSET = 16 // -top-4 (Tailwind) = -1rem = -16px above the article's own top
  const overhangTop = article.top - OVERHANG_OFFSET
  if (scrollerTop !== null && overhangTop < scrollerTop) return base // would be clipped by the scroller's own top edge — stay inside instead
  return 'overhang'
}

const TOOLBAR_PLACEMENT_CLASS: Record<ToolbarPlacement, string> = {
  'inside-top': 'top-1',
  'inside-center': 'top-1/2 -translate-y-1/2',
  overhang: '-top-4',
}

function AttachmentView({
  serverId,
  a,
  me,
  onLink,
  emojiInfo,
}: {
  serverId: number
  a: Attachment
  me: string
  onLink(href: string): void
  emojiInfo(): Promise<EmojiDTO>
}) {
  // Card shape ported from the official webapp's .attachment__content /
  // .attachment__container (channels/src/sass/layout/_webhooks.scss,
  // mm-10.11): a 1px border with no left side (the coloured bar reads as
  // the left edge instead), rounded only on the right (their border-radius
  // is "0 4px 4px 0" — Tailwind's rounded-r matches exactly, our default
  // radius is also 4px), 12px padding, a 5px margin above/below, and no
  // width cap (density-brief 2026-09-29: "full available width" — the
  // official CSS only caps width for the permalink/opengraph variant,
  // which we don't render here). The pretext (if any) sits outside the
  // card, unbordered, like .attachment__thumb-pretext.
  return (
    <>
      {a.pretext && (
        <div className="mt-1">
          <Markdown text={a.pretext} me={me} onLink={onLink} serverId={serverId} emojiInfo={emojiInfo} />
        </div>
      )}
      <div className="my-[5px] max-w-full overflow-hidden rounded-r border-y border-r border-line">
        <div className="border-l-4 p-3" style={{ borderColor: barColor(a.color) }}>
          {a.author_name && <div className="text-xs font-medium text-fg-muted">{a.author_name}</div>}
          {a.title &&
            (a.title_link ? (
              <a href={a.title_link} className="font-semibold text-accent hover:underline" onClick={(e) => { e.preventDefault(); onLink(a.title_link!) }}>
                {a.title}
              </a>
            ) : (
              <div className="font-semibold">
                <Markdown text={a.title} me={me} onLink={onLink} serverId={serverId} emojiInfo={emojiInfo} />
              </div>
            ))}
          {a.text && <Markdown text={a.text} me={me} onLink={onLink} serverId={serverId} emojiInfo={emojiInfo} />}
          {a.fields && a.fields.length > 0 && (
            <div className="mt-1 grid grid-cols-2 gap-x-4 gap-y-1">
              {a.fields.map((f, i) => (
                <div key={i} className={f.short ? '' : 'col-span-2'}>
                  {f.title && (
                    <div className="text-xs font-semibold">
                      <Markdown text={f.title} me={me} onLink={onLink} serverId={serverId} emojiInfo={emojiInfo} />
                    </div>
                  )}
                  {f.value && <Markdown text={f.value} me={me} onLink={onLink} serverId={serverId} emojiInfo={emojiInfo} />}
                </div>
              ))}
            </div>
          )}
          {a.footer && <div className="mt-0.5 text-xs text-fg-muted">{a.footer}</div>}
        </div>
      </div>
    </>
  )
}

const GIF_BOX_MAX = { w: 480, h: 360 }

function PostGifTile({
  serverId,
  postId,
  gif,
  onViewGif,
}: {
  serverId: number
  postId: string
  gif: GifPreviewView
  onViewGif?(postId: string, gif: GifPreviewView): void
}) {
  const url = mediaURL(serverId, 'postgif', postId, { k: gif.id })
  const [failed, fail] = useLoadFailure(serverId, url)
  if (failed) return null
  const box = fitBox(gif.width, gif.height, GIF_BOX_MAX.w, GIF_BOX_MAX.h)
  return (
    <button
      type="button"
      data-gif-id={gif.id}
      aria-label={gif.alt ? `${t('gif.view')}: ${gif.alt}` : t('gif.view')}
      title={gif.alt || gif.url}
      onClick={(e) => {
        e.stopPropagation()
        onViewGif?.(postId, gif)
      }}
      className="block shrink-0 overflow-hidden rounded border border-line bg-panel focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
      style={boxStyle(box)}
    >
      <img
        src={url}
        alt={gif.alt || ''}
        width={box.width}
        height={box.height}
        decoding="async"
        draggable={false}
        onError={fail}
        className="block h-full w-full object-contain"
      />
    </button>
  )
}

function PermalinkCard({
  serverId,
  pl,
  me,
  locale,
  onLink,
  onOpen,
  emojiInfo,
}: {
  serverId: number
  pl: PermalinkPreviewView
  me: string
  locale: string
  onLink(href: string): void
  onOpen?(): void
  emojiInfo(): Promise<EmojiDTO>
}) {
  const channelLabel = pl.channel_display_name || pl.channel_name || ''
  const author = pl.author || t('reaction.unknownUser')
  const open = () => onOpen?.()
  return (
    <div
      role="button"
      tabIndex={0}
      data-permalink-card
      data-permalink-post-id={pl.post_id}
      aria-label={`${t('permalink.jump')}: ${author}`}
      onClick={(e) => {
        if ((e.target as HTMLElement).closest('a, button')) return
        const sel = window.getSelection()
        if (sel && !sel.isCollapsed && sel.toString() !== '' && e.currentTarget.contains(sel.anchorNode)) return
        e.stopPropagation()
        open()
      }}
      onKeyDown={(e) => {
        if ((e.key !== 'Enter' && e.key !== ' ') || e.target !== e.currentTarget) return
        e.preventDefault()
        open()
      }}
      className="my-1.5 max-w-[520px] cursor-pointer overflow-hidden rounded-r border-y border-r border-line border-l-[3px] border-l-accent/70 bg-panel/60 px-3 py-2 text-left transition-colors hover:bg-hover/40 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
    >
      <div className="mb-1 flex min-w-0 items-center gap-1.5 text-xs">
        {pl.user_id && <Avatar serverId={serverId} userId={pl.user_id} version={pl.avatar} name={author} size={18} surface="app" />}
        <span className="truncate font-semibold text-fg">{author}</span>
        {pl.create_at ? <time className="shrink-0 text-fg-muted">{formatTime(pl.create_at, locale)}</time> : null}
        {channelLabel && (
          <span className="min-w-0 truncate text-fg-muted">
            · {pl.channel_type === 'O' || pl.channel_type === 'P' ? `#${channelLabel}` : channelLabel}
          </span>
        )}
      </div>
      {pl.message && (
        <div className="line-clamp-4 text-sm text-fg">
          <Markdown text={pl.message} me={me} onLink={onLink} serverId={serverId} emojiInfo={emojiInfo} />
        </div>
      )}
      {(pl.file_count ?? 0) > 0 && (
        <div className="mt-1 flex items-center gap-1 text-xs text-fg-muted">
          <IconAttach size={13} className="shrink-0" />
          <span>{t('permalink.files', { n: String(pl.file_count) })}</span>
        </div>
      )}
    </div>
  )
}

function EditBox({ post, actions }: { post: PostView; actions: PostActions }) {
  const [text, setText] = useState(post.message)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  // Fit the editor to the actual laid-out message, including visually
  // wrapped lines (counting '\n' cannot see those). Very long messages are
  // capped at half the viewport and scroll inside the editor.
  useLayoutEffect(() => {
    const el = textareaRef.current
    if (!el || el.scrollHeight === 0) return
    el.style.height = 'auto'
    el.style.height = `${el.scrollHeight}px`
  }, [text])
  const save = async () => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await actions.saveEdit(post, text)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="mt-1">
      <textarea
        ref={textareaRef}
        aria-label={t('post.editLabel')}
        autoFocus
        value={text}
        rows={1}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            actions.cancelEdit()
          } else if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault()
            void save()
          }
        }}
        className="max-h-[50vh] w-full resize-none overflow-y-auto rounded border border-accent bg-app px-2 py-1 text-fg focus:outline-none"
      />
      <div className="flex items-center gap-3 text-xs">
        <button className="rounded bg-accent px-2 py-0.5 text-accent-fg disabled:opacity-50" disabled={busy} onClick={() => void save()}>
          {t('post.save')}
        </button>
        <button className="text-fg underline" onClick={actions.cancelEdit}>
          {t('post.cancel')}
        </button>
        {error && (
          <span role="alert" className="text-danger">
            {error}
          </span>
        )}
      </div>
    </div>
  )
}

function ToolButton({
  label, onClick, children, pressed, haspopup, expanded, className = 'text-fg-muted',
}: {
  label: string
  onClick(e: React.MouseEvent<HTMLButtonElement>): void
  children: React.ReactNode
  pressed?: boolean
  haspopup?: 'menu' // the "…" button opens PostMenu
  expanded?: boolean
  className?: string
}) {
  return (
    <button
      aria-label={label}
      title={label}
      aria-pressed={pressed}
      aria-haspopup={haspopup}
      aria-expanded={haspopup ? expanded : undefined}
      onClick={onClick}
      className={`flex h-8 w-8 items-center justify-center rounded hover:bg-hover ${className}`}
    >
      {children}
    </button>
  )
}

// QuickReactions: the hover bar's up-to-3 one-click reactions (Task 2's
// getOneClickReactionEmojis equivalent — see emoji/recent.ts). A separate
// component so useQuickReactions (and the api.emojiInfo() call it can
// trigger) only mounts once the toolbar itself is actually rendered, i.e.
// at the post's first "show" — never for a merely-mounted, not-hot row.
function QuickReactions({ serverId, post, load, react }: { serverId: number; post: PostView; load(): Promise<EmojiDTO>; react(emoji: string, add: boolean): void }) {
  const quick = useQuickReactions(serverId, load)
  return (
    <>
      {quick.map((name) => {
        const mine = post.reactions?.some((r) => r.emoji === name && r.mine) ?? false
        return (
          <ToolButton key={name} label={t('reaction.quick', { emoji: `:${name}:` })} pressed={mine} onClick={() => react(name, !mine)}>
            <EmojiGlyph serverId={serverId} name={name} size={20} />
          </ToolButton>
        )
      })}
    </>
  )
}

// Coordinate "hot" state across mounted PostItems so virtualized scroll or
// missed pointerleave/blur events never leave two hover/focus toolbars open
// at once when pointer or focus moves to another post.
let releaseActiveHot: (() => void) | null = null

function claimActiveHot(release: () => void) {
  if (releaseActiveHot && releaseActiveHot !== release) {
    releaseActiveHot()
  }
  releaseActiveHot = release
}

function clearActiveHot(release: () => void) {
  if (releaseActiveHot === release) {
    releaseActiveHot = null
  }
}

export const PostItem = memo(function PostItem({ serverId, post, head, me, locale, actions, editing, variant = 'channel', replyContext = null, isInlineReply = false }: Props) {
  const time = formatTime(post.create_at, locale)
  // Pending posts already live in the local feed. Give quick confirmations
  // no extra label/opacity/layout at all. Use the original local send time,
  // not mount time: virtualization must not restart the grace period.
  const pendingDeadline = post.create_at + 3000
  const [revealedDeadline, revealPending] = useState<number | null>(null)
  const showPending = post.pending && !post.failed && (revealedDeadline === pendingDeadline || Date.now() >= pendingDeadline)
  useEffect(() => {
    if (!post.pending || post.failed) return
    const remaining = pendingDeadline - Date.now()
    if (remaining <= 0) return
    const timer = setTimeout(() => revealPending(pendingDeadline), remaining)
    return () => clearTimeout(timer)
  }, [post.pending, post.failed, pendingDeadline])
  const [picker, setPicker] = useState<{ postId: string; anchor: DOMRect; info: EmojiDTO | null } | null>(null)
  const activePicker = picker?.postId === post.id ? picker : null
  const trigger = useRef<HTMLElement | null>(null)
  const canReact = !post.system && !post.pending && !post.failed
  const canReply = variant === 'channel' && !post.system && !post.pending && !post.failed
  const openThread = () => actions.openThread(post)
  const openThreadFromText = (e: MouseEvent<HTMLDivElement>) => {
    // Text is an additional shortcut for existing threads. Keep dedicated
    // buttons for keyboard access and leave selection/embedded controls alone.
    if (!canReply || post.ephemeral || editing || (!post.root_id && !post.reply_count)) return
    if (e.defaultPrevented || e.button !== 0 || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return
    if ((e.target as Element).closest('a, button, input, textarea, select, [role="button"], [contenteditable], pre, code, img, video, audio')) return
    const selection = window.getSelection()
    if (selection && !selection.isCollapsed) return
    openThread()
  }
  const openPicker = (el: HTMLElement) => {
    const targetPostId = post.id
    trigger.current = el
    setPicker({ postId: targetPostId, anchor: el.getBoundingClientRect(), info: null })
    actions.emojiInfo().then(
      (info) => setPicker((p) => (p && p.postId === targetPostId ? { ...p, info } : p)),
      () => {},
    )
  }
  const closePicker = () => {
    setPicker(null)
    trigger.current?.focus()
  }
  // react wraps actions.react: adding one of our reactions bumps the
  // emoji's recency, so the quick-reactions cache is marked stale and
  // re-read at the toolbar's next show (emoji/recent.ts). Invalidating
  // only after actions.react's promise settles (not synchronously) matters
  // — final-review finding: invalidating right away could refetch before
  // the backend had even started handling this very call, caching the
  // list from *before* this reaction. actions.react's promise always
  // resolves (chat.ts) once the call has settled, success or failure —
  // Go bumps its local list at issue time regardless of outcome, so by
  // the time it settles the bump has already happened either way.
  const react = (emoji: string, add: boolean) => {
    void actions.react(post, emoji, add).then(() => {
      if (add) invalidateRecent(serverId)
    })
  }
  const pick = (name: string) => {
    const pickerPostId = activePicker?.postId
    closePicker()
    if (pickerPostId !== post.id) return
    if (!post.reactions?.some((r) => r.emoji === name && r.mine)) react(name, true)
  }
  // hot: the toolbar renders only for the "hot" post (hovered, focused, or
  // with its own menu/picker open) — see AGENTS.md's Ruling: a toolbar
  // rendered (even CSS-hidden) for every row would load every quick
  // reaction's custom-emoji picture for rows nobody is looking at.
  // Bind hot/menu/picker to exact server post.id (allowing only genuine
  // pending/failed -> confirmed transitions) so two confirmed posts sharing a
  // reused pending_post_id can never inherit each other's menu or picker.
  const [boundPost, setBoundPost] = useState({ id: post.id, pendingOrFailed: !!(post.pending || post.failed) })
  const [hotFor, setHotFor] = useState<{ id: string; pendingOrFailed: boolean } | null>(null)
  const [menuForId, setMenuForId] = useState<string | null>(null)
  const moreBtn = useRef<HTMLElement | null>(null)
  const articleRef = useRef<HTMLElement | null>(null)
  const releaseHotRef = useRef<() => void>(() => setHotFor(null))
  if (boundPost.id !== post.id) {
    const isPendingAck =
      boundPost.pendingOrFailed &&
      !post.pending &&
      !post.failed &&
      !!post.pending_post_id &&
      post.pending_post_id === boundPost.id
    setBoundPost({ id: post.id, pendingOrFailed: !!(post.pending || post.failed) })
    if (!isPendingAck) {
      setHotFor(null)
      setMenuForId(null)
      setPicker(null)
    }
  }
  const activateHot = () => {
    claimActiveHot(releaseHotRef.current)
    setHotFor({ id: post.id, pendingOrFailed: !!(post.pending || post.failed) })
  }
  const deactivateHot = () => {
    clearActiveHot(releaseHotRef.current)
    setHotFor(null)
  }
  useEffect(() => () => clearActiveHot(releaseHotRef.current), [])
  const hot =
    hotFor !== null &&
    (hotFor.id === post.id ||
      (hotFor.pendingOrFailed && !post.pending && !post.failed && !!post.pending_post_id && post.pending_post_id === hotFor.id))
  const menuOpen = menuForId === post.id
  const visible = hot || menuOpen || !!activePicker
  const openMenu = (el: HTMLElement) => {
    moreBtn.current = el
    setMenuForId(post.id)
  }
  const closeMenu = () => setMenuForId(null)
  // Toolbar placement: one layout read per show (see decideToolbarPlacement
  // above) — reset to the 'inside-top' baseline whenever the toolbar isn't
  // rendered, so the next show always starts from the same measured
  // candidate rather than carrying over a stale decision. No ResizeObserver,
  // no scroll/pointermove listener, no per-frame work: this effect runs
  // exactly once per visible:false→true transition.
  const contentRef = useRef<HTMLDivElement>(null)
  const toolbarRef = useRef<HTMLDivElement>(null)
  const [placement, setPlacement] = useState<ToolbarPlacement>('inside-top')
  useLayoutEffect(() => {
    if (!visible) {
      setPlacement('inside-top')
      return
    }
    const toolbar = toolbarRef.current
    const article = articleRef.current
    if (!toolbar || !article) return
    const toolbarRect = toolbar.getBoundingClientRect()
    const articleRect = article.getBoundingClientRect()
    // contentRef.current.children[*].getBoundingClientRect() doesn't work
    // here: the wrapper <div>s around the message text (and, it turns out,
    // Range.getClientRects() over the whole column too — a Range that
    // fully contains a block element also yields a rect for *that
    // element's own box*, not just its text) all stretch to the column's
    // full width by default regardless of how little of it their actual
    // text uses, so even a one-word message would measure as "reaches the
    // right edge". Two kinds of rects instead: a Range per individual TEXT
    // NODE (a range whose start/end sit inside the same text node yields
    // real glyph-level line rects, immune to the ancestor-box problem —
    // this is what actually answers "does the rendered text reach under
    // the toolbar"), plus the bounding box of any genuinely self-sized
    // visual block that isn't just a text wrapper: the attachment card
    // (`.rounded-r`, its own real width per the density brief), images and
    // buttons (file tiles, reaction chips) — these size to their own
    // content already (explicit dimensions or inline-level), so their
    // plain bounding rect is accurate.
    const contentRects: Rect[] = []
    if (contentRef.current) {
      const walker = document.createTreeWalker(contentRef.current, NodeFilter.SHOW_TEXT)
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        if (!node.textContent?.trim()) continue
        const range = document.createRange()
        range.selectNodeContents(node)
        // getClientRects is unimplemented in jsdom (throws) — every real
        // browser this app runs in (Chromium/WebKitGTK) supports it; guard
        // rather than crash the layout effect where it doesn't.
        if (typeof range.getClientRects === 'function') contentRects.push(...Array.from(range.getClientRects()))
      }
      for (const el of contentRef.current.querySelectorAll('.rounded-r, img, button')) contentRects.push(el.getBoundingClientRect())
    }
    const scrollerEl = article.closest('[role="log"]')
    setPlacement(
      decideToolbarPlacement({
        article: { top: articleRect.top, height: articleRect.height },
        toolbarInside: toolbarRect,
        contentChildren: contentRects,
        scrollerTop: scrollerEl ? scrollerEl.getBoundingClientRect().top : null,
      }),
    )
  }, [visible])
  const handleViewGif = useMemo(
    () => (actions.viewGif ? (g: GifPreviewView) => actions.viewGif!(post.id, g) : undefined),
    [actions.viewGif, post.id],
  )
  return (
    <article
      ref={articleRef}
      data-post-id={post.id}
      className={`group relative flex gap-3 px-4 py-0.5 text-fg hover:bg-hover/35 ${head ? 'mt-2' : ''}`}
      onPointerEnter={activateHot}
      onPointerLeave={deactivateHot}
      onFocus={(e) => {
        if (articleRef.current?.contains(e.target as Node)) activateHot()
      }}
      onBlur={(e) => {
        if (!articleRef.current?.contains(e.target as Node)) return
        const next = e.relatedTarget as Node | null
        if (!next || !articleRef.current?.contains(next)) deactivateHot()
      }}
    >
      <div className="flex w-9 shrink-0 justify-end pt-0.5">
        {head ? (
          <PostAvatar serverId={serverId} post={post} size={32} />
        ) : (
          <time className="invisible block pt-1 text-right text-[10px] text-fg-subtle group-hover:visible">{time}</time>
        )}
      </div>
      <div className="min-w-0 flex-1">
        {head && (
          // mb-0.5 (2px): official's .post__header margin-bottom — the
          // density brief's "more space between header and content"
          // (density-brief 2026-09-29).
          <header className="mb-0.5 flex items-baseline gap-2">
            <span className="font-semibold" title={post.real_author}>
              {post.system_author ? t('post.system') : post.author}
            </span>
            {post.bot && <span className="rounded bg-hover px-1 text-[10px] font-semibold text-fg-muted">BOT</span>}
            <time className="text-xs text-fg-muted">{time}</time>
          </header>
        )}
        {replyContext && (
          <button type="button" className="mb-0.5 block max-w-full truncate text-left text-xs hover:underline" onClick={openThread}>
            {replyContext.author ? (
              <>
                <span className="text-fg-muted">{t('thread.replyToPrefix')}</span>{' '}
                <span className="text-accent">{t('thread.replyToAccent', { author: replyContext.author, snippet: replyContext.snippet })}</span>
              </>
            ) : (
              <span className="text-fg-muted">{t('thread.replyInThread')}</span>
            )}
          </button>
        )}
        {/* isInlineReply: the content column (message, attachments, files,
            reactions, pending/failed) sits behind a left border bar,
            webapp-like — every reply in the run, not just the head one, so
            consecutive replies form one continuous-looking bar. The header
            (avatar/name/time) above and the replyContext line stay outside
            it. `-my-0.5 py-0.5` bleeds the bordered box into the
            <article>'s own `py-0.5` (PostItem's outer className) on both
            sides — the negative margin pulls the border out to the row's
            true top/bottom edge while the matching padding pushes the
            content back to its original position, so two consecutive
            reply rows' borders touch with no gap and no net height change
            (fix round 1, I1: the review's own crops showed a visible break
            here). Pure CSS throughout: no extra measurement, and these
            classes are horizontal-or-cancelling-vertical only, so the
            virtualizer's measured row height is unaffected. */}
        <div
          ref={contentRef}
          className={isInlineReply ? '-my-0.5 border-l-[3px] border-line/70 py-0.5 pl-2' : undefined}
          data-testid={isInlineReply ? 'reply-bar' : undefined}
        >
          {editing ? (
            <EditBox post={post} actions={actions} />
          ) : (
            <div className={post.system ? 'italic text-fg-muted' : ''} onClick={openThreadFromText}>
              {post.message && (
                <Markdown
                  text={post.message}
                  me={me.username}
                  onLink={actions.link}
                  serverId={serverId}
                  emojiInfo={actions.emojiInfo}
                  postId={post.id}
                  gifPreviews={post.gif_previews}
                  onViewGif={handleViewGif}
                />
              )}
              {post.edit_at ? <span className="text-xs text-fg-subtle">{t('post.edited')}</span> : null}
              {post.ephemeral ? <span className="text-xs not-italic text-fg-subtle">{t('post.onlyVisibleToYou')}</span> : null}
            </div>
          )}
          {post.permalinks?.map((pl) => (
            <PermalinkCard
              key={pl.post_id}
              serverId={serverId}
              pl={pl}
              me={me.username}
              locale={locale}
              onLink={actions.link}
              onOpen={() => actions.openPermalink?.(pl)}
              emojiInfo={actions.emojiInfo}
            />
          ))}
          {(() => {
            const extraGifs = (post.gif_previews ?? []).filter((g) => !g.inline)
            if (extraGifs.length === 0) return null
            return (
              <div className="mt-1 flex w-full min-w-0 flex-wrap gap-2" data-testid="post-gif-previews">
                {extraGifs.map((g) => (
                  <PostGifTile key={g.id} serverId={serverId} postId={post.id} gif={g} onViewGif={actions.viewGif} />
                ))}
              </div>
            )
          })()}
          {post.attachments?.map((a, i) => (
            <AttachmentView key={i} serverId={serverId} a={a} me={me.username} onLink={actions.link} emojiInfo={actions.emojiInfo} />
          ))}
          {post.files && post.files.length > 0 && (
            <Attachments
              serverId={serverId}
              files={post.files}
              me={me.username}
              onLink={actions.link}
              onView={(f) => actions.view(post, f.id)}
              onDownload={actions.download}
              onOpen={actions.open}
            />
          )}
          {post.reactions && post.reactions.length > 0 && (
            <Reactions
              serverId={serverId}
              postId={post.id}
              reactions={post.reactions}
              me={me}
              onToggle={(r) => react(r.emoji, !r.mine)}
              onAdd={canReact ? openPicker : undefined}
              loadReactors={(_postId, emoji) => actions.reactionUsers(post, emoji)}
            />
          )}
          {variant === 'channel' && !post.root_id && (post.reply_count ?? 0) > 0 && (
            <button type="button" className="mt-0.5 block text-xs font-medium text-accent hover:underline" onClick={openThread}>
              {post.last_reply_at
                ? `${t('thread.replies', { n: String(post.reply_count) })} · ${t('thread.lastReply', { time: formatTime(post.last_reply_at, locale) })}`
                : t('thread.replies', { n: String(post.reply_count) })}
            </button>
          )}
          {showPending && (
            <div role="status" className="flex items-center gap-2 text-xs text-fg-muted">
              {t('post.sending')}
              {/* A post with files still waiting (e.g. offline) can be cancelled
                  (DiscardPost works on a waiting post); once every file is
                  uploaded, CreatePost may already be in flight for it, so the
                  button disappears rather than race a discard against it. */}
              {(post.files?.length ?? 0) > 0 && post.files!.some((f) => f.state !== 'uploaded') && (
                <button className="underline" onClick={() => actions.discard(post)}>{t('post.cancelSending')}</button>
              )}
            </div>
          )}
          {post.failed && (
            <div role="alert" className="flex gap-2 text-xs text-danger">
              {t('post.failed')}
              <button className="underline" onClick={() => actions.retry(post)}>{t('post.retry')}</button>
              <button className="underline" onClick={() => actions.discard(post)}>{t('post.discard')}</button>
            </div>
          )}
        </div>
      </div>
      {!post.pending && !post.failed && !post.ephemeral && !editing && visible && (
        <div
          ref={toolbarRef}
          data-testid="post-toolbar"
          data-placement={placement}
          role="toolbar"
          aria-label={t('post.actions')}
          className={`absolute right-3 flex gap-0.5 rounded border border-line bg-panel px-1 shadow-sm ${TOOLBAR_PLACEMENT_CLASS[placement]}`}
        >
          {canReact && <QuickReactions serverId={serverId} post={post} load={actions.emojiInfo} react={react} />}
          {canReact && (
            <ToolButton label={t('reaction.add')} onClick={(e) => openPicker(e.currentTarget)}>
              <IconAddReaction size={20} />
            </ToolButton>
          )}
          {canReact && (
            <ToolButton
              label={t('post.saveForLater')}
              pressed={!!post.saved}
              className={post.saved ? 'text-accent' : 'text-fg-muted'}
              onClick={() => actions.save(post, !post.saved)}
            >
              {post.saved ? <IconBookmarkFilled size={20} /> : <IconBookmark size={20} />}
            </ToolButton>
          )}
          {canReply && (
            <ToolButton label={t('post.reply')} onClick={openThread}>
              <IconReply size={20} />
            </ToolButton>
          )}
          <ToolButton label={t('post.more')} haspopup="menu" expanded={menuOpen} onClick={(e) => openMenu(e.currentTarget)}>
            <IconMore size={20} />
          </ToolButton>
        </div>
      )}
      {activePicker &&
        createPortal(
          <Suspense fallback={null}>
            <EmojiPicker serverId={serverId} anchor={activePicker.anchor} info={activePicker.info} onPick={pick} onClose={closePicker} />
          </Suspense>,
          document.body,
        )}
      {menuOpen &&
        moreBtn.current &&
        createPortal(
          <PostMenu
            anchorEl={moreBtn.current}
            canEdit={post.user_id === me.id && !post.system}
            onMarkUnread={() => actions.markUnread(post)}
            onCopyLink={() => actions.copyLink(post)}
            onEdit={() => actions.edit(post)}
            onDelete={() => actions.remove(post)}
            onClose={closeMenu}
          />,
          document.body,
        )}
    </article>
  )
})
