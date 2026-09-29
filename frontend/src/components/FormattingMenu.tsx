import { useEffect, useRef } from 'react'
import type { MarkdownMode } from '../composerFormatting'
import { t } from '../i18n'
import type { IconProps } from './icons'
import { placeBelow } from './panelPosition'

const W = 220
const ITEM_H = 34
const PAD = 8

export interface FormattingMenuItem {
  mode: MarkdownMode
  label: string
  Icon(p: IconProps): React.ReactElement
}

interface Props {
  // anchorEl: the toolbar's "more formatting" button — its rect places the
  // menu, and (like PostMenu/Downloads/EmojiPicker) it is excluded from the
  // outside-click check.
  anchorEl: HTMLElement
  items: FormattingMenuItem[]
  onPick(mode: MarkdownMode): void
  onClose(): void
}

// FormattingMenu: the composer toolbar's overflow — the formatting buttons
// that don't fit the row (coordinator ruling 2026-09-29, composer brief
// follow-up: no horizontal scrollbar under the toolbar; collapse into a
// popover instead, like the webapp's responsive formatting bar). Same
// portal/keyboard/focus contract as PostMenu.tsx, whose own doc comment
// explains the "why" of each rule (not repeated here): a document-level
// Escape/outside-click listener, arrow/Home/End roving focus, no Tab trap
// (focus leaving both the menu and the anchor closes it), and Esc/an item
// click return focus to the anchor while an outside click does not steal it.
export function FormattingMenu({ anchorEl, items, onPick, onClose }: Props) {
  const root = useRef<HTMLDivElement>(null)

  const closeAndFocusAnchor = () => {
    anchorEl.focus()
    onClose()
  }

  useEffect(() => {
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

  // Focus the first item on open — no unmount-time focus restore (see
  // PostMenu.tsx's closeAndFocusAnchor comment for why that would be too late).
  useEffect(() => {
    root.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
  }, [])

  const menuItems = () => [...(root.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]

  const onMenuKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const list = menuItems()
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

  const onMenuBlur = (e: React.FocusEvent<HTMLDivElement>) => {
    const next = e.relatedTarget as Node | null
    if (!next || (!root.current?.contains(next) && !anchorEl.contains(next))) onClose()
  }

  const itemCls = 'flex items-center gap-2 px-3 py-1.5 text-left text-fg hover:bg-hover focus:bg-hover focus:outline-none'
  const pos = placeBelow(anchorEl.getBoundingClientRect(), window.innerWidth, window.innerHeight, W, items.length * ITEM_H + PAD)

  return (
    <div
      ref={root}
      role="menu"
      aria-label={t('composer.moreFormatting')}
      className="fixed z-50 flex flex-col rounded-lg border border-line bg-panel py-1 text-fg shadow-xl"
      style={{ left: pos.left, top: pos.top, width: W }}
      onKeyDown={onMenuKey}
      onBlur={onMenuBlur}
    >
      {items.map(({ mode, label, Icon }) => (
        <button
          key={mode}
          type="button"
          role="menuitem"
          className={itemCls}
          onClick={() => {
            closeAndFocusAnchor()
            onPick(mode)
          }}
        >
          <Icon size={18} />
          {label}
        </button>
      ))}
    </div>
  )
}
