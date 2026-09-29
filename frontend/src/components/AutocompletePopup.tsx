import { useEffect, useRef } from 'react'
import type { ACGroup, ACItem } from '../autocomplete'
import { completionText } from '../autocomplete'
import { t, type I18nKey } from '../i18n'
import { Avatar } from './Avatar'
import { EmojiGlyph } from './EmojiGlyph'
import { ChannelTypeMarker, IconGroup } from './icons'

const HEADERS: Record<ACGroup, I18nKey> = {
  members: 'autocomplete.members',
  special: 'autocomplete.special',
  others: 'autocomplete.others',
  myChannels: 'autocomplete.myChannels',
  otherChannels: 'autocomplete.otherChannels',
  emoji: 'autocomplete.emoji',
  commands: 'autocomplete.commands',
}

interface Props {
  serverId: number
  listId: string
  optionId(i: number): string
  items: ACItem[]
  active: number
  onPick(i: number): void
  onHover(i: number): void
}

function Row({ serverId, item }: { serverId: number; item: ACItem }) {
  switch (item.type) {
    case 'user': {
      const u = item.user
      const extra = [u.full_name, u.nickname ? `(${u.nickname})` : ''].filter(Boolean).join(' ')
      return (
        <>
          <Avatar serverId={serverId} userId={u.id} version={u.avatar} name={u.username} status={u.status} size={24} surface="app" />
          <span className="font-semibold">@{u.username}</span>
          {extra && <span className="truncate text-fg-muted">{extra}</span>}
          {u.me && <span className="text-fg-muted">{t('autocomplete.you')}</span>}
        </>
      )
    }
    case 'special':
      return (
        <>
          <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-hover text-fg-muted">
            <IconGroup size={16} />
          </span>
          <span className="font-semibold">@{item.name}</span>
          <span className="truncate text-fg-muted">{t(`autocomplete.${item.name}` as I18nKey)}</span>
        </>
      )
    case 'channel':
      return (
        <>
          <span className="flex h-6 w-6 shrink-0 items-center justify-center text-fg-muted">
            <ChannelTypeMarker type={item.channel.type} size={16} />
          </span>
          <span className="truncate font-semibold">{item.channel.display_name || item.channel.name}</span>
          <span className="truncate text-fg-muted">~{item.channel.name}</span>
        </>
      )
    case 'emoji':
      return (
        <>
          <span className="flex h-6 w-6 shrink-0 items-center justify-center">
            <EmojiGlyph serverId={serverId} name={item.name} size={20} />
          </span>
          <span className="truncate">{completionText(item)}</span>
        </>
      )
    case 'command':
      return (
        <span className="flex min-w-0 flex-col">
          <span className="truncate">
            <span className="font-semibold">/{item.command.trigger}</span>
            {item.command.hint ? <span className="text-fg-muted"> {item.command.hint}</span> : null}
          </span>
          {item.command.description && <span className="truncate text-xs text-fg-muted">{item.command.description}</span>}
        </span>
      )
  }
}

// AutocompletePopup is the composer's suggestion list, above the message
// box. It never takes focus: the textarea keeps it and points at the
// active row with aria-activedescendant; a mouse press is kept from
// blurring the textarea (preventDefault on mousedown).
export function AutocompletePopup({ serverId, listId, optionId, items, active, onPick, onHover }: Props) {
  const listRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(`[id="${CSS.escape(optionId(active))}"]`)
    el?.scrollIntoView?.({ block: 'nearest' })
  }, [active, optionId])

  const groups: { group: ACGroup; rows: { item: ACItem; i: number }[] }[] = []
  items.forEach((item, i) => {
    const last = groups[groups.length - 1]
    if (last && last.group === item.group) last.rows.push({ item, i })
    else groups.push({ group: item.group, rows: [{ item, i }] })
  })
  return (
    <div
      ref={listRef}
      id={listId}
      role="listbox"
      aria-label={t('autocomplete.label')}
      data-testid="autocomplete"
      onMouseDown={(e) => e.preventDefault()}
      className="absolute bottom-full left-0 right-0 z-30 mb-1 max-h-72 overflow-y-auto rounded-lg border border-line bg-panel py-1 text-sm shadow-lg"
    >
      {groups.map(({ group, rows }) => (
        <div key={group} role="group" aria-labelledby={`${listId}-${group}`}>
          <div id={`${listId}-${group}`} className="px-3 pb-1 pt-2 text-xs font-semibold text-fg-muted">
            {t(HEADERS[group])}
          </div>
          {rows.map(({ item, i }) => (
            <div
              key={item.key}
              id={optionId(i)}
              role="option"
              aria-selected={i === active}
              onClick={() => onPick(i)}
              onMouseMove={() => i !== active && onHover(i)}
              className={`flex cursor-pointer items-center gap-2 px-3 py-1 text-fg ${i === active ? 'bg-hover' : ''}`}
            >
              <Row serverId={serverId} item={item} />
            </div>
          ))}
        </div>
      ))}
    </div>
  )
}
