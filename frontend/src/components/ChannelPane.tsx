import type { ChannelDTO, ServerDTO } from '../api/types'
import { t } from '../i18n'
import { channelGlyph } from './glyph'

export function ChannelPane({ server, channel, onReauth }: { server: ServerDTO; channel: ChannelDTO | null; onReauth(): void }) {
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
      <div className="min-h-0 flex-1" />
    </section>
  )
}
