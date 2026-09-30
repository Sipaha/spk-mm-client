import { parseSearchTerms } from './terms'

test('plain words, split like the server splits them', () => {
  expect(parseSearchTerms('привет мир')).toEqual(['привет', 'мир'])
  expect(parseSearchTerms('  hello   world ')).toEqual(['hello', 'world'])
})

test('a quoted phrase is one term, as a whole', () => {
  expect(parseSearchTerms('"привет мир" foo')).toEqual(['привет мир', 'foo'])
  expect(parseSearchTerms('a"b c"')).toEqual(['a', 'b c'])
  expect(parseSearchTerms('"" x')).toEqual(['x'])
})

test('filters are not terms, with or without a space after the colon', () => {
  expect(parseSearchTerms('from:bob in:town-square hello')).toEqual(['hello'])
  expect(parseSearchTerms('hello channel: off-topic on:2026-09-01 before:2026-01-01 after:2025-01-01 ext:pdf')).toEqual(['hello'])
  expect(parseSearchTerms('in:@alice,bob,carol x')).toEqual(['x'])
})

test('negated words, phrases and filters are not highlighted', () => {
  expect(parseSearchTerms('hello -world -from:bob -in:town -"not this" yes')).toEqual(['hello', 'yes'])
})

test('a trailing * stays (prefix), punctuation around a word goes', () => {
  expect(parseSearchTerms('прив* (hello), world!')).toEqual(['прив*', 'hello', 'world'])
})

test('@mentions and #hashtags keep their sign', () => {
  expect(parseSearchTerms('@bob #release notes')).toEqual(['@bob', '#release', 'notes'])
})

test('empty or filters only: no terms; duplicates once', () => {
  expect(parseSearchTerms('')).toEqual([])
  expect(parseSearchTerms('   ')).toEqual([])
  expect(parseSearchTerms('from:bob')).toEqual([])
  expect(parseSearchTerms('*')).toEqual([])
  expect(parseSearchTerms('a A a')).toEqual(['a'])
})
