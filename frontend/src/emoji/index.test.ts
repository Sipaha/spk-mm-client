import data from './data'
import { buildIndex, CATEGORY_IDS, emojiChar, searchEmoji } from './index'

const idx = buildIndex(data)

test('the dataset: nine categories in the webapp order, aliases share one emoji', () => {
  expect(idx.categories.map((c) => c.id)).toEqual([...CATEGORY_IDS])
  expect(idx.categories.map((c) => c.emojis.length)).toEqual([151, 347, 140, 129, 215, 84, 250, 220, 269])
  expect(idx.categories[0].emojis[0]).toEqual({ char: '😀', names: ['grinning'] })
  expect(idx.byName.get('+1')?.char).toBe('👍')
  expect(idx.byName.get('thumbsup')).toBe(idx.byName.get('+1'))
  expect(idx.byName.get('+1')?.names[0]).toBe('+1')
})

test('emojiChar: common names before the set loads, skin tones, custom → null', () => {
  expect(emojiChar('+1', null)).toBe('👍')
  expect(emojiChar('rocket_ship_to_nowhere', null)).toBeNull()
  expect(emojiChar('+1_light_skin_tone', idx)).toBe('👍🏻')
  expect(emojiChar('v_medium_dark_skin_tone', idx)).toBe('✌🏾')
  expect(emojiChar('partyparrot', idx)).toBeNull()
})

test('search: exact name first, then prefixes, aliases count', () => {
  expect(searchEmoji(idx, 'smile')[0].names[0]).toBe('smile')
  const thumbs = searchEmoji(idx, 'thumbs')
  expect(thumbs.slice(0, 2).map((e) => e.names[0])).toEqual(['+1', '-1'])
  expect(searchEmoji(idx, '   ')).toEqual([])
})
