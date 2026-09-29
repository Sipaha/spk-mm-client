import { useEffect, useLayoutEffect, useSyncExternalStore, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { t } from '../i18n'
import { useStore } from '../store'
import { IconClose } from './icons'

// How long a toast stays unless dismissed.
export const TOAST_MS = 6000

// Toast hosts. The toast is one component (mounted once, in App) rendered
// into the best host there is: the open file viewer (a fixed z-50 modal —
// a toast under it was never seen: review R1), else the rightmost feed (the
// thread panel's over the channel's), else a fixed box over the window (no
// feed mounted yet — the add-server screen, a channel still loading: R4).
// One component means one live region, and moving between hosts (a channel
// switch, a thread opened or closed) keeps the timer running (R3).
export const TOAST_HOST = { channel: 0, thread: 1, viewer: 2 } as const
export type HostPlace = 'feed' | 'viewer'
interface Host {
  el: HTMLElement
  priority: number
  place: HostPlace
  seq: number
}
let hosts: Host[] = []
let hostSeq = 0
const listeners = new Set<() => void>()
const notify = () => listeners.forEach((l) => l())
const best = () => hosts.reduce<Host | null>((b, h) => (!b || h.priority > b.priority || (h.priority === b.priority && h.seq > b.seq) ? h : b), null)

// useToastHost registers the element as a toast host while mounted.
export function useToastHost(ref: RefObject<HTMLElement | null>, priority: number, place: HostPlace) {
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const h = { el, priority, place, seq: ++hostSeq }
    hosts = [...hosts, h]
    notify()
    return () => {
      hosts = hosts.filter((x) => x !== h)
      notify()
    }
  }, [ref, priority, place])
}

function useBestHost() {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => void listeners.delete(l)
    },
    best,
  )
}

// Where the toast sits in each host: in a feed, bottom right of its box —
// above the composer whatever its height, and right-16 keeps it clear of the
// jump-to-latest button (right-5, 36 px wide); in the viewer, its bottom
// right; with no host, fixed over the window's bottom right.
const PLACE: Record<HostPlace | 'window', string> = {
  feed: 'absolute bottom-4 left-4 right-16 z-20',
  viewer: 'absolute bottom-4 left-4 right-4 z-10',
  window: 'fixed bottom-24 right-4 z-50 max-w-sm',
}

// Toast: a short floating message — a download's error, "saved but not
// opened" — out of the layout flow (the old download banner above the feed
// pushed the conversation down: user report 2026-09-29). Over a feed the
// card lets the pointer through except on its close button, so the last
// post's toolbar under it stays usable (R2); elsewhere it takes clicks (in
// the viewer a click through it would land on the backdrop and close it).
export function Toast() {
  const toast = useStore((s) => s.toast)
  const dismiss = useStore((s) => s.dismissToast)
  const host = useBestHost()
  useEffect(() => {
    if (!toast) return
    const timer = setTimeout(() => dismiss(toast.id), TOAST_MS)
    return () => clearTimeout(timer)
  }, [toast, dismiss])
  const region = (
    <div data-testid="toast-region" role="status" aria-live="polite" className={`pointer-events-none flex flex-col items-end ${PLACE[host?.place ?? 'window']}`}>
      {toast && (
        <div
          data-tone={toast.tone}
          className={`${host?.place === 'feed' ? 'pointer-events-none' : 'pointer-events-auto'} flex max-w-sm items-start gap-2 rounded-md px-3 py-2 text-sm shadow-lg ring-1 ${toast.tone === 'error' ? 'bg-panel text-danger ring-danger/40' : 'bg-panel text-fg ring-line'}`}
        >
          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{toast.text}</span>
          <button type="button" aria-label={t('app.dismiss')} title={t('app.dismiss')} className="pointer-events-auto flex shrink-0 items-center justify-center rounded px-1 text-fg-muted hover:bg-hover hover:text-fg" onClick={() => dismiss(toast.id)}>
            <IconClose size={16} />
          </button>
        </div>
      )}
    </div>
  )
  return host ? createPortal(region, host.el) : region
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
