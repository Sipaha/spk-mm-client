import { setLocale } from './i18n'
import { formatDay, formatSize, formatTime } from './format'

const at = (y: number, m: number, d: number, h = 12, min = 0) => new Date(y, m - 1, d, h, min).getTime()
const norm = (s: string) => s.replace(/\s/g, ' ')

beforeEach(() => setLocale('ru'))

test('time follows the format locale', () => {
  expect(formatTime(at(2026, 9, 24, 13, 5), 'ru-RU')).toBe('13:05')
  expect(norm(formatTime(at(2026, 9, 24, 13, 5), 'en-US'))).toBe('01:05 PM')
})

test('day labels: today, yesterday, weekday date, other year', () => {
  const now = at(2026, 9, 24, 9)
  expect(formatDay(at(2026, 9, 24, 23, 59), 'ru-RU', now)).toBe('Сегодня')
  expect(formatDay(at(2026, 9, 23, 0, 1), 'ru-RU', now)).toBe('Вчера')
  expect(formatDay(at(2026, 9, 20), 'ru-RU', now)).toBe('воскресенье, 20 сентября')
  expect(formatDay(at(2025, 12, 31), 'ru-RU', now)).toBe('среда, 31 декабря 2025 г.')
})

test('sizes', () => {
  expect(formatSize(512)).toBe('512 B')
  expect(formatSize(1536)).toBe('1.5 KB')
  expect(formatSize(3 * 1024 * 1024)).toBe('3.0 MB')
})
