# Спайки этапа 1 — результаты

Дата прогона: 2026-09-24. Машина: Linux (Cinnamon-подобное окружение),
webkit2gtk-4.1, `github.com/wailsapp/wails/v3 v3.0.0-beta.25` /
`@wailsio/runtime 3.0.0-beta.25`.

## S1. Вход через mmauth:// (Linux)

- Регистрация схемы (`scripts/install-dev-linux.sh`, `make install-dev-linux`):
  **pending — верифицируется с пользователем**. Скрипт пишет
  `~/.local/share/applications/spk-mattermost.desktop` и переключает
  `xdg-mime default … x-scheme-handler/mmauth` — это меняет обработчик
  `mmauth://` у пользователя за пределами каталога солюшена, поэтому
  выполняется контроллером вместе с пользователем, не агентом.
- Доставка URL во второй экземпляр: **проверено автоматически** (без реальной
  регистрации схемы, напрямую через `OnSecondInstanceLaunch`/`SecondInstanceData.Args`,
  см. Task 13 report, пункт (d)) — второй запуск с
  `mmauth://callback?MMAUTHTOKEN=x&srv=http://127.0.0.1:1` в аргументах
  завершается быстро (~0.05–0.27 с), первый экземпляр логирует
  `WARN login failed code=no_pending_login` (ожидаемо — вход не был начат) и
  поднимает своё окно. Механизм доставки deep link в работающий экземпляр
  работает.
- Вход на реальный сервер (Mattermost 10.11.22) через GitLab: **pending —
  верифицируется с пользователем**. Шаг требует, чтобы пользователь вошёл в
  GitLab в открывшемся системном браузере своими учётными данными — агент не
  вводит чужие учётные данные и не может выполнить это headless.
- Windows/macOS: не проверено (нет машин) — проверяется в этапе 4 (CI + ручная
  проверка), как и предполагала спецификация.

## S2. Уведомления Wails v3 (Linux)

- Показ уведомления: **pending — верифицируется с пользователем**. Требует
  живого демона уведомлений рабочего стола и человека, наблюдающего
  всплывающее окно — недоступно headless в этом окружении.
- Клик по уведомлению → окно на передний план: **pending — верифицируется с
  пользователем** (тот же ограничитель — клик по системному уведомлению не
  автоматизируется без реального пользователя за столом).
- Решение: используем сервис Wails (`pkg/services/notifications`), обёрнутый
  собственным `notifier` (`internal/desktop/notify.go`). На Linux его
  `ServiceStartup` делает синхронный `dbus.ConnectSessionBus()` без таймаута
  (см. S3) — оборачиваем в `startWithTimeout` с таймаутом 2 с и деградируем до
  тихих no-op отправок с `WARN`-логом при недоступности D-Bus, вместо падения
  приложения. Код клика (`OnNotificationResponse` → `show()`) реализован и
  собирается; сам клик — см. выше, pending.

## S3. Особенности Wails beta.25, найденные при сборке

Собрано по фактам из Task 10/11/13 (реализация browser-режима, фронтенда и
desktop-раннера):

- **`SingleInstance` на Linux требует D-Bus синхронно, без таймаута, и роняет
  весь процесс.** `application.New()` при непустом `Options.SingleInstance`
  сразу пытается захватить D-Bus session-bus имя; при неудаче зовёт свой
  внутренний `fatal()` → `os.Exit(1)` ещё **до возврата** из `application.New()`
  — это не перехватить ни `defer`, ни `recover`, ни возвратом ошибки. Нарушает
  общее правило «никаких блокирующих обращений к системным сервисам без
  таймаута на старте». Исправлено пробой доступности шины
  (`internal/desktop/singleinstance_linux.go`): `dbus.ConnectSessionBus()` с
  таймаутом 2 с через тот же `startWithTimeout`; если шина недоступна —
  `SingleInstance: nil` (Wails просто не берёт лок, повторный запуск открывает
  второе окно вместо падения приложения). На Windows/macOS single-instance не
  использует D-Bus — там это чистый passthrough
  (`internal/desktop/singleinstance_other.go`).
- **Сервис уведомлений на Linux тоже делает синхронный, неотменяемый
  `dbus.ConnectSessionBus()`** внутри своего `ServiceStartup` — тот же риск, в
  другой подсистеме. Обёрнут `notifier` (реализует
  `application.ServiceStartup`/`ServiceShutdown`) через `startWithTimeout` с
  таймаутом 2 с; при неудаче/таймауте — `WARN`-лог и тихая деградация вместо
  краха.
- **Форма пейлоада событий.** `EventManager.Emit(name string, data ...any)`:
  при ровно одном аргументе (не слайсе) — `event.Data = data[0]`, т.е. на
  фронт приходит сам пейлоад, а не однослайсовый массив. Приложение всегда
  зовёт `app.Event.Emit(ev.Type, ev.Payload)` — один аргумент — поэтому
  «размотка массива из одного элемента» в `wailsClient.subscribeEvents`
  (frontend) фактически мёртвый код для этого приложения (защитный, но не
  используется живым путём emit).
- **API Wails v3 beta.25 совпало с ожидаемым 1:1** — `application.New/Get`,
  `Browser.OpenURL`, `SystemTray.New()`, `Menu.Add/.OnClick/.AddSeparator`,
  `WebviewWindow.RegisterHook`, `WindowEvent.Cancel()`,
  `ApplicationEvent.Context().URL()`, `SingleInstanceOptions`,
  `notifications.New()/OnNotificationResponse/SendNotification`,
  `application.NewService[T]`, `WindowManager.NewWithOptions` — ничего не
  пришлось адаптировать против кода из брифов.
- **Тулчейн фронтенда новее, чем ожидали брифы** (TypeScript 6.0.3, Vite 8.3.0,
  Vitest 4.1.11): `tsconfig.json` потребовал `"vite/client"` в `"types"`
  (иначе `TS2882` на side-effect импорте `./index.css`); `vite.config.ts`
  потребовал `import { defineConfig } from 'vitest/config'` вместо
  `'vite'` (тройной slash-референс на `vitest` больше не подтягивает
  расширение типов `UserConfig.test` надёжно, иначе `TS2769`).
- **Импорт `@wailsio/runtime` на уровне модуля имеет побочные эффекты** —
  вешает обработчики мыши на `window` и `window.setInterval` (перетаскивание/
  ресайз окна, `dist/drag.js`), которые переживают демонтаж jsdom `window`
  между тестовыми файлами и валят весь `vitest run` необработанной ошибкой
  `window is not defined`. Исправлено глобальным `vi.mock('@wailsio/runtime', …)`
  в `frontend/vitest.setup.ts`; тесты `wailsClient` переопределяют мок локально.

## S4. Память оболочки

Измерено `scripts/pss.sh` (сумма PSS процесса `build/bin/spk-mattermost-desktop`
и его потомков — WebKitNetworkProcess, WebKitWebProcess), пустое окно «Добавить
сервер», без подключённого чата, `SPK_MATTERMOST_HOME` — свежий временный
каталог.

- **Сразу после старта** (окно подтверждено `wmctrl -l`, +10 с на осадку,
  ~23 с с момента запуска):

  ```
       73.8 MB  <pid>  build/bin/spk-mattermost-desktop
       12.3 MB  <pid>  webkit2gtk-4.1/WebKitNetworkProcess
       63.6 MB  <pid>  webkit2gtk-4.1/WebKitWebProcess
  TOTAL PSS: 149.7 MB
  ```

- **После ~60 секунд** (без взаимодействия, окно на переднем плане, ~92 с с
  момента запуска):

  ```
       70.5 MB  <pid>  build/bin/spk-mattermost-desktop
       12.3 MB  <pid>  webkit2gtk-4.1/WebKitNetworkProcess
       67.5 MB  <pid>  webkit2gtk-4.1/WebKitWebProcess
  TOTAL PSS: 150.3 MB
  ```

  Рост между замерами — ~0.6 МБ (149.7 → 150.3), в пределах шума измерения;
  устойчивого роста в состоянии простоя не наблюдается.

- Ориентир бюджета из спецификации — ≤150 МБ **с 2–3 серверами и ~100
  каналами**, без роста за час. Замеренное здесь — пустая оболочка (0
  серверов, экран «Добавить сервер»), поэтому число уже стоит вплотную к
  верхней границе бюджета для куда более скромного состояния — тревожный
  сигнал для этапа 2: как только появится реальный контент (лента, рендер
  markdown, файлы), фактическое использование почти наверняка превысит 150 МБ.
  Нужно будет держать бюджет в фокусе с первых тестов синхронизации, а не
  откладывать до этапа 4.
