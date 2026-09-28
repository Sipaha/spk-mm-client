import { t } from './i18n'

// MAX_SHOWN: the tooltip names at most this many reactors (mine's "You"
// counts as one of them, matching the webapp — see the brief's examples);
// the rest fold into "and N others" / "и ещё N".
export const MAX_SHOWN = 10

export interface ReactorTextInput {
  mine: boolean
  names: string[] // display names, oldest reaction first, excluding "me"
  unknown: number // reactors whose profile could not be resolved
}

// listify: "A" | "A and B" | "A, B and C" — comma-joins every item but the
// last, "and"/"и" before the last one (also how the truncated tail reads:
// the overflow phrase is just the list's last item).
function listify(items: string[]): string {
  if (items.length <= 1) return items[0] ?? ''
  return `${items.slice(0, -1).join(', ')} ${t('reaction.and')} ${items[items.length - 1]}`
}

export interface ReactorWhoParts {
  shown: string[] // display names to show, "You" first when mine — at most MAX_SHOWN
  overflowLabel: string | null // "and N others" / "и ещё N"'s label, or null: nothing left out
}

// reactorWhoParts splits the "who reacted" list into the names to show and
// the overflow tail's label (if any) — kept separate from reactorWhoText so
// the interactive tooltip can render the overflow as a real <button> (the
// UI ruling on 2026-09-28: clicking "and N others" opens the full-list
// modal) while everything else stays plain text.
export function reactorWhoParts({ mine, names, unknown }: ReactorTextInput): ReactorWhoParts {
  const known = mine ? [t('reaction.you'), ...names] : names
  const shown = known.slice(0, MAX_SHOWN)
  const overflow = known.length - shown.length + unknown
  if (overflow <= 0) return { shown, overflowLabel: null }
  const overflowLabel = overflow === 1 ? t('reaction.othersOne', { n: String(overflow) }) : t('reaction.othersMany', { n: String(overflow) })
  return { shown, overflowLabel }
}

// reactorWhoText builds "You, bob and carol reacted" / "bob, carol and 3
// others reacted" (no trailing emoji) — see reactorTooltipText for the
// full tooltip string, and AGENTS.md/the brief for the exact wording rules
// this was derived from.
export function reactorWhoText(input: ReactorTextInput): string {
  const { shown, overflowLabel } = reactorWhoParts(input)
  const parts = overflowLabel ? [...shown, overflowLabel] : shown
  return `${listify(parts)} ${t('reaction.reacted')}`
}

// reactorTooltipText is the full tooltip content: who reacted, then the
// emoji name in colons (the brief's spec, matching the webapp's own
// reaction tooltip, which also renders ":name:" as text rather than an
// image there).
export function reactorTooltipText(input: ReactorTextInput, emoji: string): string {
  return `${reactorWhoText(input)} :${emoji}:`
}
