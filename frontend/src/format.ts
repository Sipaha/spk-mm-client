import { t } from './i18n'
import { useStore } from './store'

// Dates follow the system format locale (LC_TIME from Go), not the UI language.
export const formatLocale = () => useStore.getState().info.format_locale || navigator.language || 'en-US'

export function formatTime(ms: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit' }).format(ms)
}

const dayStart = (ms: number) => {
  const d = new Date(ms)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

export function formatDay(ms: number, locale: string, now = Date.now()): string {
  const days = Math.round((dayStart(now) - dayStart(ms)) / 86_400_000) // round: DST days are 23/25 h
  if (days === 0) return t('day.today')
  if (days === 1) return t('day.yesterday')
  const sameYear = new Date(ms).getFullYear() === new Date(now).getFullYear()
  return new Intl.DateTimeFormat(locale, { weekday: 'long', day: 'numeric', month: 'long', ...(sameYear ? {} : { year: 'numeric' }) }).format(ms)
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
