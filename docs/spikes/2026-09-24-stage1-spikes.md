# Спайки этапа 1 — результаты

Дата прогона: 2026-09-24. Машина: Linux (Cinnamon-подобное окружение),
webkit2gtk-4.1, `github.com/wailsapp/wails/v3 v3.0.0-beta.25` /
`@wailsio/runtime 3.0.0-beta.25`.

## S1. Вход через mmauth:// (Linux)

- Регистрация схемы (`make install-dev-linux`): **работает** (2026-09-24, с
  разрешения пользователя) — `~/.local/share/applications/spk-mattermost.desktop`,
  `xdg-mime query default x-scheme-handler/mmauth` → `spk-mattermost.desktop`.
- Доставка URL во второй экземпляр: **проверено автоматически** (без реальной
  регистрации схемы, напрямую через `OnSecondInstanceLaunch`/`SecondInstanceData.Args`,
  см. Task 13 report, пункт (d)) — второй запуск с
  `mmauth://callback?MMAUTHTOKEN=x&srv=http://127.0.0.1:1` в аргументах
  завершается быстро (~0.05–0.27 с), первый экземпляр логирует
  `WARN login failed code=no_pending_login` (ожидаемо — вход не был начат) и
  поднимает своё окно. Механизм доставки deep link в работающий экземпляр
  работает.
- Вход на реальный сервер (Mattermost 10.11.22, mm.citeck.ru) через GitLab:
  **работает** (2026-09-24, вход в GitLab выполнил пользователь). Кнопка
  «Sign in with GitLab» открыла системный браузер, после входа браузер передал
  `mmauth://callback?…` приложению, лог: `signed in via GitLab srv=1
  username=pavel.simonov`, токен сохранён в `servers`.
- Windows/macOS: не проверено (нет машин) — проверяется в этапе 4 (CI + ручная
  проверка), как и предполагала спецификация.

## S2. Уведомления Wails v3 (Linux)

- Показ уведомления (тестовый пункт трея в dev-сборке): **работает**
  (проверил пользователь, Cinnamon).
- Клик по уведомлению → окно на передний план: **работает** — лог
  `notification clicked data=map[id:… target:test]`, пользователь подтвердил.
- Трей (клик по иконке скрывает/показывает окно, меню): **работает**
  (проверил пользователь).
- Решение: используем сервис Wails (`pkg/services/notifications`), обёрнутый
  собственным `notifier` (`internal/desktop/notify.go`). На Linux его
  `ServiceStartup` делает синхронный `dbus.ConnectSessionBus()` без таймаута
  (см. S3) — оборачиваем в `startWithTimeout` с таймаутом 2 с и деградируем до
  тихих no-op отправок с `WARN`-логом при недоступности D-Bus, вместо падения
  приложения. Код клика (`OnNotificationResponse` → `show()`) реализован и
  собирается; клик проверен (см. выше).

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

### Этап 2, задача 13: Go-часть после предзагрузки 100 каналов (2026-09-24)

Замер только Go-процесса (browser-режим — без WebKit, тот бюджет уже учтён
в замере выше): `make build`, свежий `SPK_MATTERMOST_HOME`,
`--mm-fake --mm-fake-channels 100 --test-api`, вход alice/secret. Через
HTTP API (`OpenChannel`) открыты все 100 `load-NNN` каналов (по 20 постов
каждый) плюс Town Square (150 постов) и Off-Topic — везде окно в 60
последних постов держится в памяти по правилу «горячего слоя». Замер через
scripts/pss.sh <pid> дважды с интервалом 3 с (значение не растёт):

```
   PRIVATE        PSS  PID CMD
   16.6 MB    27.8 MB  spk-mattermost --browser --port 5182 --mm-fake --mm-fake-channels 100 --test-api
TOTAL PRIVATE: 16.6 MB (budgeted)   TOTAL PSS: 27.8 MB (reference)
```

Итог: 16.6 МБ Private_Dirty для ~100 открытых каналов — заметно меньше
бюджета в 75 МБ, оценённого в замере выше. Виртуализация ленты (React,
не Go) и `react-markdown`/`remark-*` живут во фронтенд-процессе (WebKit в
desktop-режиме), в этот замер не входят.

### Release-сборка и GPU-политика WebKitGTK (2026-09-24)

WebKitGTK 2.52.3, NVIDIA 580 + Mesa, X11. Тот же сценарий (пустое окно, свежий
`SPK_MATTERMOST_HOME`, 25 с), по одному прогону на вариант; разброс между
повторами одного варианта ±3–4 МБ. Колонки — Private_Dirty (MB): главный
процесс / WebKitWebProcess / итого (Network везде 7.4):

| Сборка | `SPK_MATTERMOST_GPU` | main | Web | TOTAL | PSS |
|---|---|---|---|---|---|
| dev (`build-desktop`) | always (default) | 39.4 | 30.8 | 77.6 | 146 |
| dev | ondemand | 39.3 | 34.7 | 81.4 | 152 |
| dev | never | 39.3 | 31.4 | 78.1 | 149 |
| release (`make release`) | always | 38.4–39.4 | 30.4–32.0 | 76.2–78.9 | 145–148 |
| release | ondemand | 35.2 | 31.5 | 74.1 | 143 |
| release | never | 38.4 | 30.3 | 76.1 | 145 |
| release | always + `GDK_GL=disable` | 38.5 | 30.4 | 76.2 | 146 |
| release | always + `WEBKIT_DISABLE_DMABUF_RENDERER=1` | 39.3 | 31.5 | 78.2 | 148 |

Итог: ни release-сборка, ни политика аппаратного ускорения, ни GL-переменные
окружения память оболочки не меняют (всё в пределах шума). ~12 МБ Private_Dirty
главного процесса — релокации GL-драйверов (libLLVM из Mesa ~6.6, NVIDIA ~4,
gallium ~1.2); их грузит сам WebKit в UI-процессе при любой политике, `maps`
одинаков во всех вариантах. Экономить на оболочке нечего — бюджет этапа 2
считаем от ~75–80 МБ.

Решение: политика по умолчанию остаётся Wails-овской (`Always`). Переменная
`SPK_MATTERMOST_GPU=always|ondemand|never` оставлена как аварийный переключатель
для сломанных GPU-драйверов (`internal/desktop/instance.go`). Для замеров рядом с
основным клиентом: при нестандартном `SPK_MATTERMOST_HOME` single-instance ID
получает суффикс-хэш каталога, так что второй экземпляр не пересылает запуск в
основной (но и `mmauth://` уходит в основной).

## S5. Язык интерфейса (2026-09-24)

UI берёт язык из `navigator.language`, трей — из окружения; обе стороны
следуют **языку сообщений** системы по правилам gettext (`LANGUAGE`, затем
`LC_ALL` > `LC_MESSAGES` > `LANG`; `LANGUAGE` игнорируется при локали C).
WebKitGTK выставляет `navigator.language` по тем же правилам — проверено
запуском с `LANGUAGE=ru LANG=ru_RU.UTF-8`: окно «Добавить сервер Mattermost»
по-русски.

На машине разработки язык сообщений английский (`LANG=en_US.UTF-8`,
`LANGUAGE=en_US`), русские только региональные форматы (`LC_TIME`,
`LC_NUMERIC`…), поэтому UI там английский — это корректно, а не ошибка
детекта. Явный выбор языка — настройка в UI (вопрос к пользователю, см. план
этапа 2).
