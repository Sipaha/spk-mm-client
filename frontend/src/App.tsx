import { useEffect, useLayoutEffect, useState } from 'react'
import { ApiError, client, isDesktop } from './api/client'
import type { ServerDTO } from './api/types'
import {
  closeThread, loadSidebar, onAttachmentRefused, onAttachmentsChanged, onDownloadsChanged, openChannel,
  openFromNotification, refreshChannel, refreshServers, refreshThread, report, selectServer,
} from './chat'
import { AddServerForm } from './components/AddServerForm'
import { ChannelPane } from './components/ChannelPane'
import { ServerPanel } from './components/ServerPanel'
import { ServerRail } from './components/ServerRail'
import { Sidebar } from './components/Sidebar'
import { Splitter } from './components/Splitter'
import {
  RAIL_WIDTH, SIDEBAR_DEFAULT, THREAD_DEFAULT, clampSidebarWidth, clampThreadWidth, sidebarBounds, threadBounds,
} from './components/splitter'
import { SearchPane } from './components/SearchPane'
import { QuickSwitcher } from './components/QuickSwitcher'
import { ThreadPane } from './components/ThreadPane'
import { Announcer, Toast } from './components/Toast'
import { IconClose } from './components/icons'
import { errorMessage } from './errors'
import { t } from './i18n'
import { useStore } from './store'
import { useNarrow } from './useNarrow'

// useWindowWidth: only used to re-clamp the splitters on resize (theme
// brief 2026-09-28 scope 3a) — rAF-throttled like Feed.tsx's own resize-
// driven work, not a per-pixel state stream.
function useWindowWidth(): number {
  const [width, setWidth] = useState(() => window.innerWidth)
  useEffect(() => {
    let raf = 0
    const onResize = () => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => setWidth(window.innerWidth))
    }
    window.addEventListener('resize', onResize)
    return () => {
      window.removeEventListener('resize', onResize)
      cancelAnimationFrame(raf)
    }
  }, [])
  return width
}

export function App() {
  const {
    servers, selectedId, lastError, loginFailures, signInFor, sidebar, channel, thread, rhs, search, heldChannel, setError, loginFailed, setInfo,
    showSignIn,
  } = useStore()

  // Sidebar/thread-panel splitter widths (theme brief 2026-09-28 scope 3a):
  // app-wide, not per server. Nothing here blocks startup — these defaults
  // render immediately, client.getLayout() below only overrides them once
  // it resolves.
  const [sidebarWidth, setSidebarWidthState] = useState(SIDEBAR_DEFAULT)
  const [threadWidth, setThreadWidthState] = useState(THREAD_DEFAULT)
  const windowWidth = useWindowWidth()
  const narrow = useNarrow()
  // The right panel shows one thing at a time (rhs): a thread or the
  // search results — the same width, splitter and narrow overlay.
  const panel = rhs === 'thread' && thread ? 'thread' : rhs === 'search' && search ? 'search' : null
  const threadOpen = panel !== null && !narrow

  const selected = servers.find((s) => s.id === selectedId)
  const chat = selected && selected.signed_in && signInFor !== selected.id

  // Sidebar-menu-brief addendum (2026-09-29): the server rail (ServerRail
  // below) is redundant clutter while actually chatting with exactly one
  // server — there's nothing to pick between — so it's hidden then and the
  // sidebar/thread panel get its 64px back. It stays visible outside chat
  // (0 servers: today's add-server screen; not-yet-signed-in ServerPanel;
  // or right after "Add server" deselects the one server, selectedId turns
  // null and chat turns false) — those screens have no menu of their own to
  // reach the rail's "+"/other servers from, so hiding it there would be a
  // dead end. With 2+ servers it shows as before, everywhere.
  const showRail = servers.length !== 1 || !chat
  const railWidth = showRail ? RAIL_WIDTH : 0

  useEffect(() => {
    client
      .getLayout()
      .then((l) => {
        if (l.sidebar_width > 0) setSidebarWidthState((w) => clampSidebarWidth(l.sidebar_width, window.innerWidth, l.thread_width || w, railWidth))
        if (l.thread_width > 0) setThreadWidthState((w) => clampThreadWidth(l.thread_width, window.innerWidth, l.sidebar_width || w, railWidth))
      })
      .catch(() => {})
  }, [])

  // Keep both widths inside their (window-size-dependent) bounds as the
  // window resizes — the floor always wins, so on a very narrow window the
  // feed is what gives, not either pane going below its own minimum. The
  // rail appearing/disappearing (railWidth) reclamps them too, so crossing
  // the one-server boundary doesn't leave a stale width outside the new
  // bounds.
  useEffect(() => {
    setSidebarWidthState((w) => clampSidebarWidth(w, windowWidth, threadOpen ? threadWidth : 0, railWidth))
  }, [windowWidth, threadOpen, threadWidth, railWidth])
  useEffect(() => {
    if (!threadOpen) return
    setThreadWidthState((w) => clampThreadWidth(w, windowWidth, sidebarWidth, railWidth))
  }, [windowWidth, threadOpen, sidebarWidth, railWidth])

  // The CSS vars Sidebar.tsx/ThreadPane.tsx read (var(--spk-…, default)):
  // committed here so a resize-driven clamp (above) or the initial load
  // (above) is visible even though neither goes through Splitter's own
  // live rAF path.
  useLayoutEffect(() => {
    document.documentElement.style.setProperty('--spk-sidebar-width', `${sidebarWidth}px`)
  }, [sidebarWidth])
  useLayoutEffect(() => {
    document.documentElement.style.setProperty('--spk-thread-width', `${threadWidth}px`)
  }, [threadWidth])

  const commitSidebarWidth = (w: number) => {
    setSidebarWidthState(w)
    void client.setSidebarWidth(w).catch(() => {})
  }
  const commitThreadWidth = (w: number) => {
    setThreadWidthState(w)
    void client.setThreadWidth(w).catch(() => {})
  }

  useEffect(() => {
    client.appInfo().then(setInfo).catch(() => {})
    void refreshServers()
    return client.subscribeEvents((ev) => {
      const p = ev.payload ?? {}
      const s = useStore.getState()
      switch (ev.type) {
        case 'servers_changed':
          void refreshServers()
          break
        case 'sidebar_changed':
          if (p.server_id === s.selectedId) void loadSidebar(Number(p.server_id), s.sidebar?.team_id ?? '')
          break
        case 'channel_changed':
          void refreshChannel(Number(p.server_id), String(p.channel_id))
          break
        case 'thread_changed':
          void refreshThread(Number(p.server_id), String(p.root_id))
          break
        case 'open_channel':
          void openFromNotification(Number(p.server_id), String(p.channel_id), p.root_id ? String(p.root_id) : undefined)
          break
        case 'downloads_changed':
          onDownloadsChanged(p)
          break
        case 'attachments_changed':
          onAttachmentsChanged(p)
          break
        case 'attachment_refused':
          onAttachmentRefused(p)
          break
        case 'login_failed':
          loginFailed(errorMessage(new ApiError(String(p.code ?? 'internal'), '')))
          break
        case 'open_external':
          // Browser mode is dev/e2e only: window.open without a user gesture is
          // allowed under Playwright (popup blocking off) but may be blocked in a
          // regular browser — acceptable there. Desktop opens the OS browser in Go.
          if (!isDesktop()) window.open(String(p.url), '_blank')
          break
      }
    })
  }, [setInfo, loginFailed])

  useEffect(() => {
    // Go marks the open channel read only while the window is focused and visible.
    const set = (focused: boolean) => void client.setFocused(focused).catch(() => {})
    const visible = () => document.visibilityState === 'visible'
    const onFocus = () => set(visible())
    const onBlur = () => set(false)
    const onVisibility = () => set(visible() && document.hasFocus())
    const onOnline = () => void client.networkChanged().catch(() => {})
    set(visible() && document.hasFocus())
    window.addEventListener('focus', onFocus)
    window.addEventListener('blur', onBlur)
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('online', onOnline)
    return () => {
      window.removeEventListener('focus', onFocus)
      window.removeEventListener('blur', onBlur)
      document.removeEventListener('visibilitychange', onVisibility)
      window.removeEventListener('online', onOnline)
    }
  }, [])

  const signOut = (s: ServerDTO) => client.logout(s.id).catch(report)
  const remove = (s: ServerDTO) => {
    if (confirm(t('server.removeConfirm', { name: s.name }))) client.removeServer(s.id).catch(report)
  }
  const banner = lastError && (
    <p role="alert" className="flex items-center justify-between bg-danger/15 px-4 py-2 text-sm text-danger">
      {lastError}
      <button aria-label={t('app.dismiss')} className="flex items-center justify-center px-2" onClick={() => setError(null)}>
        <IconClose />
      </button>
    </p>
  )

  return (
    <div className="flex h-screen bg-app text-sm text-fg">
      <QuickSwitcher />
      {showRail && <ServerRail servers={servers} selectedId={selectedId} onSelect={selectServer} />}
      {chat ? (
        <>
          <Sidebar
            server={selected}
            sidebar={sidebar}
            activeChannelId={channel?.id ?? null}
            held={heldChannel}
            onTeam={(teamId) => void loadSidebar(selected.id, teamId, true)}
            onChannel={(channelId) => void openChannel(selected.id, channelId)}
            onSignOut={() => void signOut(selected)}
            onRemove={() => remove(selected)}
            onReauth={() => showSignIn(selected.id)}
            onAddServer={() => selectServer(null)}
          />
          <Splitter
            value={sidebarWidth}
            {...sidebarBounds(windowWidth, threadOpen ? threadWidth : 0, railWidth)}
            defaultValue={SIDEBAR_DEFAULT}
            sign={1}
            cssVar="--spk-sidebar-width"
            label={t('layout.resizeSidebar')}
            onCommit={commitSidebarWidth}
          />
          <main className="flex min-w-0 flex-1 flex-col">
            {banner}
            <div className="relative flex min-h-0 flex-1">
              <ChannelPane server={selected} channel={channel} onReauth={() => showSignIn(selected.id)} />
              {panel && (
                <>
                  {!narrow && (
                    <Splitter
                      value={threadWidth}
                      {...threadBounds(windowWidth, sidebarWidth, railWidth)}
                      defaultValue={THREAD_DEFAULT}
                      sign={-1}
                      cssVar="--spk-thread-width"
                      label={t(panel === 'search' ? 'layout.resizeSearch' : 'layout.resizeThread')}
                      onCommit={commitThreadWidth}
                    />
                  )}
                  {panel === 'thread' && thread ? (
                    <ThreadPane server={selected} thread={thread} onClose={() => closeThread(selected.id)} />
                  ) : (
                    search && <SearchPane server={selected} search={search} />
                  )}
                </>
              )}
            </div>
          </main>
        </>
      ) : (
        <main className="flex-1 overflow-auto">
          {banner}
          {selected ? (
            <ServerPanel
              key={selected.id}
              server={selected}
              client={client}
              loginFailures={loginFailures}
              reauth={signInFor === selected.id}
              onCancel={signInFor === selected.id ? () => showSignIn(null) : undefined}
            />
          ) : (
            <AddServerForm add={client.addServer} onAdded={selectServer} />
          )}
        </main>
      )}
      <Toast />
      <Announcer />
    </div>
  )
}
