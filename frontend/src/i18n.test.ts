import { dict, setLocale, t } from './i18n'

test('ru and en have exactly the same keys and no empty values', () => {
  const ru = Object.keys(dict.ru).sort()
  const en = Object.keys(dict.en).sort()
  expect(en).toEqual(ru)
  for (const loc of ['ru', 'en'] as const) {
    for (const [k, v] of Object.entries(dict[loc])) expect(v, `${loc}.${k}`).not.toBe('')
  }
})

test('t interpolates variables and falls back to the key', () => {
  setLocale('en')
  expect(t('server.signedInAs', { name: 'alice' })).toBe('Signed in as alice')
  expect(t('no.such.key' as never)).toBe('no.such.key')
  setLocale('ru')
})
