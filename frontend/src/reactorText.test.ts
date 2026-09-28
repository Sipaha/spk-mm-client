import { setLocale } from './i18n'
import { reactorTooltipText } from './reactorText'

// Wording verified against the webapp reference (fix round 1/2, 2026-09-28):
// webapp/channels/src/i18n/en.json "reaction.reacted" = "{users} {reactionVerb}
// with {emoji}" (reactionVerb is always "reacted"); ru.json = "{users}
// {reactionVerb} с {emoji}" (reactionVerb is always the singular
// "отреагировал", not pluralized there either). The overflow tail
// ("reaction.usersAndOthersReacted"/"reaction.othersReacted") is read the
// same way: en "{n} other user(s)"/"{n} user(s)"; ru CLDR one/few/many
// ("другой пользователь"/"других пользователя"/"других пользователей" when
// some reactors are named, "пользователь"/"пользователя"/"пользователей"
// when none are).

beforeEach(() => setLocale('en'))

test('1 reactor, mine', () => {
  expect(reactorTooltipText({ mine: true, names: [], unknown: 0 }, 'thumbsup')).toBe('You reacted with :thumbsup:')
})

test('2 reactors, mine + one other', () => {
  expect(reactorTooltipText({ mine: true, names: ['bob'], unknown: 0 }, 'thumbsup')).toBe('You and bob reacted with :thumbsup:')
})

test('3 reactors, mine + two others (the goal example)', () => {
  expect(reactorTooltipText({ mine: true, names: ['bob', 'carol'], unknown: 0 }, 'thumbsup')).toBe('You, bob and carol reacted with :thumbsup:')
})

test('not mine, with unresolved reactors folded into the overflow tail', () => {
  expect(reactorTooltipText({ mine: false, names: ['bob', 'carol'], unknown: 3 }, 'thumbsup')).toBe('bob, carol and 3 other users reacted with :thumbsup:')
})

test('exactly one overflow entry uses the singular ("other user", not "other users")', () => {
  expect(reactorTooltipText({ mine: false, names: ['bob'], unknown: 1 }, 'thumbsup')).toBe('bob and 1 other user reacted with :thumbsup:')
})

test('no one named at all: the bare "N users" form (othersReacted), not "N other users"', () => {
  expect(reactorTooltipText({ mine: false, names: [], unknown: 5 }, 'thumbsup')).toBe('5 users reacted with :thumbsup:')
  expect(reactorTooltipText({ mine: false, names: [], unknown: 1 }, 'thumbsup')).toBe('1 user reacted with :thumbsup:')
})

test('12 reactors, not mine: the first 10 names shown, 2 folded into the tail', () => {
  const names = Array.from({ length: 12 }, (_, i) => `user${i}`)
  expect(reactorTooltipText({ mine: false, names, unknown: 0 }, 'thumbsup')).toBe(
    'user0, user1, user2, user3, user4, user5, user6, user7, user8, user9 and 2 other users reacted with :thumbsup:',
  )
})

test('12 reactors, mine: "You" counts toward the first 10 shown', () => {
  const names = Array.from({ length: 11 }, (_, i) => `user${i}`)
  expect(reactorTooltipText({ mine: true, names, unknown: 0 }, 'thumbsup')).toBe(
    'You, user0, user1, user2, user3, user4, user5, user6, user7, user8 and 2 other users reacted with :thumbsup:',
  )
})

test('ru: matches the webapp reference exactly (singular verb, "с" before the emoji)', () => {
  setLocale('ru')
  expect(reactorTooltipText({ mine: true, names: [], unknown: 0 }, 'thumbsup')).toBe('Вы отреагировал с :thumbsup:')
  expect(reactorTooltipText({ mine: true, names: ['bob'], unknown: 0 }, 'thumbsup')).toBe('Вы и bob отреагировал с :thumbsup:')
  expect(reactorTooltipText({ mine: true, names: ['bob', 'carol'], unknown: 0 }, 'thumbsup')).toBe('Вы, bob и carol отреагировал с :thumbsup:')
})

// CLDR one/few/many, "someone is named" form ("другой пользователь" / "других
// пользователя" / "других пользователей") — one=1, few=2..4 (not 12..14),
// many=everything else (including the 11..14 exception band).
test('ru: overflow tail CLDR forms when someone is named', () => {
  setLocale('ru')
  expect(reactorTooltipText({ mine: false, names: ['bob', 'carol'], unknown: 1 }, 'thumbsup')).toBe('bob, carol и 1 другой пользователь отреагировал с :thumbsup:')
  expect(reactorTooltipText({ mine: false, names: ['bob', 'carol'], unknown: 3 }, 'thumbsup')).toBe('bob, carol и 3 других пользователя отреагировал с :thumbsup:')
  expect(reactorTooltipText({ mine: false, names: ['bob', 'carol'], unknown: 12 }, 'thumbsup')).toBe('bob, carol и 12 других пользователей отреагировал с :thumbsup:')
})

// Same three CLDR forms, "nobody is named" form ("пользователь" /
// "пользователя" / "пользователей").
test('ru: overflow tail CLDR forms when nobody is named', () => {
  setLocale('ru')
  expect(reactorTooltipText({ mine: false, names: [], unknown: 1 }, 'thumbsup')).toBe('1 пользователь отреагировал с :thumbsup:')
  expect(reactorTooltipText({ mine: false, names: [], unknown: 3 }, 'thumbsup')).toBe('3 пользователя отреагировал с :thumbsup:')
  expect(reactorTooltipText({ mine: false, names: [], unknown: 12 }, 'thumbsup')).toBe('12 пользователей отреагировал с :thumbsup:')
})
