import { useEffect, useRef } from 'react'
import { t } from '../i18n'
import { IconDelete, IconEdit, IconLink, IconMarkUnread } from './icons'
import { placeBelow } from './panelPosition'

const W = 220
const ITEM_H = 34
const PAD = 8

interface Props {
  // anchorEl: the "…" button — its rect places the menu, and (like
  // Downloads/EmojiPicker) it is excluded from the outside-click check and
  // is where focus returns once the menu closes.
  anchorEl: HTMLElement
  canEdit: boolean // own, non-system post: adds Edit and Delete
  onMarkUnread(): void
  onCopyLink(): void
  onEdit(): void
  onDelete(): void
  onClose(): void
}

// PostMenu is the post toolbar's "…" dropdown: a portal into document.body
// (position: fixed inside a virtualized feed row clips to that row's box —
// see the "Things that bite" note in AGENTS.md), closed by Esc or a click
// outside, focus returned to anchorEl on close — same pattern as
// Downloads.tsx and EmojiPicker.tsx.
export default function PostMenu({ anchorEl, canEdit, onMarkUnread, onCopyLink, onEdit, onDelete, onClose }: Props) {
  const root = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node
      if (root.current && !root.current.contains(target) && !anchorEl.contains(target)) onClose()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose, anchorEl])

  // Move focus into the menu on open (its first item) and back to the "…"
  // button that opened it once this unmounts — same rule as Downloads.
  useEffect(() => {
    root.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
    return () => anchorEl.focus()
  }, [anchorEl])

  const items = () => [...(root.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]

  const onMenuKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const list = items()
    const i = list.indexOf(document.activeElement as HTMLElement)
    if (i < 0) return
    let next: HTMLElement | undefined
    switch (e.key) {
      case 'ArrowDown':
        next = list[(i + 1) % list.length]
        break
      case 'ArrowUp':
        next = list[(i - 1 + list.length) % list.length]
        break
      case 'Home':
        next = list[0]
        break
      case 'End':
        next = list[list.length - 1]
        break
      default:
        return
    }
    e.preventDefault()
    next?.focus()
  }

  const itemCls = 'flex items-center gap-2 px-3 py-1.5 text-left text-fg hover:bg-hover focus:bg-hover focus:outline-none'
  const count = 2 + (canEdit ? 2 : 0)
  const pos = placeBelow(anchorEl.getBoundingClientRect(), window.innerWidth, window.innerHeight, W, count * ITEM_H + PAD)

  return (
    <div
      ref={root}
      role="menu"
      aria-label={t('post.actions')}
      className="fixed z-50 flex flex-col rounded-lg border border-line bg-panel py-1 text-fg shadow-xl"
      style={{ left: pos.left, top: pos.top, width: W }}
      onKeyDown={onMenuKey}
    >
      <button type="button" role="menuitem" className={itemCls} onClick={() => { onClose(); onMarkUnread() }}>
        <IconMarkUnread size={18} />
        {t('post.markUnread')}
      </button>
      <button type="button" role="menuitem" className={itemCls} onClick={() => { onClose(); onCopyLink() }}>
        <IconLink size={18} />
        {t('post.copyLink')}
      </button>
      {canEdit && (
        <button type="button" role="menuitem" className={itemCls} onClick={() => { onClose(); onEdit() }}>
          <IconEdit size={18} />
          {t('post.edit')}
        </button>
      )}
      {canEdit && (
        <button type="button" role="menuitem" className={`${itemCls} text-danger`} onClick={() => { onClose(); onDelete() }}>
          <IconDelete size={18} />
          {t('post.delete')}
        </button>
      )}
    </div>
  )
}
