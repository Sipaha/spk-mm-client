import type { ServerDTO } from '../api/types'
import { t } from '../i18n'

const stateLabel: Partial<Record<ServerDTO['state'], Parameters<typeof t>[0]>> = {
  connecting: 'status.connecting',
  reconnecting: 'status.reconnecting',
  needs_reauth: 'status.needsReauth',
}

export function ServerRail(props: { servers: ServerDTO[]; selectedId: number | null; onSelect: (id: number | null) => void }) {
  return (
    <nav className="flex w-16 shrink-0 flex-col items-center gap-3 bg-neutral-800 py-3">
      {props.servers.map((s) => {
        const problem = s.signed_in ? stateLabel[s.state] : undefined
        const dim = !s.signed_in || s.state === 'needs_reauth'
        // The mention pill / unread dot are visually siblings of the button
        // (for absolute positioning), but a screen-reader user tabbing
        // through the rail must still hear them as part of the button's
        // name — not just the bare server name.
        const badge = s.mentions > 0 ? t('rail.mentions', { n: String(s.mentions) }) : s.unread ? t('rail.unread') : null
        const accessibleName = badge ? `${s.name} — ${badge}` : s.name
        return (
          <div key={s.id} className="relative">
            <button
              title={problem ? `${s.name} — ${t(problem)}` : s.name}
              aria-label={accessibleName}
              aria-current={s.id === props.selectedId}
              onClick={() => props.onSelect(s.id)}
              className={`h-11 w-11 rounded-xl text-sm font-semibold text-white ${s.id === props.selectedId ? 'bg-blue-600' : 'bg-neutral-600'} ${dim ? 'opacity-60' : ''} ${s.state === 'reconnecting' ? 'ring-2 ring-amber-400' : ''}`}
            >
              {s.name.slice(0, 2).toUpperCase()}
            </button>
            {s.mentions > 0 ? (
              <span
                aria-label={t('rail.mentions', { n: String(s.mentions) })}
                className="absolute -right-1.5 -top-1.5 min-w-5 rounded-full bg-red-600 px-1 text-center text-[10px] font-bold leading-5 text-white"
              >
                {s.mentions > 99 ? '99+' : s.mentions}
              </span>
            ) : s.unread ? (
              <span aria-label={t('rail.unread')} className="absolute -left-2 top-1/2 h-2 w-2 -translate-y-1/2 rounded-full bg-white" />
            ) : null}
          </div>
        )
      })}
      <button
        title={t('rail.add')}
        aria-label={t('rail.add')}
        onClick={() => props.onSelect(null)}
        className="h-11 w-11 rounded-xl border border-dashed border-neutral-500 text-xl text-neutral-300"
      >
        +
      </button>
    </nav>
  )
}
