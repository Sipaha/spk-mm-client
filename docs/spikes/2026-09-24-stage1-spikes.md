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

- **На Linux D-Bus без таймаута дёргают сразу несколько мест Wails и сама
  GLib.** (1) `SingleInstance`: `application.New()` при непустом
  `Options.SingleInstance` делает `dbus.ConnectSessionBus()` и при неудаче
  зовёт внутренний `fatal()` → `os.Exit(1)` ещё **до возврата** из
  `application.New()` — не перехватить ни `defer`, ни `recover`. (2) Сервис
  уведомлений: синхронный `dbus.ConnectSessionBus()` в `ServiceStartup`.
  (3) Трей: `SystemTray.Run` делает `InvokeSync(dbus.SessionBus())` на
  главном GTK-потоке — при «зависшей» шине (сокет принимает соединение и
  молчит) замерзает весь UI. (4) GLib: `g_application_run` регистрирует
  `GApplication` на session bus без таймаута — при зависшей шине окно не
  появляется вообще, даже если (1)–(3) выключены (проверено: без п. ниже
  окно не появилось за 10 с, главный поток стоял в `g_application_run`).
  Слушатель темы Wails (`monitorThemeChanges`, gtk3) и монитор питания
  (system bus) работают в своих горутинах на собственных соединениях и
  главный поток не блокируют; `isDarkMode` в gtk3-сборке D-Bus не трогает.
- **Решение — одна проба, одно решение.** `internal/desktop/busprobe_linux.go`
  один раз пробует `dbus.ConnectSessionBus()` с таймаутом 2 с
  (`probeBus` → `busOK` / `busUnreachable` / `busTimedOut`);
  `integrationsFor` (`internal/desktop/integrations.go`, без тега, с
  юнит-тестом) по результату решает: шина в порядке → single-instance,
  уведомления и трей включены, закрытие окна прячет его в трей; шина
  недоступна **или** не ответила → всё это выключено, `DBUS_SESSION_BUS_ADDRESS`
  процесса подменяется мёртвым адресом (`unix:path=/dev/null/…`, до
  инициализации GTK — GLib, a11y и дочерние процессы WebKit падают сразу, а
  не висят), закрытие окна завершает приложение (трея нет — окно было бы не
  вернуть). Уведомления при этом не ждут второй таймаут (`notifier.disable`).
  На Windows/macOS D-Bus нет — проба не выполняется
  (`internal/desktop/busprobe_other.go`). Проверено вручную: обычный запуск —
  окно, SNI-трей на шине, закрытие прячет окно; зависшая шина — окно через
  ~2,5 с, закрытие завершает процесс; недоступная шина — окно через ~0,5 с,
  закрытие завершает процесс.
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

Бюджетная метрика — **Private_Dirty** всех процессов клиента (память, которую
держит только наш клиент и которую ядро не может сбросить без свопа). PSS —
справочно: он включает долю общих библиотек WebKit/GTK/ICU, которые делятся с
другими приложениями на WebKit (на этой машине их использует ещё citeck-launcher,
Evolution и др.), поэтому «плавает» в зависимости от того, что запущено.
Решение пользователя 2026-09-24. Замер — `scripts/pss.sh <pid>` (печатает обе
метрики).

Пустое окно «Добавить сервер», 0 серверов, dev-сборка (`make build-desktop`,
devtools включены), свежий `SPK_MATTERMOST_HOME`, ~15 с после появления окна:

```
   PRIVATE        PSS  PID CMD
   35.0 MB    85.7 MB  build/bin/spk-mattermost-desktop
    7.4 MB    15.6 MB  webkit2gtk-4.1/WebKitNetworkProcess
   30.9 MB    86.4 MB  webkit2gtk-4.1/WebKitWebProcess
TOTAL PRIVATE: 73.3 MB (budgeted)   TOTAL PSS: 187.7 MB (reference)
```

Более ранние замеры только PSS давали 150–196 МБ — разброс как раз из-за доли
общих страниц. Устойчивого роста в простое (замеры через ~20 с и ~90 с) не было.

Итог: бюджет ≤150 МБ Private_Dirty для 2–3 серверов и ~100 каналов оставляет
~75 МБ на сам чат (кэш постов в Go ~10–35 МБ по оценке спецификации + DOM
виртуализированной ленты). Реалистично, но следить с первых задач этапа 2.
Не проверено: release-сборка (без devtools) и `WebviewGpuPolicy` WebKitGTK
(по умолчанию GPU-композитинг включён) — кандидаты на экономию.
