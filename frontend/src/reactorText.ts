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

export interface ReactorWhoParts {
  shown: string[] // display names to show, "You" first when mine — at most MAX_SHOWN
  overflowLabel: string | null // the tail's label (overflowLabelFor), or null: nothing left out
}

// reactorWhoParts splits the "who reacted" list into the names to show and
// the overflow tail's label (if any) — kept separate so the interactive
// tooltip (Reactions.tsx's TooltipBody) can render the overflow as a real
// <button> (the UI ruling on 2026-09-28: clicking "and N others" opens the
// full-list modal) while everything else stays plain text. The tooltip
// renders only these names — no trailing "reacted with :emoji:" (UI ruling,
// 2026-09-29, user request: hovering/focusing a specific chip already tells
// you which emoji it is, so repeating it in the tooltip text is redundant).
export function reactorWhoParts({ mine, names, unknown }: ReactorTextInput): ReactorWhoParts {
  const known = mine ? [t('reaction.you'), ...names] : names
  const shown = known.slice(0, MAX_SHOWN)
  const overflow = known.length - shown.length + unknown
  if (overflow <= 0) return { shown, overflowLabel: null }
  return { shown, overflowLabel: overflowLabelFor(overflow, shown.length > 0) }
}
