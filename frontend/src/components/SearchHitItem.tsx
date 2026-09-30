import { memo, useState } from 'react'
import type { EmojiDTO, SearchHit } from '../api/types'
import { errorMessage } from '../errors'
import { formatTime } from '../format'
import { t } from '../i18n'
import { IconAttach } from './icons'
import { Markdown } from './Markdown'
import { PostAvatar } from './PostAvatar'
import { hitTerms } from './remarkHighlight'

// channelLabel: where a hit is, as in: names it — "~town-square", "@bob",
// a GM by its members (spec «Поиск», Секция 2); '' for a channel the client
// does not know yet.
export function channelLabel(hit: Pick<SearchHit, 'channel_name' | 'channel_display' | 'channel_type'>): string {
  switch (hit.channel_type) {
    case 'O':
    case 'P':
      return hit.channel_name ? `~${hit.channel_name}` : hit.channel_display
    case 'D':
      return hit.channel_name ? `@${hit.channel_name}` : hit.channel_display
    default:
      return hit.channel_display
  }
}

interface Props {
  serverId: number
  hit: SearchHit
  terms: string[] // the session's (a hit's own matches win — hitTerms)
  me: string
  locale: string
  onOpen(hit: SearchHit): Promise<unknown>
  onLink(href: string): void
  emojiInfo(): Promise<EmojiDTO>
}

// SearchHitItem is one result: the post as the feed shows its head
// (avatar, author, time) plus its channel, its text with the search's
// words marked, its files by name and its reply count. A click (not on a
// link), Enter or "Jump" goes to the post; a failed jump (deleted, no
// access, offline) shows here, the other results stay. A hit whose channel
// the client does not know yet (jumpable false) cannot be opened — until a
// sidebar refresh brings its channel (store.ts withSidebar).
export const SearchHitItem = memo(function SearchHitItem({ serverId, hit, terms, me, locale, onOpen, onLink, emojiInfo }: Props) {
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const open = async () => {
    if (!hit.jumpable || busy) return
    setError(null)
    setBusy(true)
    try {
      await onOpen(hit)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const highlight = hitTerms(hit, terms)
  const label = channelLabel(hit)
  const files = hit.files ?? []
  const attachText = (hit.attachments ?? []).map((a) => a.title || a.fallback || a.pretext || '').filter(Boolean)
  return (
    <article
      data-hit-id={hit.id}
      // The card is the jump's button; links in its text stay links (their
      // clicks are not the card's — see onClick).
      role="button"
      tabIndex={0}
      aria-disabled={!hit.jumpable || undefined}
      aria-label={`${hit.author}, ${formatTime(hit.create_at, locale)}${label ? `, ${label}` : ''}`}
      onClick={(e) => {
        if ((e.target as HTMLElement).closest('a, button')) return
        // Text selected with the mouse inside the card: a copy, not a jump.
        const sel = window.getSelection()
        if (sel && !sel.isCollapsed && sel.toString() !== '' && e.currentTarget.contains(sel.anchorNode)) return
        void open()
      }}
      onKeyDown={(e) => {
        if (e.key !== 'Enter' || e.target !== e.currentTarget) return
        e.preventDefault()
        void open()
      }}
      className={`group relative flex gap-3 px-4 py-2 text-fg focus:outline-none focus-visible:bg-hover ${
        hit.jumpable ? 'cursor-pointer hover:bg-hover' : 'cursor-default'
      }`}
    >
      <div className={`flex w-9 shrink-0 justify-end pt-0.5 ${hit.jumpable ? '' : 'opacity-60'}`}>
        <PostAvatar serverId={serverId} post={hit} size={32} />
      </div>
      <div className="min-w-0 flex-1">
        <header className="mb-0.5 flex min-w-0 items-baseline gap-2">
          <span className="shrink-0 font-semibold" title={hit.real_author}>
            {hit.author}
          </span>
          {hit.bot && <span className="rounded bg-hover px-1 text-[10px] font-semibold text-fg-muted">BOT</span>}
          <time className="shrink-0 text-xs text-fg-muted">{formatTime(hit.create_at, locale)}</time>
          {label && (
            <span className="min-w-0 truncate text-xs text-fg-muted" title={hit.channel_display}>
              {label}
            </span>
          )}
          {hit.jumpable && (
            <button
              type="button"
              onClick={() => void open()}
              className="invisible ml-auto shrink-0 self-center rounded px-1.5 text-xs font-medium text-accent hover:underline focus:visible group-hover:visible group-focus:visible"
            >
              {t('search.jump')}
            </button>
          )}
        </header>
        <div className={hit.jumpable ? '' : 'opacity-60'}>
          {hit.message && <Markdown text={hit.message} me={me} onLink={onLink} serverId={serverId} emojiInfo={emojiInfo} highlight={highlight} />}
          {attachText.length > 0 && <div className="truncate text-xs text-fg-muted">{attachText.join(' · ')}</div>}
          {files.length > 0 && (
            <div className="mt-0.5 flex min-w-0 items-center gap-1 text-xs text-fg-muted">
              <IconAttach size={14} className="shrink-0" />
              <span className="truncate">{files.map((f) => f.name).join(', ')}</span>
            </div>
          )}
          {!hit.root_id && (hit.reply_count ?? 0) > 0 && (
            <div className="mt-0.5 text-xs text-fg-muted">{t('search.replies', { n: String(hit.reply_count) })}</div>
          )}
        </div>
        {!hit.jumpable && <div className="mt-0.5 text-xs text-fg-subtle">{t('search.notJumpable')}</div>}
        {error && (
          <div role="alert" className="mt-0.5 text-xs text-danger">
            {error}
          </div>
        )}
      </div>
    </article>
  )
})
