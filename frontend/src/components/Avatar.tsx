import { memo } from 'react'
import { t, type I18nKey } from '../i18n'
import { mediaURL, useLoadFailure } from '../media'

const COLORS = ['bg-rose-500', 'bg-orange-500', 'bg-amber-600', 'bg-lime-600', 'bg-emerald-600', 'bg-teal-600', 'bg-sky-600', 'bg-indigo-500', 'bg-violet-500', 'bg-fuchsia-600']

// colorFor: the initials placeholder keeps a stable color per user.
export function colorFor(id: string): string {
  let h = 0
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) | 0
  return COLORS[Math.abs(h) % COLORS.length]
}

// Offline and out-of-office look (and read) the same, as in the webapp's status_icon.
export function presenceKey(status: string): 'online' | 'away' | 'dnd' | 'offline' {
  return status === 'online' || status === 'away' || status === 'dnd' ? status : 'offline'
}

export const presenceLabel = (status: string) => t(`presence.${presenceKey(status)}` as I18nKey)

// Literal class names: Tailwind only generates classes it finds in the source.
const SURFACE = {
  app: { ring: 'ring-app', hollow: 'bg-app' },
  sidebar: { ring: 'ring-sidebar', hollow: 'bg-sidebar' },
}
const FILL: Record<string, string> = { online: 'bg-online', away: 'bg-away', dnd: 'bg-dnd' }

export function StatusDot({ status, surface, small }: { status: string; surface: 'app' | 'sidebar'; small?: boolean }) {
  const k = presenceKey(status)
  const s = SURFACE[surface]
  const fill = FILL[k] ?? `border-2 border-fg-subtle ${s.hollow}`
  return (
    <span
      aria-hidden="true"
      data-status={k}
      title={presenceLabel(status)}
      className={`absolute -bottom-0.5 -right-0.5 block rounded-full ring-2 ${s.ring} ${small ? 'h-2 w-2' : 'h-2.5 w-2.5'} ${fill}`}
    />
  )
}

interface Props {
  serverId: number
  userId: string
  version?: string // picture version from Go; absent/"" = profile not loaded
  name: string
  status?: string
  size: number
  surface: 'app' | 'sidebar'
}

// Avatar is decorative (alt=""): the name is always shown next to it. Its box
// has the final size from the start, so a loading picture never moves text.
// Pictures in feed rows and the sidebar load eagerly, never loading="lazy":
// the feed is virtualized already, and while the window is hidden (in the
// tray) WebKit never runs its lazy-load check, so each removed, not yet
// loaded lazy image stays referenced — with its whole detached feed — until
// the next paint (AGENTS.md, "Things that bite").
export const Avatar = memo(function Avatar({ serverId, userId, version, name, status, size, surface }: Props) {
  // A failed load shows initials until the picture version changes or the
  // server goes live again.
  const [failed, fail] = useLoadFailure(serverId, version ?? '')
  const src = version && !failed ? mediaURL(serverId, 'avatar', userId, { v: version }) : null
  return (
    <span className="relative block shrink-0" style={{ width: size, height: size }}>
      {src ? (
        <img
          src={src}
          alt=""
          width={size}
          height={size}
          decoding="async"
          draggable={false}
          onError={fail}
          className="block h-full w-full rounded-full bg-hover object-cover"
        />
      ) : (
        <span
          aria-hidden="true"
          className={`flex h-full w-full items-center justify-center rounded-full font-semibold text-white ${colorFor(userId)}`}
          style={{ fontSize: Math.round(size * 0.4) }}
        >
          {(name[0] ?? '?').toUpperCase()}
        </span>
      )}
      {status ? <StatusDot status={status} surface={surface} small={size <= 24} /> : null}
    </span>
  )
})
