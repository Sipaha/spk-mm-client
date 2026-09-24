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
