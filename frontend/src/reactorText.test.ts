import { setLocale } from './i18n'
import { reactorWhoParts } from './reactorText'

// Wording verified against the webapp reference (fix round 1/2, 2026-09-28)
// for the overflow tail only — the tooltip itself no longer appends
// "reacted with :emoji:" (UI ruling, 2026-09-29: the user already hovered a
// specific reaction chip to get here, so naming the emoji again is
// redundant — see Reactions.tsx's TooltipBody). The overflow tail
// ("reaction.usersAndOthersReacted"/"reaction.othersReacted") is still read
// the same way: en "{n} other user(s)"/"{n} user(s)"; ru CLDR one/few/many
// ("другой пользователь"/"других пользователя"/"других пользователей" when
// some reactors are named, "пользователь"/"пользователя"/"пользователей"
// when none are).

beforeEach(() => setLocale('en'))

test('1 reactor, mine', () => {
  expect(reactorWhoParts({ mine: true, names: [], unknown: 0 })).toEqual({ shown: ['You'], overflowLabel: null })
})

test('2 reactors, mine + one other', () => {
  expect(reactorWhoParts({ mine: true, names: ['bob'], unknown: 0 })).toEqual({ shown: ['You', 'bob'], overflowLabel: null })
})

test('3 reactors, mine + two others (the goal example)', () => {
  expect(reactorWhoParts({ mine: true, names: ['bob', 'carol'], unknown: 0 })).toEqual({ shown: ['You', 'bob', 'carol'], overflowLabel: null })
})

test('not mine, with unresolved reactors folded into the overflow tail', () => {
  expect(reactorWhoParts({ mine: false, names: ['bob', 'carol'], unknown: 3 })).toEqual({ shown: ['bob', 'carol'], overflowLabel: '3 other users' })
})

test('exactly one overflow entry uses the singular ("other user", not "other users")', () => {
  expect(reactorWhoParts({ mine: false, names: ['bob'], unknown: 1 })).toEqual({ shown: ['bob'], overflowLabel: '1 other user' })
})

test('no one named at all: the bare "N users" form (othersReacted), not "N other users"', () => {
  expect(reactorWhoParts({ mine: false, names: [], unknown: 5 })).toEqual({ shown: [], overflowLabel: '5 users' })
  expect(reactorWhoParts({ mine: false, names: [], unknown: 1 })).toEqual({ shown: [], overflowLabel: '1 user' })
})

test('12 reactors, not mine: the first 10 names shown, 2 folded into the tail', () => {
  const names = Array.from({ length: 12 }, (_, i) => `user${i}`)
  const { shown, overflowLabel } = reactorWhoParts({ mine: false, names, unknown: 0 })
  expect(shown).toEqual(names.slice(0, 10))
  expect(overflowLabel).toBe('2 other users')
})

test('12 reactors, mine: "You" counts toward the first 10 shown', () => {
  const names = Array.from({ length: 11 }, (_, i) => `user${i}`)
  const { shown, overflowLabel } = reactorWhoParts({ mine: true, names, unknown: 0 })
  expect(shown).toEqual(['You', ...names.slice(0, 9)])
  expect(overflowLabel).toBe('2 other users')
})

test('ru: matches the webapp reference exactly ("Вы" for "You", "и" would join the shown names elsewhere)', () => {
  setLocale('ru')
  expect(reactorWhoParts({ mine: true, names: [], unknown: 0 })).toEqual({ shown: ['Вы'], overflowLabel: null })
  expect(reactorWhoParts({ mine: true, names: ['bob'], unknown: 0 })).toEqual({ shown: ['Вы', 'bob'], overflowLabel: null })
  expect(reactorWhoParts({ mine: true, names: ['bob', 'carol'], unknown: 0 })).toEqual({ shown: ['Вы', 'bob', 'carol'], overflowLabel: null })
})

// CLDR one/few/many, "someone is named" form ("другой пользователь" / "других
// пользователя" / "других пользователей") — one=1, few=2..4 (not 12..14),
// many=everything else (including the 11..14 exception band).
test('ru: overflow tail CLDR forms when someone is named', () => {
  setLocale('ru')
  expect(reactorWhoParts({ mine: false, names: ['bob', 'carol'], unknown: 1 }).overflowLabel).toBe('1 другой пользователь')
  expect(reactorWhoParts({ mine: false, names: ['bob', 'carol'], unknown: 3 }).overflowLabel).toBe('3 других пользователя')
  expect(reactorWhoParts({ mine: false, names: ['bob', 'carol'], unknown: 12 }).overflowLabel).toBe('12 других пользователей')
})

// Same three CLDR forms, "nobody is named" form ("пользователь" /
// "пользователя" / "пользователей").
test('ru: overflow tail CLDR forms when nobody is named', () => {
  setLocale('ru')
  expect(reactorWhoParts({ mine: false, names: [], unknown: 1 }).overflowLabel).toBe('1 пользователь')
  expect(reactorWhoParts({ mine: false, names: [], unknown: 3 }).overflowLabel).toBe('3 пользователя')
  expect(reactorWhoParts({ mine: false, names: [], unknown: 12 }).overflowLabel).toBe('12 пользователей')
})
