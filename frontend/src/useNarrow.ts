import { useEffect, useState } from 'react'

export const NARROW_QUERY = '(max-width: 1023px)'

// useNarrow: below 1024px window width the thread panel overlays the feed
// instead of sitting beside it (ThreadPane.tsx) and, for the same reason,
// its splitter is not offered (App.tsx — theme brief 2026-09-28 scope 3a:
// "In narrow-window overlay mode the thread panel is not resizable").
// Guarded for environments without matchMedia (defensive only — not
// expected in a real browser or WebKitGTK).
export function useNarrow(): boolean {
  const supported = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
  const [narrow, setNarrow] = useState(() => (supported ? window.matchMedia(NARROW_QUERY).matches : false))
  useEffect(() => {
    if (!supported) return
    const mql = window.matchMedia(NARROW_QUERY)
    const onChange = () => setNarrow(mql.matches)
    onChange()
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [supported])
  return narrow
}
