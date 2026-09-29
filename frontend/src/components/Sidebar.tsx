import { useEffect, useRef, useState } from 'react'
import type { CategoryView, ChannelItem, ServerDTO, SidebarDTO } from '../api/types'
import { t } from '../i18n'
import { Avatar, presenceLabel } from './Avatar'
import { ChannelTypeMarker, IconArrowDown, IconChevronDown, IconChevronRight, IconMore } from './icons'
import { useMenuA11y } from './menuA11y'
import type { HeldChannel } from './sidebarSections'
import { computeSidebarSections } from './sidebarSections'
import { useUnreadOverflow } from './unreadOverflow'

interface Props {
  server: ServerDTO
  sidebar: SidebarDTO | null
  activeChannelId: string | null
  // held (sidebar-sections-brief.md, fix round 1): the active channel's
  // unread/mention snapshot from the moment it was opened — captured
  // centrally in chat.ts's openChannel (every switch's single gateway,
  // regardless of origin) and stored on `useStore`, not owned here.
  held: HeldChannel | null
  onTeam(teamId: string): void
  onChannel(channelId: string): void
  onSignOut(): void
  onRemove(): void
  onReauth(): void
  // onAddServer: the "⋯" menu's "Add server" item (sidebar-menu brief
  // addendum 2026-09-29) — opens the same add-server flow as the (now
  // possibly hidden, single-server) rail's "+" tile.
  onAddServer(): void
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
  // text-[11px] (Addendum 2, metrics parity): the official webapp's
  // #SidebarContainer .badge is 11px (sass/components/_badge.scss);
  // shape/colour stay ours (rounded-full + danger, an earlier decision).
  return (
    <span aria-label={t('rail.mentions', { n: String(n) })} className="ml-auto rounded-full bg-danger px-1.5 text-[11px] font-bold leading-4 text-danger-fg">
      {n > 99 ? '99+' : n}
    </span>
  )
}

// Mattermost-style tone (theme brief scope 2): read channels use the dimmer
// --color-sidebar-fg; unread channels are bold, full --color-sidebar-fg-
// unread; the active channel gets a 3px accent left border plus a tinted
// background instead of a solid fill — border-transparent on the other two
// tones keeps the same 3px reserved so switching tones never shifts layout.
// pl-[16px] (+ the 3px border) totals 19px, the official webapp's
// `.SidebarChannel .SidebarLink` padding-left (sidebar-unread-brief.md
// Addendum 2 — metrics parity; source: webapp 10.11
// sass/layout/_sidebar-left.scss).
function rowTone(active: boolean, unread: boolean): string {
  if (active) return 'border-l-[3px] border-sidebar-active-border bg-sidebar-active-bg pl-[16px] text-sidebar-fg-unread'
  if (unread) return 'border-l-[3px] border-transparent pl-[16px] font-semibold text-sidebar-fg-unread'
  return 'border-l-[3px] border-transparent pl-[16px] text-sidebar-fg'
}

// isCountableUnread: which channels count towards the "More unreads"/"More
// mentions" overflow pills (ruling 1's last bullet + ruling 2's detection
// scope) — unread or mentioned, and not muted unless it has a mention. Read,
// non-mentioned, non-muted, and muted-without-a-mention channels are never
// observed at all (ruling 2: "Observers only on unread rows").
function isCountableUnread(c: ChannelItem): boolean {
  return (c.unread || c.mentions > 0) && (!c.muted || c.mentions > 0)
}

function ChannelRow({
  serverId,
  item,
  active,
  onClick,
  rowRef,
}: {
  serverId: number
  item: ChannelItem
  active: boolean
  onClick(): void
  rowRef?: (el: HTMLButtonElement | null) => void
}) {
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
      ref={rowRef}
      aria-current={active}
      aria-label={label}
      onClick={onClick}
      // gap-1.5/py-1.5/pr-4 (Addendum 2, metrics parity): 6px icon↔text gap,
      // 32px row height (py-1.5's 6px top+bottom around the 20px avatar/
      // text-sm line-height), 16px right padding — the official webapp's
      // numbers (icon margin 0 6px 0 -2px, `.SidebarChannel .SidebarLink`
      // height:32px/padding:7px 16px 7px 19px — the left 19px comes from
      // rowTone's border+pl-[16px] above).
      className={`flex w-full items-center gap-1.5 rounded py-1.5 pr-4 text-left hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent ${tone} ${item.muted ? 'opacity-50' : ''}`}
    >
      {person ? (
        <Avatar serverId={serverId} userId={item.user_id!} version={item.avatar} name={item.name} status={status} size={20} surface="sidebar" />
      ) : (
        <span className="flex w-4 shrink-0 items-center justify-center text-xs opacity-70">
          <ChannelTypeMarker type={item.type} size={16} />
        </span>
      )}
      <span className="truncate">{item.name}</span>
      {item.mentions > 0 && <MentionPill n={item.mentions} />}
    </button>
  )
}

// OverflowPill: the "More unreads"/"More mentions" indicator (sidebar-
// unread-brief.md ruling 2). Absolutely positioned over the (non-scrolling)
// wrapper around the channel list — never in the scrolling element itself,
// so it stays put instead of scrolling away (same reason as Feed.tsx's
// jump-to-latest button). Always rendered, toggled via aria-hidden/opacity
// (fade ≤150ms, no layout shift) so it's a real, stable, labelled button —
// not conditionally mounted — for a11y and for the click handler to keep
// working through the fade-out.
function OverflowPill({ pos, visible, mention, onClick }: { pos: 'top' | 'bottom'; visible: boolean; mention: boolean; onClick(): void }) {
  const text = t(mention ? 'sidebar.moreMentions' : 'sidebar.moreUnreads')
  const label = t(mention ? (pos === 'top' ? 'sidebar.moreMentionsAbove' : 'sidebar.moreMentionsBelow') : pos === 'top' ? 'sidebar.moreUnreadsAbove' : 'sidebar.moreUnreadsBelow')
  return (
    <button
      type="button"
      onClick={onClick}
      aria-hidden={!visible}
      aria-label={label}
      tabIndex={visible ? 0 : -1}
      // inset-x-2/mx-auto/w-fit (fix round 1, review 2026-09-29): centering
      // via `left-1/2 -translate-x-1/2` on a width:auto absolutely
      // positioned element makes the browser's shrink-to-fit algorithm
      // compute the *available* width as containing-block width minus
      // `left` (the transform is paint-time-only, not part of layout) —
      // roughly half the sidebar's width — which wrapped "More mentions"
      // onto two lines, overlapping the row beneath it, at the sidebar's
      // own default 256px width (not just a narrow demo window). inset-x-2
      // constrains both edges equally instead, so shrink-to-fit never
      // enters the picture; whitespace-nowrap guarantees no wrap even if
      // that budget is ever tight; max-w-[calc(100%-1rem)] + the inner
      // span's truncate are a defensive fallback for a sidebar narrower
      // than the splitter's own 180px floor (theme-brief.md 3a). Together
      // these also keep the pill from ever reaching the scroller's
      // scrollbar, since its box can no longer grow past inset-x-2's
      // symmetric 8px margins.
      className={`absolute inset-x-2 z-10 mx-auto flex w-fit max-w-[calc(100%-1rem)] items-center gap-1.5 whitespace-nowrap rounded-full px-3 py-1 text-xs font-semibold shadow-lg ring-1 ring-line transition-opacity duration-150 ${pos === 'top' ? 'top-2' : 'bottom-2'} ${mention ? 'bg-mention-bg text-mention-fg' : 'bg-panel text-fg'} ${visible ? 'opacity-100' : 'pointer-events-none opacity-0'}`}
    >
      <IconArrowDown size={12} className={`shrink-0 ${pos === 'top' ? 'rotate-180' : ''}`} />
      <span className="min-w-0 truncate">{text}</span>
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
  onAddServer,
  onSignOut,
  onRemove,
  onClose,
}: {
  anchorEl: HTMLElement
  onAddServer(): void
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
      {/* Add server: reachable here regardless of server count (brief addendum) —
          the rail's own "+" tile disappears when exactly one server is configured. */}
      <button type="button" role="menuitem" className="px-3 py-1.5 text-left text-fg hover:bg-hover" onClick={() => { closeAndFocusAnchor(); onAddServer() }}>
        {t('rail.add')}
      </button>
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
  const [scrollerEl, setScrollerEl] = useState<HTMLDivElement | null>(null)
  const menuAnchor = useRef<HTMLButtonElement>(null)
  const teams = p.sidebar?.teams ?? []

  // A server or channel switch closes the menu (brief ruling 2026-09-29):
  // this component isn't remounted when either changes, so without this the
  // menu could stay open across the switch, anchored to the same button but
  // now floating over a different server/channel's header.
  useEffect(() => {
    setMenu(false)
  }, [p.server.id, p.activeChannelId])

  // sections: the Unreads category (sidebar-sections-brief.md) plus the
  // regular categories with those channels already removed — computed once,
  // pure, and covered by sidebarSections.test.ts (not re-derived per render
  // loop or duplicated between the JSX and the overflow-pill row list
  // below).
  const sections = computeSidebarSections(p.sidebar?.categories ?? null, p.held)

  // categoriesView: the same per-category "shown" filtering the render loop
  // below uses (collapsed categories still show their own unread rows —
  // though with the Unreads section above, any channel that would qualify
  // has already left this category entirely), computed once so the
  // overflow pills' countable-row list (ruling 2) can't drift from what's
  // actually rendered.
  const categoriesView = sections.categories.map((cat) => {
    const isCollapsed = collapsed[cat.id] ?? cat.collapsed
    const all = cat.channels ?? []
    const shown = isCollapsed ? all.filter((c) => c.unread || c.id === p.activeChannelId) : all
    return { cat, isCollapsed, shown }
  })
  const overflowRows = [
    ...sections.unread.filter(isCountableUnread).map((c) => ({ id: c.id, mentions: c.mentions })),
    ...categoriesView.flatMap(({ shown }) => shown.filter(isCountableUnread).map((c) => ({ id: c.id, mentions: c.mentions }))),
  ]
  const overflow = useUnreadOverflow(scrollerEl, overflowRows)

  return (
    <aside
      aria-label={t('sidebar.label')}
      // Width comes from the --spk-sidebar-width CSS var the splitter drives
      // (App.tsx; theme brief scope 3a) — 256px (the old fixed w-64) until
      // the saved value (or a live drag) sets it.
      style={{ width: 'var(--spk-sidebar-width, 256px)' }}
      className="flex shrink-0 flex-col bg-sidebar text-sm text-fg-muted"
    >
      {/* px-4 (Addendum 2, metrics parity): the official webapp's
          .sidebarHeaderContainer is `padding: 0 16px` (sidebar_header.scss). */}
      <header className="relative flex items-center justify-between gap-2 px-4 py-2">
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
          <ServerMenu anchorEl={menuAnchor.current} onAddServer={p.onAddServer} onSignOut={p.onSignOut} onRemove={p.onRemove} onClose={() => setMenu(false)} />
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
      {/* relative, non-scrolling wrapper: the overflow pills are its
          absolutely positioned children so they float over the visible area
          instead of scrolling away with the list inside (same reasoning as
          Feed.tsx's jump-to-latest button, which hit exactly this bug). */}
      <div className="relative min-h-0 flex-1">
        <div ref={setScrollerEl} className="h-full overflow-y-auto pb-4">
          {!p.sidebar && <p className="px-3 py-2 text-fg-muted">{t('sidebar.loading')}</p>}
          {sections.unread.length > 0 && (
            // Unreads (sidebar-sections-brief.md): always on, never
            // collapsible (SidebarCategoryHeaderStatic in the webapp has no
            // chevron), hidden entirely when empty. mt-1.5 matches the
            // regular categories' own top gap below.
            <section className="mt-1.5">
              {/* pl-4 (16px): the official static header has no chevron
                  (SidebarCategoryHeaderStatic renders no <i>), so
                  .SidebarChannelGroupHeader_text keeps its default
                  padding-left:16px instead of the collapsible headers'
                  padding-left:0 — the 16px column is blank space here,
                  keeping the label aligned with collapsible headers' text. */}
              <div className="flex h-8 w-full items-center pl-4 pr-3 text-left text-xs font-semibold uppercase tracking-wider text-fg-muted">
                {t('cat.unreads')}
              </div>
              <ul>
                {sections.unread.map((c) => (
                  <li key={c.id}>
                    <ChannelRow
                      serverId={p.server.id}
                      item={c}
                      active={c.id === p.activeChannelId}
                      onClick={() => p.onChannel(c.id)}
                      rowRef={isCountableUnread(c) ? overflow.rowRef(c.id) : undefined}
                    />
                  </li>
                ))}
              </ul>
            </section>
          )}
          {categoriesView.map(({ cat, isCollapsed, shown }) => (
            // mt-1.5 (Addendum 2, metrics parity): the official webapp's
            // .SidebarChannelGroup_content margin-bottom is 6px.
            <section key={cat.id} className="mt-1.5">
              <button
                aria-expanded={!isCollapsed}
                onClick={() => setCollapsed({ ...collapsed, [cat.id]: !isCollapsed })}
                // h-8 (32px, the official .SidebarChannelGroupHeader height)
                // and flush against the sidebar's left edge — no left
                // padding, no gap: the official webapp's
                // .SidebarChannelGroupHeader_groupButton has padding:0, its
                // chevron a max-width:16px column (w-4 here, centered like
                // the source's ~5px-inset glyph), and
                // .SidebarChannelGroupHeader_text right after it at
                // padding-left:0 (sass/layout/_sidebar-left.scss) — channel
                // rows are indented further (19px total via rowTone's
                // border+pl-16), not this same amount, by design (screenshot
                // /agents/tmp/sidebar-shots/ref-webapp-sections.png: the
                // header sits closer to the edge than its rows).
                className="flex h-8 w-full items-center pr-3 text-left text-xs font-semibold uppercase tracking-wider text-fg-muted hover:text-fg"
              >
                <span className="flex w-4 shrink-0 items-center justify-center">{isCollapsed ? <IconChevronRight size={12} /> : <IconChevronDown size={12} />}</span>
                {categoryName(cat)}
              </button>
              <ul>
                {shown.map((c) => (
                  <li key={c.id}>
                    <ChannelRow
                      serverId={p.server.id}
                      item={c}
                      active={c.id === p.activeChannelId}
                      onClick={() => p.onChannel(c.id)}
                      rowRef={isCountableUnread(c) ? overflow.rowRef(c.id) : undefined}
                    />
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
        <OverflowPill pos="top" visible={overflow.above} mention={overflow.aboveMentions} onClick={overflow.scrollToAbove} />
        <OverflowPill pos="bottom" visible={overflow.below} mention={overflow.belowMentions} onClick={overflow.scrollToBelow} />
      </div>
    </aside>
  )
}
