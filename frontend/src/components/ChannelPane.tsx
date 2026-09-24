import { useMemo } from 'react'
import type { ChannelDTO, ServerDTO } from '../api/types'
import { copyLink, deletePost, discardPost, editLastOwn, editPost, loadOlder, markUnread, openLink, retryPost, saveDraft, sendPost } from '../chat'
import { formatLocale } from '../format'
import { t } from '../i18n'
import { useStore } from '../store'
import { Composer } from './Composer'
import { Feed } from './Feed'
import { channelGlyph } from './glyph'
import type { PostActions } from './PostItem'

export function ChannelPane({ server, channel, onReauth }: { server: ServerDTO; channel: ChannelDTO | null; onReauth(): void }) {
  const channelId = channel?.id ?? ''
  const teamName = channel?.team_name ?? ''
  const editingId = useStore((s) => s.editingId)
  // Stable per channel: PostItem is memoized on its props.
  const actions = useMemo<PostActions>(
    () => ({
      link: openLink,
      retry: (p) => retryPost(server.id, channelId, p.id),
      discard: (p) => discardPost(server.id, channelId, p.id),
      edit: (p) => useStore.getState().setEditing(p.id),
      saveEdit: (p, message) => editPost(server.id, p.id, message),
      cancelEdit: () => useStore.getState().setEditing(null),
      remove: (p) => {
        if (confirm(t('post.deleteConfirm'))) deletePost(server.id, p.id)
      },
      markUnread: (p) => markUnread(server.id, p.id),
      copyLink: (p) => copyLink(server.url, teamName, p.id),
    }),
    [server.id, server.url, channelId, teamName],
  )
  const me = useMemo(() => ({ id: channel?.me_id ?? '', username: server.username }), [channel?.me_id, server.username])
  if (!channel) {
    return <div className="flex flex-1 items-center justify-center bg-app text-fg-muted">{t('channel.none')}</div>
  }
  return (
    <section aria-label={channel.name} className="flex min-h-0 flex-1 flex-col bg-app">
      <header className="flex min-w-0 items-baseline gap-3 border-b border-line bg-panel px-4 py-2">
        <h1 className="shrink-0 font-semibold text-fg">
          <span className="mr-1 text-fg-subtle">{channelGlyph(channel.type)}</span>
          {channel.name}
        </h1>
        {channel.header && (
          <p className="truncate text-xs text-fg-muted" title={channel.header}>
            {channel.header}
          </p>
        )}
      </header>
      {server.state === 'needs_reauth' && (
        <div role="status" className="flex items-center gap-3 bg-mention-bg px-4 py-1.5 text-sm text-mention-fg">
          {t('channel.sessionExpired')}
          <button className="font-medium underline" onClick={onReauth}>
            {t('status.signInAgain')}
          </button>
        </div>
      )}
      {channel.loaded && channel.syncing && server.state !== 'needs_reauth' && (
        <div role="status" className="border-b border-line px-4 py-0.5 text-xs text-fg-muted">
          {t('channel.syncing')}
        </div>
      )}
      <Feed key={`feed-${channel.id}`} channel={channel} me={me} locale={formatLocale()} actions={actions} editingId={editingId} onLoadOlder={() => loadOlder(server.id, channel.id)} />
      <Composer
        key={`composer-${channel.id}`}
        channel={channel}
        onSend={(m) => sendPost(server.id, channel.id, m)}
        onDraft={(text) => saveDraft(server.id, channel.id, text)}
        onEditLast={() => editLastOwn(channel)}
      />
    </section>
  )
}
