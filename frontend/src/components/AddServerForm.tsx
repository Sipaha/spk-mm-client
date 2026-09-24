import { useState } from 'react'
import type { ServerDTO } from '../api/types'
import { errorMessage } from '../errors'
import { t } from '../i18n'

export function AddServerForm(props: { add: (url: string) => Promise<Pick<ServerDTO, 'id'>>; onAdded: (id: number) => void }) {
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const s = await props.add(url)
      props.onAdded(s.id)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="mx-auto mt-24 flex w-96 flex-col gap-3 text-fg">
      <h1 className="text-lg font-semibold">{t('add.title')}</h1>
      <label className="flex flex-col gap-1 text-sm">
        {t('add.urlLabel')}
        <input
          className="rounded border border-line bg-app px-2 py-1.5 text-fg"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder={t('add.urlPlaceholder')}
          autoFocus
        />
      </label>
      {error && <p role="alert" className="text-sm text-danger">{error}</p>}
      <button type="submit" disabled={busy || !url.trim()} className="rounded bg-blue-600 px-3 py-1.5 text-white disabled:opacity-50">
        {busy ? t('add.checking') : t('add.submit')}
      </button>
    </form>
  )
}
