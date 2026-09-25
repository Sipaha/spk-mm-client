import { useEffect, useState } from 'react'

export type TextState = { status: 'loading' } | { status: 'error' } | { status: 'ok'; text: string; truncated: boolean }

// useTextFile loads a text snippet from /media/…/text/<id> (same origin; in
// browser mode the page cookie authorizes it). The response is immutable,
// so the viewer re-reading the same URL hits the webview cache.
export function useTextFile(url: string): TextState {
  const [res, setRes] = useState<{ url: string; state: TextState }>({ url, state: { status: 'loading' } })
  useEffect(() => {
    const ac = new AbortController()
    fetch(url, { signal: ac.signal, credentials: 'same-origin' })
      .then(async (r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return { status: 'ok' as const, text: await r.text(), truncated: r.headers.get('X-Truncated') === '1' }
      })
      .then(
        (state) => setRes({ url, state }),
        () => {
          if (!ac.signal.aborted) setRes({ url, state: { status: 'error' } })
        },
      )
    return () => ac.abort()
  }, [url])
  return res.url === url ? res.state : { status: 'loading' }
}
