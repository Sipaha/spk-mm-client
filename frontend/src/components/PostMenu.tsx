import { t } from '../i18n'
import { IconDelete, IconEdit, IconLink, IconMarkUnread } from './icons'
import { useMenuA11y } from './menuA11y'
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
// see the "Things that bite" note in AGENTS.md). Its keyboard/focus/
// outside-click contract is the shared useMenuA11y (menuA11y.ts) — this
// component only adds the note below about *why* anchorEl.focus() has to
// happen synchronously here, because it's specific to this toolbar.
//
// anchorEl lives inside the hovered post's toolbar, which PostItem only
// keeps mounted while "hot" (hovered/focused/menu open/picker open) — if
// hot has already gone false (pointer left while this menu was open),
// onClose() alone drops `visible` to false in the same render that closes
// the menu, and the toolbar (with anchorEl) unmounts *before* an
// unmount-time cleanup could ever focus it. useMenuA11y's
// closeAndFocusAnchor focuses it *before* onClose() runs instead, which
// fires a real DOM focus event that bubbles to the post's <article> and
// marks it hot again in the very same update, so the toolbar survives the
// transition and the focus lands correctly (fix round 1, controller review
// of e63449f: Esc/click-outside/item-click all failed to restore focus once
// hot was already false).
export default function PostMenu({ anchorEl, canEdit, onMarkUnread, onCopyLink, onEdit, onDelete, onClose }: Props) {
  const { root, onMenuKey, onMenuBlur, closeAndFocusAnchor } = useMenuA11y(anchorEl, onClose)

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
