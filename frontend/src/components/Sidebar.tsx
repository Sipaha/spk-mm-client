import { useState } from 'react'
import type { CategoryView, ChannelItem, ServerDTO, SidebarDTO } from '../api/types'
import { t } from '../i18n'
import { Avatar, presenceLabel } from './Avatar'
import { channelGlyph } from './glyph'

interface Props {
  server: ServerDTO
  sidebar: SidebarDTO | null
  activeChannelId: string | null
  onTeam(teamId: string): void
  onChannel(channelId: string): void
  onSignOut(): void
  onRemove(): void
  onReauth(): void
}

function categoryName(c: CategoryView): string {
  switch (c.type) {
    case 'favorites':
      return t('cat.favorites')
    case 'channels':
      return t('cat.channels')
    case 'direct_messages':
      return t('cat.dms')
    default:
      return c.name
  }
}

function MentionPill({ n }: { n: number }) {
  return (
    <span aria-label={t('rail.mentions', { n: String(n) })} className="ml-auto rounded-full bg-red-600 px-1.5 text-[10px] font-bold leading-4 text-white">
      {n > 99 ? '99+' : n}
    </span>
  )
}

function ChannelRow({ serverId, item, active, onClick }: { serverId: number; item: ChannelItem; active: boolean; onClick(): void }) {
  const tone = active ? 'bg-blue-700 text-white' : item.unread ? 'font-semibold text-fg' : 'text-fg-subtle'
  const person = item.type === 'D' && item.user_id
  const status = item.bot ? '' : (item.status ?? '')
  // The presence goes into the button's name as a whole label: a visually
  // hidden span would be joined with a stray space by Chromium ("bob , Online").
  const label =
    person && status
      ? [item.name, presenceLabel(status), ...(item.mentions > 0 ? [t('rail.mentions', { n: String(item.mentions) })] : [])].join(', ')
      : undefined
  return (
    <button
      aria-current={active}
      aria-label={label}
      onClick={onClick}
      className={`flex w-full items-center gap-2 rounded px-3 py-1 text-left hover:bg-hover ${tone} ${item.muted ? 'opacity-50' : ''}`}
    >
      {person ? (
        <Avatar serverId={serverId} userId={item.user_id!} version={item.avatar} name={item.name} status={status} size={20} surface="sidebar" />
      ) : (
        <span className="w-4 shrink-0 text-center text-xs opacity-70">{channelGlyph(item.type)}</span>
      )}
      <span className="truncate">{item.name}</span>
      {item.mentions > 0 && <MentionPill n={item.mentions} />}
    </button>
  )
}

function StatusLine({ state, onReauth }: { state: ServerDTO['state']; onReauth(): void }) {
  if (state === 'connecting' || state === 'reconnecting') {
    return <p role="status" className="px-3 pb-2 text-xs text-mention-fg">{t(state === 'connecting' ? 'status.connecting' : 'status.reconnecting')}</p>
  }
  if (state === 'needs_reauth') {
    return (
      <p role="status" className="flex items-center gap-2 px-3 pb-2 text-xs text-mention-fg">
        {t('status.needsReauth')}
        <button className="rounded bg-mention-fg px-2 py-0.5 font-medium text-mention-bg" onClick={onReauth}>
          {t('status.signInAgain')}
        </button>
      </p>
    )
  }
  return null
}

export function Sidebar(p: Props) {
  const [menu, setMenu] = useState(false)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const teams = p.sidebar?.teams ?? []
  return (
    <aside aria-label={t('sidebar.label')} className="flex w-64 shrink-0 flex-col bg-sidebar text-sm text-fg-muted">
      <header className="relative flex items-center justify-between gap-2 px-3 py-2">
        <div className="min-w-0">
          <div className="truncate font-semibold text-fg">{p.server.name}</div>
          <div className="truncate text-xs text-fg-subtle">@{p.server.username}</div>
        </div>
        <button aria-label={t('sidebar.menu')} aria-expanded={menu} className="rounded px-2 text-lg hover:bg-hover" onClick={() => setMenu(!menu)}>
          ⋯
        </button>
        {menu && (
          <div role="menu" className="absolute right-2 top-11 z-10 flex w-44 flex-col rounded border border-line bg-panel py-1 shadow-lg">
            <button role="menuitem" className="px-3 py-1.5 text-left text-fg hover:bg-hover" onClick={() => { setMenu(false); p.onSignOut() }}>
              {t('server.signOut')}
            </button>
            <button role="menuitem" className="px-3 py-1.5 text-left text-danger hover:bg-hover" onClick={() => { setMenu(false); p.onRemove() }}>
              {t('server.remove')}
            </button>
          </div>
        )}
      </header>
      <StatusLine state={p.server.state} onReauth={p.onReauth} />
      {teams.length > 1 && (
        <nav aria-label={t('sidebar.teams')} className="flex flex-wrap gap-1 px-2 pb-2">
          {teams.map((tm) => (
            <button
              key={tm.id}
              aria-current={tm.id === p.sidebar?.team_id}
              onClick={() => p.onTeam(tm.id)}
              className={`flex items-center gap-1 rounded px-2 py-0.5 text-xs ${tm.id === p.sidebar?.team_id ? 'bg-hover text-fg' : tm.unread ? 'font-semibold text-fg' : 'text-fg-subtle'}`}
            >
              {tm.display_name}
              {tm.mentions > 0 && <MentionPill n={tm.mentions} />}
            </button>
          ))}
        </nav>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto pb-4">
        {!p.sidebar && <p className="px-3 py-2 text-fg-muted">{t('sidebar.loading')}</p>}
        {(p.sidebar?.categories ?? []).map((cat) => {
          const isCollapsed = collapsed[cat.id] ?? cat.collapsed
          const all = cat.channels ?? []
          const shown = isCollapsed ? all.filter((c) => c.unread || c.id === p.activeChannelId) : all
          return (
            <section key={cat.id} className="mt-3">
              <button
                aria-expanded={!isCollapsed}
                onClick={() => setCollapsed({ ...collapsed, [cat.id]: !isCollapsed })}
                className="flex w-full items-center gap-1 px-3 py-0.5 text-left text-xs font-semibold uppercase tracking-wide text-fg-muted hover:text-fg"
              >
                <span className="w-3">{isCollapsed ? '▸' : '▾'}</span>
                {categoryName(cat)}
              </button>
              <ul>
                {shown.map((c) => (
                  <li key={c.id}>
                    <ChannelRow serverId={p.server.id} item={c} active={c.id === p.activeChannelId} onClick={() => p.onChannel(c.id)} />
                  </li>
                ))}
              </ul>
            </section>
          )
        })}
      </div>
    </aside>
  )
}
