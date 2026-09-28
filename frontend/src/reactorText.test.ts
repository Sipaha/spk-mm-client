import { setLocale } from './i18n'
import { reactorTooltipText } from './reactorText'

beforeEach(() => setLocale('en'))

test('1 reactor, mine', () => {
  expect(reactorTooltipText({ mine: true, names: [], unknown: 0 }, 'thumbsup')).toBe('You reacted :thumbsup:')
})

test('2 reactors, mine + one other', () => {
  expect(reactorTooltipText({ mine: true, names: ['bob'], unknown: 0 }, 'thumbsup')).toBe('You and bob reacted :thumbsup:')
})

test('3 reactors, mine + two others (the goal example)', () => {
  expect(reactorTooltipText({ mine: true, names: ['bob', 'carol'], unknown: 0 }, 'thumbsup')).toBe('You, bob and carol reacted :thumbsup:')
})

test('not mine, with unresolved reactors folded into "and N others"', () => {
  expect(reactorTooltipText({ mine: false, names: ['bob', 'carol'], unknown: 3 }, 'thumbsup')).toBe('bob, carol and 3 others reacted :thumbsup:')
})

test('exactly one overflow entry uses the singular', () => {
  expect(reactorTooltipText({ mine: false, names: ['bob'], unknown: 1 }, 'thumbsup')).toBe('bob and 1 other reacted :thumbsup:')
})

test('12 reactors, not mine: the first 10 names shown, 2 folded into the tail', () => {
  const names = Array.from({ length: 12 }, (_, i) => `user${i}`)
  expect(reactorTooltipText({ mine: false, names, unknown: 0 }, 'thumbsup')).toBe(
    'user0, user1, user2, user3, user4, user5, user6, user7, user8, user9 and 2 others reacted :thumbsup:',
  )
})

test('12 reactors, mine: "You" counts toward the first 10 shown', () => {
  const names = Array.from({ length: 11 }, (_, i) => `user${i}`)
  expect(reactorTooltipText({ mine: true, names, unknown: 0 }, 'thumbsup')).toBe(
    'You, user0, user1, user2, user3, user4, user5, user6, user7, user8 and 2 others reacted :thumbsup:',
  )
})

test('ru: matches the brief examples exactly', () => {
  setLocale('ru')
  expect(reactorTooltipText({ mine: true, names: [], unknown: 0 }, 'thumbsup')).toBe('Вы отреагировали :thumbsup:')
  expect(reactorTooltipText({ mine: true, names: ['bob'], unknown: 0 }, 'thumbsup')).toBe('Вы и bob отреагировали :thumbsup:')
  expect(reactorTooltipText({ mine: true, names: ['bob', 'carol'], unknown: 0 }, 'thumbsup')).toBe('Вы, bob и carol отреагировали :thumbsup:')
  expect(reactorTooltipText({ mine: false, names: ['bob', 'carol'], unknown: 3 }, 'thumbsup')).toBe('bob, carol и ещё 3 отреагировали :thumbsup:')
})
