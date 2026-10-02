import { useEffect, useMemo, useRef, useState } from 'react'
import type { QuickChannelDTO } from '../api/types'
import { client } from '../api/client'
import { openChannel, selectServer } from '../chat'
import { t } from '../i18n'
import { isShortcut } from '../keyboard'
import { ChannelTypeMarker } from './icons'

const norm = (s: string) => s.toLocaleLowerCase()

// A direct/name hit must beat a group that merely contains the query. Within
// the same quality, recent activity wins so abandoned conversations sink.
export function rankQuickChannels(items: QuickChannelDTO[], query: string): QuickChannelDTO[] {
  const words = norm(query).trim().split(/\s+/).filter(Boolean)
  const score = (x: QuickChannelDTO) => {
    const name = norm(x.name)
    const context = norm(`${x.team_display} ${x.server_name}`)
    let quality = 0
    for (const word of words) {
      if (name === word) quality += 0
      else if (name.startsWith(word)) quality += 1
      else if (name.split(/[\s,._-]+/).some((part) => part.startsWith(word))) quality += 2
      else if (name.includes(word)) quality += 3
      else if (context.includes(word)) quality += 4
      else return null
    }
    return quality
  }
  return items.map((item) => ({ item, score: score(item) })).filter((x): x is { item: QuickChannelDTO; score: number } => x.score !== null)
    .sort((a, b) => a.score - b.score || b.item.last_activity_at - a.item.last_activity_at || a.item.name.localeCompare(b.item.name))
    .map((x) => x.item)
}

export function QuickSwitcher() {
  const [open, setOpen] = useState(false)
  const [items, setItems] = useState<QuickChannelDTO[]>([])
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isShortcut(e, 'KeyK', { ctrl: true, shift: false }) || e.altKey) return
      e.preventDefault()
      setOpen(true)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  useEffect(() => {
    if (!open) return
    setQuery(''); setActive(0)
    void client.quickChannels().then(setItems).catch(() => setItems([]))
    requestAnimationFrame(() => input.current?.focus())
  }, [open])

  const shown = useMemo(() => {
    return rankQuickChannels(items, query).slice(0, 60)
  }, [items, query])
  useEffect(() => setActive((n) => Math.min(n, Math.max(0, shown.length - 1))), [shown.length])
  const choose = (x: QuickChannelDTO) => {
    setOpen(false)
    selectServer(x.server_id)
    void openChannel(x.server_id, x.id)
  }
  if (!open) return null
  return (
    <div className="fixed inset-0 z-[100] flex justify-center bg-black/45 pt-[12vh]" onMouseDown={() => setOpen(false)}>
      <div role="dialog" aria-modal="true" aria-label={t('quick.title')} className="h-fit w-[min(620px,calc(100vw-32px))] overflow-hidden rounded-lg border border-line bg-panel shadow-2xl" onMouseDown={(e) => e.stopPropagation()}>
        <input ref={input} value={query} onChange={(e) => { setQuery(e.target.value); setActive(0) }} placeholder={t('quick.placeholder')} aria-label={t('quick.label')}
          className="w-full border-b border-line bg-transparent px-4 py-3 text-base text-fg outline-none placeholder:text-fg-subtle"
          onKeyDown={(e) => {
            if (e.key === 'Escape') { e.preventDefault(); setOpen(false) }
            else if (e.key === 'ArrowDown') { e.preventDefault(); setActive((n) => Math.min(n + 1, shown.length - 1)) }
            else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((n) => Math.max(n - 1, 0)) }
            else if (e.key === 'Enter' && shown[active]) { e.preventDefault(); choose(shown[active]) }
          }} />
        <div role="listbox" className="max-h-[55vh] overflow-y-auto p-1">
          {shown.map((x, i) => <button key={`${x.server_id}:${x.id}`} role="option" aria-selected={i === active} onMouseEnter={() => setActive(i)} onClick={() => choose(x)}
            className={`flex w-full items-center gap-3 rounded px-3 py-2 text-left ${i === active ? 'bg-hover' : ''}`}>
            <ChannelTypeMarker type={x.type} size={16} />
            <span className="min-w-0 flex-1 truncate font-medium text-fg">{x.name}</span>
            <span className="shrink-0 text-xs text-fg-muted">{x.team_display}{items.some((y) => y.server_id !== x.server_id) ? ` · ${x.server_name}` : ''}</span>
          </button>)}
          {shown.length === 0 && <p className="px-3 py-6 text-center text-fg-muted">{t('quick.empty')}</p>}
        </div>
      </div>
    </div>
  )
}
