import { lazy, memo, Suspense, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { Attachment, EmojiDTO, FileView, PostView } from '../api/types'
import { invalidateRecent, useQuickReactions } from '../emoji/recent'
import { errorMessage } from '../errors'
import { formatTime } from '../format'
import { t } from '../i18n'
import { Attachments } from './Attachments'
import { Avatar } from './Avatar'
import { EmojiGlyph } from './EmojiGlyph'
import { IconAddReaction, IconMore } from './icons'
import { Markdown } from './Markdown'
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
  copyLink(post: PostView): void
  view(post: PostView, fileId: string): void
  download(file: FileView): void
  open(file: FileView): void
  react(post: PostView, emoji: string, add: boolean): void
  emojiInfo(): Promise<EmojiDTO>
}

interface Props {
  serverId: number
  post: PostView
  head: boolean
  me: { id: string; username: string }
  locale: string
  crt: boolean
  actions: PostActions
  editing: boolean
  // replyButton: the toolbar's reply slot, between "add reaction" and
  // "…" — undefined/null until the threads task supplies an IconReply
  // button; kept out of PostActions since it needs no post-specific data
  // PostItem doesn't already have (the caller closes over the post itself).
  replyButton?: React.ReactNode
}

const NAMED_COLORS: Record<string, string> = { good: '#2eb886', warning: '#daa038', danger: '#a30200' }
const barColor = (c?: string) => (c && (NAMED_COLORS[c] ?? (/^#[0-9a-f]{3,8}$/i.test(c) ? c : undefined))) || '#d4d4d4'

function AttachmentView({ a, me, onLink }: { a: Attachment; me: string; onLink(href: string): void }) {
  return (
    <div className="mt-1 max-w-3xl border-l-4 pl-3" style={{ borderColor: barColor(a.color) }}>
      {a.pretext && <Markdown text={a.pretext} me={me} onLink={onLink} />}
      {a.author_name && <div className="text-xs font-medium text-fg-muted">{a.author_name}</div>}
      {a.title &&
        (a.title_link ? (
          <a href={a.title_link} className="font-semibold text-accent hover:underline" onClick={(e) => { e.preventDefault(); onLink(a.title_link!) }}>
            {a.title}
          </a>
        ) : (
          <div className="font-semibold">{a.title}</div>
        ))}
      {a.text && <Markdown text={a.text} me={me} onLink={onLink} />}
      {a.fields && a.fields.length > 0 && (
        <div className="mt-1 grid grid-cols-2 gap-x-4 gap-y-1">
          {a.fields.map((f, i) => (
            <div key={i} className={f.short ? '' : 'col-span-2'}>
              {f.title && <div className="text-xs font-semibold">{f.title}</div>}
              {f.value && <Markdown text={f.value} me={me} onLink={onLink} />}
            </div>
          ))}
        </div>
      )}
      {a.footer && <div className="mt-0.5 text-xs text-fg-muted">{a.footer}</div>}
    </div>
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
        <button className="rounded bg-blue-600 px-2 py-0.5 text-white disabled:opacity-50" disabled={busy} onClick={() => void save()}>
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

function ToolButton({ label, onClick, children }: { label: string; onClick(e: React.MouseEvent<HTMLButtonElement>): void; children: React.ReactNode }) {
  return (
    <button aria-label={label} title={label} onClick={onClick} className="flex h-8 w-8 items-center justify-center rounded text-fg-muted hover:bg-hover">
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
          <ToolButton key={name} label={t('reaction.quick', { emoji: `:${name}:` })} onClick={() => react(name, !mine)}>
            <EmojiGlyph serverId={serverId} name={name} size={20} />
          </ToolButton>
        )
      })}
    </>
  )
}

export const PostItem = memo(function PostItem({ serverId, post, head, me, locale, crt, actions, editing, replyButton }: Props) {
  const time = formatTime(post.create_at, locale)
  const [picker, setPicker] = useState<{ anchor: DOMRect; info: EmojiDTO | null } | null>(null)
  const trigger = useRef<HTMLElement | null>(null)
  const canReact = !post.system && !post.pending && !post.failed
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
  // emoji's recency on the server, so the quick-reactions cache is marked
  // stale and re-read at the toolbar's next show (emoji/recent.ts).
  const react = (emoji: string, add: boolean) => {
    actions.react(post, emoji, add)
    if (add) invalidateRecent(serverId)
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
          <Avatar serverId={serverId} userId={post.user_id} version={post.avatar} name={post.author} status={post.status} size={36} surface="app" />
        ) : (
          <time className="invisible block pt-1 text-right text-[10px] text-fg-subtle group-hover:visible">{time}</time>
        )}
      </div>
      <div className="min-w-0 flex-1">
        {head && (
          <header className="flex items-baseline gap-2">
            <span className="font-semibold">{post.author}</span>
            {post.bot && <span className="rounded bg-hover px-1 text-[10px] font-semibold text-fg-muted">BOT</span>}
            <time className="text-xs text-fg-muted">{time}</time>
          </header>
        )}
        {editing ? (
          <EditBox post={post} actions={actions} />
        ) : (
          <div className={post.system ? 'italic text-fg-muted' : ''}>
            {post.message && <Markdown text={post.message} me={me.username} onLink={actions.link} />}
            {post.edit_at ? <span className="text-xs text-fg-subtle">{t('post.edited')}</span> : null}
          </div>
        )}
        {post.attachments?.map((a, i) => <AttachmentView key={i} a={a} me={me.username} onLink={actions.link} />)}
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
          <Reactions serverId={serverId} reactions={post.reactions} onToggle={(r) => react(r.emoji, !r.mine)} onAdd={canReact ? openPicker : undefined} />
        )}
        {crt && (post.reply_count ?? 0) > 0 && <div className="mt-0.5 text-xs font-medium text-accent">{t('post.replies', { n: String(post.reply_count) })}</div>}
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
          {replyButton}
          <ToolButton label={t('post.more')} onClick={(e) => openMenu(e.currentTarget)}>
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
