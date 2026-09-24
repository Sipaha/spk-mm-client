const ru = {
  'app.title': 'spk-mattermost',
  'rail.add': 'Добавить сервер',
  'add.title': 'Добавить сервер Mattermost',
  'add.urlLabel': 'Адрес сервера',
  'add.urlPlaceholder': 'mm.example.com',
  'add.submit': 'Добавить',
  'add.checking': 'Проверяю сервер…',
  'server.signedInAs': 'Вы вошли как {name}',
  'server.signedOut': 'Вы не вошли',
  'server.gitlab': 'Войти через GitLab',
  'server.gitlabWaiting': 'Завершите вход в открывшемся браузере',
  'server.orPassword': 'или по логину и паролю',
  'server.login': 'Логин или email',
  'server.password': 'Пароль',
  'server.signIn': 'Войти',
  'server.signOut': 'Выйти',
  'server.remove': 'Удалить сервер',
  'server.removeConfirm': 'Удалить сервер {name}?',
  'err.invalid_url': 'Некорректный адрес сервера',
  'err.unreachable': 'Сервер недоступен',
  'err.not_mattermost': 'По этому адресу не сервер Mattermost',
  'err.server_exists': 'Этот сервер уже добавлен',
  'err.not_found': 'Сервер не найден',
  'err.gitlab_disabled': 'На сервере выключен вход через GitLab',
  'err.bad_credentials': 'Неверный логин или пароль',
  'err.auth_failed': 'Сервер не принял вход, попробуйте ещё раз',
  'err.login_mismatch': 'Ответ входа не относится к добавленному серверу',
  'err.no_pending_login': 'Вход не был начат из приложения',
  'err.generic': 'Что-то пошло не так ({detail})',
} as const

type Key = keyof typeof ru

const en: Record<Key, string> = {
  'app.title': 'spk-mattermost',
  'rail.add': 'Add server',
  'add.title': 'Add a Mattermost server',
  'add.urlLabel': 'Server address',
  'add.urlPlaceholder': 'mm.example.com',
  'add.submit': 'Add',
  'add.checking': 'Checking server…',
  'server.signedInAs': 'Signed in as {name}',
  'server.signedOut': 'Not signed in',
  'server.gitlab': 'Sign in with GitLab',
  'server.gitlabWaiting': 'Finish signing in in the browser window',
  'server.orPassword': 'or with login and password',
  'server.login': 'Login or email',
  'server.password': 'Password',
  'server.signIn': 'Sign in',
  'server.signOut': 'Sign out',
  'server.remove': 'Remove server',
  'server.removeConfirm': 'Remove server {name}?',
  'err.invalid_url': 'Invalid server address',
  'err.unreachable': 'Server is unreachable',
  'err.not_mattermost': 'This address is not a Mattermost server',
  'err.server_exists': 'This server is already added',
  'err.not_found': 'Server not found',
  'err.gitlab_disabled': 'GitLab sign-in is disabled on this server',
  'err.bad_credentials': 'Wrong login or password',
  'err.auth_failed': 'The server rejected the sign-in, try again',
  'err.login_mismatch': 'The sign-in response does not belong to an added server',
  'err.no_pending_login': 'Sign-in was not started from the app',
  'err.generic': 'Something went wrong ({detail})',
}

export const dict = { ru, en }
export type Locale = keyof typeof dict
export type I18nKey = Key

let locale: Locale = navigator.language?.toLowerCase().startsWith('ru') ? 'ru' : 'en'

export function setLocale(l: Locale) {
  locale = l
}

export function t(key: I18nKey, vars?: Record<string, string>): string {
  let s: string = dict[locale][key] ?? key
  for (const [k, v] of Object.entries(vars ?? {})) s = s.replaceAll(`{${k}}`, v)
  return s
}
