import { useEffect } from 'react'
import { ApiError, client, isDesktop } from './api/client'
import type { ServerDTO } from './api/types'
import { loadSidebar, openChannel, openFromNotification, refreshChannel, refreshServers, report, selectServer } from './chat'
import { AddServerForm } from './components/AddServerForm'
import { ChannelPane } from './components/ChannelPane'
import { ServerPanel } from './components/ServerPanel'
import { ServerRail } from './components/ServerRail'
import { Sidebar } from './components/Sidebar'
import { errorMessage } from './errors'
import { t } from './i18n'
import { useStore } from './store'

export function App() {
  const { servers, selectedId, lastError, loginFailures, signInFor, sidebar, channel, notice, setError, loginFailed, setInfo, showSignIn, setNotice } = useStore()

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
        case 'open_channel':
          openFromNotification(Number(p.server_id), String(p.channel_id))
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

  useEffect(() => {
    if (!notice) return
    const timer = setTimeout(() => {
      if (useStore.getState().notice === notice) setNotice(null)
    }, 8000)
    return () => clearTimeout(timer)
  }, [notice, setNotice])

  const selected = servers.find((s) => s.id === selectedId)
  const chat = selected && selected.signed_in && signInFor !== selected.id
  const signOut = (s: ServerDTO) => client.logout(s.id).catch(report)
  const remove = (s: ServerDTO) => {
    if (confirm(t('server.removeConfirm', { name: s.name }))) client.removeServer(s.id).catch(report)
  }
  const banner = lastError && (
    <p role="alert" className="flex items-center justify-between bg-danger/15 px-4 py-2 text-sm text-danger">
      {lastError}
      <button aria-label={t('app.dismiss')} className="px-2" onClick={() => setError(null)}>
        ×
      </button>
    </p>
  )
  const info = notice && (
    <p role="status" className="flex items-center justify-between bg-hover px-4 py-2 text-sm text-fg">
      <span className="min-w-0 break-all">{notice}</span>
      <button aria-label={t('app.dismiss')} className="px-2" onClick={() => setNotice(null)}>
        ×
      </button>
    </p>
  )

  return (
    <div className="flex h-screen bg-app text-sm text-fg">
      <ServerRail servers={servers} selectedId={selectedId} onSelect={selectServer} />
      {chat ? (
        <>
          <Sidebar
            server={selected}
            sidebar={sidebar}
            activeChannelId={channel?.id ?? null}
            onTeam={(teamId) => void loadSidebar(selected.id, teamId, true)}
            onChannel={(channelId) => void openChannel(selected.id, channelId)}
            onSignOut={() => void signOut(selected)}
            onRemove={() => remove(selected)}
            onReauth={() => showSignIn(selected.id)}
          />
          <main className="flex min-w-0 flex-1 flex-col">
            {banner}
            {info}
            <ChannelPane server={selected} channel={channel} onReauth={() => showSignIn(selected.id)} />
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
    </div>
  )
}
