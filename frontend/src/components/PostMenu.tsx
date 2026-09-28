import { useEffect, useRef } from 'react'
import { t } from '../i18n'
import { IconDelete, IconEdit, IconLink, IconMarkUnread } from './icons'
import { placeBelow } from './panelPosition'

const W = 220
const ITEM_H = 34
const PAD = 8

interface Props {
  // anchorEl: the "…" button — its rect places the menu, and (like
  // Downloads/EmojiPicker) it is excluded from the outside-click check.
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
// see the "Things that bite" note in AGENTS.md), closed by Esc, a click
// outside, or focus leaving both the menu and its anchor (Tab/Shift+Tab —
// this menu does not trap focus, per the ARIA APG menu-button pattern).
export default function PostMenu({ anchorEl, canEdit, onMarkUnread, onCopyLink, onEdit, onDelete, onClose }: Props) {
  const root = useRef<HTMLDivElement>(null)

  // closeAndFocusAnchor: Esc and picking an item are "internal" closes —
  // the ARIA APG menu-button pattern returns focus to the button that
  // opened the menu. Focusing anchorEl here, synchronously and *before*
  // onClose() runs its state update, matters: anchorEl lives inside the
  // hovered post's toolbar, which PostItem only keeps mounted while "hot"
  // (hovered/focused/menu open/picker open) — if hot has already gone
  // false (pointer left while this menu was open), onClose() alone drops
  // `visible` to false in the same render that closes the menu, and the
  // toolbar (with anchorEl) unmounts *before* an unmount-time cleanup could
  // ever focus it. Focusing it here instead, before that render happens,
  // fires a real DOM focus event that bubbles to the post's <article> and
  // marks it hot again in the very same update, so the toolbar survives
  // the transition and the focus lands correctly (fix round 1, controller
  // review of e63449f: Esc/click-outside/item-click all failed to restore
  // focus once hot was already false).
  const closeAndFocusAnchor = () => {
    anchorEl.focus()
    onClose()
  }

  useEffect(() => {
    // Outside click is an "external" close: the user's own click already
    // moved their attention elsewhere, so this must not steal focus back —
    // just close (fix round 1 finding: this used to also try to restore
    // focus via an unmount-time effect cleanup, both too late to find
    // anchorEl still mounted *and* wrong to steal focus from the click).
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node
      if (root.current && !root.current.contains(target) && !anchorEl.contains(target)) onClose()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        closeAndFocusAnchor()
      }
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose, anchorEl])

  // Focus the first item on open. No unmount-time focus restore here (see
  // closeAndFocusAnchor above for why, and for the actual fix): a
  // `useEffect` cleanup runs *after* React has already committed the DOM
  // removal of an unmounting component, which is too late once the
  // toolbar itself is gone.
  useEffect(() => {
    root.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
  }, [])

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

  // This menu does not trap Tab within itself (ARIA APG menu-button
  // pattern: Tab closes the menu rather than cycling through it). A
  // focusout whose relatedTarget lands outside both the menu and its
  // anchor means focus left for good — close, without stealing it back
  // (fix round 1 finding: Tab past the last item moved focus to <body>
  // while the menu stayed open — nothing closed it).
  const onMenuBlur = (e: React.FocusEvent<HTMLDivElement>) => {
    const next = e.relatedTarget as Node | null
    if (!next || (!root.current?.contains(next) && !anchorEl.contains(next))) onClose()
  }

  const itemCls = 'flex items-center gap-2 px-3 py-1.5 text-left text-fg hover:bg-hover focus:bg-hover focus:outline-none'
  const count = 2 + (canEdit ? 2 : 0)
  const pos = placeBelow(anchorEl.getBoundingClientRect(), window.innerWidth, window.innerHeight, W, count * ITEM_H + PAD)

  return (
    <div
      ref={root}
      role="menu"
      aria-label={t('post.moreMenu')}
      className="fixed z-50 flex flex-col rounded-lg border border-line bg-panel py-1 text-fg shadow-xl"
      style={{ left: pos.left, top: pos.top, width: W }}
      onKeyDown={onMenuKey}
      onBlur={onMenuBlur}
    >
      <button type="button" role="menuitem" className={itemCls} onClick={() => { closeAndFocusAnchor(); onMarkUnread() }}>
        <IconMarkUnread size={18} />
        {t('post.markUnread')}
      </button>
      <button type="button" role="menuitem" className={itemCls} onClick={() => { closeAndFocusAnchor(); onCopyLink() }}>
        <IconLink size={18} />
        {t('post.copyLink')}
      </button>
      {canEdit && (
        <button type="button" role="menuitem" className={itemCls} onClick={() => { closeAndFocusAnchor(); onEdit() }}>
          <IconEdit size={18} />
          {t('post.edit')}
        </button>
      )}
      {canEdit && (
        <button type="button" role="menuitem" className={`${itemCls} text-danger`} onClick={() => { closeAndFocusAnchor(); onDelete() }}>
          <IconDelete size={18} />
          {t('post.delete')}
        </button>
      )}
    </div>
  )
}
