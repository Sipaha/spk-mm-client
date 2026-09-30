import { useEffect, useRef } from 'react'
import type { Reactor } from '../api/types'
import { t } from '../i18n'
import { Avatar } from './Avatar'
import { EmojiGlyph } from './EmojiGlyph'
import { IconClose } from './icons'

interface Props {
  serverId: number
  emoji: string
  count: number
  mine: boolean
  meId: string
  meAvatar: string // my own picture version; '' = not loaded yet (ChannelDTO.me_avatar)
  users: Reactor[] // everyone except me, oldest reaction first (ReactionUsersDTO.users)
  // anchorEl: the reaction chip whose "and N others" opened this — focus
  // returns to it on close, like Viewer/Downloads/PostMenu.
  anchorEl: HTMLElement
  onClose(): void
}

// ReactorsModal is the full "who reacted" list behind a tooltip's "and N
// others" button (UI ruling, 2026-09-28): a portal into document.body, Esc
// or the backdrop closes it, focus is trapped inside while open and
// returns to anchorEl on close — dark theme like Viewer/Downloads. Its
// list is uncapped (a busy post can have hundreds of reactors) and always
// scrolled into view the moment it opens, unlike the feed/sidebar's
// Avatars — so every row's Avatar loads lazily (fix round 2: AGENTS.md's
// "necessarily visible" exception, the same one the emoji picker uses).
export function ReactorsModal({ serverId, emoji, count, mine, meId, meAvatar, users, anchorEl, onClose }: Props) {
  const root = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
        return
      }
      if (e.key !== 'Tab' || !root.current) return
      // A manual focus trap: Tab past the last focusable element wraps to
      // the first, Shift+Tab past the first wraps to the last — the panel
      // is a portal, so it isn't otherwise bounded in the document's tab
      // order the way an inline dialog would be.
      const focusable = [...root.current.querySelectorAll<HTMLElement>('button')]
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  // Focus the close button on open, return focus to the chip that opened
  // this on close — same rule as Viewer/Downloads/PostMenu.
  useEffect(() => {
    root.current?.querySelector<HTMLElement>('[data-close]')?.focus()
    return () => anchorEl.focus()
  }, [anchorEl])

  const closeOnBackdrop = (e: React.MouseEvent<HTMLDivElement>) => {
    if (e.target === e.currentTarget) onClose()
  }

  const title = t('reaction.whoTitle', { emoji: `:${emoji}:`, n: String(count) })

  return (
    <div data-overlay="true" className="fixed inset-0 z-50 flex items-center justify-center bg-black/85 p-4" onClick={closeOnBackdrop}>
      <div
        ref={root}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="flex max-h-[80vh] w-80 flex-col rounded-lg border border-line bg-panel text-fg shadow-xl"
      >
        <header className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-2">
          <EmojiGlyph serverId={serverId} name={emoji} size={20} />
          <span className="font-medium">{count}</span>
          <button type="button" data-close className="ml-auto rounded p-1 hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-accent" aria-label={t('viewer.close')} onClick={onClose}>
            <IconClose size={18} />
          </button>
        </header>
        <div role="list" aria-label={t('reaction.whoList')} className="min-h-0 flex-1 overflow-y-auto p-2">
          {mine && (
            <div role="listitem" className="flex items-center gap-2 rounded px-1 py-1.5 text-sm">
              <Avatar serverId={serverId} userId={meId} version={meAvatar || undefined} name={t('reaction.you')} size={24} surface="app" loading="lazy" />
              <span className="truncate">{t('reaction.you')}</span>
            </div>
          )}
          {users.map((u) => (
            <div key={u.id} role="listitem" className="flex items-center gap-2 rounded px-1 py-1.5 text-sm">
              <Avatar serverId={serverId} userId={u.id} version={u.avatar || undefined} name={u.name || t('reaction.unknownUser')} size={24} surface="app" loading="lazy" />
              <span className={`truncate ${u.name ? '' : 'text-fg-muted italic'}`}>{u.name || t('reaction.unknownUser')}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
