import { describe, expect, test } from 'vitest'
import type { AutocompleteDTO } from './api/types'
import { applyCompletion, buildItems, completionText, emojiItems, findTrigger } from './autocomplete'
import { buildIndex } from './emoji/index'

const at = (text: string, caret = text.length) => findTrigger(text, caret)

describe('findTrigger', () => {
  test('@ opens from the trigger itself, at the start or after a non-word character', () => {
    expect(at('@')).toEqual({ kind: 'users', prefix: '', start: 0, end: 1 })
    expect(at('hi @bo')).toEqual({ kind: 'users', prefix: 'bo', start: 3, end: 6 })
    expect(at('(@bo')).toMatchObject({ kind: 'users', prefix: 'bo', start: 1 })
    expect(at('hi @bob.sm')).toMatchObject({ prefix: 'bob.sm' })
  })

  test('an email address or a word does not trigger', () => {
    expect(at('a@b')).toBeNull()
    expect(at('mail alice@example')).toBeNull()
    expect(at('@bob ')).toBeNull() // the word ended
  })

  test('~ opens from the trigger itself; not after ~ or a word', () => {
    expect(at('see ~off')).toEqual({ kind: 'channels', prefix: 'off', start: 4, end: 8 })
    expect(at('~')).toMatchObject({ kind: 'channels', prefix: '' })
    expect(at('a~b')).toBeNull()
    expect(at('~~x')).toBeNull()
  })

  test(': needs two characters and a word start; not mid-word', () => {
    expect(at(':t')).toBeNull()
    expect(at(':th')).toEqual({ kind: 'emoji', prefix: 'th', start: 0, end: 3 })
    expect(at('ok :+1')).toMatchObject({ kind: 'emoji', prefix: '+1', start: 3 })
    expect(at('12:30')).toBeNull()
    expect(at('a:thu')).toBeNull()
    expect(at('http://x')).toBeNull()
    expect(at(':-)')).toBeNull()
    expect(at(':thumbsup: ')).toBeNull()
  })

  test('/ only at the very start of the message, while the command word is typed', () => {
    expect(at('/')).toEqual({ kind: 'commands', prefix: '', start: 0, end: 1 })
    expect(at('/ec')).toEqual({ kind: 'commands', prefix: 'ec', start: 0, end: 3 })
    expect(at('/echo hi')).toBeNull()
    expect(at('a /ec')).toBeNull()
    expect(at(' /ec')).toBeNull()
    expect(at('x\n/ec')).toBeNull()
  })

  test('only the text before the caret counts', () => {
    expect(at('hi @bob', 6)).toMatchObject({ prefix: 'bo', end: 6 })
    expect(at('hi @bob and more', 16)).toBeNull()
  })
})

describe('applyCompletion', () => {
  test('replaces the typed word and adds one space', () => {
    expect(applyCompletion('hi @bo', { start: 3, end: 6 }, '@bob')).toEqual({ value: 'hi @bob ', caret: 8 })
    expect(applyCompletion('hi @bo there', { start: 3, end: 6 }, '@bob')).toEqual({ value: 'hi @bob there', caret: 8 })
    expect(applyCompletion('/ec', { start: 0, end: 3 }, '/echo')).toEqual({ value: '/echo ', caret: 6 })
  })
})

const dto = (d: Partial<AutocompleteDTO>): AutocompleteDTO => ({ users: null, others: null, channels: null, emoji: null, commands: null, ...d })

describe('buildItems', () => {
  test('users: channel members, then matching special mentions, then the others', () => {
    const items = buildItems({ kind: 'users', prefix: 'a', start: 0, end: 2 }, dto({
      users: [{ id: 'u1', username: 'alice', me: true }],
      others: [{ id: 'u3', username: 'anna' }],
    }))
    expect(items.map((i) => [i.group, completionText(i)])).toEqual([
      ['members', '@alice'],
      ['special', '@all'],
      ['others', '@anna'],
    ])
    const none = buildItems({ kind: 'users', prefix: 'h', start: 0, end: 2 }, null)
    expect(none.map(completionText)).toEqual(['@here'])
  })

  test('channels: joined ones as "my channels", the rest as "other channels"', () => {
    const items = buildItems({ kind: 'channels', prefix: 'off', start: 0, end: 4 }, dto({
      channels: [
        { id: 'c1', name: 'off-topic', display_name: 'Off-Topic', type: 'O', joined: true },
        { id: 'c2', name: 'offices', display_name: 'Offices', type: 'O' },
      ],
    }))
    expect(items.map((i) => [i.group, completionText(i)])).toEqual([
      ['myChannels', '~off-topic'],
      ['otherChannels', '~offices'],
    ])
  })

  test('commands', () => {
    const items = buildItems({ kind: 'commands', prefix: 'e', start: 0, end: 2 }, dto({ commands: [{ trigger: 'echo', hint: 'text' }] }))
    expect(items.map(completionText)).toEqual(['/echo'])
  })
})

describe('emojiItems', () => {
  const idx = buildIndex(['👍 +1 thumbsup\n👎 -1 thumbsdown\n😄 smile\n🎉 tada\n🤔 thinking_face', ''])

  test('matches any alias anywhere, prefix matches first, thumbs up before down, recent first', () => {
    expect(emojiItems('thu', idx, [], []).map((i) => i.name)).toEqual(['thumbsup', 'thumbsdown'])
    expect(emojiItems('in', idx, [], []).map((i) => i.name)).toEqual(['thinking_face'])
    expect(emojiItems('ta', idx, ['tacocat'], ['tacocat']).map((i) => i.name)).toEqual(['tacocat', 'tada'])
  })

  test('custom emoji after standard ones of the same rank; a standard name wins over a custom one', () => {
    const items = emojiItems('sm', idx, ['smile', 'smiley_cat'], [])
    expect(items.map((i) => [i.name, i.custom])).toEqual([
      ['smile', false],
      ['smiley_cat', true],
    ])
  })

  test('without the standard set loaded: custom only', () => {
    expect(emojiItems('par', null, ['partyparrot'], []).map((i) => i.name)).toEqual(['partyparrot'])
  })
})
