import type { ReactionView } from '../api/types'
import { emojiChar, useEmojiIndex } from '../emoji'
import { t } from '../i18n'
import { EmojiGlyph } from './EmojiGlyph'
import { IconAddReaction } from './icons'

interface Props {
  serverId: number
  reactions: ReactionView[]
  onToggle(r: ReactionView): void
  onAdd?(trigger: HTMLElement): void // the "add reaction" chip opens the picker next to itself
}

// Reactions: a chip per emoji; clicking toggles our own reaction with the
// chip's own name (aliases stay separate, as on the server).
export function Reactions({ serverId, reactions, onToggle, onAdd }: Props) {
  const idx = useEmojiIndex(reactions.some((r) => !emojiChar(r.emoji, null)))
  return (
    <div className="mt-1 flex flex-wrap items-center gap-1">
      {reactions.map((r) => {
        const label = emojiChar(r.emoji, idx) ?? `:${r.emoji}:`
        return (
          <button
            key={r.emoji}
            type="button"
            aria-pressed={r.mine}
            aria-label={t(r.mine ? 'reaction.chipMine' : 'reaction.chip', { emoji: label, n: String(r.count) })}
            title={`:${r.emoji}:`}
            onClick={() => onToggle(r)}
            className={`flex h-7 items-center gap-1.5 rounded-full border px-2 text-sm ${r.mine ? 'border-accent bg-accent/15 text-fg' : 'border-line text-fg-muted hover:border-fg-subtle'}`}
          >
            <EmojiGlyph serverId={serverId} name={r.emoji} size={18} />
            <span>{r.count}</span>
          </button>
        )
      })}
      {onAdd && (
        <button
          type="button"
          aria-label={t('reaction.add')}
          title={t('reaction.add')}
          onClick={(e) => onAdd(e.currentTarget)}
          className="flex h-7 w-9 items-center justify-center rounded-full border border-line text-fg-muted hover:bg-hover"
        >
          <IconAddReaction size={18} />
        </button>
      )}
    </div>
  )
}
