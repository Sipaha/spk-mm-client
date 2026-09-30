import type { MarkdownMode } from '../composerFormatting'
import { t } from '../i18n'
import type { IconProps } from './icons'
import { useMenuA11y } from './menuA11y'
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
// portal/keyboard/focus contract as PostMenu.tsx, via the shared
// useMenuA11y (menuA11y.ts, which explains the "why" of each rule): a
// document-level Escape/outside-click listener, arrow/Home/End roving
// focus, no Tab trap (focus leaving both the menu and the anchor closes
// it), and Esc/an item click return focus to the anchor while an outside
// click does not steal it.
export function FormattingMenu({ anchorEl, items, onPick, onClose }: Props) {
  const { root, onMenuKey, onMenuBlur, closeAndFocusAnchor } = useMenuA11y(anchorEl, onClose)

  const itemCls = 'flex items-center gap-2 px-3 py-1.5 text-left text-fg hover:bg-hover focus:bg-hover focus:outline-none'
  const pos = placeBelow(anchorEl.getBoundingClientRect(), window.innerWidth, window.innerHeight, W, items.length * ITEM_H + PAD)

  return (
    <div
      ref={root}
      role="menu"
      aria-label={t('composer.moreFormatting')}
      data-overlay="true"
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
