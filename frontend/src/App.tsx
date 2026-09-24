import { useEffect } from 'react'
import { ApiError, client, isDesktop } from './api/client'
import { AddServerForm } from './components/AddServerForm'
import { ServerPanel } from './components/ServerPanel'
import { ServerRail } from './components/ServerRail'
import { errorMessage } from './errors'
import { useStore } from './store'

export function App() {
  const { servers, selectedId, lastError, setServers, select, setError } = useStore()

  useEffect(() => {
    const refresh = () => client.listServers().then(setServers).catch((e) => setError(errorMessage(e)))
    refresh()
    return client.subscribeEvents((ev) => {
      if (ev.type === 'servers_changed') refresh()
      if (ev.type === 'login_failed') setError(errorMessage(new ApiError(String(ev.payload?.code ?? 'internal'), '')))
      // Browser mode is dev/e2e only: window.open without a user gesture is
      // allowed under Playwright (popup blocking off) but may be blocked in a
      // regular browser — acceptable there. Desktop opens the OS browser in Go.
      if (ev.type === 'open_external' && !isDesktop()) window.open(String(ev.payload?.url), '_blank')
    })
  }, [setServers, setError])

  const selected = servers.find((s) => s.id === selectedId)
  return (
    <div className="flex h-screen">
      <ServerRail servers={servers} selectedId={selectedId} onSelect={select} />
      <main className="flex-1 overflow-auto">
        {lastError && (
          <p role="alert" className="bg-red-50 px-4 py-2 text-sm text-red-700">
            {lastError}
          </p>
        )}
        {selected ? (
          <ServerPanel key={selected.id} server={selected} client={client} />
        ) : (
          <AddServerForm add={client.addServer} onAdded={select} />
        )}
      </main>
    </div>
  )
}
