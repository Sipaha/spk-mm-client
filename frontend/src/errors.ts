import { ApiError } from './api/client'
import { t, type I18nKey } from './i18n'

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const key = `err.${err.code}` as I18nKey
    const text = t(key)
    if (text !== key) return text
    return t('err.generic', { detail: err.code })
  }
  return t('err.generic', { detail: err instanceof Error ? err.message : String(err) })
}

// downloadErrorMessage: a failed download's error code (a CodedError code,
// or "interrupted" — the app quit mid-download), localized the same way as
// errorMessage — a known err.<code> key, else the generic fallback naming
// the code.
export function downloadErrorMessage(code: string): string {
  const key = `err.${code}` as I18nKey
  const text = t(key)
  return text !== key ? text : t('err.generic', { detail: code })
}
