import { useEffect } from 'react'
import { t } from '../i18n'
import { useStore } from '../store'
import { IconClose } from './icons'

// How long a toast stays unless dismissed.
export const TOAST_MS = 6000

// Toast: a short floating message — a download's error, "saved but not
// opened" — over the bottom right of the rightmost feed (Feed's overlay:
// the thread panel's when one is open, else the channel's). Absolute in the
// feed's box, so it takes no space in the layout (the old download banner
// above the feed pushed the conversation down: user report 2026-09-29),
// always sits above the composer whatever its height, and right-16 keeps it
// clear of the jump-to-latest button (right-5, 36 px wide) — review M3. The
// live region is mounted with its feed so a screen reader announces each
// new message.
export function Toast() {
  const toast = useStore((s) => s.toast)
  const dismiss = useStore((s) => s.dismissToast)
  useEffect(() => {
    if (!toast) return
    const timer = setTimeout(() => dismiss(toast.id), TOAST_MS)
    return () => clearTimeout(timer)
  }, [toast, dismiss])
  return (
    <div data-testid="toast-region" role="status" aria-live="polite" className="pointer-events-none absolute bottom-4 left-4 right-16 z-20 flex flex-col items-end">
      {toast && (
        <div
          data-tone={toast.tone}
          className={`pointer-events-auto flex max-w-sm items-start gap-2 rounded-md px-3 py-2 text-sm shadow-lg ring-1 ${toast.tone === 'error' ? 'bg-panel text-danger ring-danger/40' : 'bg-panel text-fg ring-line'}`}
        >
          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{toast.text}</span>
          <button type="button" aria-label={t('app.dismiss')} title={t('app.dismiss')} className="flex shrink-0 items-center justify-center rounded px-1 text-fg-muted hover:bg-hover hover:text-fg" onClick={() => dismiss(toast.id)}>
            <IconClose size={16} />
          </button>
        </div>
      )}
    </div>
  )
}

// Announcer: a visually hidden polite live region for screen-reader-only
// messages — a finished download (the card's ✓ is visual only; review M2).
// Always mounted (App), so each new message is read out.
export function Announcer() {
  const a = useStore((s) => s.announcement)
  return (
    <div data-testid="announcer" role="status" aria-live="polite" className="sr-only">
      {a && <span key={a.id}>{a.text}</span>}
    </div>
  )
}
