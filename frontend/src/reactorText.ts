import { getLocale, t, type I18nKey } from './i18n'

// MAX_SHOWN: the tooltip names at most this many reactors (mine's "You"
// counts as one of them, matching the webapp — see the brief's examples);
// the rest fold into an overflow phrase — see overflowLabelFor.
export const MAX_SHOWN = 10

// overflowLabelFor: the tail's noun phrase, e.g. "3 other users" / "3
// других пользователя" — wording and CLDR plural category taken verbatim
// from the webapp (fix round 2, controller finding — read from the
// sparse-checked webapp/channels/src/i18n/{en,ru}.json,
// "reaction.usersAndOthersReacted"/"reaction.othersReacted", also
// reaction_tooltip.tsx:41-60). Two shapes:
//   - hasShown: at least one reactor is named before it — "otherUser*"
//     ("bob, carol and 3 other users"/"...и 3 других пользователя").
//   - !hasShown: nobody is named at all, it stands alone — "users*"
//     ("3 users"/"3 пользователя").
// Intl.PluralRules picks the exact CLDR category (English only ever
// selects one/other; Russian selects one/few/many — see i18n.ts's keys).
function overflowLabelFor(n: number, hasShown: boolean): string {
  const category = new Intl.PluralRules(getLocale()).select(n)
  const suffix = category === 'one' ? 'One' : category === 'few' ? 'Few' : 'Many'
  const key = `reaction.${hasShown ? 'otherUser' : 'users'}${suffix}` as I18nKey
  return t(key, { n: String(n) })
}

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
  overflowLabel: string | null // the tail's label (overflowLabelFor), or null: nothing left out
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
  return { shown, overflowLabel: overflowLabelFor(overflow, shown.length > 0) }
}

// reactorWhoText builds "You, bob and carol reacted with" / "bob, carol and
// 3 others reacted with" (no trailing emoji) — see reactorTooltipText for
// the full tooltip string. 'reaction.reacted' bakes in the verb *and* the
// preposition before the emoji ("reacted with" / "отреагировал с") to
// match the webapp's own reaction tooltip exactly — read verbatim from the
// sparse-checked reference at webapp/channels/src/i18n/{en,ru}.json, key
// "reaction.reacted": en "{users} {reactionVerb} with {emoji}" (reactionVerb
// is always "reacted"); ru "{users} {reactionVerb} с {emoji}" (reactionVerb
// is always the singular "отреагировал", not pluralized by the webapp
// either — controller finding, fix round 1, 2026-09-28).
export function reactorWhoText(input: ReactorTextInput): string {
  const { shown, overflowLabel } = reactorWhoParts(input)
  const parts = overflowLabel ? [...shown, overflowLabel] : shown
  return `${listify(parts)} ${t('reaction.reacted')}`
}

// reactorTooltipText is the full tooltip content: who reacted, then the
// emoji name in colons (matching the webapp's own reaction tooltip, which
// also renders ":name:" as text there rather than an image).
export function reactorTooltipText(input: ReactorTextInput, emoji: string): string {
  return `${reactorWhoText(input)} :${emoji}:`
}
