import { useEffect, useMemo, useRef, useState } from 'react'
import type { EmojiDTO } from '../api/types'
import { searchEmoji, useEmojiIndex } from '../emoji'
import { t, type I18nKey } from '../i18n'
import { mediaURL } from '../media'

export const COLS = 9
const W = 360
const H = 380

interface Props {
  serverId: number
  anchor: DOMRect
  info: EmojiDTO | null // recent and custom; null while loading
  onPick(name: string): void
  onClose(): void
}

interface Item {
  name: string
  char?: string // absent: a custom emoji, drawn from /media/
}

interface Cell extends Item {
  row: number
  col: number
}

interface Section {
  id: string
  title: string
  cells: Cell[]
}

// placePicker puts the picker under its button, or above it when there is
// no room below, always inside the viewport.
export function placePicker(anchor: Pick<DOMRect, 'top' | 'bottom' | 'right'>, vw: number, vh: number) {
  const left = Math.max(8, Math.min(anchor.right - W, vw - W - 8))
  const below = anchor.bottom + 4
  const top = below + H <= vh - 8 ? below : Math.max(8, anchor.top - H - 4)
  return { left, top }
}

// layout numbers rows through all sections, so arrow keys move by the
// grid's visible rows and columns; empty sections are dropped.
function layout(sections: { id: string; title: string; items: Item[] }[]): Section[] {
  let row = 0
  return sections
    .filter((s) => s.items.length > 0)
    .map((s) => {
      const cells = s.items.map((it, i) => ({ ...it, row: row + Math.floor(i / COLS), col: i % COLS }))
      row += Math.ceil(s.items.length / COLS)
      return { id: s.id, title: s.title, cells }
    })
}

export default function EmojiPicker({ serverId, anchor, info, onPick, onClose }: Props) {
  const idx = useEmojiIndex()
  const [query, setQuery] = useState('')
  const root = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) onClose()
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [onClose])

  const sections = useMemo(() => {
    if (!idx) return []
    const custom = info?.custom_enabled ? info.custom : []
    const q = query.trim().toLowerCase()
    if (q) {
      return layout([{
        id: 'results', title: '',
        items: [
          ...searchEmoji(idx, q).map((e) => ({ name: e.names[0], char: e.char })),
          ...custom.filter((n) => n.toLowerCase().includes(q)).map((name) => ({ name })),
        ],
      }])
    }
    const recent: Item[] = []
    for (const n of info?.recent ?? []) {
      const e = idx.byName.get(n)
      if (e) recent.push({ name: n, char: e.char })
      else if (custom.includes(n)) recent.push({ name: n })
    }
    return layout([
      { id: 'recent', title: t('picker.recent'), items: recent },
      ...idx.categories.map((c) => ({
        id: c.id, title: t(`picker.cat.${c.id}` as I18nKey), items: c.emojis.map((e) => ({ name: e.names[0], char: e.char })),
      })),
      { id: 'custom', title: t('picker.custom'), items: custom.map((name) => ({ name })) },
    ])
  }, [idx, info, query])

  const cellAt = (row: number, col: number) => root.current?.querySelector<HTMLButtonElement>(`[data-row="${row}"][data-col="${col}"]`) ?? null
  const rowCells = (row: number) => [...(root.current?.querySelectorAll<HTMLButtonElement>(`[data-row="${row}"]`) ?? [])]

  const onGridKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const el = e.target as HTMLElement
    if (el.dataset.row === undefined) return
    const row = Number(el.dataset.row)
    const col = Number(el.dataset.col)
    let next: HTMLElement | null | undefined
    switch (e.key) {
      case 'ArrowRight':
        next = cellAt(row, col + 1) ?? rowCells(row + 1)[0]
        break
      case 'ArrowLeft':
        next = col > 0 ? cellAt(row, col - 1) : rowCells(row - 1).at(-1)
        break
      case 'ArrowDown': {
        const r = rowCells(row + 1)
        next = r[Math.min(col, r.length - 1)]
        break
      }
      case 'ArrowUp': {
        const r = rowCells(row - 1)
        next = row === 0 ? input.current : r[Math.min(col, r.length - 1)]
        break
      }
      default:
        return
    }
    e.preventDefault()
    next?.focus()
  }

  const pos = placePicker(anchor, window.innerWidth, window.innerHeight)
  return (
    <div
      ref={root}
      role="dialog"
      aria-label={t('picker.label')}
      className="fixed z-50 flex flex-col rounded-lg border border-line bg-panel text-fg shadow-xl"
      style={{ left: pos.left, top: pos.top, width: W, height: H }}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.preventDefault()
          e.stopPropagation()
          onClose()
        }
      }}
    >
      <input
        ref={input}
        autoFocus
        aria-label={t('picker.search')}
        placeholder={t('picker.search')}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') {
            e.preventDefault()
            rowCells(0)[0]?.focus()
          } else if (e.key === 'Enter') {
            e.preventDefault()
            const first = sections[0]?.cells[0]
            if (first) onPick(first.name)
          }
        }}
        className="m-2 rounded border border-line bg-app px-2 py-1 text-sm text-fg focus:border-accent focus:outline-none"
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2" onKeyDown={onGridKey}>
        {!idx && <p className="p-2 text-xs text-fg-muted">{t('picker.loading')}</p>}
        {idx && sections.length === 0 && <p className="p-2 text-xs text-fg-muted">{t('picker.empty')}</p>}
        {sections.map((s) => (
          <section key={s.id} aria-label={s.title || t('picker.results')}>
            {s.title && <h3 className="sticky top-0 bg-panel py-1 text-xs font-semibold text-fg-muted">{s.title}</h3>}
            <div className="grid grid-cols-9 gap-0.5">
              {s.cells.map((c) => (
                <button
                  key={`${s.id}:${c.name}`}
                  type="button"
                  data-row={c.row}
                  data-col={c.col}
                  tabIndex={c.row === 0 && c.col === 0 ? 0 : -1}
                  aria-label={`:${c.name}:`}
                  title={`:${c.name}:`}
                  onClick={() => onPick(c.name)}
                  className="flex h-9 w-9 items-center justify-center rounded text-xl hover:bg-hover focus:bg-hover focus:outline-none"
                >
                  {c.char ?? <img src={mediaURL(serverId, 'emoji', c.name)} alt="" width={24} height={24} loading="lazy" />}
                </button>
              ))}
            </div>
          </section>
        ))}
      </div>
    </div>
  )
}
