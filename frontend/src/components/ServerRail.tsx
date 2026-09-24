import type { ServerDTO } from '../api/types'
import { t } from '../i18n'

export function ServerRail(props: { servers: ServerDTO[]; selectedId: number | null; onSelect: (id: number | null) => void }) {
  return (
    <nav className="flex w-16 flex-col items-center gap-2 bg-neutral-800 py-3">
      {props.servers.map((s) => (
        <button
          key={s.id}
          title={s.name}
          aria-label={s.name}
          aria-current={s.id === props.selectedId}
          onClick={() => props.onSelect(s.id)}
          className={`h-11 w-11 rounded-xl text-sm font-semibold text-white ${s.id === props.selectedId ? 'bg-blue-600' : 'bg-neutral-600'} ${s.signed_in ? '' : 'opacity-60'}`}
        >
          {s.name.slice(0, 2).toUpperCase()}
        </button>
      ))}
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
