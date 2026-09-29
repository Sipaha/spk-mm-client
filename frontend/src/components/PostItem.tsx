import { lazy, memo, Suspense, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { Attachment, EmojiDTO, FileView, PostView, ReactionUsersDTO } from '../api/types'
import { invalidateRecent, useQuickReactions } from '../emoji/recent'
import { errorMessage } from '../errors'
import { formatTime } from '../format'
import { t } from '../i18n'
import { Attachments } from './Attachments'
import { EmojiGlyph } from './EmojiGlyph'
import { IconAddReaction, IconBookmark, IconBookmarkFilled, IconMore, IconReply } from './icons'
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

function EditBox({ post, actions }: { post: PostView; actions: PostActions }) {
  const [text, setText] = useState(post.message)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
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
        aria-label={t('post.editLabel')}
        autoFocus
        value={text}
        rows={Math.min(10, text.split('\n').length + 1)}
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
        className="w-full resize-none rounded border border-accent bg-app px-2 py-1 text-fg focus:outline-none"
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

export const PostItem = memo(function PostItem({ serverId, post, head, me, locale, actions, editing, variant = 'channel', replyContext = null, isInlineReply = false }: Props) {
  const time = formatTime(post.create_at, locale)
  const [picker, setPicker] = useState<{ anchor: DOMRect; info: EmojiDTO | null } | null>(null)
  const trigger = useRef<HTMLElement | null>(null)
  const canReact = !post.system && !post.pending && !post.failed
  const canReply = variant === 'channel' && !post.system && !post.pending && !post.failed
  const openThread = () => actions.openThread(post)
  const openPicker = (el: HTMLElement) => {
    trigger.current = el
    setPicker({ anchor: el.getBoundingClientRect(), info: null })
    actions.emojiInfo().then(
      (info) => setPicker((p) => (p ? { ...p, info } : p)),
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
    closePicker()
    if (!post.reactions?.some((r) => r.emoji === name && r.mine)) react(name, true)
  }
  // hot: the toolbar renders only for the "hot" post (hovered, focused, or
  // with its own menu/picker open) — see AGENTS.md's Ruling: a toolbar
  // rendered (even CSS-hidden) for every row would load every quick
  // reaction's custom-emoji picture for rows nobody is looking at.
  const [hot, setHot] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const moreBtn = useRef<HTMLElement | null>(null)
  const articleRef = useRef<HTMLElement | null>(null)
  const visible = hot || menuOpen || !!picker
  const openMenu = (el: HTMLElement) => {
    moreBtn.current = el
    setMenuOpen(true)
  }
  const closeMenu = () => setMenuOpen(false)
  return (
    <article
      ref={articleRef}
      data-post-id={post.id}
      className={`group relative flex gap-3 px-4 py-0.5 text-fg hover:bg-hover ${head ? 'mt-2' : ''} ${post.pending ? 'opacity-60' : ''}`}
      onPointerEnter={() => setHot(true)}
      onPointerLeave={() => setHot(false)}
      onFocus={() => setHot(true)}
      onBlur={(e) => {
        const next = e.relatedTarget as Node | null
        if (!next || !articleRef.current?.contains(next)) setHot(false)
      }}
    >
      <div className="w-9 shrink-0 pt-0.5">
        {head ? (
          <PostAvatar serverId={serverId} post={post} size={36} />
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
              {post.author}
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
        <div className={isInlineReply ? '-my-0.5 border-l-[3px] border-line/70 py-0.5 pl-2' : undefined} data-testid={isInlineReply ? 'reply-bar' : undefined}>
          {editing ? (
            <EditBox post={post} actions={actions} />
          ) : (
            <div className={post.system ? 'italic text-fg-muted' : ''}>
              {post.message && <Markdown text={post.message} me={me.username} onLink={actions.link} serverId={serverId} emojiInfo={actions.emojiInfo} />}
              {post.edit_at ? <span className="text-xs text-fg-subtle">{t('post.edited')}</span> : null}
            </div>
          )}
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
          {post.pending && (
            <div className="flex items-center gap-2 text-xs text-fg-muted">
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
      {!post.pending && !post.failed && !editing && visible && (
        <div
          data-testid="post-toolbar"
          role="toolbar"
          aria-label={t('post.actions')}
          className="absolute -top-4 right-3 flex gap-0.5 rounded border border-line bg-panel px-1 shadow-sm"
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
      {picker &&
        createPortal(
          <Suspense fallback={null}>
            <EmojiPicker serverId={serverId} anchor={picker.anchor} info={picker.info} onPick={pick} onClose={closePicker} />
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
