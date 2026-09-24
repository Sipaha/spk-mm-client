import { useMemo } from 'react'
import type { ChannelDTO, ServerDTO } from '../api/types'
import { discardPost, loadOlder, openLink, retryPost } from '../chat'
import { formatLocale } from '../format'
import { t } from '../i18n'
import { Feed } from './Feed'
import { channelGlyph } from './glyph'
import type { PostActions } from './PostItem'

export function ChannelPane({ server, channel, onReauth }: { server: ServerDTO; channel: ChannelDTO | null; onReauth(): void }) {
  const channelId = channel?.id ?? ''
  // Stable per channel: PostItem is memoized on its props.
  const actions = useMemo<PostActions>(
    () => ({
      link: openLink,
      retry: (p) => retryPost(server.id, channelId, p.id),
      discard: (p) => discardPost(server.id, channelId, p.id),
    }),
    [server.id, channelId],
  )
  const me = useMemo(() => ({ id: channel?.me_id ?? '', username: server.username }), [channel?.me_id, server.username])
  if (!channel) {
    return <div className="flex flex-1 items-center justify-center text-neutral-500">{t('channel.none')}</div>
  }
  return (
    <section aria-label={channel.name} className="flex min-h-0 flex-1 flex-col">
      <header className="flex min-w-0 items-baseline gap-3 border-b border-neutral-200 px-4 py-2">
        <h1 className="shrink-0 font-semibold">
          <span className="mr-1 text-neutral-400">{channelGlyph(channel.type)}</span>
          {channel.name}
        </h1>
        {channel.header && (
          <p className="truncate text-xs text-neutral-500" title={channel.header}>
            {channel.header}
          </p>
        )}
      </header>
      {server.state === 'needs_reauth' && (
        <div role="status" className="flex items-center gap-3 bg-amber-50 px-4 py-1.5 text-sm text-amber-900">
          {t('channel.sessionExpired')}
          <button className="font-medium underline" onClick={onReauth}>
            {t('status.signInAgain')}
          </button>
        </div>
      )}
      {channel.loaded && channel.syncing && server.state !== 'needs_reauth' && (
        <div role="status" className="border-b border-neutral-100 px-4 py-0.5 text-xs text-neutral-500">
          {t('channel.syncing')}
        </div>
      )}
      <Feed key={channel.id} channel={channel} me={me} locale={formatLocale()} actions={actions} onLoadOlder={() => loadOlder(server.id, channel.id)} />
    </section>
  )
}
