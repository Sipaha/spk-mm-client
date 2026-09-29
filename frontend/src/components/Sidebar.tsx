import { useEffect, useRef, useState } from 'react'
import type { CategoryView, ChannelItem, ServerDTO, SidebarDTO } from '../api/types'
import { t } from '../i18n'
import { Avatar, presenceLabel } from './Avatar'
import { ChannelTypeMarker, IconChevronDown, IconChevronRight, IconMore } from './icons'
import { useMenuA11y } from './menuA11y'

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
    <span aria-label={t('rail.mentions', { n: String(n) })} className="ml-auto rounded-full bg-danger px-1.5 text-[10px] font-bold leading-4 text-danger-fg">
      {n > 99 ? '99+' : n}
    </span>
  )
}

// Mattermost-style tone (theme brief scope 2): read channels use the dimmer
// --color-sidebar-fg; unread channels are bold, full --color-sidebar-fg-
// unread; the active channel gets a 3px accent left border plus a tinted
// background instead of a solid fill — border-transparent on the other two
// tones keeps the same 3px reserved so switching tones never shifts layout.
function rowTone(active: boolean, unread: boolean): string {
  if (active) return 'border-l-[3px] border-sidebar-active-border bg-sidebar-active-bg pl-[9px] text-sidebar-fg-unread'
  if (unread) return 'border-l-[3px] border-transparent pl-[9px] font-semibold text-sidebar-fg-unread'
  return 'border-l-[3px] border-transparent pl-[9px] text-sidebar-fg'
}

function ChannelRow({ serverId, item, active, onClick }: { serverId: number; item: ChannelItem; active: boolean; onClick(): void }) {
  const tone = rowTone(active, item.unread)
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
      className={`flex w-full items-center gap-2 rounded py-1 pr-3 text-left hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent ${tone} ${item.muted ? 'opacity-50' : ''}`}
    >
      {person ? (
        <Avatar serverId={serverId} userId={item.user_id!} version={item.avatar} name={item.name} status={status} size={20} surface="sidebar" />
      ) : (
        <span className="flex w-4 shrink-0 items-center justify-center text-xs opacity-70">
          <ChannelTypeMarker type={item.type} size={14} />
        </span>
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

// ServerMenu is the header's "⋯" dropdown (sign out / remove server). Same
// keyboard/focus/outside-click contract as PostMenu/FormattingMenu, via the
// shared useMenuA11y (menuA11y.ts) — plus two closes of its own, because
// unlike a post's toolbar this menu sits in a header that outlives it: a
// window blur (the user alt-tabbed away) and a server/channel switch (this
// component keeps rendering with new props instead of unmounting, so a
// stale menu would otherwise linger over the next server's header — see the
// effect in Sidebar below).
function ServerMenu({
  anchorEl,
  onSignOut,
  onRemove,
  onClose,
}: {
  anchorEl: HTMLElement
  onSignOut(): void
  onRemove(): void
  onClose(): void
}) {
  const { root, onMenuKey, onMenuBlur, closeAndFocusAnchor } = useMenuA11y(anchorEl, onClose)

  useEffect(() => {
    window.addEventListener('blur', onClose)
    return () => window.removeEventListener('blur', onClose)
  }, [onClose])

  return (
    <div
      ref={root}
      role="menu"
      aria-label={t('sidebar.menu')}
      className="absolute right-2 top-11 z-10 flex w-44 flex-col rounded border border-line bg-panel py-1 shadow-lg"
      onKeyDown={onMenuKey}
      onBlur={onMenuBlur}
    >
      <button type="button" role="menuitem" className="px-3 py-1.5 text-left text-fg hover:bg-hover" onClick={() => { closeAndFocusAnchor(); onSignOut() }}>
        {t('server.signOut')}
      </button>
      <button type="button" role="menuitem" className="px-3 py-1.5 text-left text-danger hover:bg-hover" onClick={() => { closeAndFocusAnchor(); onRemove() }}>
        {t('server.remove')}
      </button>
    </div>
  )
}

export function Sidebar(p: Props) {
  const [menu, setMenu] = useState(false)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const menuAnchor = useRef<HTMLButtonElement>(null)
  const teams = p.sidebar?.teams ?? []

  // A server or channel switch closes the menu (brief ruling 2026-09-29):
  // this component isn't remounted when either changes, so without this the
  // menu could stay open across the switch, anchored to the same button but
  // now floating over a different server/channel's header.
  useEffect(() => {
    setMenu(false)
  }, [p.server.id, p.activeChannelId])

  return (
    <aside
      aria-label={t('sidebar.label')}
      // Width comes from the --spk-sidebar-width CSS var the splitter drives
      // (App.tsx; theme brief scope 3a) — 256px (the old fixed w-64) until
      // the saved value (or a live drag) sets it.
      style={{ width: 'var(--spk-sidebar-width, 256px)' }}
      className="flex shrink-0 flex-col bg-sidebar text-sm text-fg-muted"
    >
      <header className="relative flex items-center justify-between gap-2 px-3 py-2">
        <div className="min-w-0">
          <div className="truncate font-semibold text-fg">{p.server.name}</div>
          <div className="truncate text-xs text-fg-subtle">@{p.server.username}</div>
        </div>
        <button
          ref={menuAnchor}
          aria-label={t('sidebar.menu')}
          aria-haspopup="menu"
          aria-expanded={menu}
          className="flex h-8 w-8 shrink-0 items-center justify-center rounded hover:bg-hover"
          onClick={() => setMenu(!menu)}
        >
          <IconMore />
        </button>
        {menu && menuAnchor.current && (
          <ServerMenu anchorEl={menuAnchor.current} onSignOut={p.onSignOut} onRemove={p.onRemove} onClose={() => setMenu(false)} />
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
                <span className="flex w-3 items-center">{isCollapsed ? <IconChevronRight size={12} /> : <IconChevronDown size={12} />}</span>
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
