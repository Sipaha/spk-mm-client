import { useEffect, useRef, type RefObject } from 'react'

export interface MenuA11y {
  // root: attach to the menu's own element (role="menu") — used both to
  // find its [role="menuitem"] children and to exclude the menu itself from
  // the outside-click check.
  root: RefObject<HTMLDivElement | null>
  closeAndFocusAnchor(): void
  onMenuKey(e: React.KeyboardEvent<HTMLDivElement>): void
  onMenuBlur(e: React.FocusEvent<HTMLDivElement>): void
}

// useMenuA11y is the shared "menu button" popover contract (ARIA APG
// menu-button pattern) originally written for PostMenu.tsx and duplicated
// into FormattingMenu.tsx; both now call this instead of carrying their own
// copy. Sidebar's server menu (fix 2026-09-29: the menu had no outside-
// click/Escape handling at all, so it stayed open while the user clicked
// elsewhere) uses it too. It gives a consumer:
//   - a document-level Escape/outside-pointerdown listener (closed on
//     mousedown outside both the menu and its anchor — pointerdown, not
//     click, so the very next click doesn't also fall through to whatever
//     is underneath)
//   - focus on the first [role="menuitem"] when the menu mounts
//   - arrow/Home/End roving focus among the items
//   - no Tab trap: focus leaving both the menu and the anchor closes it
//     (the ARIA APG menu-button pattern lets Tab close the menu rather than
//     cycle through it)
// Esc and picking an item are "internal" closes — they return focus to the
// anchor via closeAndFocusAnchor, synchronously and *before* onClose() runs
// its state update: some anchors (PostMenu's "…" button) only stay mounted
// while their row is "hot", and onClose() alone can drop that in the same
// render, unmounting the anchor before an unmount-time effect could ever
// focus it (fix round 1, controller review of e63449f). An outside
// click/tap is "external" — the user's own click already moved their
// attention elsewhere, so it must not steal focus back.
export function useMenuA11y(anchorEl: HTMLElement, onClose: () => void): MenuA11y {
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

  // Focus the first item on open. No unmount-time focus restore here (see
  // the doc comment above for why, and for the actual fix).
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

  const onMenuBlur = (e: React.FocusEvent<HTMLDivElement>) => {
    const next = e.relatedTarget as Node | null
    if (!next || (!root.current?.contains(next) && !anchorEl.contains(next))) onClose()
  }

  return { root, closeAndFocusAnchor, onMenuKey, onMenuBlur }
}
