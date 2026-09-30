import { ApiError } from './api/client'
import { errorMessage } from './errors'
import { setLocale } from './i18n'

test('maps known codes to localized text and unknown ones to the generic message', () => {
  setLocale('en')
  expect(errorMessage(new ApiError('bad_credentials', ''))).toBe('Wrong login or password')
  expect(errorMessage(new ApiError('weird_code', 'x'))).toBe('Something went wrong (weird_code)')
  expect(errorMessage(new Error('boom'))).toBe('Something went wrong (boom)')
  setLocale('ru')
})

test('jump and gap codes have their own text', () => {
  setLocale('en')
  expect(errorMessage(new ApiError('post_gone', ''))).toBe('The message was deleted or is not available')
  expect(errorMessage(new ApiError('no_progress', ''))).toBe('Could not load newer messages')
  expect(errorMessage(new ApiError('cancelled', ''))).toBe('Cancelled')
  setLocale('ru')
  expect(errorMessage(new ApiError('post_gone', ''))).toBe('Сообщение удалено или недоступно')
})

test('offline (a search asked nothing) has its own text', () => {
  setLocale('en')
  expect(errorMessage(new ApiError('offline', ''))).toBe('No connection to the server')
  setLocale('ru')
  expect(errorMessage(new ApiError('offline', ''))).toBe('Нет связи с сервером')
})
