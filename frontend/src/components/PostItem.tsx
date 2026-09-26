import { lazy, memo, Suspense, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { Attachment, EmojiDTO, FileView, PostView } from '../api/types'
import { errorMessage } from '../errors'
import { formatTime } from '../format'
import { t } from '../i18n'
import { Attachments } from './Attachments'
import { Avatar } from './Avatar'
import { Markdown } from './Markdown'
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
    <button aria-label={label} title={label} onClick={onClick} className="rounded px-1.5 py-0.5 text-fg-muted hover:bg-hover">
      {children}
    </button>
  )
}

export const PostItem = memo(function PostItem({ serverId, post, head, me, locale, crt, actions, editing }: Props) {
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
  const pick = (name: string) => {
    closePicker()
    if (!post.reactions?.some((r) => r.emoji === name && r.mine)) actions.react(post, name, true)
  }
  return (
    <article
      data-post-id={post.id}
      className={`group relative flex gap-3 px-4 py-0.5 text-fg hover:bg-hover ${head ? 'mt-2' : ''} ${post.pending ? 'opacity-60' : ''}`}
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
          <Reactions serverId={serverId} reactions={post.reactions} onToggle={(r) => actions.react(post, r.emoji, !r.mine)} onAdd={canReact ? openPicker : undefined} />
        )}
        {crt && (post.reply_count ?? 0) > 0 && <div className="mt-0.5 text-xs font-medium text-accent">{t('post.replies', { n: String(post.reply_count) })}</div>}
        {post.pending && <div className="text-xs text-fg-muted">{t('post.sending')}</div>}
        {post.failed && (
          <div role="alert" className="flex gap-2 text-xs text-danger">
            {t('post.failed')}
            <button className="underline" onClick={() => actions.retry(post)}>{t('post.retry')}</button>
            <button className="underline" onClick={() => actions.discard(post)}>{t('post.discard')}</button>
          </div>
        )}
      </div>
      {!post.pending && !post.failed && !editing && (
        <div
          role="toolbar"
          aria-label={t('post.actions')}
          className="absolute -top-3 right-3 hidden gap-0.5 rounded border border-line bg-panel px-1 shadow-sm group-focus-within:flex group-hover:flex"
        >
          {canReact && (
            <ToolButton label={t('reaction.add')} onClick={(e) => openPicker(e.currentTarget)}>
              ☺
            </ToolButton>
          )}
          {post.user_id === me.id && !post.system && (
            <ToolButton label={t('post.edit')} onClick={() => actions.edit(post)}>✎</ToolButton>
          )}
          <ToolButton label={t('post.markUnread')} onClick={() => actions.markUnread(post)}>◉</ToolButton>
          <ToolButton label={t('post.copyLink')} onClick={() => actions.copyLink(post)}>🔗</ToolButton>
          {post.user_id === me.id && !post.system && (
            <ToolButton label={t('post.delete')} onClick={() => actions.remove(post)}>🗑</ToolButton>
          )}
        </div>
      )}
      {picker &&
        createPortal(
          <Suspense fallback={null}>
            <EmojiPicker serverId={serverId} anchor={picker.anchor} info={picker.info} onPick={pick} onClose={closePicker} />
          </Suspense>,
          document.body,
        )}
    </article>
  )
})
