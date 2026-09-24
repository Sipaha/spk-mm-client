import { useState } from 'react'
import type { Client } from '../api/client'
import type { ServerDTO } from '../api/types'
import { errorMessage } from '../errors'
import { t } from '../i18n'

// loginFailures counts login_failed events; any change ends the GitLab wait.
export function ServerPanel({ server, client, loginFailures = 0 }: { server: ServerDTO; client: Client; loginFailures?: number }) {
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [waitingGitLab, setWaitingGitLab] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // The GitLab wait ends when the login fails or the session state changes
  // (adjusting state during render, not in an effect).
  const [seen, setSeen] = useState({ loginFailures, signedIn: server.signed_in })
  if (seen.loginFailures !== loginFailures || seen.signedIn !== server.signed_in) {
    setSeen({ loginFailures, signedIn: server.signed_in })
    setWaitingGitLab(false)
  }

  // run disables every action button until fn settles: no double submit.
  const run = async (fn: () => Promise<unknown>) => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="mx-auto mt-16 flex w-[28rem] flex-col gap-4">
      <header>
        <h1 className="text-xl font-semibold">{server.name}</h1>
        <p className="text-sm text-neutral-500">{server.url}</p>
      </header>
      {server.signed_in ? (
        <div className="flex items-center justify-between">
          <span>{t('server.signedInAs', { name: server.username })}</span>
          <button className="rounded border px-3 py-1 disabled:opacity-50" disabled={busy} onClick={() => run(() => client.logout(server.id))}>
            {t('server.signOut')}
          </button>
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <p className="text-sm text-neutral-500">{t('server.signedOut')}</p>
          {server.gitlab && (
            <>
              <button
                className="rounded bg-orange-600 px-3 py-1.5 text-white disabled:opacity-50"
                disabled={busy}
                onClick={() => run(async () => { await client.startGitLabLogin(server.id); setWaitingGitLab(true) })}
              >
                {t('server.gitlab')}
              </button>
              {waitingGitLab && <p className="text-sm text-neutral-600">{t('server.gitlabWaiting')}</p>}
              <p className="text-center text-xs text-neutral-400">{t('server.orPassword')}</p>
            </>
          )}
          <form
            className="flex flex-col gap-2"
            onSubmit={(e) => { e.preventDefault(); void run(() => client.loginWithPassword(server.id, login, password)) }}
          >
            <label className="flex flex-col gap-1 text-sm">
              {t('server.login')}
              <input className="rounded border border-neutral-300 px-2 py-1.5" value={login} onChange={(e) => setLogin(e.target.value)} />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t('server.password')}
              <input type="password" className="rounded border border-neutral-300 px-2 py-1.5" value={password} onChange={(e) => setPassword(e.target.value)} />
            </label>
            <button type="submit" disabled={busy || !login || !password} className="rounded bg-blue-600 px-3 py-1.5 text-white disabled:opacity-50">
              {t('server.signIn')}
            </button>
          </form>
        </div>
      )}
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <button
        className="self-start text-sm text-red-600 underline disabled:opacity-50"
        disabled={busy}
        onClick={() => { if (confirm(t('server.removeConfirm', { name: server.name }))) void run(() => client.removeServer(server.id)) }}
      >
        {t('server.remove')}
      </button>
    </section>
  )
}
