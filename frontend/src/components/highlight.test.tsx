import { render } from '@testing-library/react'
import { Markdown } from './Markdown'
import { parseSearchTerms } from '../search/terms'
import { hitTerms } from './remarkHighlight'

const marks = (text: string, highlight: string[]) => {
  const { container } = render(<Markdown text={text} me="alice" onLink={() => {}} highlight={highlight} />)
  return { container, marked: [...container.querySelectorAll('mark')].map((m) => m.textContent) }
}

test('Cyrillic words, any case, whole words only', () => {
  const { marked } = marks('Привет, мир! ПРИВЕТСТВИЕ и снова привет', ['привет'])
  expect(marked).toEqual(['Привет', 'привет'])
})

test('a trailing * highlights by prefix, case-insensitively', () => {
  expect(marks('Приветствие ПРИВЕТ спривет', ['прив*']).marked).toEqual(['Приветствие', 'ПРИВЕТ'])
})

test('a phrase is highlighted as a whole where it occurs', () => {
  expect(marks('say привет   мир today, мир', ['привет мир']).marked).toEqual(['привет   мир'])
})

test('a phrase that does not occur in the post falls back to its words', () => {
  expect(marks('мир, а потом привет', ['привет мир']).marked).toEqual(['мир', 'привет'])
})

test('code is never highlighted, emphasis is', () => {
  const { container, marked } = marks('**hello** `hello` and\n\n```\nhello\n```', ['hello'])
  expect(marked).toEqual(['hello'])
  expect(container.querySelector('strong mark')).not.toBeNull()
  expect(container.querySelector('code mark')).toBeNull()
})

test('a link text is highlighted, its URL is not changed', () => {
  const { container, marked } = marks('see [the hello page](https://hello.example/hello) and https://hello.example', ['hello'])
  expect(marked).toEqual(['hello', 'hello'])
  const links = [...container.querySelectorAll('a')]
  expect(links[0].getAttribute('href')).toBe('https://hello.example/hello')
  expect(links[1].getAttribute('href')).toBe('https://hello.example')
})

test('regex special characters are literal', () => {
  expect(marks('a c++ b x c.d cxd', ['c.d', '(x)', 'c+']).marked).toEqual(['c+', 'c.d'])
  expect(marks('price $5 and 5', ['$5']).marked).toEqual(['$5'])
})

test('mentions keep working and are highlighted inside', () => {
  const { container, marked } = marks('ping @bob now', ['@bob'])
  expect(marked).toEqual(['@bob'])
  expect(container.querySelector('[data-mention="bob"] mark')).not.toBeNull()
})

test('a phrase split by formatting falls back to its words', () => {
  expect(marks('**привет** мир', ['привет мир']).marked).toEqual(['привет', 'мир'])
})

test('filters and negated words of a query are not highlighted', () => {
  expect(marks('from bob hello world', parseSearchTerms('from:bob -hello world')).marked).toEqual(['world'])
})

test('no terms: nothing marked', () => {
  expect(marks('hello', []).marked).toEqual([])
})

test("a hit's own matches (Elasticsearch) win over the session's terms", () => {
  expect(hitTerms({ matches: ['Hellos'] }, ['hello'])).toEqual(['Hellos'])
  expect(hitTerms({ matches: [] }, ['hello'])).toEqual(['hello'])
})
