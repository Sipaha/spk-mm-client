import { useEffect, useState } from 'react'

export type TextState = { status: 'loading' } | { status: 'error' } | { status: 'ok'; text: string; truncated: boolean }

// useTextFile loads a text snippet from /media/…/text/<id> (same origin; in
// browser mode the page cookie authorizes it). The response is immutable,
// so the viewer re-reading the same URL hits the webview cache. A failed
// load is tried again when epoch (the server's live epoch) changes.
export function useTextFile(url: string, epoch = 0): TextState {
  const [res, setRes] = useState<{ url: string; epoch: number; state: TextState }>({ url, epoch, state: { status: 'loading' } })
  const current = res.url === url
  const retry = current && res.state.status === 'error' && res.epoch !== epoch
  const attempt = current && !retry ? res.epoch : epoch
  useEffect(() => {
    const ac = new AbortController()
    fetch(url, { signal: ac.signal, credentials: 'same-origin' })
      .then(async (r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return { status: 'ok' as const, text: await r.text(), truncated: r.headers.get('X-Truncated') === '1' }
      })
      .then(
        (state) => setRes({ url, epoch: attempt, state }),
        () => {
          if (!ac.signal.aborted) setRes({ url, epoch: attempt, state: { status: 'error' } })
        },
      )
    return () => ac.abort()
  }, [url, attempt])
  return current && res.epoch === attempt ? res.state : { status: 'loading' }
}
