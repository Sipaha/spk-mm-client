import type { ReactionView } from '../api/types'
import { emojiChar, useEmojiIndex } from '../emoji'
import { t } from '../i18n'
import { EmojiGlyph } from './EmojiGlyph'

interface Props {
  serverId: number
  reactions: ReactionView[]
  onToggle(r: ReactionView): void
  onAdd?(trigger: HTMLElement): void // the "☺+" chip opens the picker next to itself
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
            className={`flex h-6 items-center gap-1 rounded-full border px-1.5 text-xs ${r.mine ? 'border-accent bg-accent/15 text-fg' : 'border-line text-fg-muted hover:border-fg-subtle'}`}
          >
            <EmojiGlyph serverId={serverId} name={r.emoji} />
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
          className="flex h-6 items-center rounded-full border border-line px-1.5 text-xs text-fg-muted hover:bg-hover"
        >
          ☺+
        </button>
      )}
    </div>
  )
}
