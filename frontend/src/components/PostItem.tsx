import { memo } from 'react'
import type { Attachment, PostView } from '../api/types'
import { formatSize, formatTime } from '../format'
import { t } from '../i18n'
import { emojiFor } from './emoji'
import { Markdown } from './Markdown'

export interface PostActions {
  link(href: string): void
  retry(post: PostView): void
  discard(post: PostView): void
}

interface Props {
  post: PostView
  head: boolean
  me: { id: string; username: string }
  locale: string
  crt: boolean
  actions: PostActions
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

export const PostItem = memo(function PostItem({ post, head, me, locale, crt, actions }: Props) {
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
        <div className={post.system ? 'italic text-neutral-500' : ''}>
          {post.message && <Markdown text={post.message} me={me.username} onLink={actions.link} />}
          {post.edit_at ? <span className="text-xs text-neutral-400">{t('post.edited')}</span> : null}
        </div>
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
    </article>
  )
})
