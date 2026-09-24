import { memo, useState } from 'react'
import type { Attachment, PostView } from '../api/types'
import { errorMessage } from '../errors'
import { formatSize, formatTime } from '../format'
import { t } from '../i18n'
import { emojiFor } from './emoji'
import { Markdown } from './Markdown'

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
}

interface Props {
  post: PostView
  head: boolean
  me: { id: string; username: string }
  locale: string
  crt: boolean
  actions: PostActions
  editing: boolean
}

const COLORS = ['bg-rose-500', 'bg-orange-500', 'bg-amber-600', 'bg-lime-600', 'bg-emerald-600', 'bg-teal-600', 'bg-sky-600', 'bg-indigo-500', 'bg-violet-500', 'bg-fuchsia-600']

// Initials until avatars (stage 3); the color is stable per user.
function Avatar({ id, name }: { id: string; name: string }) {
  let h = 0
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) | 0
  return (
    <div aria-hidden className={`flex h-9 w-9 items-center justify-center rounded-full text-sm font-semibold text-white ${COLORS[Math.abs(h) % COLORS.length]}`}>
      {(name[0] ?? '?').toUpperCase()}
    </div>
  )
}

const NAMED_COLORS: Record<string, string> = { good: '#2eb886', warning: '#daa038', danger: '#a30200' }
const barColor = (c?: string) => (c && (NAMED_COLORS[c] ?? (/^#[0-9a-f]{3,8}$/i.test(c) ? c : undefined))) || '#d4d4d4'

function AttachmentView({ a, me, onLink }: { a: Attachment; me: string; onLink(href: string): void }) {
  return (
    <div className="mt-1 max-w-3xl border-l-4 pl-3" style={{ borderColor: barColor(a.color) }}>
      {a.pretext && <Markdown text={a.pretext} me={me} onLink={onLink} />}
      {a.author_name && <div className="text-xs font-medium text-neutral-600">{a.author_name}</div>}
      {a.title &&
        (a.title_link ? (
          <a href={a.title_link} className="font-semibold text-blue-700 hover:underline" onClick={(e) => { e.preventDefault(); onLink(a.title_link!) }}>
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
      {a.footer && <div className="mt-0.5 text-xs text-neutral-500">{a.footer}</div>}
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
        className="w-full resize-none rounded border border-blue-400 px-2 py-1 focus:outline-none"
      />
      <div className="flex items-center gap-3 text-xs">
        <button className="rounded bg-blue-600 px-2 py-0.5 text-white disabled:opacity-50" disabled={busy} onClick={() => void save()}>
          {t('post.save')}
        </button>
        <button className="underline" onClick={actions.cancelEdit}>
          {t('post.cancel')}
        </button>
        {error && (
          <span role="alert" className="text-red-600">
            {error}
          </span>
        )}
      </div>
    </div>
  )
}

function ToolButton({ label, onClick, children }: { label: string; onClick(): void; children: React.ReactNode }) {
  return (
    <button aria-label={label} title={label} onClick={onClick} className="rounded px-1.5 py-0.5 text-neutral-600 hover:bg-neutral-100">
      {children}
    </button>
  )
}

export const PostItem = memo(function PostItem({ post, head, me, locale, crt, actions, editing }: Props) {
  const time = formatTime(post.create_at, locale)
  return (
    <article
      data-post-id={post.id}
      className={`group relative flex gap-3 px-4 py-0.5 hover:bg-neutral-50 ${head ? 'mt-2' : ''} ${post.pending ? 'opacity-60' : ''}`}
    >
      <div className="w-9 shrink-0 pt-0.5">
        {head ? <Avatar id={post.user_id} name={post.author} /> : <time className="invisible block pt-1 text-right text-[10px] text-neutral-400 group-hover:visible">{time}</time>}
      </div>
      <div className="min-w-0 flex-1">
        {head && (
          <header className="flex items-baseline gap-2">
            <span className="font-semibold">{post.author}</span>
            {post.bot && <span className="rounded bg-neutral-200 px-1 text-[10px] font-semibold text-neutral-600">BOT</span>}
            <time className="text-xs text-neutral-500">{time}</time>
          </header>
        )}
        {editing ? (
          <EditBox post={post} actions={actions} />
        ) : (
          <div className={post.system ? 'italic text-neutral-500' : ''}>
            {post.message && <Markdown text={post.message} me={me.username} onLink={actions.link} />}
            {post.edit_at ? <span className="text-xs text-neutral-400">{t('post.edited')}</span> : null}
          </div>
        )}
        {post.attachments?.map((a, i) => <AttachmentView key={i} a={a} me={me.username} onLink={actions.link} />)}
        {post.files && post.files.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-2">
            {post.files.map((f, i) => (
              <div key={i} className="flex items-center gap-2 rounded border border-neutral-200 px-2 py-1 text-xs">
                <span aria-hidden>📎</span>
                <span className="max-w-64 truncate">{f.name}</span>
                <span className="text-neutral-500">{formatSize(f.size)}</span>
              </div>
            ))}
          </div>
        )}
        {post.reactions && post.reactions.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {post.reactions.map((r) => (
              <span key={r.emoji} title={`:${r.emoji}:`} className={`rounded-full border px-1.5 text-xs ${r.mine ? 'border-blue-400 bg-blue-50' : 'border-neutral-200'}`}>
                {emojiFor(r.emoji)} {r.count}
              </span>
            ))}
          </div>
        )}
        {crt && (post.reply_count ?? 0) > 0 && <div className="mt-0.5 text-xs font-medium text-blue-700">{t('post.replies', { n: String(post.reply_count) })}</div>}
        {post.pending && <div className="text-xs text-neutral-500">{t('post.sending')}</div>}
        {post.failed && (
          <div role="alert" className="flex gap-2 text-xs text-red-600">
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
          className="absolute -top-3 right-3 hidden gap-0.5 rounded border border-neutral-200 bg-white px-1 shadow-sm group-focus-within:flex group-hover:flex"
        >
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
    </article>
  )
})
