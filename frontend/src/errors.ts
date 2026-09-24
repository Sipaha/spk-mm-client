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
