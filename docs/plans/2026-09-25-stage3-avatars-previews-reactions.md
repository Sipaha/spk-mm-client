# spk-mattermost — Этап 3, часть 1: аватары и статусы, превью файлов, реакции. План реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> Проект переименован в spk-mm-client (Task 0, 2026-09-25): модуль
> `github.com/spk/spk-mm-client`, `cmd/spk-mm-client`, переменные
> `SPK_MM_CLIENT_*`, данные `~/.spk/mm-client`; ниже старые имена читать как
> новые.

**Goal:** Лента и сайдбар выглядят и работают как у официального клиента в трёх местах, которых не хватило на живой проверке этапа 2: аватары с точкой статуса присутствия (в ленте и у DM), превью картинок и текстовых файлов с просмотрщиком и скачиванием любых файлов, постановка и снятие реакций (клик по чипу, пикер эмодзи с поиском, недавними и кастомными эмодзи).

**Architecture:** Картинки и фрагменты файлов UI получает только с того же origin по `/media/<server>/<kind>/<key>`: Go-пакет `internal/media` достаёт объект REST-клиентом сервера (токен и лимитер запросов не покидают Go), проверяет тип и размер, уменьшает картинки ленты и держит их в дисковом LRU-кэше с лимитом; в desktop этот обработчик стоит в asset handler Wails, в browser mode — в HTTP-сервере за cookie страницы. Статусы присутствия живут в горячем слое (`state`) и опрашиваются воркером (`POST /users/status/ids`) для видимых пользователей; реакции ставятся оптимистично с откатом при ошибке и отбрасыванием устаревших эхо. Набор эмодзи — тот же, что у Mattermost, сгенерированный компактный модуль в ленивом чанке вместе с пикером.

**Tech Stack:** Go 1.26, Wails v3.0.0-beta.25, `golang.org/x/image/draw` (новая зависимость — масштабирование), `github.com/adrg/xdg` (уже косвенная, становится прямой — каталог «Загрузки»), React 19, TypeScript 6, Vite 8, Tailwind 4, Zustand 5, `@tanstack/react-virtual` 3, Vitest, Playwright.

**Проверка плана:** код задач 1–13 прогнан на копии репозитория 2026-09-25 — `go test -race ./...`, `go vet` и `golangci-lint` для обоих наборов тегов, сборка desktop и кросс-сборка Windows, `vitest` (108 тестов), `tsc`, `eslint`, `vite build`, `playwright test` (15 сценариев, включая новые `media.spec.ts`) — зелёные. Не прогонялись: скриншоты, desktop вживую, замеры памяти, живая проверка.

**Spec:** `docs/specs/2026-09-24-spk-mattermost-design.md`. Проверенные факты API Mattermost 10.11, в том числе новые для этого этапа (аватары, статусы, файлы, реакции, эмодзи): `docs/research/2026-09-24-mattermost-api-facts.md`, **раздел 7**. Правила проекта и ловушки: `AGENTS.md`. **Исполнитель каждой задачи читает все три документа.**

## Объём этапа

Решение пользователя по итогам живой проверки 2026-09-25: сначала аватары, затем превью картинок и текстовых файлов, затем реакции.

Входит:
1. **Аватары и статусы.** Аватар автора в ленте и собеседника у DM в сайдбаре; цветная точка статуса справа снизу (online / away / dnd / offline), как у официального клиента; статусы живые — WS `status_change` (свой) и опрос `POST /users/status/ids` (чужие), с учётом лимита ≤ 10 запросов/с на сервер. GM: официальный клиент показывает не аватары, а число участников (нужна статистика канала) — у нас остаётся значок группы, число участников — в бэклог.
2. **Превью.** Картинки — превью в ленте (одна — крупно, несколько — миниатюрами) и просмотрщик по клику (полный размер, Esc — закрыть, ←/→ — соседние файлы поста). Текстовые файлы (список расширений — Task 8, `TEXT_EXT`) — первые строки моноширинно в ленте, «Развернуть» и просмотр целиком (до 64 КБ). Остальные файлы — карточка. У любого файла — «Скачать» (в «Загрузки») и «Открыть» (скачать и открыть системным приложением).
3. **Реакции.** Клик по чипу ставит/снимает свою; кнопка «Добавить реакцию» в действиях поста и «☺+» после чипов открывают пикер (поиск, недавние, стандартные по категориям, кастомные эмодзи сервера); живые обновления по WS (показ уже есть с этапа 2).

**Не входит** (в `docs/backlog.md` через Task 13): превью видео, аудио и markdown-файлов (решение пользователя — «в todo»); выбор оттенка кожи в пикере; имена отреагировавших во всплывающей подсказке; число участников у GM; иконки вебхуков (`override_icon_url` — внешний URL, не грузим). Остальное из этапа 3 — треды (CRT, панель), поиск, загрузка файлов, автодополнение `@ ~ :`, Ctrl+K — **позже, после отдельного согласования с пользователем**; задач по ним в этом плане нет.

## Архитектура и решения

**1. Как бинарные данные попадают в webview.** UI не обращается к Mattermost и не знает токенов: `<img src>` и `fetch` идут на тот же origin по `/media/<server id>/<kind>/<key>[?…]`, где kind ∈ `avatar | thumb | feed | full | text | emoji`. Обработчик — `internal/media.Cache` (`http.Handler`):
- **desktop**: `desktop.withMedia` ставит его перед файловым сервером UI в `application.AssetOptions.Handler` — запросы webview к `wails://localhost/media/…` обслуживает Go, наружу из webview ничего не уходит (Wails beta.25 строит `http.Request` из полного URI со строкой запроса — `internal/assetserver/assetserver_webview.go`, так что `?v=`/`?src=` доходят);
- **browser mode** (dev/e2e): тот же обработчик в `newBrowserHandler` под `mediaGuard`: `<img>` не умеет ставить заголовок `Authorization`, поэтому `serveIndex` выдаёт cookie `spk_media` (значение — токен запуска, `HttpOnly`, `SameSite=Strict`, `Path=/media/`) — чужая страница, встроившая наш URL, cookie не отправит; DNS-rebinding закрыт существующим `LoopbackHostGuard`.

Сам объект Cache берёт через интерфейс `media.Origin`, который реализует `api.Service`: `Get` — один GET через REST-клиент воркера сервера (`rest.Client.Stream`: тот же токен и **тот же лимитер 10 req/s** — синхронизация и картинки делят бюджет сервера), `EmojiID` — имя кастомного эмодзи → id. Открытого прокси нет: пути к API строятся только из проверенных ключей (id — `^[A-Za-z0-9_-]{1,64}$`, имя эмодзи — `^[a-zA-Z0-9_+-]{1,64}$`, версия аватара — целое, `src` — `preview|file`).

**2. Дисковый кэш.** `<data>/media/` (`paths.MediaDir`, 0700), лимит **256 МиБ** по умолчанию. Имя файла — sha256 от ключа `server/kind/key/variant` (у аватара variant — `last_picture_update`, у `feed`/`full` — `src`, у эмодзи ключ — id, а не имя), индекс в памяти (размер + время последнего использования), при старте — обход каталога (время — mtime), удаление `*.tmp`, вытеснение LRU после каждой записи до лимита. Запись — во временный файл и `rename`. Одинаковые одновременные запросы делят одну загрузку (in-flight), загрузок одновременно не больше 6, у каждой таймаут 60 с (загрузка не отменяется, если ушёл первый запросивший — результат пригодится). Неудачи кэшируются в памяти: «навсегда плохие» (403, 404, 413, 415) — 5 минут, временные (502, 504) — 30 с, записей не больше 1000 — так недоступный сервер или отсутствующая миниатюра не превращаются в шквал запросов при каждой перерисовке. Ответ webview: `Cache-Control: private, max-age=31536000, immutable` (объект по ключу неизменен: версия аватара и id файла в ключе; у эмодзи по имени — `max-age=3600`), `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, `Content-Type` — из сниффинга сохранённых байтов. Не проверено, кэширует ли WebKitGTK ответы custom scheme `wails://` по `Cache-Control`; если нет — повторный показ берётся из дискового кэша Go (чтение файла, без сети).

**3. Безопасность содержимого.** Принимаются только растровые типы по сниффингу (`image/png|jpeg|gif|webp|bmp`), **SVG не отображается никогда** (скрипты/внешние ссылки; сервер для SVG превью и не делает — факты §7.3) — такой файл остаётся карточкой. Защита от «бомб распаковки»: `image.DecodeConfig` — не больше 50 Мпикс. Лимиты размера: аватар и миниатюра 2 МиБ, эмодзи 1 МиБ, картинка 25 МиБ. Текст — не больше 64 КиБ (запрос `Range: bytes=0-65535`, принимаются и 206, и 200), только валидный UTF-8 без NUL, иначе 415 (это бинарный файл с текстовым именем). Markdown сообщений как и раньше не загружает удалённые картинки.

**4. Память.** Горячий слой хранит о файле только метаданные (id, имя, размер, mime, ширина/высота, есть ли превью); `mini_preview` отбрасывается при разборе. В ленте картинка не больше 480×360 CSS px: Go отдаёт `feed` — превью сервера (до 1920 px, в WebKit ~11 МБ декодированных пикселей) **уменьшенное до ≤ 960 px** (×2 для HiDPI, ~2,7 МБ); несколько картинок в посте — миниатюры 120×100. `loading="lazy"` + виртуализированная лента (строки вне экрана размонтированы). Полноразмерная картинка (`full`) грузится только в открытом просмотрщике. **Сдвигов вёрстки нет:** размер рамки картинки известен до загрузки (из `width/height` file_info, иначе фиксированная 240×180 с `object-contain`), текстовый фрагмент в свёрнутом виде — фиксированной высоты (8 строк). Ориентир памяти (Private_Dirty ~150 МБ) — ориентир, не жёсткий предел: удобство важнее; обязательно — отсутствие роста (дисковый кэш ограничен, карты в памяти ограничены).

**5. Скачивание и «Открыть».** Wails beta.25 даёт `Dialog.SaveFile()` (GTK3: модальный `gtk_dialog_run` без родителя, только desktop) и `Browser.OpenFile(path)` (`xdg-open`, не ждёт). Выбрано **без диалога**: Go сохраняет в каталог загрузок (`SPK_MATTERMOST_DOWNLOADS` → `xdg.UserDirs.Download`, который читает `user-dirs.dirs` и находит локализованные «Загрузки» → `~/Downloads`) с уникальным именем `name (1).ext`, UI показывает «Сохранено: <путь>». Причины: так же ведёт себя официальный Desktop по умолчанию; одинаково в desktop и browser mode (e2e); модальный GTK-диалог на главном потоке — лишний риск зависания (урок S3). «Открыть» = сохранить туда же (повторно не качает, если файл уже сохранён и размер совпадает) и `Browser.OpenFile`; исполняемые/запускаемые типы (`.desktop`, `.sh`, `.exe`, `.AppImage`, …) не открываются — только сохраняются, UI сообщает об этом. В browser mode «открытие» записывается и читается e2e через test-API.

**6. Статусы.** `state.Server.presence` (id → статус; в снимок не пишется — после старта статусы приходят первым опросом). Свой статус — WS `status_change` (чужие сервер по WS не шлёт, факты §7.2). Опрос: при переходе в `live`, при открытии канала (с задержкой 300 мс, серия переключений — один опрос) и раз в 60 с; кого опрашиваем — `StatusTargets`: я, собеседники видимых в сайдбаре DM, авторы открытого канала (без ботов), не больше 400 id, по 100 в запросе — несколько запросов в минуту на сервер. У ботов статуса нет; offline и ooo выглядят одинаково (как в `status_icon.tsx`).

**7. Аватары.** В DTO — строка-версия `avatar` = `last_picture_update` (может быть 0 и **отрицательной** — факты §7.1), только если профиль загружен; пусто → инициалы без запроса. `user_updated` меняет версию → новый URL → новая загрузка. Сервер для пользователя без картинки отдаёт сгенерированную, отдельной ветки не нужно.

**8. Реакции.** `Worker.React` применяет реакцию локально сразу и откатывает к состоянию сервера при ошибке; одновременно не больше одного запроса на пару пост+эмодзи — клики во время запроса видны сразу, а на сервер после ответа уходит только последний («поставил—снял—поставил» за время запроса — ни одного лишнего запроса). Эхо своих реакций идемпотентно, а «намерение» (последний клик по паре, 30 с) отбрасывает запоздалое эхо предыдущего клика — нет мигания «снял → снова стоит». Имена: клик по чипу повторяет имя чипа; пикер ставит **первое** short name (как `getEmojiName` веб-клиента: `+1`, `smile`), показ понимает алиасы и оттенки кожи. Ошибки: 403 (нет прав, архивный канал) → `forbidden`, 400 `too_many_reactions` → свой код. После удачной постановки обновляется preference `recent_emojis` (формат веб-клиента, ≤ 27).

**9. Эмодзи.** Набор — тот же, что у Mattermost 10.11: `emoji-datasource@6.1.1` + `additional_shortnames.json` веб-клиента (факты §7.5), генератор `frontend/scripts/gen-emoji.mjs` пишет компактный модуль `src/emoji/data.ts` (9 категорий, 1805 эмодзи, **41 КБ, 16 КБ gzip** — замерено 2026-09-25), он и пикер (~2 КБ gzip) — **ленивые чанки**; основной бандл за весь этап растёт с 142 до ~148 КБ gzip; частые имена показываются сразу из встроенной таблицы. Символы рисует системный шрифт (Noto Color Emoji) — без спрайтов. **Кастомные эмодзи — входят:** на сервере пользователя `EnableCustomEmoji=true`, и реакции с ними уже видны как `:name:`. Стоимость мала: список имён грузится один раз на воркер (≤ 20 страниц по 200) после bootstrap, `emoji_added` дополняет его, картинки идут через `media` (`emoji/<name>`; Go превращает имя в id по списку, иначе `GET /emoji/name/{name}` с кэшем промахов).

## Global Constraints

- Go-модуль `github.com/spk/spk-mattermost`, Go `1.26`; Wails Go `v3.0.0-beta.25` = npm `@wailsio/runtime` `3.0.0-beta.25`.
- `go build ./...` и `go test ./...` без тега `wails` обязаны проходить; desktop-код — за тегом `wails` (Linux: `wails gtk3`).
- UI никогда не ходит к серверу Mattermost напрямую — ни API, ни картинки: только методы `api.API` и `/media/…` того же origin; токены не попадают в DTO, логи, события и URL (`store.Server.LogValue` маскирует).
- Горячий слой: окно **60** последних постов на канал в памяти Go; вложения в памяти не держим (только метаданные файлов); переключение канала читает только память.
- Снимок в SQLite — отложенная запись: не чаще раза в 3 с одной транзакцией и при остановке; статусы присутствия в снимок не пишутся.
- REST: ≤ 10 запросов/с на сервер (token bucket, **общий для синхронизации и медиа**); POST не повторяются при сетевых ошибках; 429 — с `Retry-After`.
- WebSocket: авторизация заголовком `Authorization: Bearer`, **без** `authentication_challenge`; `sequence_number` = следующий ожидаемый seq.
- Никаких блокирующих вызовов системных сервисов на пути старта; у сетевых операций — таймауты; модальных GTK-диалогов не вводим.
- Память: ориентир — Private_Dirty всех процессов ~150 МБ с 2–3 серверами и ~100 каналами (`scripts/pss.sh <pid>`), **ориентир, не жёсткий предел** — удобство важнее, настройки, замедляющие UI, не принимаются; обязательно — без роста за час работы (все кэши ограничены).
- Медиа: дисковый кэш ≤ 256 МиБ; в ленте картинки ≤ 960 px по большей стороне; текстовое превью ≤ 64 КиБ; SVG не отображается; только растровые типы по сниффингу.
- UI: без UI-библиотеки и роутера; все строки — через i18n (`ru` и `en`, паритет ключей проверяется `i18n.test.ts`); доступность: `alt` у картинок, `aria-label` у кнопок-иконок и чипов реакций («👍 3, ваша реакция»), клавиатура в пикере и просмотрщике.
- Тесты Go — с `-race`; линт — `golangci-lint run` для обоих наборов тегов (без тегов и `--build-tags "wails gtk3"`), `gofmt` входит в линт.
- Playwright: каждый созданный контекст закрывается в том же вызове (`try`/`finally` → `ctx.close()`), после работы висящих окон нет.
- Код в плане выровнен вручную: после вставки Go-файлов — `gofmt -w <файлы>` (неотформатированный файл роняет `golangci-lint`).
- Коммиты — `Pavel Simonov <sipahabk@gmail.com>` (локальный git config репозитория), без `Co-Authored-By`, без amend; пуш в `main` — контроллер, после живой проверки (Task 13 Step 6).

## Review Focus

1. **Картинка у пользователя сменилась, пока клиент работает** (`user_updated` с новым `last_picture_update`, в том числе отрицательным после сброса). Ожидается новая картинка без перезапуска и без показа старой из кэша. Тесты: Task 3 `TestAvatarVersionIsPartOfTheKey`; Task 5 `TestAvatarVersionFollowsUserUpdated`.
2. **Картинка догружается в уже прокрученной ленте** (пользователь внизу или читает середину). Ожидается: ни строка, ни соседние посты не прыгают — рамка картинки имеет окончательный размер до загрузки. Тесты: Task 8 `Attachments.test` «the image box has its final size before the image loads», `files.test` `fitBox`.
3. **Быстрый двойной клик по чипу / эхо приходит поздно.** Ожидается: каждый клик виден сразу, на сервер уходит последний (без параллельных запросов), запоздалое эхо прошлого клика не возвращает снятую реакцию, итог совпадает с сервером. Тесты: Task 10 `TestLateEchoOfUndoneReactionIsIgnored`, `TestReactionClicksWhileInFlightAreQueuedLastWins`; e2e «reactions: chips toggle…» (Task 13).
4. **Враждебные и странные файлы:** SVG, картинка с огромными размерами, бинарник с именем `.txt`, имя `../../.bashrc`, файл `.desktop` на «Открыть». Ожидается: не отображаются/не открываются, сохраняются только внутри «Загрузок». Тесты: Task 3 `TestSVGIsRefused`, `TestHugeDimensionsAreRefused`, `TestTextPreview` (NUL → 415); Task 7 `TestSanitizeName`, `TestOpenFileOpensSafeTypesOnly`.
5. **Сервер недоступен или сессия истекла, пока грузятся картинки.** Ожидается: инициалы/карточки вместо картинок, без шквала запросов и без зависаний. Тесты: Task 3 `TestFailuresAreCachedBriefly`; Task 4 `TestMediaOriginStreamsThroughTheWorker` (вышедший сервер → `ErrNoServer` → 404).

## Процедура скриншота (browser mode с фейком)

UI-задачи проверяют результат глазами по этой процедуре; шаг задачи называет, что сделать в сценарии и что должно быть на снимке. Временные файлы — только в `/home/spk/.spk/sawe/ss/Mattermost/.agents/tmp/`.

1. `make build`; выбрать свободный порт: `P=5183; ss -lptn "sport = :$P"` (занят — взять другой, чужой процесс не убивать).
2. Запуск:
   ```bash
   T=$(mktemp -d -p /home/spk/.spk/sawe/ss/Mattermost/.agents/tmp)
   SPK_MATTERMOST_HOME=$T SPK_MATTERMOST_DOWNLOADS=$T/dl build/bin/spk-mattermost --browser --port $P --mm-fake --test-api > $T/log 2>&1 &
   echo $! > $T/pid; echo $T
   ```
3. Playwright MCP `browser_run_code_unsafe` (подставить `P` и `T`; сценарий задачи — на месте комментария):
   ```js
   async (page) => {
     const base = 'http://127.0.0.1:P'
     const ctx = await page.context().browser().newContext({ viewport: { width: 1400, height: 900 }, locale: 'en-US' })
     try {
       const p = await ctx.newPage()
       await p.goto(base + '/')
       const token = await p.locator('meta[name="spk-mattermost-api-token"]').getAttribute('content')
       const h = { Authorization: `Bearer ${token}`, Origin: base }
       const test = (path, data) => p.request.post(`${base}/api/_test/${path}`, { headers: h, data })
       const fake = (await (await p.request.get(`${base}/api/_test/fake-url`, { headers: h })).json()).url
       await p.getByLabel('Server address').fill(fake)
       await p.getByRole('button', { name: 'Add', exact: true }).click()
       await p.getByLabel('Login or email').fill('alice')
       await p.getByLabel('Password').fill('secret')
       await p.getByRole('button', { name: 'Sign in', exact: true }).click()
       await p.getByRole('heading', { name: /Town Square/ }).waitFor()
       // сценарий задачи; снимки: await p.screenshot({ path: 'T/<имя>.png' })
       return 'ok'
     } finally {
       await ctx.close()
     }
   }
   ```
4. Посмотреть каждый PNG инструментом Read. Тема тёмная из коробки — светлых пятен быть не должно.
5. `kill $(cat $T/pid)`; `browser_tabs` — лишних вкладок/контекстов нет.

---

## File Structure

```
internal/
├── mm/model/model.go            # + User.LastPictureUpdate, FileInfo.Width/Height, Emoji
├── mm/rest/media.go             # Stream (GET потоком, лимитер, 429), WithHTTPClient
├── mm/rest/chat.go              # + StatusesByIDs, FileInfo, CustomEmojiPage, EmojiByName
├── mm/rest/write.go             # + SaveReaction, DeleteReaction
├── mm/rest/endpoints.go         # + ClientConfig.EnableCustomEmoji
├── mm/ws/decode.go              # + DecodeEmoji
├── mmfake/media.go              # фейк: аватары (ETag), статусы, файлы (thumbnail/preview/Range), кастомные эмодзи
├── mmfake/reactions.go          # фейк: POST/DELETE реакций, события, управление из тестов
├── mmfake/seed.go               # + картинки пользователей, статусы, посты с файлами, реакции, :partyparrot:
├── mmfake/net.go                # + Hits (счётчик запросов), FailWith (ошибка с id)
├── media/media.go               # дисковый LRU-кэш и обработчик /media/…
├── media/image.go               # проверка типа/размера, уменьшение картинок ленты, текст
├── state/presence.go            # статусы присутствия, StatusTargets, версии аватаров
├── state/emoji.go               # кастомные эмодзи сервера (имя → id)
├── state/reactions.go           # своя реакция локально, намерения, недавние эмодзи
├── state/view.go, sidebar.go    # + avatar/status в PostView и ChannelItem, FileView с размерами
├── mmsync/status.go             # опрос статусов
├── mmsync/emoji.go              # загрузка кастомных эмодзи, EmojiID с кэшем промахов
├── mmsync/react.go              # React (оптимистично, откат, одна пара — один запрос)
├── api/media.go                 # Service как media.Origin
├── api/files.go                 # DownloadFile/OpenFile, каталог загрузок, имена, запрет запускаемых
├── api/reactions.go             # AddReaction/RemoveReaction/EmojiInfo
├── api/api.go                   # + методы, коды ошибок, DTO
├── api/transport/{http,wails}.go
└── desktop/media.go, run.go     # /media/ в asset handler
cmd/spk-mattermost/{browser.go,static.go,run_desktop_wails.go}  # медиа-кэш, cookie, test-API
frontend/
├── scripts/gen-emoji.mjs        # генератор набора эмодзи
└── src/
    ├── media.ts                 # mediaURL
    ├── emoji/data.ts            # сгенерированный набор (ленивый чанк)
    ├── emoji/index.ts           # загрузка набора, emojiChar (алиасы, оттенки), поиск, встроенная таблица
    ├── components/Avatar.tsx    # аватар + точка статуса
    ├── components/files.ts      # вид файла, источник картинки, рамка
    ├── components/Attachments.tsx, FileCard.tsx, TextSnippet.tsx, textFile.ts, Viewer.tsx
    ├── components/Reactions.tsx, EmojiGlyph.tsx, EmojiPicker.tsx
    └── PostItem.tsx, Feed.tsx, ChannelPane.tsx, Sidebar.tsx, App.tsx, store.ts, chat.ts, api/*, i18n.ts, index.css
tests/e2e/{media.spec.ts,chat.spec.ts,playwright.config.ts}
```

---

### Task 1: Модель и REST — поток медиа, статусы, файлы, кастомные эмодзи

**Files:**
- Modify: `internal/mm/model/model.go` (User, FileInfo, новый Emoji)
- Create: `internal/mm/rest/media.go`, `internal/mm/rest/media_test.go`
- Modify: `internal/mm/rest/chat.go`, `internal/mm/rest/chat_test.go`, `internal/mm/rest/endpoints.go`, `internal/mm/rest/endpoints_test.go`
- Modify: `internal/mm/ws/decode.go`, `internal/mm/ws/decode_test.go`

**Interfaces:**
- Consumes: `rest.Client.do`, `classify`, `retryAfter`, `backoff`, `maxAttempts`, `errBodyLimit` (этап 2).
- Produces:
  - `model.User.LastPictureUpdate int64` (`json:"last_picture_update,omitempty"`); `model.FileInfo.Width, Height int` (`width`, `height`, omitempty); `type model.Emoji struct{ ID, Name, CreatorID string; DeleteAt int64 }` (`id`, `name`, `creator_id`, `delete_at`).
  - `func (c *rest.Client) Stream(ctx context.Context, path string, hdr http.Header) (*http.Response, error)` — GET, вызывающий закрывает тело; статус ≥ 400 → `*rest.Error`.
  - `func (c *rest.Client) WithHTTPClient(hc *http.Client) *rest.Client`.
  - `func (c *rest.Client) StatusesByIDs(ctx, ids []string) ([]model.Status, error)` (по 100 id).
  - `func (c *rest.Client) FileInfo(ctx, fileID string) (model.FileInfo, error)`.
  - `func (c *rest.Client) CustomEmojiPage(ctx, page, perPage int) ([]model.Emoji, error)`; `func (c *rest.Client) EmojiByName(ctx, name string) (model.Emoji, error)`.
  - `rest.ClientConfig.EnableCustomEmoji string`.
  - `func ws.DecodeEmoji(e ws.Event) (model.Emoji, error)`.

Почему `Stream` отдельно от `do`: `do` декодирует JSON и повторяет GET при сетевых ошибках, а поток файла нельзя прозрачно повторить с середины; общий у них — лимитер на каждую попытку и ожидание 429 (факты §3, лимиты).

- [ ] **Step 1: Падающие тесты**

`internal/mm/rest/media_test.go`:
```go
package rest

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestStreamReturnsBodyAndHeadersAndSendsAuth(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/users/u1/image", r.URL.Path)
		assert.Equal(t, "5", r.URL.Query().Get("_"))
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		assert.Equal(t, "bytes=0-9", r.Header.Get("Range"))
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGDATA"))
	})
	resp, err := c.Stream(context.Background(), "/api/v4/users/u1/image?_=5", http.Header{"Range": {"bytes=0-9"}})
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "PNGDATA", string(b))
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
}

func TestStreamErrorStatusIsClassified(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"id":"app.file_info.get.app_error","message":"nope"}`))
	})
	_, err := c.Stream(context.Background(), "/api/v4/files/f1", nil)
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, http.StatusNotFound, e.Status)
	assert.Equal(t, KindAPI, e.Kind)
	assert.Equal(t, "app.file_info.get.app_error", e.ID)
}

func TestStreamWaitsOut429AndTakesALimiterTokenPerAttempt(t *testing.T) {
	var n atomic.Int32
	c, slept := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	lim := rate.NewLimiter(rate.Every(time.Hour), 2)
	resp, err := c.WithLimiter(lim).Stream(context.Background(), "/x", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, int32(2), n.Load())
	assert.Equal(t, []time.Duration{2 * time.Second}, *slept)
	assert.InDelta(t, 0, lim.Tokens(), 0.01, "each attempt took a token")
}

func TestWithHTTPClientKeepsTokenAndLimiter(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("x"))
	})
	lim := rate.NewLimiter(rate.Every(time.Hour), 1)
	resp, err := c.WithLimiter(lim).WithHTTPClient(&http.Client{}).Stream(context.Background(), "/y", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.InDelta(t, 0, lim.Tokens(), 0.01)
}
```

В `internal/mm/rest/chat_test.go` дописать:
```go
func TestStatusesByIDsChunksBy100(t *testing.T) {
	var sizes []int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/users/status/ids", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var ids []string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&ids))
		sizes = append(sizes, len(ids))
		out := make([]map[string]any, len(ids))
		for i, id := range ids {
			out[i] = map[string]any{"user_id": id, "status": "away", "manual": false, "last_activity_at": 1, "dnd_end_time": 0}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	ids := make([]string, 250)
	for i := range ids {
		ids[i] = fmt.Sprintf("u%03d", i)
	}
	list, err := c.StatusesByIDs(context.Background(), ids)
	require.NoError(t, err)
	assert.Equal(t, []int{100, 100, 50}, sizes)
	require.Len(t, list, 250)
	assert.Equal(t, "u249", list[249].UserID)
	assert.Equal(t, "away", list[249].Status)
}

func TestFileInfoAndCustomEmojiCalls(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/files/f1/info":
			_, _ = w.Write([]byte(`{"id":"f1","name":"a.png","extension":"png","size":10,"mime_type":"image/png","width":640,"height":480,"has_preview_image":true,"mini_preview":"AAAA"}`))
		case "/api/v4/emoji":
			assert.Equal(t, "2", r.URL.Query().Get("page"))
			assert.Equal(t, "200", r.URL.Query().Get("per_page"))
			assert.Equal(t, "name", r.URL.Query().Get("sort"))
			_, _ = w.Write([]byte(`[{"id":"e1","name":"parrot","creator_id":"u1","create_at":5}]`))
		case "/api/v4/emoji/name/parrot":
			_, _ = w.Write([]byte(`{"id":"e1","name":"parrot"}`))
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	fi, err := c.FileInfo(ctx, "f1")
	require.NoError(t, err)
	assert.Equal(t, 640, fi.Width)
	assert.Equal(t, 480, fi.Height)
	assert.True(t, fi.HasPreviewImage)
	list, err := c.CustomEmojiPage(ctx, 2, 200)
	require.NoError(t, err)
	assert.Equal(t, []model.Emoji{{ID: "e1", Name: "parrot", CreatorID: "u1"}}, list)
	e, err := c.EmojiByName(ctx, "parrot")
	require.NoError(t, err)
	assert.Equal(t, "e1", e.ID)
}
```
(в импорты `chat_test.go` добавить `"github.com/spk/spk-mattermost/internal/mm/model"`, если его там нет.)

В `internal/mm/rest/endpoints_test.go` дописать:
```go
func TestClientConfigCustomEmojiFlag(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"SiteName":"MM","EnableCustomEmoji":"true"}`))
	})
	cfg, err := c.ClientConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "true", cfg.EnableCustomEmoji)
}
```

В `internal/mm/ws/decode_test.go` дописать:
```go
func TestDecodeEmoji(t *testing.T) {
	e, err := DecodeEmoji(ev("emoji_added", `{"emoji":"{\"id\":\"e1\",\"name\":\"parrot\",\"creator_id\":\"u1\"}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, model.Emoji{ID: "e1", Name: "parrot", CreatorID: "u1"}, e)
}
```
(в импорты — `"github.com/spk/spk-mattermost/internal/mm/model"`.)

Run: `go test ./internal/mm/...`
Expected: FAIL — `c.Stream undefined`, `model.Emoji undefined`, `DecodeEmoji undefined`.

- [ ] **Step 2: Реализация — модель**

В `internal/mm/model/model.go`:
- в `User` после `UpdateAt` добавить поле
  ```go
  	// LastPictureUpdate versions the profile picture: it changes on every
  	// upload and reset, and is negative for a generated default picture.
  	LastPictureUpdate int64 `json:"last_picture_update,omitempty"`
  ```
- в `FileInfo` после `MimeType`:
  ```go
  	Width           int    `json:"width,omitempty"`
  	Height          int    `json:"height,omitempty"`
  ```
  (`mini_preview` намеренно не объявлен — при разборе отбрасывается, в памяти не живёт.)
- после `Reaction`:
  ```go
  // Emoji is a server's custom emoji.
  type Emoji struct {
  	ID        string `json:"id"`
  	Name      string `json:"name"`
  	CreatorID string `json:"creator_id,omitempty"`
  	DeleteAt  int64  `json:"delete_at,omitempty"`
  }
  ```

- [ ] **Step 3: Реализация — REST и WS**

`internal/mm/rest/media.go`:
```go
package rest

import (
	"context"
	"io"
	"net/http"
)

// WithHTTPClient returns a copy that sends through hc, keeping the token and
// the limiter: file transfers use a client without a whole-request timeout
// (the caller's context bounds them).
func (c *Client) WithHTTPClient(hc *http.Client) *Client {
	cp := *c
	cp.hc = hc
	return &cp
}

// Stream GETs path (an API path, query included) and hands the open response
// to the caller, who must close its body: avatars, thumbnails, files. Like do
// it takes a limiter token per attempt and waits out 429; unlike do it never
// decodes the body and never retries transport errors — a half-read stream
// cannot be resumed transparently. A status ≥ 400 comes back as *Error.
func (c *Client) Stream(ctx context.Context, path string, hdr http.Header) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		if c.lim != nil {
			if err := c.lim.Wait(ctx); err != nil {
				return nil, &Error{Kind: KindNetwork, Err: err}
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			return nil, err
		}
		for k, vs := range hdr {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, &Error{Kind: KindNetwork, Err: ctx.Err()}
			}
			return nil, &Error{Kind: KindNetwork, Err: err}
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, errBodyLimit))
			_ = resp.Body.Close()
			if serr := c.sleep(ctx, retryAfter(resp.Header, backoff(attempt))); serr != nil {
				return nil, &Error{Kind: KindNetwork, Err: serr}
			}
			continue
		}
		if resp.StatusCode >= 400 {
			err := classify(resp)
			_ = resp.Body.Close()
			return nil, err
		}
		return resp, nil
	}
}
```

В `internal/mm/rest/chat.go`: в блок `const` добавить `statusChunk = 100`, в конец файла:
```go
// StatusesByIDs fetches presence in chunks. The server answers "offline" for
// users it has no status for, and an empty list if statuses are disabled.
func (c *Client) StatusesByIDs(ctx context.Context, ids []string) ([]model.Status, error) {
	var all []model.Status
	for start := 0; start < len(ids); start += statusChunk {
		end := min(start+statusChunk, len(ids))
		var out []model.Status
		if _, err := c.do(ctx, http.MethodPost, "/api/v4/users/status/ids", ids[start:end], &out); err != nil {
			return nil, err
		}
		all = append(all, out...)
	}
	return all, nil
}

func (c *Client) FileInfo(ctx context.Context, fileID string) (model.FileInfo, error) {
	var out model.FileInfo
	return out, c.get(ctx, "/api/v4/files/"+url.PathEscape(fileID)+"/info", &out)
}

// CustomEmojiPage lists custom emoji by name; perPage ≤ 200 (server cap).
func (c *Client) CustomEmojiPage(ctx context.Context, page, perPage int) ([]model.Emoji, error) {
	q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}, "sort": {"name"}}
	var out []model.Emoji
	return out, c.get(ctx, "/api/v4/emoji?"+q.Encode(), &out)
}

// EmojiByName fetches a custom emoji; 404 when there is none with that name.
func (c *Client) EmojiByName(ctx context.Context, name string) (model.Emoji, error) {
	var out model.Emoji
	return out, c.get(ctx, "/api/v4/emoji/name/"+url.PathEscape(name), &out)
}
```

В `internal/mm/rest/endpoints.go` в `ClientConfig` добавить поле
```go
	EnableCustomEmoji       string `json:"EnableCustomEmoji"`
```

В `internal/mm/ws/decode.go` дописать:
```go
// DecodeEmoji decodes emoji_added (data.emoji is a JSON string).
func DecodeEmoji(e Event) (model.Emoji, error) {
	var out model.Emoji
	return out, embedded(e, "emoji", &out)
}
```

- [ ] **Step 4: Тесты проходят**

Run: `go test -race ./internal/mm/...`
Expected: PASS.

- [ ] **Step 5: Линт обоих наборов тегов**

Run: `go vet ./... && golangci-lint run && golangci-lint run --build-tags "wails gtk3"`
Expected: без замечаний.

- [ ] **Step 6: Commit**

```bash
git add internal/mm
git commit -m "rest: media stream with the server limiter, statuses by ids, file info, custom emoji; model picture version and file dimensions"
```

### Task 2: mmfake — аватары, статусы, файлы, кастомные эмодзи; сид Off-Topic

**Files:**
- Modify: `go.mod`, `go.sum` (+ `golang.org/x/image v0.44.0`)
- Create: `internal/mmfake/media.go`, `internal/mmfake/media_test.go`
- Modify: `internal/mmfake/server.go` (Options, `Start`, `clientConfig`, `userJSON`), `internal/mmfake/chat.go` (`chatData`, `createPostLocked`, `usersByIDs`, `me`), `internal/mmfake/seed.go`, `internal/mmfake/net.go` (`Hits`)
- Modify: `internal/mmfake/chat_test.go` (`TestSeedVisibleToAlice` — сид Off-Topic)
- Modify: `cmd/spk-mattermost/browser.go` (test-API `fake/status`, `fake/picture`)

**Interfaces:**
- Consumes: Task 1 (`model.User.LastPictureUpdate`, `model.FileInfo.Width/Height`, `model.Emoji`).
- Produces (фейк, для Tasks 3–13):
  - REST: `GET /api/v4/users/{uid}/image` (PNG, `ETag` = `last_picture_update`, `If-None-Match` → 304); `POST /api/v4/users/status/ids`; `GET /api/v4/files/{fid}`, `/thumbnail`, `/preview`, `/info` (права — член канала файла; `Range` через `http.ServeContent`; нет миниатюры/превью → 400); `GET /api/v4/emoji`, `GET /api/v4/emoji/name/{name}`, `GET /api/v4/emoji/{id}/image` (501 при `Options.DisableCustomEmoji`); `config/client` → `EnableCustomEmoji`.
  - Сид: картинки у всех (у carol — отрицательная версия, «сгенерированная»), статусы alice/bob `online`, carol `away`; в `c-offtopic` после «Welcome to off-topic»: bob «Build screenshot» + `f-build` (`build.png` 960×540, превью), carol «Two diagrams» + `f-diag1` (`flow.png` 600×400) и `f-diag2` (`arch.png` 400×600), bob «Server log attached» + `f-log` (`server.log`, 60 строк `… request #N handled …`), carol «Spec draft» + `f-spec` (`spec.pdf`); кастомный эмодзи `e-parrot` = `partyparrot`.
  - Управление: `SetPicture(username string) int64` (новая версия, `user_updated` всем), `PostFile(channelID, username, message, name, mime string, data []byte) model.Post`, `AddEmoji(name string) model.Emoji` (`emoji_added` всем), `Hits(method, path string) int`; `seedPostLocked` теперь возвращает `*fpost`.
  - test-API: `POST /api/_test/fake/status {username, status}`, `POST /api/_test/fake/picture {username}`.

Картинки фейка — узор из диагональных полос цвета пользователя (на скриншотах видно, что это картинка). Генерация и масштабирование дорогие под `-race` (фейк стартует в десятках тестов), поэтому байты сидовых картинок считаются один раз на процесс (`sync.OnceValue`), а миниатюры/превью кэшируются по содержимому (`derive`). Проверить после шага 6: время `go test -race ./internal/api/ ./cmd/...` выросло не больше чем на несколько секунд относительно этапа 2 (~8 с и ~2 с).

- [ ] **Step 1: Зависимость**

Run: `go get golang.org/x/image@v0.44.0`
Expected: `go.mod` — `golang.org/x/image v0.44.0` в `require`.

- [ ] **Step 2: Падающие тесты**

`internal/mmfake/media_test.go`:
```go
package mmfake

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

func (a authed) raw(method, path string, hdr http.Header) (*http.Response, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.s.URL()+path, nil)
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	req.Header.Set("Authorization", "Bearer "+a.tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestAvatarETagAndPictureChange(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	resp, body := a.raw("GET", "/api/v4/users/u-bob/image", nil)
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.True(t, bytes.HasPrefix(body, []byte("\x89PNG")))
	etag := resp.Header.Get("ETag")
	resp, _ = a.raw("GET", "/api/v4/users/u-bob/image", http.Header{"If-None-Match": {etag}})
	assert.Equal(t, http.StatusNotModified, resp.StatusCode)

	at := s.SetPicture("bob")
	resp, _ = a.raw("GET", "/api/v4/users/u-bob/image", http.Header{"If-None-Match": {etag}})
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, strconv.FormatInt(at, 10), resp.Header.Get("ETag"))

	var users []model.User
	require.Equal(t, 200, a.call("POST", "/api/v4/users/ids", []string{"u-bob", "u-carol"}, &users))
	assert.Equal(t, at, users[0].LastPictureUpdate)
	assert.Less(t, users[1].LastPictureUpdate, int64(0), "carol has a generated picture")
}

func TestStatusesByIDs(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var out []model.Status
	require.Equal(t, 200, a.call("POST", "/api/v4/users/status/ids", []string{"u-bob", "u-carol", "u-nobody"}, &out))
	assert.Equal(t, []model.Status{{UserID: "u-bob", Status: "online"}, {UserID: "u-carol", Status: "away"}, {UserID: "u-nobody", Status: "offline"}}, out)
	s.SetStatus("bob", "dnd")
	require.Equal(t, 200, a.call("POST", "/api/v4/users/status/ids", []string{"u-bob"}, &out))
	assert.Equal(t, "dnd", out[0].Status)
	assert.Equal(t, 400, a.call("POST", "/api/v4/users/status/ids", []string{}, nil))
}

func TestSeededFilesServeDataThumbnailPreviewAndRange(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var info model.FileInfo
	require.Equal(t, 200, a.call("GET", "/api/v4/files/f-build/info", nil, &info))
	assert.Equal(t, model.FileInfo{ID: "f-build", Name: "build.png", Extension: "png", Size: info.Size, MimeType: "image/png", Width: 960, Height: 540, HasPreviewImage: true}, info)

	resp, body := a.raw("GET", "/api/v4/files/f-build/thumbnail", nil)
	require.Equal(t, 200, resp.StatusCode)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)
	assert.LessOrEqual(t, cfg.Width, 120)
	assert.LessOrEqual(t, cfg.Height, 100)

	resp, body = a.raw("GET", "/api/v4/files/f-build/preview", nil)
	require.Equal(t, 200, resp.StatusCode)
	cfg, _, err = image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, 960, cfg.Width)

	resp, body = a.raw("GET", "/api/v4/files/f-log", http.Header{"Range": {"bytes=0-9"}})
	assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Len(t, body, 10)
	assert.Contains(t, resp.Header.Get("Content-Range"), "bytes 0-9/")

	resp, _ = a.raw("GET", "/api/v4/files/f-spec/thumbnail", nil)
	assert.Equal(t, 400, resp.StatusCode, "no thumbnail for a pdf")
	resp, _ = a.raw("GET", "/api/v4/files/f-nope/info", nil)
	assert.Equal(t, 404, resp.StatusCode)

	var posts model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/channels/c-offtopic/posts?page=0&per_page=60", nil, &posts))
	var names []string
	for _, p := range posts.Ascending() {
		if p.Metadata != nil {
			for _, f := range p.Metadata.Files {
				names = append(names, f.Name)
			}
		}
	}
	assert.Equal(t, []string{"build.png", "flow.png", "arch.png", "server.log", "spec.pdf"}, names)
	assert.Equal(t, 1, s.Hits("GET", "/api/v4/files/f-build/info"))
}

func TestFilePermissionIsTheChannel(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	s.mu.Lock()
	s.newFileLocked("f-secret", "c-secret", "s.txt", "text/plain", []byte("psst"))
	s.mu.Unlock()
	bob := loginAs(t, s, "bob")
	resp, _ := bob.raw("GET", "/api/v4/files/f-secret", nil)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestPostFileCarriesMetadata(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	p := s.PostFile("c-offtopic", "bob", "look", "notes.txt", "text/plain", []byte("hello"))
	require.NotNil(t, p.Metadata)
	require.Len(t, p.Metadata.Files, 1)
	assert.Equal(t, "notes.txt", p.Metadata.Files[0].Name)
	assert.Equal(t, p.FileIDs, []string{p.Metadata.Files[0].ID})
}

func TestCustomEmoji(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var cfg map[string]string
	require.Equal(t, 200, a.call("GET", "/api/v4/config/client?format=old", nil, &cfg))
	assert.Equal(t, "true", cfg["EnableCustomEmoji"])
	var list []model.Emoji
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji?page=0&per_page=200&sort=name", nil, &list))
	assert.Equal(t, []model.Emoji{{ID: "e-parrot", Name: "partyparrot", CreatorID: "u-bob"}}, list)
	var e model.Emoji
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji/name/partyparrot", nil, &e))
	assert.Equal(t, "e-parrot", e.ID)
	assert.Equal(t, 404, a.call("GET", "/api/v4/emoji/name/nope", nil, nil))
	resp, body := a.raw("GET", "/api/v4/emoji/e-parrot/image", nil)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, bytes.HasPrefix(body, []byte("\x89PNG")))

	added := s.AddEmoji("shipit")
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji?page=0&per_page=200", nil, &list))
	assert.Len(t, list, 2)
	var got model.Emoji
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji/name/shipit", nil, &got))
	assert.Equal(t, added.ID, got.ID)
	var evs []string
	for _, ev := range s.Events() {
		evs = append(evs, ev.Name)
	}
	assert.Contains(t, evs, "emoji_added")

	off := Start(Options{DisableCustomEmoji: true})
	defer off.Close()
	b := loginAs(t, off, "alice")
	assert.Equal(t, 501, b.call("GET", "/api/v4/emoji?page=0&per_page=200", nil, nil))
	require.Equal(t, 200, b.call("GET", "/api/v4/config/client?format=old", nil, &cfg))
	assert.Equal(t, "false", cfg["EnableCustomEmoji"])
}

func TestPictureChangeIsBroadcast(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	s.SetPicture("carol")
	evs := s.Events()
	last := evs[len(evs)-1]
	assert.Equal(t, "user_updated", last.Name)
	assert.ElementsMatch(t, []string{"u-alice", "u-bob", "u-carol"}, last.To)
}
```

В `internal/mmfake/chat_test.go` в `TestSeedVisibleToAlice` ничего по числу каналов не меняется (новые посты — в существующем `c-offtopic`); дописать в конец теста:
```go
	off := s.Channel("c-offtopic")
	assert.Equal(t, int64(5), off.TotalMsgCount, "welcome + four posts with files")
	assert.Equal(t, off.TotalMsgCount, s.Member("c-offtopic", "alice").MsgCount, "seed is read")
```

Run: `go test ./internal/mmfake/`
Expected: FAIL — `s.SetPicture undefined`, `Options.DisableCustomEmoji undefined` и т.д.

- [ ] **Step 3: Реализация — хранилище фейка и маршруты**

`internal/mmfake/media.go`:
```go
package mmfake

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // image.Decode of uploaded GIFs
	"image/jpeg"
	"image/png"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

// ffile is an uploaded file with the images the real server derives from it.
type ffile struct {
	info      model.FileInfo
	channelID string
	data      []byte
	thumb     []byte // JPEG fitted into 120×100; nil for non-images
	preview   []byte // JPEG ≤1920 wide; nil unless HasPreviewImage
}

type femoji struct {
	e   model.Emoji
	png []byte
}

type picture struct {
	at  int64 // last_picture_update (negative: generated default)
	png []byte
}

var palette = []color.RGBA{
	{79, 140, 255, 255}, {46, 184, 134, 255}, {218, 160, 56, 255}, {163, 108, 230, 255}, {230, 90, 110, 255},
}

// patternPNG draws w×h diagonal stripes of c and a darker shade, so a picture
// in a screenshot reads as an image rather than a flat box.
func patternPNG(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	dark := color.RGBA{c.R / 2, c.G / 2, c.B / 2, 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px := c
			if ((x+y)/24)%2 == 1 {
				px = dark
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = px.R, px.G, px.B, px.A
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// scaledJPEG mirrors the server's thumbnail/preview: the image fitted into
// maxW×maxH (0 = unbounded), JPEG-encoded.
func scaledJPEG(data []byte, maxW, maxH int) []byte {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := 1.0
	if maxW > 0 && w > maxW {
		scale = float64(maxW) / float64(w)
	}
	if maxH > 0 && float64(h)*scale > float64(maxH) {
		scale = float64(maxH) / float64(h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	var out bytes.Buffer
	_ = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 80})
	return out.Bytes()
}

// seedImages are generated once per process: stripes under -race are slow.
var seedImages = sync.OnceValue(func() map[string][]byte {
	return map[string][]byte{
		"build.png": patternPNG(960, 540, palette[0]),
		"flow.png":  patternPNG(600, 400, palette[1]),
		"arch.png":  patternPNG(400, 600, palette[2]),
		"avatar0":   patternPNG(128, 128, palette[0]),
		"avatar1":   patternPNG(128, 128, palette[1]),
		"avatar2":   patternPNG(128, 128, palette[2]),
		"avatar3":   patternPNG(128, 128, palette[3]),
		"avatar4":   patternPNG(128, 128, palette[4]),
		"emoji":     patternPNG(64, 64, palette[4]),
	}
})

func logText(lines int) []byte {
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "2026-09-24 12:%02d:%02d INFO request #%d handled in %dms\n", i/60, i%60, i, 3+i%17)
	}
	return []byte(b.String())
}

// newFileLocked registers an uploaded file of channelID ("" id = generated);
// images get the derived images the server makes: a thumbnail always, a
// preview except for GIF (kept animated) and SVG.
func (s *Server) newFileLocked(id, channelID, name, mime string, data []byte) *ffile {
	if id == "" {
		id = "f-" + newID()[:12]
	}
	ext := ""
	if i := strings.LastIndex(name, "."); i >= 0 {
		ext = strings.ToLower(name[i+1:])
	}
	f := &ffile{channelID: channelID, data: data,
		info: model.FileInfo{ID: id, Name: name, Extension: ext, Size: int64(len(data)), MimeType: mime}}
	if strings.HasPrefix(mime, "image/") && mime != "image/svg+xml" {
		if d, ok := derive(data, mime); ok {
			f.info.Width, f.info.Height, f.thumb, f.preview = d.w, d.h, d.thumb, d.preview
			f.info.HasPreviewImage = d.preview != nil
		}
	}
	s.chat.files[id] = f
	return f
}

type derivedImage struct {
	w, h           int
	thumb, preview []byte
}

// derived caches derivedImage by content: every fake Start seeds the same
// pictures, and decoding + re-encoding them under -race costs seconds.
var derived sync.Map // [32]byte → derivedImage

// derive makes what the server derives from an uploaded image: dimensions,
// a thumbnail and — except for GIF, kept animated — a preview.
func derive(data []byte, mime string) (derivedImage, bool) {
	key := sha256.Sum256(append([]byte(mime+"\x00"), data...))
	if d, ok := derived.Load(key); ok {
		return d.(derivedImage), true
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return derivedImage{}, false
	}
	d := derivedImage{w: cfg.Width, h: cfg.Height, thumb: scaledJPEG(data, 120, 100)}
	if mime != "image/gif" {
		d.preview = scaledJPEG(data, 1920, 0)
	}
	derived.Store(key, d)
	return d, true
}

func (s *Server) fileInfosLocked(ids []string) []model.FileInfo {
	var out []model.FileInfo
	for _, id := range ids {
		if f := s.chat.files[id]; f != nil {
			out = append(out, f.info)
		}
	}
	return out
}

func (s *Server) allUserIDs() []string {
	out := make([]string, len(s.opts.Users))
	for i, u := range s.opts.Users {
		out[i] = u.ID
	}
	return out
}

func (s *Server) mediaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v4/users/{uid}/image", s.handleAuthed(s.userImage))
	mux.HandleFunc("POST /api/v4/users/status/ids", s.handleAuthed(s.statusesByIDs))
	mux.HandleFunc("GET /api/v4/files/{fid}", s.handleAuthed(s.fileHandler("")))
	mux.HandleFunc("GET /api/v4/files/{fid}/thumbnail", s.handleAuthed(s.fileHandler("thumbnail")))
	mux.HandleFunc("GET /api/v4/files/{fid}/preview", s.handleAuthed(s.fileHandler("preview")))
	mux.HandleFunc("GET /api/v4/files/{fid}/info", s.handleAuthed(s.fileInfo))
	mux.HandleFunc("GET /api/v4/emoji", s.handleAuthed(s.emojiList))
	// One pattern for /emoji/name/{name} and /emoji/{id}/image: as two
	// patterns they overlap on /emoji/name/image and ServeMux panics.
	mux.HandleFunc("GET /api/v4/emoji/{a}/{b}", s.handleAuthed(s.emojiPath))
}

func (s *Server) userImage(w http.ResponseWriter, r *http.Request, _ User) {
	s.mu.Lock()
	pic := s.chat.pictures[r.PathValue("uid")]
	s.mu.Unlock()
	if pic == nil {
		appError(w, 404, "app.user.missing_account.const", "no such user")
		return
	}
	etag := strconv.FormatInt(pic.at, 10)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "max-age=86400, private")
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(pic.png)
}

// statusesByIDs mirrors getUserStatusesByIds: unknown users are "offline".
func (s *Server) statusesByIDs(w http.ResponseWriter, r *http.Request, _ User) {
	var ids []string
	if err := json.NewDecoder(r.Body).Decode(&ids); err != nil || len(ids) == 0 {
		appError(w, 400, "api.context.invalid_param.app_error", "user_ids")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Status, 0, len(ids))
	for _, id := range ids {
		st := s.chat.status[id]
		if st == "" {
			st = "offline"
		}
		out = append(out, model.Status{UserID: id, Status: st})
	}
	writeJSON(w, 200, out)
}

func (s *Server) fileFor(w http.ResponseWriter, r *http.Request, u User) *ffile {
	s.mu.Lock()
	f := s.chat.files[r.PathValue("fid")]
	allowed := f != nil && s.isMemberLocked(f.channelID, u.ID)
	s.mu.Unlock()
	switch {
	case f == nil:
		appError(w, 404, "app.file_info.get.app_error", "not found")
		return nil
	case !allowed:
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return nil
	}
	return f
}

// fileHandler serves the file ("") or a derived image; Range works through
// http.ServeContent like on the real server.
func (s *Server) fileHandler(what string) func(http.ResponseWriter, *http.Request, User) {
	return func(w http.ResponseWriter, r *http.Request, u User) {
		f := s.fileFor(w, r, u)
		if f == nil {
			return
		}
		data, ctype := f.data, f.info.MimeType
		switch what {
		case "thumbnail":
			data, ctype = f.thumb, "image/jpeg"
		case "preview":
			data, ctype = f.preview, "image/jpeg"
		}
		if data == nil {
			appError(w, 400, "api.file.get_file_"+what+".no_"+what+".app_error", "no "+what)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeContent(w, r, f.info.Name, time.Time{}, bytes.NewReader(data))
	}
}

func (s *Server) fileInfo(w http.ResponseWriter, r *http.Request, u User) {
	if f := s.fileFor(w, r, u); f != nil {
		writeJSON(w, 200, f.info)
	}
}

func (s *Server) emojiList(w http.ResponseWriter, r *http.Request, _ User) {
	if s.opts.DisableCustomEmoji {
		appError(w, 501, "api.emoji.disabled.app_error", "custom emoji disabled")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	per, _ := strconv.Atoi(q.Get("per_page"))
	if per <= 0 {
		per = 60
	}
	per = min(per, 200)
	s.mu.Lock()
	all := make([]model.Emoji, 0, len(s.chat.emoji))
	for _, e := range s.chat.emoji {
		all = append(all, e.e)
	}
	s.mu.Unlock()
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	start := min(page*per, len(all))
	writeJSON(w, 200, all[start:min(start+per, len(all))])
}

func (s *Server) emojiPath(w http.ResponseWriter, r *http.Request, _ User) {
	if s.opts.DisableCustomEmoji {
		appError(w, 501, "api.emoji.disabled.app_error", "custom emoji disabled")
		return
	}
	a, b := r.PathValue("a"), r.PathValue("b")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case a == "name":
		for _, e := range s.chat.emoji {
			if e.e.Name == b {
				writeJSON(w, 200, e.e)
				return
			}
		}
		appError(w, 404, "app.emoji.get_by_name.no_result", "no such emoji")
	case b == "image" && s.chat.emoji[a] != nil:
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=2592000, private")
		_, _ = w.Write(s.chat.emoji[a].png)
	default:
		appError(w, 404, "app.emoji.get.no_result", "no such emoji")
	}
}

// ---- test controls ----

// SetPicture gives username a new profile picture and tells everyone
// (user_updated), like an avatar upload. Returns the new version.
func (s *Server) SetPicture(username string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.userIDByName(username)
	at := s.nowLocked()
	s.chat.pictureSeq++
	s.chat.pictures[id] = &picture{at: at, png: seedImages()[fmt.Sprintf("avatar%d", (s.chat.pictureSeq+2)%len(palette))]}
	u, _ := s.userByID(id)
	out := userJSON(u)
	out.LastPictureUpdate = at
	s.publishLocked("user_updated", map[string]any{"user": out}, wsBroadcast{}, s.allUserIDs(), nil)
	return at
}

// PostFile posts message with one attached file as username (an upload in
// the real client); the post carries the file's info in metadata.files.
func (s *Server) PostFile(channelID, username, message, name, mime string, data []byte) model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.newFileLocked("", channelID, name, mime, data)
	p, e := s.createPostLocked(s.userIDByName(username), model.Post{ChannelID: channelID, Message: message, FileIDs: []string{f.info.ID}})
	if e != nil {
		panic("mmfake: PostFile: " + e.id)
	}
	return p
}

// AddEmoji creates a custom emoji and announces it (emoji_added to all).
func (s *Server) AddEmoji(name string) model.Emoji {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := model.Emoji{ID: "e-" + newID()[:12], Name: name, CreatorID: "u-bob"}
	s.chat.emoji[e.ID] = &femoji{e: e, png: seedImages()["emoji"]}
	b, _ := json.Marshal(e)
	s.publishLocked("emoji_added", map[string]any{"emoji": string(b)}, wsBroadcast{}, s.allUserIDs(), nil)
	return e
}
```

`internal/mmfake/server.go`:
- в `Options` добавить `DisableCustomEmoji bool // custom emoji endpoints answer 501 and the config says false`;
- в `Start` после `s.chatRoutes(mux)` — `s.mediaRoutes(mux)`;
- в `clientConfig` в карту — `"EnableCustomEmoji": fmt.Sprint(!s.opts.DisableCustomEmoji),`;
- в `me` перед `writeJSON` — картинка:
  ```go
  	s.mu.Lock()
  	if pic := s.chat.pictures[u.ID]; pic != nil {
  		out.LastPictureUpdate = pic.at
  	}
  	s.mu.Unlock()
  ```

`internal/mmfake/chat.go`:
- в `chatData` добавить поля
  ```go
  	files      map[string]*ffile
  	emoji      map[string]*femoji // by id
  	pictures   map[string]*picture
  	pictureSeq int
  ```
- в `usersByIDs` версию картинки:
  ```go
  func (s *Server) usersByIDs(w http.ResponseWriter, r *http.Request, _ User) {
  	var ids []string
  	_ = json.NewDecoder(r.Body).Decode(&ids)
  	out := []model.User{}
  	s.mu.Lock()
  	defer s.mu.Unlock()
  	for _, id := range ids {
  		if u, ok := s.userByID(id); ok {
  			mu := userJSON(u)
  			if pic := s.chat.pictures[id]; pic != nil {
  				mu.LastPictureUpdate = pic.at
  			}
  			out = append(out, mu)
  		}
  	}
  	writeJSON(w, 200, out)
  }
  ```
- в `createPostLocked` сразу после создания `p := &fpost{…}`:
  ```go
  	if len(in.FileIDs) > 0 {
  		p.FileIDs = append([]string(nil), in.FileIDs...)
  		p.Metadata = &model.PostMetadata{Files: s.fileInfosLocked(in.FileIDs)}
  	}
  ```

`internal/mmfake/net.go`: в `Server` (`server.go`) добавить поле `hits map[string]int`; в `conditions` перед `s.mu.Unlock()`:
```go
		if s.hits == nil {
			s.hits = map[string]int{}
		}
		s.hits[r.Method+" "+r.URL.Path]++
```
и в конец `net.go`:
```go
// Hits counts requests by method and path (query excluded) since Start.
func (s *Server) Hits(method, path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[method+" "+path]
}
```

- [ ] **Step 4: Реализация — сид**

`internal/mmfake/seed.go`:
- в `s.chat = chatData{…}` добавить `files: map[string]*ffile{}, emoji: map[string]*femoji{}, pictures: map[string]*picture{},`;
- `seedPostLocked` возвращает созданный пост (последняя строка — `return p`, сигнатура `func (s *Server) seedPostLocked(channelID, userID, msg string) *fpost`);
- добавить
  ```go
  // seedFilePostLocked is seedPostLocked with attached files.
  func (s *Server) seedFilePostLocked(channelID, userID, msg string, files ...*ffile) *fpost {
  	p := s.seedPostLocked(channelID, userID, msg)
  	for _, f := range files {
  		p.FileIDs = append(p.FileIDs, f.info.ID)
  	}
  	p.Metadata = &model.PostMetadata{Files: s.fileInfosLocked(p.FileIDs)}
  	return p
  }
  ```
- в `seed()` заменить строку `s.seedPostLocked("c-offtopic", "u-bob", "Welcome to off-topic")` на
  ```go
  	s.seedPostLocked("c-offtopic", "u-bob", "Welcome to off-topic")
  	img := seedImages()
  	s.seedFilePostLocked("c-offtopic", "u-bob", "Build screenshot",
  		s.newFileLocked("f-build", "c-offtopic", "build.png", "image/png", img["build.png"]))
  	s.seedFilePostLocked("c-offtopic", "u-carol", "Two diagrams",
  		s.newFileLocked("f-diag1", "c-offtopic", "flow.png", "image/png", img["flow.png"]),
  		s.newFileLocked("f-diag2", "c-offtopic", "arch.png", "image/png", img["arch.png"]))
  	s.seedFilePostLocked("c-offtopic", "u-bob", "Server log attached",
  		s.newFileLocked("f-log", "c-offtopic", "server.log", "text/plain", logText(60)))
  	s.seedFilePostLocked("c-offtopic", "u-carol", "Spec draft",
  		s.newFileLocked("f-spec", "c-offtopic", "spec.pdf", "application/pdf", []byte("%PDF-1.4\n% fake pdf for spk-mattermost tests\n")))
  	s.chat.emoji["e-parrot"] = &femoji{e: model.Emoji{ID: "e-parrot", Name: "partyparrot", CreatorID: "u-bob"}, png: img["emoji"]}
  	for i, u := range o.Users {
  		at := base + int64(i)
  		if u.Username == "carol" {
  			at = -at // a generated default picture: the server stores -now
  		}
  		s.chat.pictures[u.ID] = &picture{at: at, png: img[fmt.Sprintf("avatar%d", i%len(palette))]}
  	}
  	s.chat.status["u-alice"], s.chat.status["u-bob"], s.chat.status["u-carol"] = "online", "online", "away"
  ```
  (`base` — уже объявленный выше момент начала сида; `o.Users` — пользователи фейка; статусы ставятся по фиксированным id сида — для своих `Options.Users` они просто не найдутся.)

- [ ] **Step 5: test-API**

В `cmd/spk-mattermost/browser.go` после маршрута `fake/revoke`:
```go
		tm.HandleFunc("POST /api/_test/fake/status", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Username string `json:"username"`
				Status   string `json:"status"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			fake.SetStatus(in.Username, in.Status)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}))
		tm.HandleFunc("POST /api/_test/fake/picture", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Username string `json:"username"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			writeJSON(w, http.StatusOK, map[string]int64{"at": fake.SetPicture(in.Username)})
		}))
```

- [ ] **Step 6: Тесты проходят, остальное не сломано**

Run: `go test -race ./internal/mmfake/ ./internal/mmsync/ ./internal/api/ ./cmd/...`
Expected: PASS (посты с файлами в Off-Topic не ломают тестов синхронизации: они проверяют относительные счётчики).

- [ ] **Step 7: Линт**

Run: `go vet ./... && golangci-lint run && golangci-lint run --build-tags "wails gtk3"`
Expected: без замечаний.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/mmfake cmd/spk-mattermost/browser.go
git commit -m "mmfake: avatars with ETag, statuses by ids, files with thumbnails/previews/Range, custom emoji; seed media in Off-Topic"
```

### Task 3: `internal/media` — дисковый кэш и обработчик `/media/…`

**Files:**
- Create: `internal/media/media.go`, `internal/media/image.go`, `internal/media/media_test.go`

**Interfaces:**
- Consumes: Task 1 (`rest.Client.Stream`, `*rest.Error`) — только в тестах и в `statusFor`.
- Produces:
  - `type media.Origin interface { Get(ctx context.Context, serverID int64, path string, hdr http.Header) (*http.Response, error); EmojiID(ctx context.Context, serverID int64, name string) (string, error) }` — `EmojiID` возвращает `""` без ошибки, если эмодзи нет.
  - `var media.ErrNoServer` — сервер не найден/не вошёл (→ 404).
  - `type media.Options struct { Dir string; MaxBytes int64; Origin Origin; Fetches int; Timeout time.Duration; Now func() time.Time }`; `media.DefaultMaxBytes = 256 << 20`.
  - `func media.New(o Options) (*media.Cache, error)`; `*Cache` — `http.Handler` для путей `/media/{server}/{kind}/{key}`; `func (c *Cache) Size() int64`.
  - Константы `media.FeedMax = 960`, `media.TextLimit = 64 << 10`.
  - URL-контракт для UI: `avatar/{userId}?v={версия}`, `thumb/{fileId}`, `feed/{fileId}?src=preview|file`, `full/{fileId}?src=preview|file`, `text/{fileId}` (заголовок `X-Truncated: 1`, если показано не всё), `emoji/{name}`.

Решения — раздел «Архитектура» п. 1–4. Ответы: 400 — плохой путь/параметр, 404 — нет сервера/объекта (в том числе 400 «нет миниатюры» от сервера), 403 — сервер отказал в правах, 405 — не GET/HEAD, 413 — больше лимита или больше 50 Мпикс, 415 — не растровая картинка / не текст, 502 — сервер недоступен или ответил не 200/206, 503 — клиент ушёл, 504 — таймаут.

- [ ] **Step 1: Падающие тесты**

`internal/media/media_test.go`:
```go
package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/rest"
)

// testOrigin is server 1 backed by an httptest upstream; other ids are
// "not signed in".
type testOrigin struct {
	url   string
	emoji map[string]string
	mu    sync.Mutex
	hits  map[string]int
}

func (o *testOrigin) Get(ctx context.Context, serverID int64, path string, hdr http.Header) (*http.Response, error) {
	if serverID != 1 {
		return nil, ErrNoServer
	}
	return rest.New(o.url, "tok", nil).Stream(ctx, path, hdr)
}

func (o *testOrigin) EmojiID(_ context.Context, _ int64, name string) (string, error) {
	return o.emoji[name], nil
}

func (o *testOrigin) count(path string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hits[path]
}

type clock struct{ ns atomic.Int64 }

// now ticks a second per call: every access is strictly newer (LRU order).
func (c *clock) now() time.Time { return time.Unix(0, c.ns.Add(int64(time.Second))) }

func (c *clock) jump(d time.Duration) { c.ns.Add(int64(d)) }

type env struct {
	t      *testing.T
	cache  *Cache
	origin *testOrigin
	srv    *httptest.Server
	clock  *clock
	dir    string
}

func newEnv(t *testing.T, maxBytes int64, h http.HandlerFunc) *env {
	t.Helper()
	o := &testOrigin{hits: map[string]int{}, emoji: map[string]string{"partyparrot": "e1"}}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.hits[r.URL.Path]++
		o.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(up.Close)
	o.url = up.URL
	e := &env{t: t, origin: o, clock: &clock{}, dir: t.TempDir()}
	e.open(maxBytes, time.Minute)
	return e
}

func (e *env) open(maxBytes int64, timeout time.Duration) {
	c, err := New(Options{Dir: e.dir, MaxBytes: maxBytes, Origin: e.origin, Timeout: timeout, Now: e.clock.now})
	require.NoError(e.t, err)
	if e.srv != nil {
		e.srv.Close()
	}
	e.cache = c
	e.srv = httptest.NewServer(c)
	e.t.Cleanup(e.srv.Close)
}

func (e *env) get(path string) (*http.Response, []byte) {
	e.t.Helper()
	resp, err := http.Get(e.srv.URL + path)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func pngOf(w, h int) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

// fakePNG has a PNG signature and n bytes in total (not decodable).
func fakePNG(n int) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, n-8)...)
}

// hugePNG is a valid PNG header claiming w×h pixels.
func hugePNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // 8-bit grayscale
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	b.Write(make([]byte, 64))
	return b.Bytes()
}

func TestAvatarIsFetchedOnceAndServedWithCacheHeaders(t *testing.T) {
	img := pngOf(4, 4)
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/users/u1/image", r.URL.Path)
		assert.Equal(t, "5", r.URL.Query().Get("_"))
		w.Header().Set("Content-Type", "text/html") // never trusted: we sniff
		_, _ = w.Write(img)
	})
	for i := 0; i < 2; i++ {
		resp, body := e.get("/media/1/avatar/u1?v=5")
		require.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, img, body)
		assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		assert.Equal(t, "default-src 'none'; sandbox", resp.Header.Get("Content-Security-Policy"))
		assert.Equal(t, "private, max-age=31536000, immutable", resp.Header.Get("Cache-Control"))
	}
	assert.Equal(t, 1, e.origin.count("/api/v4/users/u1/image"))
	resp, err := http.Head(e.srv.URL + "/media/1/avatar/u1?v=5")
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(len(img)), e.cache.Size())
}

func TestAvatarVersionIsPartOfTheKey(t *testing.T) {
	var queries []string
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		_, _ = w.Write(pngOf(2, 2))
	})
	e.get("/media/1/avatar/u1?v=5")
	e.get("/media/1/avatar/u1?v=-7")
	e.get("/media/1/avatar/u1?v=0")
	e.get("/media/1/avatar/u1")
	assert.Equal(t, []string{"_=5", "_=-7", ""}, queries, "a new version refetches; 0 and absent are the same key")
}

func TestConcurrentRequestsShareOneFetch(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write(pngOf(2, 2))
	})
	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := e.get("/media/1/thumb/f1")
			codes[i] = resp.StatusCode
		}()
	}
	require.Eventually(t, func() bool { return e.origin.count("/api/v4/files/f1/thumbnail") == 1 }, 5*time.Second, 5*time.Millisecond)
	close(release)
	wg.Wait()
	assert.Equal(t, 1, e.origin.count("/api/v4/files/f1/thumbnail"))
	for _, c := range codes {
		assert.Equal(t, 200, c)
	}
}

func TestSVGIsRefused(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	})
	resp, _ := e.get("/media/1/full/f1?src=file")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
	resp, _ = e.get("/media/1/full/f1?src=file")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
	assert.Equal(t, 1, e.origin.count("/api/v4/files/f1"), "the refusal is remembered")
}

func TestTooLargeIsRefused(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fakePNG(3 << 20)) })
	resp, _ := e.get("/media/1/thumb/f1")
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.Zero(t, e.cache.Size())
}

func TestHugeDimensionsAreRefused(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(hugePNG(20000, 20000)) })
	for _, p := range []string{"/media/1/feed/f1", "/media/1/full/f1"} {
		resp, _ := e.get(p)
		assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, p)
	}
}

func TestFeedScalesLargeImagesDownAndKeepsSmallOnes(t *testing.T) {
	big, small := pngOf(2000, 500), pngOf(300, 200)
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/files/big/preview":
			_, _ = w.Write(big)
		case "/api/v4/files/small":
			_, _ = w.Write(small)
		default:
			http.NotFound(w, r)
		}
	})
	resp, body := e.get("/media/1/feed/big")
	require.Equal(t, 200, resp.StatusCode)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, [2]int{FeedMax, 240}, [2]int{cfg.Width, cfg.Height})

	resp, body = e.get("/media/1/feed/small?src=file")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, small, body, "an image that fits is passed through")

	resp, body = e.get("/media/1/full/big")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, big, body, "the viewer gets the preview as is")
}

func TestTextPreview(t *testing.T) {
	long := strings.Repeat("a", TextLimit-1) + "й" + "tail"
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, fmt.Sprintf("bytes=0-%d", TextLimit-1), r.Header.Get("Range"))
		body := map[string]string{"/api/v4/files/long": long, "/api/v4/files/short": "hello\n", "/api/v4/files/bin": "ab\x00cd"}[r.URL.Path]
		http.ServeContent(w, r, "x.txt", time.Time{}, strings.NewReader(body))
	})
	resp, body := e.get("/media/1/text/long")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "1", resp.Header.Get("X-Truncated"))
	assert.Equal(t, strings.Repeat("a", TextLimit-1), string(body), "a character cut in half is dropped")

	resp, body = e.get("/media/1/text/short")
	require.Equal(t, 200, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("X-Truncated"))
	assert.Equal(t, "hello\n", string(body))

	resp, _ = e.get("/media/1/text/bin")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
}

func TestEmojiResolvesNameToID(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/emoji/e1/image", r.URL.Path)
		_, _ = w.Write(pngOf(8, 8))
	})
	resp, _ := e.get("/media/1/emoji/partyparrot")
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "private, max-age=3600", resp.Header.Get("Cache-Control"))
	resp, _ = e.get("/media/1/emoji/nope")
	assert.Equal(t, 404, resp.StatusCode)
	assert.Equal(t, 1, e.origin.count("/api/v4/emoji/e1/image"))
}

func TestEvictionKeepsTheCacheUnderItsCapLeastRecentlyUsedFirst(t *testing.T) {
	e := newEnv(t, 3000, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fakePNG(1000)) })
	for _, u := range []string{"a1", "a2", "a3"} {
		e.get("/media/1/avatar/" + u)
	}
	e.get("/media/1/avatar/a1") // a1 is used again: a2 is now the oldest
	e.get("/media/1/avatar/a4")
	assert.Equal(t, int64(3000), e.cache.Size())
	e.get("/media/1/avatar/a1")
	assert.Equal(t, 1, e.origin.count("/api/v4/users/a1/image"), "a1 stayed")
	e.get("/media/1/avatar/a2")
	assert.Equal(t, 2, e.origin.count("/api/v4/users/a2/image"), "a2 was evicted")
	files, _ := filepath.Glob(filepath.Join(e.dir, "*.bin"))
	assert.Len(t, files, 3)
}

func TestIndexSurvivesRestartAndLeftoversAreRemoved(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngOf(2, 2)) })
	e.get("/media/1/avatar/u1?v=1")
	require.NoError(t, os.WriteFile(filepath.Join(e.dir, "x.tmp"), []byte("half"), 0o600))
	e.open(0, time.Minute)
	resp, _ := e.get("/media/1/avatar/u1?v=1")
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 1, e.origin.count("/api/v4/users/u1/image"))
	_, err := os.Stat(filepath.Join(e.dir, "x.tmp"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Positive(t, e.cache.Size())
}

func TestFailuresAreCachedBriefly(t *testing.T) {
	fail := atomic.Int32{}
	fail.Store(http.StatusNotFound)
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) {
		if code := int(fail.Load()); code != 0 {
			w.WriteHeader(code)
			return
		}
		_, _ = w.Write(pngOf(2, 2))
	})
	resp, _ := e.get("/media/1/thumb/f1")
	assert.Equal(t, 404, resp.StatusCode)
	e.get("/media/1/thumb/f1")
	assert.Equal(t, 1, e.origin.count("/api/v4/files/f1/thumbnail"), "404 is remembered")
	e.clock.jump(6 * time.Minute)
	fail.Store(http.StatusInternalServerError)
	resp, _ = e.get("/media/1/thumb/f1")
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	e.get("/media/1/thumb/f1")
	assert.Equal(t, 2, e.origin.count("/api/v4/files/f1/thumbnail"), "a 5xx is remembered too")
	e.clock.jump(31 * time.Second)
	fail.Store(0)
	resp, _ = e.get("/media/1/thumb/f1")
	assert.Equal(t, 200, resp.StatusCode, "…but only for 30 s")
}

func TestUpstreamRefusalAndTimeout(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/files/slow/thumbnail" {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	e.open(0, 100*time.Millisecond)
	resp, _ := e.get("/media/1/thumb/f1")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp, _ = e.get("/media/1/thumb/slow")
	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
}

func TestBadRequests(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngOf(2, 2)) })
	for _, p := range []string{
		"/media/x/avatar/u1", "/media/0/avatar/u1", "/media/1/bogus/u1", "/media/1/avatar/..%2Fx",
		"/media/1/avatar/u1?v=abc", "/media/1/feed/f1?src=evil", "/media/1/avatar", "/media/1/avatar/u1/extra",
		"/media/1/emoji/bad%20name",
	} {
		resp, _ := e.get(p)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, p)
	}
	resp, _ := e.get("/media/2/avatar/u1")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a server that is not signed in")
	resp, err := http.Post(e.srv.URL+"/media/1/avatar/u1", "text/plain", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Zero(t, e.origin.count("/api/v4/users/u1/image"))
}
```

Run: `go test ./internal/media/`
Expected: FAIL — пакета нет (`undefined: New`, `ErrNoServer`, …).

- [ ] **Step 2: Реализация — кэш и обработчик**

`internal/media/media.go`:
```go
// Package media serves pictures and file snippets of Mattermost servers to
// the UI from a bounded on-disk cache. The UI never talks to a Mattermost
// server itself: it requests /media/<server id>/<kind>/<key> on its own
// origin, and the cache fetches the object through the server's REST client
// (Origin — the token and the per-server rate limiter stay in Go), checks
// what came back, keeps it on disk and serves it with long-lived headers.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/rest"
)

// Kind is the second segment of a /media/ URL: what is served.
type Kind string

// Kinds of media objects.
const (
	KindAvatar Kind = "avatar" // user picture; key user id, ?v=last_picture_update
	KindThumb  Kind = "thumb"  // file thumbnail (server: JPEG ≤120×100)
	KindFeed   Kind = "feed"   // image in the feed: preview or original, scaled to ≤ FeedMax
	KindFull   Kind = "full"   // image in the viewer: preview or original as is
	KindText   Kind = "text"   // first TextLimit bytes of a text file
	KindEmoji  Kind = "emoji"  // custom emoji picture; key emoji name
)

// Limits of the cache and of what it accepts.
const (
	DefaultMaxBytes = 256 << 20
	FeedMax         = 960     // px: feed boxes are ≤480×360 CSS px, ×2 for HiDPI
	TextLimit       = 64 << 10 // bytes of a text file shown
	maxPixels       = 50_000_000
	negTTL          = 5 * time.Minute  // 403/404/413/415: will not change soon
	negTTLTransient = 30 * time.Second // network trouble
	maxNeg          = 1000
)

var (
	// ErrNoServer means the server id is unknown or not signed in (404).
	ErrNoServer = errors.New("media: server not signed in")
	errTooLarge = errors.New("media: object too large")
	errType     = errors.New("media: content type not allowed")
)

// Origin fetches objects from signed-in servers (implemented by api.Service).
type Origin interface {
	// Get GETs an API path through the server's REST client; the caller
	// closes the body.
	Get(ctx context.Context, serverID int64, path string, hdr http.Header) (*http.Response, error)
	// EmojiID resolves a custom emoji name; "" and no error: no such emoji.
	EmojiID(ctx context.Context, serverID int64, name string) (string, error)
}

type Options struct {
	Dir      string
	MaxBytes int64         // total size cap; 0 → DefaultMaxBytes
	Origin   Origin
	Fetches  int           // concurrent upstream fetches; 0 → 6
	Timeout  time.Duration // per upstream fetch; 0 → 60 s
	Now      func() time.Time
}

type entry struct {
	size int64
	used time.Time
}

type negEntry struct {
	status int
	until  time.Time
}

type call struct {
	done   chan struct{}
	status int
}

type Cache struct {
	o        Options
	sem      chan struct{}
	mu       sync.Mutex
	index    map[string]*entry // file name → entry
	total    int64
	neg      map[string]negEntry // cache key → recent failure
	inflight map[string]*call    // cache key → fetch in progress
}

// New opens (creating) the cache directory: leftovers of interrupted writes
// are removed, existing objects are indexed (last use = mtime) and trimmed
// to the cap.
func New(o Options) (*Cache, error) {
	if o.Dir == "" || o.Origin == nil {
		return nil, errors.New("media: Dir and Origin are required")
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.Fetches <= 0 {
		o.Fetches = 6
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return nil, err
	}
	des, err := os.ReadDir(o.Dir)
	if err != nil {
		return nil, err
	}
	c := &Cache{o: o, sem: make(chan struct{}, o.Fetches), index: map[string]*entry{},
		neg: map[string]negEntry{}, inflight: map[string]*call{}}
	for _, de := range des {
		name := de.Name()
		switch {
		case strings.HasSuffix(name, ".tmp"):
			_ = os.Remove(filepath.Join(o.Dir, name))
		case strings.HasSuffix(name, ".bin"):
			if fi, err := de.Info(); err == nil {
				c.index[name] = &entry{size: fi.Size(), used: fi.ModTime()}
				c.total += fi.Size()
			}
		}
	}
	c.mu.Lock()
	c.evictLocked("")
	c.mu.Unlock()
	return c, nil
}

// Size is the total size of cached objects.
func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

type request struct {
	server  int64
	kind    Kind
	key     string // user id, file id or emoji name
	variant string // avatar: picture version; feed/full: "preview" | "file"
}

var (
	idRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	emojiRe = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,64}$`)
)

func parse(u *url.URL) (request, bool) {
	tail, ok := strings.CutPrefix(u.Path, "/media/")
	parts := strings.Split(tail, "/")
	if !ok || len(parts) != 3 {
		return request{}, false
	}
	srv, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || srv <= 0 {
		return request{}, false
	}
	q := request{server: srv, kind: Kind(parts[1]), key: parts[2]}
	switch q.kind {
	case KindAvatar:
		q.variant = u.Query().Get("v")
		if q.variant == "" {
			q.variant = "0"
		}
		if _, err := strconv.ParseInt(q.variant, 10, 64); err != nil {
			return request{}, false
		}
	case KindFeed, KindFull:
		q.variant = u.Query().Get("src")
		if q.variant == "" {
			q.variant = "preview"
		}
		if q.variant != "preview" && q.variant != "file" {
			return request{}, false
		}
	case KindThumb, KindText:
	case KindEmoji:
		return q, emojiRe.MatchString(q.key)
	default:
		return request{}, false
	}
	return q, idRe.MatchString(q.key)
}

// spec is what to fetch for a request and how much of it to accept.
type spec struct {
	path  string
	hdr   http.Header
	max   int64
	text  bool
	scale bool
}

func (q request) spec(id string) spec {
	esc := url.PathEscape(id)
	switch q.kind {
	case KindAvatar:
		p := "/api/v4/users/" + esc + "/image"
		if q.variant != "0" {
			p += "?_=" + url.QueryEscape(q.variant)
		}
		return spec{path: p, max: 2 << 20}
	case KindThumb:
		return spec{path: "/api/v4/files/" + esc + "/thumbnail", max: 2 << 20}
	case KindFeed, KindFull:
		p := "/api/v4/files/" + esc
		if q.variant == "preview" {
			p += "/preview"
		}
		return spec{path: p, max: 25 << 20, scale: q.kind == KindFeed}
	case KindText:
		return spec{path: "/api/v4/files/" + esc, max: TextLimit, text: true,
			hdr: http.Header{"Range": {fmt.Sprintf("bytes=0-%d", TextLimit-1)}}}
	default: // KindEmoji: id is the resolved emoji id
		return spec{path: "/api/v4/emoji/" + esc + "/image", max: 1 << 20}
	}
}

func fileName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".bin"
}

func (c *Cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q, ok := parse(r.URL)
	if !ok {
		http.Error(w, "bad media path", http.StatusBadRequest)
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		name, status := c.get(r.Context(), q)
		if status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
		f, err := os.Open(filepath.Join(c.o.Dir, name))
		if errors.Is(err, fs.ErrNotExist) { // evicted between get and open
			c.forget(name)
			continue
		}
		if err != nil {
			http.Error(w, "cache read failed", http.StatusInternalServerError)
			return
		}
		c.serve(w, r, q, f)
		_ = f.Close()
		return
	}
	http.Error(w, "cache busy", http.StatusServiceUnavailable)
}

func (c *Cache) serve(w http.ResponseWriter, r *http.Request, q request, f *os.File) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if q.kind == KindEmoji {
		h.Set("Cache-Control", "private, max-age=3600") // by name: may be re-created
	} else {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "cache read failed", http.StatusInternalServerError)
		return
	}
	var body io.ReadSeeker = f
	if q.kind == KindText {
		var flag [1]byte
		if _, err := io.ReadFull(f, flag[:]); err != nil {
			http.Error(w, "cache read failed", http.StatusInternalServerError)
			return
		}
		if flag[0] == 'T' {
			h.Set("X-Truncated", "1")
		}
		h.Set("Content-Type", "text/plain; charset=utf-8")
		body = io.NewSectionReader(f, 1, fi.Size()-1)
	} else {
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		h.Set("Content-Type", http.DetectContentType(head[:n]))
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "cache read failed", http.StatusInternalServerError)
			return
		}
	}
	http.ServeContent(w, r, "", time.Time{}, body)
}

// get makes sure the object is on disk and returns its file name, or an
// HTTP status for why it is not.
func (c *Cache) get(ctx context.Context, q request) (string, int) {
	id := q.key
	if q.kind == KindEmoji {
		var status int
		if id, status = c.emojiID(ctx, q); status != 0 {
			return "", status
		}
	}
	key := fmt.Sprintf("%d/%s/%s/%s", q.server, q.kind, id, q.variant)
	name := fileName(key)
	now := c.o.Now()
	c.mu.Lock()
	if e := c.index[name]; e != nil {
		e.used = now
		c.mu.Unlock()
		return name, 0
	}
	if n, ok := c.neg[key]; ok && now.Before(n.until) {
		c.mu.Unlock()
		return "", n.status
	}
	cl := c.inflight[key]
	if cl == nil {
		cl = &call{done: make(chan struct{})}
		c.inflight[key] = cl
		go c.fill(key, name, q, id, cl)
	}
	c.mu.Unlock()
	select {
	case <-cl.done:
		if cl.status != 0 {
			return "", cl.status
		}
		return name, 0
	case <-ctx.Done():
		return "", http.StatusServiceUnavailable
	}
}

func (c *Cache) emojiID(ctx context.Context, q request) (string, int) {
	ctx, cancel := context.WithTimeout(ctx, c.o.Timeout)
	defer cancel()
	id, err := c.o.Origin.EmojiID(ctx, q.server, q.key)
	switch {
	case err != nil:
		return "", statusFor(err)
	case id == "" || !idRe.MatchString(id):
		return "", http.StatusNotFound
	}
	return id, 0
}

// fill runs one upstream fetch for everyone waiting on cl. It does not use
// a requester's context: the first viewer scrolling away must not waste a
// fetch the next one needs.
func (c *Cache) fill(key, name string, q request, id string, cl *call) {
	c.sem <- struct{}{}
	status := c.fetch(q, id, name)
	<-c.sem
	c.mu.Lock()
	delete(c.inflight, key)
	if status != 0 {
		ttl := negTTLTransient
		switch status {
		case http.StatusForbidden, http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
			ttl = negTTL
		}
		if len(c.neg) >= maxNeg {
			c.neg = map[string]negEntry{}
		}
		c.neg[key] = negEntry{status: status, until: c.o.Now().Add(ttl)}
	}
	cl.status = status
	c.mu.Unlock()
	close(cl.done)
}

func (c *Cache) fetch(q request, id, name string) int {
	ctx, cancel := context.WithTimeout(context.Background(), c.o.Timeout)
	defer cancel()
	sp := q.spec(id)
	resp, err := c.o.Origin.Get(ctx, q.server, sp.path, sp.hdr)
	if err != nil {
		return statusFor(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return http.StatusBadGateway
	}
	tmp, err := os.CreateTemp(c.o.Dir, "*.tmp")
	if err != nil {
		slog.Warn("media cache write failed", "err", err)
		return http.StatusInternalServerError
	}
	var size int64
	if sp.text {
		size, err = writeText(tmp, resp.Body, resp.Header.Get("Content-Range"))
	} else {
		size, err = writeImage(tmp, resp.Body, sp)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(c.o.Dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return statusFor(err)
	}
	c.mu.Lock()
	if old := c.index[name]; old != nil {
		c.total -= old.size
	}
	c.index[name] = &entry{size: size, used: c.o.Now()}
	c.total += size
	c.evictLocked(name)
	c.mu.Unlock()
	return 0
}

// evictLocked drops least recently used objects until the cache fits its
// cap; keep (the object just written) is never dropped.
func (c *Cache) evictLocked(keep string) {
	for c.total > c.o.MaxBytes {
		victim := ""
		var oldest time.Time
		for name, e := range c.index {
			if name != keep && (victim == "" || e.used.Before(oldest)) {
				victim, oldest = name, e.used
			}
		}
		if victim == "" {
			return
		}
		if err := os.Remove(filepath.Join(c.o.Dir, victim)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("media eviction failed", "err", err) // e.g. open on Windows: retried on the next write
			return
		}
		c.total -= c.index[victim].size
		delete(c.index, victim)
	}
}

func (c *Cache) forget(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.index[name]; e != nil {
		c.total -= e.size
		delete(c.index, name)
	}
}

func statusFor(err error) int {
	var re *rest.Error
	switch {
	case errors.Is(err, ErrNoServer):
		return http.StatusNotFound
	case errors.Is(err, errTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, errType):
		return http.StatusUnsupportedMediaType
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.As(err, &re) && (re.Status == http.StatusUnauthorized || re.Status == http.StatusForbidden):
		return http.StatusForbidden
	case errors.As(err, &re) && (re.Status == http.StatusBadRequest || re.Status == http.StatusNotFound || re.Status == http.StatusNotImplemented):
		return http.StatusNotFound // no thumbnail/preview, deleted file, custom emoji off
	}
	return http.StatusBadGateway
}
```

- [ ] **Step 3: Реализация — проверка и уменьшение**

`internal/media/image.go`:
```go
package media

import (
	"bufio"
	"bytes"
	"errors"
	"image"
	_ "image/gif" // DecodeConfig of GIF headers
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/draw"
)

// rasterTypes are what http.DetectContentType may say for an accepted
// image. SVG (text/xml) is never among them: it can carry scripts.
var rasterTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true}

const headPeek = 64 << 10

// writeImage copies an image to dst after checking it: a raster type by
// sniffing, at most sp.max bytes, at most maxPixels when the header is
// readable. Feed PNG/JPEG larger than FeedMax are scaled down.
func writeImage(dst io.Writer, src io.Reader, sp spec) (int64, error) {
	br := bufio.NewReaderSize(src, headPeek)
	head, err := br.Peek(headPeek)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	ctype := http.DetectContentType(head)
	if len(head) == 0 || !rasterTypes[ctype] {
		return 0, errType
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(head)); err == nil && int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return 0, errTooLarge
	}
	if sp.scale && (ctype == "image/png" || ctype == "image/jpeg") {
		data, err := io.ReadAll(io.LimitReader(br, sp.max+1))
		if err != nil {
			return 0, err
		}
		if int64(len(data)) > sp.max {
			return 0, errTooLarge
		}
		scaled, err := downscale(data, ctype)
		if err != nil {
			return 0, err
		}
		if scaled != nil {
			data = scaled
		}
		n, err := dst.Write(data)
		return int64(n), err
	}
	n, err := io.Copy(dst, io.LimitReader(br, sp.max+1))
	if err != nil {
		return n, err
	}
	if n > sp.max {
		return 0, errTooLarge
	}
	return n, nil
}

// downscale fits a PNG/JPEG into FeedMax×FeedMax, keeping its format (PNG
// keeps transparency); nil when it already fits.
func downscale(data []byte, ctype string) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errType
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, errTooLarge
	}
	if cfg.Width <= FeedMax && cfg.Height <= FeedMax {
		return nil, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errType
	}
	scale := min(float64(FeedMax)/float64(cfg.Width), float64(FeedMax)/float64(cfg.Height))
	dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(cfg.Width)*scale)), max(1, int(float64(cfg.Height)*scale))))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	if ctype == "image/png" {
		err = png.Encode(&out, dst)
	} else {
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85})
	}
	return out.Bytes(), err
}

// writeText stores up to TextLimit bytes of a text file behind a one-byte
// flag ('T' — there is more, 'F' — complete). Content with NUL bytes or
// invalid UTF-8 is refused: a binary file with a text name, or a legacy
// encoding we would show as garbage.
func writeText(dst io.Writer, src io.Reader, contentRange string) (int64, error) {
	buf, err := io.ReadAll(io.LimitReader(src, TextLimit+1))
	if err != nil {
		return 0, err
	}
	truncated := len(buf) > TextLimit || rangeTotal(contentRange) > TextLimit
	if len(buf) > TextLimit {
		buf = buf[:TextLimit]
	}
	if truncated {
		buf = trimPartialRune(buf)
	}
	if bytes.IndexByte(buf, 0) >= 0 || !utf8.Valid(buf) {
		return 0, errType
	}
	flag := byte('F')
	if truncated {
		flag = 'T'
	}
	if _, err := dst.Write([]byte{flag}); err != nil {
		return 0, err
	}
	n, err := dst.Write(buf)
	return int64(n) + 1, err
}

// trimPartialRune drops a multi-byte character cut in half at the end.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return b
}

// rangeTotal reads the full size from "bytes 0-65535/200000"; -1 if unknown.
func rangeTotal(h string) int64 {
	_, total, ok := strings.Cut(h, "/")
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(total, 10, 64)
	if err != nil {
		return -1
	}
	return n
}
```

- [ ] **Step 4: Тесты проходят**

Run: `go test -race -count=3 ./internal/media/`
Expected: PASS (три прогона — ловим гонки in-flight и LRU).

- [ ] **Step 5: Линт**

Run: `go vet ./... && golangci-lint run && golangci-lint run --build-tags "wails gtk3"`
Expected: без замечаний.

- [ ] **Step 6: Commit**

```bash
git add internal/media
git commit -m "media: bounded on-disk LRU cache for avatars, thumbnails, feed images, text snippets and custom emoji, served on /media/"
```

### Task 4: Подключение медиа — Service как Origin, кастомные эмодзи в Go, browser mode и desktop

**Files:**
- Create: `internal/state/emoji.go`, `internal/state/emoji_test.go`
- Modify: `internal/state/server.go` (`Config.CustomEmoji`, поле `emoji`, `New`), `internal/state/events.go` (`emoji_added`)
- Create: `internal/mmsync/emoji.go`, `internal/mmsync/emoji_test.go`
- Modify: `internal/mmsync/worker.go` (поля `emojiLoad`, `missMu`, `emojiMiss`; `NewWorker`; `bootstrap`, `finishRefresh`, `fetchMeta`)
- Create: `internal/api/media.go`, `internal/api/media_test.go`
- Modify: `internal/api/service.go` (поле `transfer`)
- Create: `internal/desktop/media.go`, `internal/desktop/media_test.go`
- Modify: `internal/desktop/run.go` (`Options.Media`, `AssetOptions.Handler`)
- Modify: `cmd/spk-mattermost/static.go` (cookie), `cmd/spk-mattermost/browser.go` (кэш, `mediaGuard`, сигнатура `newBrowserHandler`), `cmd/spk-mattermost/run_desktop_wails.go`, `cmd/spk-mattermost/browser_test.go`

**Interfaces:**
- Consumes: Task 1 (`Stream`, `WithHTTPClient`, `CustomEmojiPage`, `EmojiByName`, `ClientConfig.EnableCustomEmoji`, `ws.DecodeEmoji`), Task 2 (фейк: эмодзи, `Hits`, аватары), Task 3 (`media.Origin`, `media.ErrNoServer`, `media.New`).
- Produces:
  - `state.Config.CustomEmoji bool` (`json:"custom_emoji,omitempty"`); `(*state.Server).SetCustomEmoji([]model.Emoji)`, `AddCustomEmoji(model.Emoji)`, `CustomEmojiID(name) (string, bool)`, `CustomEmojiNames() []string` (по алфавиту), `CustomEmojiEnabled() bool`.
  - `(*mmsync.Worker).REST() *rest.Client`; `(*mmsync.Worker).EmojiID(ctx, name) (string, error)`.
  - `(*api.Service).Get(ctx, serverID int64, path string, hdr http.Header) (*http.Response, error)`, `(*api.Service).EmojiID(ctx, serverID int64, name string) (string, error)` — `api.Service` реализует `media.Origin`; сервер без работающего воркера или в `needs_reauth` → `media.ErrNoServer`.
  - `desktop.Options.Media http.Handler`; `newBrowserHandler(svc, em, dist, fake, testAPI, notes, mediaH http.Handler)`; cookie `spk_media`.

Кастомные эмодзи в Go появляются здесь, а не в задаче реакций: они нужны `media` (вид `emoji`), чтобы превратить имя в id.

- [ ] **Step 1: Падающие тесты — state и воркер**

`internal/state/emoji_test.go`:
```go
package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/ws"
)

func TestCustomEmojiNamesAndEvents(t *testing.T) {
	s := newFixture()
	assert.False(t, s.CustomEmojiEnabled())
	b := fixture()
	b.Config.CustomEmoji = true
	s.Bootstrap(b)
	assert.True(t, s.CustomEmojiEnabled())

	s.SetCustomEmoji([]model.Emoji{{ID: "e1", Name: "parrot"}, {ID: "e2", Name: "gone", DeleteAt: 5}})
	id, ok := s.CustomEmojiID("parrot")
	assert.True(t, ok)
	assert.Equal(t, "e1", id)
	_, ok = s.CustomEmojiID("gone")
	assert.False(t, ok, "deleted emoji are not offered")

	d, _ := json.Marshal(map[string]any{"emoji": `{"id":"e3","name":"shipit"}`})
	s.ApplyEvent(ws.Event{Type: "emoji_added", Data: d})
	assert.Equal(t, []string{"parrot", "shipit"}, s.CustomEmojiNames())
}
```

`internal/mmsync/emoji_test.go`:
```go
package mmsync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mmfake"
)

func TestCustomEmojiLoadAndEmojiID(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(func() bool { _, ok := h.w.State().CustomEmojiID("partyparrot"); return ok }, "custom emoji list not loaded")
	ctx := context.Background()
	id, err := h.w.EmojiID(ctx, "partyparrot")
	require.NoError(t, err)
	assert.Equal(t, "e-parrot", id)

	id, err = h.w.EmojiID(ctx, "nope")
	require.NoError(t, err)
	assert.Empty(t, id)
	_, _ = h.w.EmojiID(ctx, "nope")
	assert.Equal(t, 1, h.fake.Hits("GET", "/api/v4/emoji/name/nope"), "a miss is remembered")

	e := h.fake.AddEmoji("shipit")
	h.eventually(func() bool { got, ok := h.w.State().CustomEmojiID("shipit"); return ok && got == e.ID }, "emoji_added not applied")
	id, err = h.w.EmojiID(ctx, "shipit")
	require.NoError(t, err)
	assert.Equal(t, e.ID, id)
	assert.Zero(t, h.fake.Hits("GET", "/api/v4/emoji/name/shipit"), "known from the event, no lookup")
}

func TestCustomEmojiDisabledSkipsTheList(t *testing.T) {
	h := newHarness(t, mmfake.Options{DisableCustomEmoji: true})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	id, err := h.w.EmojiID(context.Background(), "partyparrot")
	require.NoError(t, err)
	assert.Empty(t, id)
	assert.Zero(t, h.fake.Hits("GET", "/api/v4/emoji"))
	assert.Zero(t, h.fake.Hits("GET", "/api/v4/emoji/name/partyparrot"))
}
```

Run: `go test ./internal/state/ ./internal/mmsync/`
Expected: FAIL — `CustomEmojiEnabled undefined`, `h.w.EmojiID undefined`.

- [ ] **Step 2: Реализация — state**

`internal/state/emoji.go`:
```go
package state

import (
	"sort"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

// SetCustomEmoji replaces the server's custom emoji (name → id).
func (s *Server) SetCustomEmoji(list []model.Emoji) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emoji = make(map[string]string, len(list))
	for _, e := range list {
		s.addEmojiLocked(e)
	}
}

func (s *Server) AddCustomEmoji(e model.Emoji) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addEmojiLocked(e)
}

func (s *Server) addEmojiLocked(e model.Emoji) {
	if e.ID != "" && e.Name != "" && e.DeleteAt == 0 {
		s.emoji[e.Name] = e.ID
	}
}

func (s *Server) CustomEmojiID(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.emoji[name]
	return id, ok
}

// CustomEmojiNames lists the custom emoji for the picker, by name.
func (s *Server) CustomEmojiNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.emoji))
	for n := range s.emoji {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (s *Server) CustomEmojiEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.CustomEmoji
}
```

`internal/state/server.go`:
- в `Config` добавить `CustomEmoji bool `json:"custom_emoji,omitempty"``;
- в `Server` после `users` — `emoji map[string]string // custom emoji name → id (not in the snapshot)`;
- в `New` — `emoji: map[string]string{},`.

`internal/state/events.go`, в `switch ev.Type` перед `case "status_change"`:
```go
	case "emoji_added":
		if e, err := ws.DecodeEmoji(ev); err == nil {
			s.addEmojiLocked(e)
		}
```

- [ ] **Step 3: Реализация — воркер**

`internal/mmsync/emoji.go`:
```go
package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/rest"
)

const (
	emojiPageSize = 200 // the server's per_page cap
	maxEmojiPages = 20
	emojiMissTTL  = 10 * time.Minute
	maxEmojiMiss  = 1000
)

// REST is the server's REST client — its token and its rate limiter, shared
// with sync. Media streams go through it.
func (w *Worker) REST() *rest.Client { return w.rc }

// startEmojiLoad reads the server's custom emoji once per worker, in the
// background, after a bootstrap; a failed read is retried after the next.
func (w *Worker) startEmojiLoad() {
	if !w.st.CustomEmojiEnabled() || !w.emojiLoad.CompareAndSwap(false, true) {
		return
	}
	started := w.goBG(func(ctx context.Context) {
		if err := w.loadCustomEmoji(ctx); err != nil {
			w.emojiLoad.Store(false)
			if sessionExpired(err) {
				w.signalAuth()
			}
			slog.Warn("custom emoji unavailable", "srv", w.srv.ID, "err", err)
		}
	})
	if !started {
		w.emojiLoad.Store(false)
	}
}

func (w *Worker) loadCustomEmoji(ctx context.Context) error {
	var all []model.Emoji
	for page := 0; page < maxEmojiPages; page++ {
		list, err := w.rc.CustomEmojiPage(ctx, page, emojiPageSize)
		if err != nil {
			return err
		}
		all = append(all, list...)
		if len(list) < emojiPageSize {
			break
		}
	}
	w.st.SetCustomEmoji(all)
	return nil
}

// EmojiID resolves a custom emoji name for media: from the list (kept
// current by emoji_added), else one GET /emoji/name/{name}. A name the
// server does not know is remembered for emojiMissTTL, so a message full of
// ":not_an_emoji:" costs one request.
func (w *Worker) EmojiID(ctx context.Context, name string) (string, error) {
	if id, ok := w.st.CustomEmojiID(name); ok {
		return id, nil
	}
	if !w.st.CustomEmojiEnabled() {
		return "", nil
	}
	now := w.cfg.Now()
	w.missMu.Lock()
	at, missed := w.emojiMiss[name]
	w.missMu.Unlock()
	if missed && now.Sub(at) < emojiMissTTL {
		return "", nil
	}
	e, err := w.rc.EmojiByName(ctx, name)
	var re *rest.Error
	if errors.As(err, &re) && (re.Status == http.StatusNotFound || re.Status == http.StatusNotImplemented) {
		w.missMu.Lock()
		if len(w.emojiMiss) >= maxEmojiMiss {
			w.emojiMiss = map[string]time.Time{}
		}
		w.emojiMiss[name] = now
		w.missMu.Unlock()
		return "", nil
	}
	if err != nil {
		return "", err
	}
	w.st.AddCustomEmoji(e)
	return e.ID, nil
}
```

`internal/mmsync/worker.go`:
- в `Worker` после `viewing sync.Map`:
  ```go
  	emojiLoad atomic.Bool // custom emoji list read (or being read) by this worker
  	missMu    sync.Mutex
  	emojiMiss map[string]time.Time // custom emoji names the server does not have
  ```
- в `NewWorker` в литерал — `emojiMiss: map[string]time.Time{},`;
- в `fetchMeta` в `b.Config = state.Config{…}` добавить `CustomEmoji: cfg.EnableCustomEmoji == "true",`;
- в `bootstrap` после `w.enqueueAll()` и в `finishRefresh` после `w.enqueueAll()` — `w.startEmojiLoad()`.

Run: `go test -race ./internal/state/ ./internal/mmsync/`
Expected: PASS.

- [ ] **Step 4: Падающие тесты — Service, desktop, browser**

`internal/api/media_test.go`:
```go
package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/media"
)

func TestMediaOriginStreamsThroughTheWorker(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()

	resp, err := f.svc.Get(ctx, id, "/api/v4/users/u-bob/image", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	_, err = f.svc.Get(ctx, 999, "/api/v4/users/u-bob/image", nil)
	assert.ErrorIs(t, err, media.ErrNoServer)

	// Custom emoji are known once the first bootstrap has read the config.
	f.eventually(func() bool { eid, err := f.svc.EmojiID(ctx, id, "partyparrot"); return err == nil && eid == "e-parrot" }, "custom emoji name not resolved")

	fake.RevokeAll()
	fake.DropConnections(true)
	f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "session never expired")
	_, err = f.svc.Get(ctx, id, "/api/v4/users/u-bob/image", nil)
	assert.ErrorIs(t, err, media.ErrNoServer, "a dead session does not fetch")
}

func TestMediaCacheThroughTheService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	mc, err := media.New(media.Options{Dir: t.TempDir(), Origin: f.svc})
	require.NoError(t, err)
	ts := httptest.NewServer(mc)
	defer ts.Close()
	for i := 0; i < 2; i++ {
		resp, err := http.Get(fmt.Sprintf("%s/media/%d/thumb/f-build", ts.URL, id))
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "image/jpeg", resp.Header.Get("Content-Type"))
	}
	assert.Equal(t, 1, fake.Hits("GET", "/api/v4/files/f-build/thumbnail"))
}
```

`internal/desktop/media_test.go`:
```go
package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithMediaRoutesMediaAndAssets(t *testing.T) {
	assets := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("asset " + r.URL.Path)) })
	mediaH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("media " + r.URL.Path)) })
	serve := func(h http.Handler, path string) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Body.String()
	}
	h := withMedia(assets, mediaH)
	for path, want := range map[string]string{
		"/": "asset /", "/index.html": "asset /index.html", "/assets/app.js": "asset /assets/app.js",
		"/media/1/avatar/u1": "media /media/1/avatar/u1",
	} {
		assert.Equal(t, want, serve(h, path), path)
	}
	assert.Equal(t, "asset /media/1/avatar/u1", serve(withMedia(assets, nil), "/media/1/avatar/u1"), "no cache: assets only")
}
```

В `cmd/spk-mattermost/browser_test.go`:
- `setup` превратить в обёртку и добавить `setupWithService`:
  ```go
  func setup(t *testing.T, testAPI bool) (*httptest.Server, string, *mmfake.Server) {
  	ts, token, fake, _ := setupWithService(t, testAPI)
  	return ts, token, fake
  }

  func setupWithService(t *testing.T, testAPI bool) (*httptest.Server, string, *mmfake.Server, *api.Service) {
  	t.Helper()
  	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
  	require.NoError(t, err)
  	t.Cleanup(func() { _ = st.Close() })
  	em := events.NewEmitter()
  	fake := mmfake.Start(mmfake.Options{})
  	t.Cleanup(fake.Close)
  	svc := api.NewService(st, em, func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
  	dist := fstest.MapFS{"index.html": {Data: []byte("<html><head></head><body></body></html>")}}
  	notes := &api.RecordingNotifier{}
  	svc.SetNotifier(notes)
  	require.NoError(t, svc.Start(context.Background()))
  	t.Cleanup(svc.Close)
  	mc, err := media.New(media.Options{Dir: t.TempDir(), Origin: svc})
  	require.NoError(t, err)
  	h, token := newBrowserHandler(svc, em, dist, fake, testAPI, notes, mc)
  	ts := httptest.NewServer(h)
  	t.Cleanup(ts.Close)
  	return ts, token, fake, svc
  }
  ```
  (импорты: `"net/http/cookiejar"`, `"github.com/spk/spk-mattermost/internal/media"`).
- новый тест:
  ```go
  func TestMediaNeedsThePageCookie(t *testing.T) {
  	ts, token, fake, svc := setupWithService(t, false)
  	ctx := context.Background()
  	srv, err := svc.AddServer(ctx, fake.URL())
  	require.NoError(t, err)
  	_, err = svc.LoginWithPassword(ctx, srv.ID, "alice", "secret")
  	require.NoError(t, err)
  	u := fmt.Sprintf("%s/media/%d/avatar/u-bob?v=0", ts.URL, srv.ID)

  	resp, err := http.Get(u)
  	require.NoError(t, err)
  	_ = resp.Body.Close()
  	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "no cookie, no token")

  	jar, _ := cookiejar.New(nil)
  	c := &http.Client{Jar: jar}
  	resp, err = c.Get(ts.URL + "/")
  	require.NoError(t, err)
  	_ = resp.Body.Close()
  	var ck *http.Cookie
  	for _, x := range resp.Cookies() {
  		if x.Name == "spk_media" {
  			ck = x
  		}
  	}
  	require.NotNil(t, ck, "the page sets the media cookie")
  	assert.True(t, ck.HttpOnly)
  	assert.Equal(t, http.SameSiteStrictMode, ck.SameSite)
  	assert.Equal(t, "/media/", ck.Path)

  	resp, err = c.Get(u)
  	require.NoError(t, err)
  	_ = resp.Body.Close()
  	assert.Equal(t, 200, resp.StatusCode)
  	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))

  	req, _ := http.NewRequest(http.MethodGet, u, nil)
  	req.Header.Set("Authorization", "Bearer "+token)
  	resp, err = http.DefaultClient.Do(req)
  	require.NoError(t, err)
  	_ = resp.Body.Close()
  	assert.Equal(t, 200, resp.StatusCode, "API-style callers may use the bearer token")
  }
  ```

Run: `go test ./internal/api/ ./internal/desktop/ ./cmd/...`
Expected: FAIL — `f.svc.Get undefined`, `withMedia undefined`, `newBrowserHandler` — лишний аргумент.

- [ ] **Step 5: Реализация — Service как Origin**

`internal/api/media.go`:
```go
package api

import (
	"context"
	"net/http"

	"github.com/spk/spk-mattermost/internal/media"
	"github.com/spk/spk-mattermost/internal/mmsync"
)

var _ media.Origin = (*Service)(nil)

// running is the worker of a signed-in server with a live session; media
// never fetches for a server that needs a new sign-in.
func (s *Service) running(id int64) *mmsync.Worker {
	m := s.manager()
	if m == nil {
		return nil
	}
	w := m.Worker(id)
	if w == nil || w.Status() == mmsync.StatusNeedsReauth {
		return nil
	}
	return w
}

// Get implements media.Origin: one GET through the REST client of the
// server — its token and its rate limiter, shared with sync — on the
// transfer HTTP client (no whole-request timeout; ctx bounds it).
func (s *Service) Get(ctx context.Context, serverID int64, path string, hdr http.Header) (*http.Response, error) {
	w := s.running(serverID)
	if w == nil {
		return nil, media.ErrNoServer
	}
	return w.REST().WithHTTPClient(s.transfer).Stream(ctx, path, hdr)
}

// EmojiID implements media.Origin.
func (s *Service) EmojiID(ctx context.Context, serverID int64, name string) (string, error) {
	w := s.running(serverID)
	if w == nil {
		return "", media.ErrNoServer
	}
	return w.EmojiID(ctx, name)
}

func transportOf(hc *http.Client) http.RoundTripper {
	if hc != nil && hc.Transport != nil {
		return hc.Transport
	}
	return http.DefaultTransport
}
```

`internal/api/service.go`: в `Service` после `hc *http.Client` — `transfer *http.Client // media and downloads: no whole-request timeout`; в `NewService` в литерал — `transfer: &http.Client{Transport: transportOf(hc)},`.

- [ ] **Step 6: Реализация — desktop**

`internal/desktop/media.go`:
```go
package desktop

import "net/http"

// withMedia routes /media/ to the media cache and everything else to the UI
// assets: the webview loads pictures from the UI's own origin, so nothing in
// the webview ever talks to a Mattermost server. media nil → assets only.
func withMedia(assets, media http.Handler) http.Handler {
	if media == nil {
		return assets
	}
	mux := http.NewServeMux()
	mux.Handle("/media/", media)
	mux.Handle("/", assets)
	return mux
}
```

`internal/desktop/run.go`: в `Options` добавить `Media http.Handler // /media/ (internal/media); nil: pictures are not served`; импорт `"net/http"`; строку `Assets: application.AssetOptions{Handler: application.AssetFileServerFS(o.FrontendFS)},` заменить на
```go
		Assets: application.AssetOptions{Handler: withMedia(application.AssetFileServerFS(o.FrontendFS), o.Media)},
```

`cmd/spk-mattermost/run_desktop_wails.go`: после `defer svc.Close()`:
```go
	// A nil *media.Cache in the interface would not be a nil handler:
	// mediaH stays a nil interface when the cache cannot be opened.
	var mediaH http.Handler
	if mc, err := media.New(media.Options{Dir: p.MediaDir, Origin: svc}); err != nil {
		slog.Warn("media cache unavailable; pictures will not load", "err", err)
	} else {
		mediaH = mc
	}
```
и в `desktop.Options{…}` — `Media: mediaH,` (импорт `"github.com/spk/spk-mattermost/internal/media"`).

- [ ] **Step 7: Реализация — browser mode**

`cmd/spk-mattermost/static.go`: в начале `serveIndex` после чтения `index.html` (перед `w.Header().Set("Content-Type", …)`):
```go
	// <img> cannot send the bearer token: the page's own media requests
	// carry this cookie instead. HttpOnly: scripts never read it;
	// SameSite=Strict: another site embedding our /media/ URL does not send it.
	http.SetCookie(w, &http.Cookie{Name: mediaCookie, Value: token, Path: "/media/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
```

`cmd/spk-mattermost/browser.go`:
- константа и guard:
  ```go
  const mediaCookie = "spk_media"

  // mediaGuard admits the page's own requests (the cookie serveIndex set) and
  // API-style callers with the bearer token.
  func mediaGuard(token string, next http.Handler) http.Handler {
  	want := []byte(token)
  	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		c, err := r.Cookie(mediaCookie)
  		ok := err == nil && subtle.ConstantTimeCompare([]byte(c.Value), want) == 1
  		ok = ok || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
  		if !ok {
  			http.Error(w, "unauthorized", http.StatusUnauthorized)
  			return
  		}
  		next.ServeHTTP(w, r)
  	})
  }
  ```
  (импорт `"crypto/subtle"`);
- сигнатура `func newBrowserHandler(svc *api.Service, em *events.Emitter, dist fs.FS, fake *mmfake.Server, testAPI bool, notes *api.RecordingNotifier, mediaH http.Handler) (http.Handler, string)`; перед `mux.Handle("/", frontendHandler(token, dist))`:
  ```go
  	if mediaH != nil {
  		mux.Handle("/media/", mediaGuard(token, mediaH))
  	}
  ```
- в `buildBrowserServer` после `closers = append(closers, svc.Close)`:
  ```go
  	mc, err := media.New(media.Options{Dir: p.MediaDir, Origin: svc})
  	if err != nil {
  		cleanup()
  		return nil, nil, "", nil, fmt.Errorf("media cache: %w", err)
  	}
  ```
  и вызов `newBrowserHandler(svc, em, frontendFS(), fake, o.TestAPI, notes, mc)`.

- [ ] **Step 8: Тесты и сборки**

Run: `go test -race ./... && make build-desktop && make cross-check`
Expected: всё PASS, desktop-бинарь собран, кросс-проверка Windows без ошибок.

- [ ] **Step 9: Проверка вживую в browser mode**

Запустить по шагам 1–2 «Процедуры скриншота» (порт `P`, каталог `T`), затем:
```bash
TOKEN=$(curl -s http://127.0.0.1:$P/ | sed -n 's/.*api-token" content="\([^"]*\)".*/\1/p')
FAKE=$(curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:$P/api/_test/fake-url | sed -n 's/.*"url":"\([^"]*\)".*/\1/p')
ID=$(curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Origin: http://127.0.0.1:$P" -d "{\"url\":\"$FAKE\"}" http://127.0.0.1:$P/api/AddServer | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Origin: http://127.0.0.1:$P" -d "{\"id\":$ID,\"login\":\"alice\",\"password\":\"secret\"}" http://127.0.0.1:$P/api/LoginWithPassword > /dev/null
curl -s -o $T/bob.png -w '%{http_code} %{content_type}\n' -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$P/media/$ID/avatar/u-bob?v=0"
curl -s -o /dev/null -w '%{http_code}\n' "http://127.0.0.1:$P/media/$ID/avatar/u-bob?v=0"
ls -la $T/media
kill $(cat $T/pid)
```
Expected: `200 image/png`; без токена и cookie — `401`; в `$T/media` — один `*.bin`, `$T/bob.png` открывается как картинка (посмотреть Read).

- [ ] **Step 10: Линт**

Run: `go vet ./... && golangci-lint run && golangci-lint run --build-tags "wails gtk3"`
Expected: без замечаний.

- [ ] **Step 11: Commit**

```bash
git add internal/state internal/mmsync internal/api internal/desktop cmd/spk-mattermost
git commit -m "media: served in the desktop asset handler and behind the page cookie in browser mode; custom emoji names resolved in Go"
```

### Task 5: Статусы присутствия и версии аватаров в горячем слое и воркере

**Files:**
- Create: `internal/state/presence.go`, `internal/state/presence_test.go`
- Modify: `internal/state/server.go` (поле `presence`, `New`, `Bootstrap`), `internal/state/events.go` (`status_change`), `internal/state/view.go` (`PostView.Avatar/Status`, `postViewLocked`, pending), `internal/state/sidebar.go` (`ChannelItem`)
- Create: `internal/mmsync/status.go`, `internal/mmsync/status_test.go`
- Modify: `internal/mmsync/worker.go` (`Config.StatusEvery`, поле `statusDue`, цикл, запрос после `live`), `internal/mmsync/actions.go` (`OpenChannel`)

**Interfaces:**
- Consumes: Task 1 (`rest.StatusesByIDs`, `model.User.LastPictureUpdate`), Task 2 (фейк: статусы, `SetStatus`, `SetPicture`).
- Produces:
  - `state.PostView.Avatar string` (`json:"avatar,omitempty"`) — версия картинки автора (`last_picture_update` строкой, может быть `"0"` и отрицательной), `""` — профиль не загружен; `state.PostView.Status string` (`json:"status,omitempty"`) — `online|away|dnd|offline|ooo|""` (у ботов и вебхуков — `""`).
  - `state.ChannelItem.UserID` (`user_id`, собеседник DM), `Avatar` (`avatar`), `Status` (`status`), `Bot` (`bot`) — все omitempty, заполнены только у DM.
  - `(*state.Server).SetPresence([]model.Status) Change`, `StatusTargets(limit int) []string` (я первым, затем по алфавиту).
  - `mmsync.Config.StatusEvery time.Duration` (0 → 60 с); опрос при `live`, при `OpenChannel` (с задержкой 300 мс) и по таймеру.

Решение — «Архитектура» п. 6–7.

- [ ] **Step 1: Падающие тесты — state**

`internal/state/presence_test.go`:
```go
package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/ws"
)

func statusEv(uid, st string) ws.Event {
	d, _ := json.Marshal(map[string]any{"user_id": uid, "status": st})
	return ws.Event{Type: "status_change", Data: d, Broadcast: ws.Broadcast{UserID: uid}}
}

func sidebarItem(s *Server, id string) ChannelItem {
	for _, c := range s.Sidebar("t1").Categories {
		for _, it := range c.Channels {
			if it.ID == id {
				return it
			}
		}
	}
	return ChannelItem{}
}

func TestStatusTargetsAreMeShownDMPartnersAndActiveAuthors(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetUsers([]model.User{{ID: "u9", Username: "ci", IsBot: true}})
	s.SetWindow("town", []model.Post{mkPost("a", "town", "u3", 1000), mkPost("b", "town", "u9", 1001), mkPost("c", "town", "u2", 1002)}, true, 5)
	s.SetActive("town")
	assert.Equal(t, []string{"u1", "u2", "u3"}, s.StatusTargets(100), "bots have no status")
	s.SetActive("off")
	assert.Equal(t, []string{"u1", "u2"}, s.StatusTargets(100), "dm3 is closed (direct_channel_show=false), u3 is not shown")
	assert.Equal(t, []string{"u1"}, s.StatusTargets(1), "me first, then the cap")
}

func TestPresenceShowsInPostsAndDMRows(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetUsers([]model.User{{ID: "u2", Username: "bob", LastPictureUpdate: 77}})
	s.SetWindow("dm2", []model.Post{mkPost("p", "dm2", "u2", 1000)}, true, 5)
	s.SetActive("dm2")
	ch := s.SetPresence([]model.Status{{UserID: "u2", Status: "away"}, {UserID: "u1", Status: "dnd", DNDEndTime: 9}})
	assert.Equal(t, Change{Sidebar: true, Channels: []string{"dm2"}}, ch)

	v, _ := s.ChannelView("dm2")
	require.Len(t, v.Posts, 1)
	assert.Equal(t, "77", v.Posts[0].Avatar)
	assert.Equal(t, "away", v.Posts[0].Status)
	assert.Equal(t, ChannelItem{ID: "dm2", Name: "bob", Type: "D", Unread: true, Mentions: 1, UserID: "u2", Avatar: "77", Status: "away"}, sidebarItem(s, "dm2"))
	assert.Equal(t, ChannelItem{ID: "town", Name: "Town Square", Type: "O"}, sidebarItem(s, "town"), "channels carry no user")

	s.mu.Lock()
	assert.Equal(t, model.Status{Status: "dnd", DNDEndTime: 9}, model.Status{Status: s.status.Status, DNDEndTime: s.status.DNDEndTime}, "my own status feeds the DND check of notifications")
	s.mu.Unlock()
	assert.True(t, s.SetPresence([]model.Status{{UserID: "u2", Status: "away"}}).Empty(), "no change, no refresh")
}

func TestUnknownAuthorHasNoAvatarAndBotsNoStatus(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetUsers([]model.User{{ID: "u9", Username: "ci", IsBot: true}})
	s.SetWindow("town", []model.Post{mkPost("x", "town", "u404", 1000), mkPost("y", "town", "u9", 1001)}, true, 5)
	s.SetPresence([]model.Status{{UserID: "u404", Status: "online"}, {UserID: "u9", Status: "online"}})
	v, _ := s.ChannelView("town")
	assert.Empty(t, v.Posts[0].Avatar, "profile not loaded: initials, no request")
	assert.Equal(t, "online", v.Posts[0].Status)
	assert.Equal(t, "0", v.Posts[1].Avatar)
	assert.Empty(t, v.Posts[1].Status, "bots have no presence")
}

func TestStatusChangeEventUpdatesPresence(t *testing.T) {
	s := newFixture()
	s.SetActive("town")
	eff := s.ApplyEvent(statusEv("u1", "away"))
	assert.True(t, eff.Sidebar)
	assert.Equal(t, []string{"town"}, eff.Channels)
	s.mu.Lock()
	assert.Equal(t, "away", s.status.Status)
	assert.Equal(t, "away", s.presence["u1"])
	s.mu.Unlock()
	assert.True(t, s.ApplyEvent(statusEv("u1", "away")).Empty())
}

func TestAvatarVersionFollowsUserUpdated(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("dm2", []model.Post{mkPost("p", "dm2", "u2", 1000)}, true, 5)
	v, _ := s.ChannelView("dm2")
	assert.Equal(t, "0", v.Posts[0].Avatar)
	d, _ := json.Marshal(map[string]any{"user": map[string]any{"id": "u2", "username": "bob", "last_picture_update": -42}})
	s.ApplyEvent(ws.Event{Type: "user_updated", Data: d})
	v, _ = s.ChannelView("dm2")
	assert.Equal(t, "-42", v.Posts[0].Avatar, "a reset picture has a negative version")
	assert.Equal(t, "-42", sidebarItem(s, "dm2").Avatar)
}
```

Run: `go test ./internal/state/`
Expected: FAIL — `StatusTargets undefined`, `PostView.Avatar undefined`.

- [ ] **Step 2: Реализация — state**

`internal/state/presence.go`:
```go
package state

import (
	"sort"
	"strconv"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

// SetPresence records statuses from a status poll and reports the views to
// refresh; my own also updates the status the notification rules read.
func (s *Server) SetPresence(list []model.Status) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, st := range list {
		if st.UserID == "" {
			continue
		}
		if s.presence[st.UserID] != st.Status {
			s.presence[st.UserID] = st.Status
			changed = true
		}
		if st.UserID == s.me.ID {
			s.status.Status, s.status.DNDEndTime = st.Status, st.DNDEndTime
		}
	}
	if !changed {
		return Change{}
	}
	return s.presenceChangeLocked()
}

// presenceChangeLocked: statuses show in the sidebar (DMs) and the feed.
func (s *Server) presenceChangeLocked() Change {
	c := Change{Sidebar: true}
	if s.active != "" {
		c.Channels = []string{s.active}
	}
	return c
}

// StatusTargets lists the users whose presence is on screen — me first,
// then the partners of DMs the sidebar shows and the authors of the open
// channel (bots have none), at most limit. Mattermost sends status_change
// only to the user it is about, so these are polled (the webapp does the same).
func (s *Server) StatusTargets(limit int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := map[string]bool{}
	add := func(id string) {
		if id == "" || id == s.me.ID {
			return
		}
		if u, ok := s.users[id]; ok && u.IsBot {
			return
		}
		set[id] = true
	}
	for _, c := range s.chans {
		if !c.Info.IsDM() || c.Info.DeleteAt != 0 {
			continue
		}
		if unread, _ := s.unreadLocked(c); s.dmShownLocked(c, unread) {
			add(c.Info.DMPartner(s.me.ID))
		}
	}
	if ch := s.chans[s.active]; ch != nil {
		for _, p := range ch.Win.Posts {
			if !p.IsSystem() {
				add(p.UserID)
			}
		}
		for _, p := range s.older {
			if !p.IsSystem() {
				add(p.UserID)
			}
		}
	}
	out := make([]string, 0, len(set)+1)
	if s.me.ID != "" {
		out = append(out, s.me.ID)
	}
	others := make([]string, 0, len(set))
	for id := range set {
		others = append(others, id)
	}
	sort.Strings(others)
	out = append(out, others...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Server) presenceLocked(userID string) string {
	if u, ok := s.users[userID]; ok && u.IsBot {
		return ""
	}
	return s.presence[userID]
}

// avatarLocked is the picture version put into avatar URLs; "" while the
// profile is not loaded (the UI shows initials and fetches nothing).
func (s *Server) avatarLocked(userID string) string {
	u, ok := s.users[userID]
	if !ok {
		return ""
	}
	return strconv.FormatInt(u.LastPictureUpdate, 10)
}
```

`internal/state/server.go`:
- в `Server` после `status model.Status` — `presence map[string]string // user id → online|away|dnd|offline|ooo; not in the snapshot`;
- в `New` — `presence: map[string]string{},`;
- в `Bootstrap` после `s.me, s.status, s.cfg = b.Me, b.Status, b.Config` — `s.presence[b.Me.ID] = b.Status.Status`.

`internal/state/events.go`, ветку `status_change` заменить:
```go
	case "status_change":
		// Only our own arrives (the server sends it to that user alone);
		// the rest is polled — see StatusTargets.
		uid, st := ev.Str("user_id"), ev.Str("status")
		if uid == s.me.ID {
			s.status.Status = st
		}
		if uid != "" && s.presence[uid] != st {
			s.presence[uid] = st
			eff.Change = s.presenceChangeLocked()
		}
```

`internal/state/view.go`:
- в `PostView` после `Author`:
  ```go
  	Avatar        string             `json:"avatar,omitempty"` // picture version; "" = profile not loaded
  	Status        string             `json:"status,omitempty"` // presence of the author; "" for bots
  ```
- в `postViewLocked` после блока `if u, ok := s.users[p.UserID]; ok && u.IsBot { v.Bot = true }`:
  ```go
  	v.Avatar = s.avatarLocked(p.UserID)
  	if !v.Bot {
  		v.Status = s.presenceLocked(p.UserID)
  	}
  ```
- в построении pending-постов (`PostView{ID: p.ID, UserID: s.me.ID, …}`) добавить `Avatar: s.avatarLocked(s.me.ID), Status: s.presenceLocked(s.me.ID),`.

`internal/state/sidebar.go`:
- в `ChannelItem`:
  ```go
  	// DMs only: the partner, their picture version and presence.
  	UserID string `json:"user_id,omitempty"`
  	Avatar string `json:"avatar,omitempty"`
  	Status string `json:"status,omitempty"`
  	Bot    bool   `json:"bot,omitempty"`
  ```
- цикл сборки элементов категории:
  ```go
  		for _, id := range ids {
  			ch := s.chans[id]
  			u, m := s.unreadLocked(ch)
  			it := ChannelItem{ID: id, Name: s.channelNameLocked(ch), Type: ch.Info.Type,
  				Unread: u, Mentions: m, Muted: ch.Member.Muted()}
  			if ch.Info.IsDM() {
  				partner := ch.Info.DMPartner(s.me.ID)
  				it.UserID, it.Avatar = partner, s.avatarLocked(partner)
  				if pu, ok := s.users[partner]; ok && pu.IsBot {
  					it.Bot = true
  				} else {
  					it.Status = s.presenceLocked(partner)
  				}
  			}
  			cv.Channels = append(cv.Channels, it)
  		}
  ```

Run: `go test -race ./internal/state/`
Expected: PASS (старые тесты сайдбара сравнивают id и флаги, новые поля у каналов пусты).

- [ ] **Step 3: Падающий тест — опрос в воркере**

`internal/mmsync/status_test.go`:
```go
package mmsync

import (
	"strconv"
	"testing"
	"time"

	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/state"
)

func dmItem(h *harness, id string) state.ChannelItem {
	for _, c := range h.w.State().Sidebar("").Categories {
		for _, it := range c.Channels {
			if it.ID == id {
				return it
			}
		}
	}
	return state.ChannelItem{}
}

func TestStatusesArePolledForDMPartnersAndOpenChannelAuthors(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.tune = func(c *Config) { c.StatusEvery = 100 * time.Millisecond }
	h.start()
	h.live()
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Status == "online" }, "bob's status never arrived")
	h.fake.SetStatus("bob", "dnd")
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Status == "dnd" }, "the poll did not pick up the change")

	h.eventually(h.allLoaded, "prefetch")
	h.w.OpenChannel("c-town")
	h.eventually(func() bool {
		for _, p := range h.view("c-town").Posts {
			if p.UserID == "u-carol" && p.Status == "away" {
				return true
			}
		}
		return false
	}, "authors of the open channel are polled")

	at := h.fake.SetPicture("bob")
	h.eventually(func() bool { return dmItem(h, "c-dm-bob").Avatar == strconv.FormatInt(at, 10) }, "user_updated did not bump the picture version")
}
```

Run: `go test ./internal/mmsync/ -run TestStatusesArePolled`
Expected: FAIL — `Config.StatusEvery undefined`.

- [ ] **Step 4: Реализация — опрос**

`internal/mmsync/status.go`:
```go
package mmsync

import (
	"context"
	"log/slog"
	"time"
)

const (
	defaultStatusEvery = time.Minute
	statusDebounce     = 300 * time.Millisecond
	maxStatusTargets   = 400 // 4 requests of 100 ids at most
)

// statusLoop polls the presence of users on screen: on going live, shortly
// after a channel opens (a burst of switches costs one poll) and every
// StatusEvery. Only while live — offline there is nobody to ask.
func (w *Worker) statusLoop(ctx context.Context) {
	t := time.NewTicker(w.cfg.StatusEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.statusDue:
			select {
			case <-ctx.Done():
				return
			case <-time.After(statusDebounce):
			}
			select {
			case <-w.statusDue:
			default:
			}
		}
		if w.Status() == StatusLive {
			w.pollStatuses(ctx)
		}
	}
}

func (w *Worker) pollStatuses(ctx context.Context) {
	ids := w.st.StatusTargets(maxStatusTargets)
	if len(ids) == 0 {
		return
	}
	list, err := w.rc.StatusesByIDs(ctx, ids)
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Debug("statuses unavailable", "srv", w.srv.ID, "err", err)
		return
	}
	w.changed(w.st.SetPresence(list))
}

func (w *Worker) requestStatuses() {
	select {
	case w.statusDue <- struct{}{}:
	default:
	}
}
```

`internal/mmsync/worker.go`:
- в `Config` после `Fetchers int` — `StatusEvery time.Duration // presence poll period; 0 → 1 min`;
- в `defaults()` — `if c.StatusEvery <= 0 { c.StatusEvery = defaultStatusEvery }`;
- в `Worker` рядом с `metaDue` — `statusDue chan struct{} // a presence poll is wanted soon`; в `NewWorker` — `statusDue: make(chan struct{}, 1),`;
- в `Run` список циклов: `[]func(context.Context){w.flushLoop, w.fetchLoop, w.metaLoop, w.wakeLoop, w.statusLoop}`;
- в `session` сразу после `w.setStatus(StatusLive)` — `w.requestStatuses()`.

`internal/mmsync/actions.go`, в `OpenChannel` перед `w.changed(state.Change{Sidebar: true})` — `w.requestStatuses() // the open channel's authors`.

- [ ] **Step 5: Тесты проходят**

Run: `go test -race ./internal/state/ ./internal/mmsync/ ./internal/api/`
Expected: PASS.

- [ ] **Step 6: Линт**

Run: `go vet ./... && golangci-lint run && golangci-lint run --build-tags "wails gtk3"`
Expected: без замечаний.

- [ ] **Step 7: Commit**

```bash
git add internal/state internal/mmsync
git commit -m "state, mmsync: presence polled for DM partners and the open channel's authors, picture versions in post and DM views"
```

### Task 6: UI — аватары и точки статуса в ленте и сайдбаре

**Files:**
- Create: `frontend/src/media.ts`, `frontend/src/media.test.ts`
- Create: `frontend/src/components/Avatar.tsx`, `frontend/src/components/Avatar.test.tsx`
- Modify: `frontend/src/api/types.ts` (`PostView`, `ChannelItem`)
- Modify: `frontend/src/components/PostItem.tsx` (prop `serverId`, `Avatar`), `frontend/src/components/PostItem.test.tsx`
- Modify: `frontend/src/components/Feed.tsx` (prop `serverId`), `frontend/src/components/Feed.test.tsx`, `frontend/src/components/ChannelPane.tsx`
- Modify: `frontend/src/components/Sidebar.tsx`, `frontend/src/components/Sidebar.test.tsx`
- Modify: `frontend/src/i18n.ts` (`presence.*`), `frontend/src/index.css` (цвета статусов)

**Interfaces:**
- Consumes: Task 4 (`/media/{srv}/avatar/{uid}?v=`), Task 5 (`PostView.avatar/status`, `ChannelItem.user_id/avatar/status/bot`).
- Produces:
  - `media.ts`: `type MediaKind = 'avatar' | 'thumb' | 'feed' | 'full' | 'text' | 'emoji'`; `mediaURL(serverId: number, kind: MediaKind, key: string, params?: Record<string, string>): string`.
  - `Avatar.tsx`: `Avatar({ serverId, userId, version?, name, status?, size, surface: 'app' | 'sidebar' })` (memo); `StatusDot({ status, surface, small })`; `presenceKey(status): 'online' | 'away' | 'dnd' | 'offline'`; `presenceLabel(status): string`; `colorFor(id): string`.
  - `PostItem` и `Feed` получают prop `serverId: number`.

Вид — как у официального клиента: круглый аватар, точка статуса справа снизу с обводкой цвета фона (online — зелёная `#06d6a0`, away — жёлтая `#ffbc1f`, dnd — красная `#d24b4e`, offline/ooo — полый серый круг). Точка в ленте — декоративная (`aria-hidden`, подсказка `title`); в строке DM статус входит в доступное имя кнопки через `aria-label` («bob, В сети», с упоминаниями — «bob, В сети, Упоминаний: 2»; скрытый `sr-only`-текст не годится — Chromium склеивает его с лишним пробелом: «bob , Online»).

- [ ] **Step 1: Падающие тесты**

`frontend/src/media.test.ts`:
```ts
import { mediaURL } from './media'

test('media URLs are same-origin paths with encoded keys', () => {
  expect(mediaURL(3, 'avatar', 'u-bob', { v: '-42' })).toBe('/media/3/avatar/u-bob?v=-42')
  expect(mediaURL(1, 'emoji', '+1')).toBe('/media/1/emoji/%2B1')
  expect(mediaURL(1, 'feed', 'f1', { src: 'file' })).toBe('/media/1/feed/f1?src=file')
})
```

`frontend/src/components/Avatar.test.tsx`:
```tsx
import { fireEvent, render } from '@testing-library/react'
import { setLocale } from '../i18n'
import { Avatar, presenceKey } from './Avatar'

beforeEach(() => setLocale('en'))

test('a loaded profile: picture with its version, fixed size, presence dot', () => {
  const { container } = render(<Avatar serverId={3} userId="u-bob" version="42" name="bob" status="away" size={36} surface="app" />)
  const img = container.querySelector('img')!
  expect(img).toHaveAttribute('src', '/media/3/avatar/u-bob?v=42')
  expect(img).toHaveAttribute('width', '36')
  expect(img).toHaveAttribute('height', '36')
  expect(img).toHaveAttribute('alt', '')
  expect(img).toHaveAttribute('loading', 'lazy')
  const dot = container.querySelector('[data-status]')!
  expect(dot).toHaveAttribute('data-status', 'away')
  expect(dot).toHaveAttribute('title', 'Away')
  expect(dot).toHaveAttribute('aria-hidden', 'true')
})

test('no profile yet or a failed load: initials; a new version tries again', () => {
  const { container, rerender } = render(<Avatar serverId={3} userId="u-x" name="" size={36} surface="app" />)
  expect(container.querySelector('img')).toBeNull()
  expect(container).toHaveTextContent('?')
  rerender(<Avatar serverId={3} userId="u-bob" version="1" name="bob" size={36} surface="app" />)
  fireEvent.error(container.querySelector('img')!)
  expect(container.querySelector('img')).toBeNull()
  expect(container).toHaveTextContent('B')
  rerender(<Avatar serverId={3} userId="u-bob" version="2" name="bob" size={36} surface="app" />)
  expect(container.querySelector('img')).toHaveAttribute('src', '/media/3/avatar/u-bob?v=2')
})

test('offline and out-of-office look the same; no status, no dot', () => {
  expect(presenceKey('ooo')).toBe('offline')
  expect(presenceKey('offline')).toBe('offline')
  expect(presenceKey('dnd')).toBe('dnd')
  const { container } = render(<Avatar serverId={1} userId="u" version="0" name="x" size={20} surface="sidebar" />)
  expect(container.querySelector('[data-status]')).toBeNull()
})
```

`frontend/src/components/PostItem.test.tsx`: во все девять элементов `<PostItem …/>` (восемь однострочных `render(<PostItem …`/`rerender(<PostItem …` и многострочный в тесте «attachments, files, reactions and reply count») добавить `serverId={1}` первым пропом; дописать тест:
```tsx
test('head shows the author picture with presence; an unknown author gets initials', () => {
  const { container, rerender } = render(<PostItem serverId={2} post={post({ avatar: '9', status: 'dnd' })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  expect(container.querySelector('img')).toHaveAttribute('src', '/media/2/avatar/u-bob?v=9')
  expect(container.querySelector('[data-status="dnd"]')).toHaveAttribute('title', 'Do not disturb')
  rerender(<PostItem serverId={2} post={post({ author: '' })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  expect(container.querySelector('img')).toBeNull()
  expect(container).toHaveTextContent('?')
})
```

`frontend/src/components/Feed.test.tsx`: в объект `props(...)` добавить `serverId: 1,` (после `channel: channel(o),`).

`frontend/src/components/Sidebar.test.tsx`, дописать:
```tsx
test('DM rows show the partner picture with presence; bots have no dot; groups keep their mark', () => {
  const dms: SidebarDTO = {
    ...sb,
    categories: [{
      id: 'dm', type: 'direct_messages', name: 'Direct Messages', collapsed: false,
      channels: [
        { id: 'c-dm', name: 'bob', type: 'D', unread: false, mentions: 0, muted: false, user_id: 'u-bob', avatar: '5', status: 'online' },
        { id: 'c-bot', name: 'ci', type: 'D', unread: false, mentions: 0, muted: false, user_id: 'u-ci', avatar: '0', bot: true },
        { id: 'c-gm', name: 'alice, bob, carol', type: 'G', unread: false, mentions: 0, muted: false },
      ],
    }],
  }
  renderSidebar({ sidebar: dms })
  const bob = screen.getByRole('button', { name: 'bob, Online' })
  expect(bob.querySelector('img')).toHaveAttribute('src', '/media/1/avatar/u-bob?v=5')
  expect(bob.querySelector('[data-status="online"]')).not.toBeNull()
  const ci = screen.getByRole('button', { name: 'ci' })
  expect(ci.querySelector('[data-status]')).toBeNull()
  expect(screen.getByRole('button', { name: /alice, bob, carol/ })).toHaveTextContent('👥')
})
```

Run: `cd frontend && pnpm test src/media.test.ts src/components/Avatar.test.tsx src/components/PostItem.test.tsx src/components/Sidebar.test.tsx src/components/Feed.test.tsx`
Expected: FAIL — нет `./media`, `./Avatar`; `serverId` не принимается.

- [ ] **Step 2: Реализация**

`frontend/src/media.ts`:
```ts
export type MediaKind = 'avatar' | 'thumb' | 'feed' | 'full' | 'text' | 'emoji'

// mediaURL addresses a picture or file snippet that Go serves on the UI's
// own origin (internal/media): the UI never talks to a Mattermost server
// and never sees its tokens.
export function mediaURL(serverId: number, kind: MediaKind, key: string, params: Record<string, string> = {}): string {
  const q = new URLSearchParams(params).toString()
  return `/media/${serverId}/${kind}/${encodeURIComponent(key)}${q ? `?${q}` : ''}`
}
```

`frontend/src/components/Avatar.tsx`:
```tsx
import { memo, useState } from 'react'
import { t, type I18nKey } from '../i18n'
import { mediaURL } from '../media'

const COLORS = ['bg-rose-500', 'bg-orange-500', 'bg-amber-600', 'bg-lime-600', 'bg-emerald-600', 'bg-teal-600', 'bg-sky-600', 'bg-indigo-500', 'bg-violet-500', 'bg-fuchsia-600']

// colorFor: the initials placeholder keeps a stable color per user.
export function colorFor(id: string): string {
  let h = 0
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) | 0
  return COLORS[Math.abs(h) % COLORS.length]
}

// Offline and out-of-office look (and read) the same, as in the webapp's status_icon.
export function presenceKey(status: string): 'online' | 'away' | 'dnd' | 'offline' {
  return status === 'online' || status === 'away' || status === 'dnd' ? status : 'offline'
}

export const presenceLabel = (status: string) => t(`presence.${presenceKey(status)}` as I18nKey)

// Literal class names: Tailwind only generates classes it finds in the source.
const SURFACE = {
  app: { ring: 'ring-app', hollow: 'bg-app' },
  sidebar: { ring: 'ring-sidebar', hollow: 'bg-sidebar' },
}
const FILL: Record<string, string> = { online: 'bg-online', away: 'bg-away', dnd: 'bg-dnd' }

export function StatusDot({ status, surface, small }: { status: string; surface: 'app' | 'sidebar'; small?: boolean }) {
  const k = presenceKey(status)
  const s = SURFACE[surface]
  const fill = FILL[k] ?? `border-2 border-fg-subtle ${s.hollow}`
  return (
    <span
      aria-hidden="true"
      data-status={k}
      title={presenceLabel(status)}
      className={`absolute -bottom-0.5 -right-0.5 block rounded-full ring-2 ${s.ring} ${small ? 'h-2 w-2' : 'h-2.5 w-2.5'} ${fill}`}
    />
  )
}

interface Props {
  serverId: number
  userId: string
  version?: string // picture version from Go; absent/"" = profile not loaded
  name: string
  status?: string
  size: number
  surface: 'app' | 'sidebar'
}

// Avatar is decorative (alt=""): the name is always shown next to it. Its box
// has the final size from the start, so a loading picture never moves text.
export const Avatar = memo(function Avatar({ serverId, userId, version, name, status, size, surface }: Props) {
  // A failed load shows initials until the picture version changes.
  const [failedFor, setFailedFor] = useState<string | null>(null)
  const src = version && failedFor !== version ? mediaURL(serverId, 'avatar', userId, { v: version }) : null
  return (
    <span className="relative block shrink-0" style={{ width: size, height: size }}>
      {src ? (
        <img
          src={src}
          alt=""
          width={size}
          height={size}
          loading="lazy"
          decoding="async"
          draggable={false}
          onError={() => setFailedFor(version ?? null)}
          className="block h-full w-full rounded-full bg-hover object-cover"
        />
      ) : (
        <span
          aria-hidden="true"
          className={`flex h-full w-full items-center justify-center rounded-full font-semibold text-white ${colorFor(userId)}`}
          style={{ fontSize: Math.round(size * 0.4) }}
        >
          {(name[0] ?? '?').toUpperCase()}
        </span>
      )}
      {status ? <StatusDot status={status} surface={surface} small={size <= 24} /> : null}
    </span>
  )
})
```
(`version` `"0"` — непустая строка, картинка грузится; пустая строка — профиля нет.)

`frontend/src/index.css`, в `@theme` добавить:
```css
  --color-online: #06d6a0;
  --color-away: #ffbc1f;
  --color-dnd: #d24b4e;
```

`frontend/src/api/types.ts`:
- в `ChannelItem`:
  ```ts
    user_id?: string // DMs: the partner
    avatar?: string // DMs: picture version ('' / absent: profile not loaded)
    status?: string // DMs: presence
    bot?: boolean
  ```
- в `PostView` после `author`:
  ```ts
    avatar?: string // picture version of the author; absent: profile not loaded
    status?: string // presence of the author (online | away | dnd | offline | ooo)
  ```

`frontend/src/i18n.ts`: в `ru` — `'presence.online': 'В сети'`, `'presence.away': 'Отошёл'`, `'presence.dnd': 'Не беспокоить'`, `'presence.offline': 'Не в сети'`; в `en` — `'presence.online': 'Online'`, `'presence.away': 'Away'`, `'presence.dnd': 'Do not disturb'`, `'presence.offline': 'Offline'`.

`frontend/src/components/PostItem.tsx`:
- удалить локальные `COLORS` и `function Avatar`; импорт `import { Avatar } from './Avatar'`;
- в `Props` добавить `serverId: number`, в сигнатуру компонента — `serverId`;
- ячейку аватара заменить:
  ```tsx
        <div className="w-9 shrink-0 pt-0.5">
          {head ? (
            <Avatar serverId={serverId} userId={post.user_id} version={post.avatar} name={post.author} status={post.status} size={36} surface="app" />
          ) : (
            <time className="invisible block pt-1 text-right text-[10px] text-fg-subtle group-hover:visible">{time}</time>
          )}
        </div>
  ```

`frontend/src/components/Feed.tsx`: в `Props` — `serverId: number`; в сигнатуре — `serverId`; в `renderRow` для `post` — `<PostItem serverId={serverId} post={r.post} …/>`.

`frontend/src/components/ChannelPane.tsx`: `<Feed key={…} serverId={server.id} channel={channel} …/>`.

`frontend/src/components/Sidebar.tsx`:
- импорт `import { Avatar, presenceLabel } from './Avatar'`;
- `ChannelRow` получает `serverId: number`:
  ```tsx
  function ChannelRow({ serverId, item, active, onClick }: { serverId: number; item: ChannelItem; active: boolean; onClick(): void }) {
    const tone = active ? 'bg-blue-700 text-white' : item.unread ? 'font-semibold text-fg' : 'text-fg-subtle'
    const person = item.type === 'D' && item.user_id
    const status = item.bot ? '' : (item.status ?? '')
    // The presence goes into the button's name as a whole label: a visually
    // hidden span would be joined with a stray space by Chromium ("bob , Online").
    const label =
      person && status
        ? [item.name, presenceLabel(status), ...(item.mentions > 0 ? [t('rail.mentions', { n: String(item.mentions) })] : [])].join(', ')
        : undefined
    return (
      <button
        aria-current={active}
        aria-label={label}
        onClick={onClick}
        className={`flex w-full items-center gap-2 rounded px-3 py-1 text-left hover:bg-hover ${tone} ${item.muted ? 'opacity-50' : ''}`}
      >
        {person ? (
          <Avatar serverId={serverId} userId={item.user_id!} version={item.avatar} name={item.name} status={status} size={20} surface="sidebar" />
        ) : (
          <span className="w-4 shrink-0 text-center text-xs opacity-70">{channelGlyph(item.type)}</span>
        )}
        <span className="truncate">{item.name}</span>
        {item.mentions > 0 && <MentionPill n={item.mentions} />}
      </button>
    )
  }
  ```
- в списке — `<ChannelRow serverId={p.server.id} item={c} …/>`.

- [ ] **Step 3: Тесты, типы, линт**

Run: `cd frontend && pnpm test && pnpm lint && pnpm build`
Expected: все тесты PASS (включая `i18n.test.ts` — паритет ключей), `tsc` и сборка без ошибок.

- [ ] **Step 4: Скриншот browser mode**

«Процедура скриншота», сценарий:
```js
await p.waitForSelector('[aria-label="Messages"] img[src*="/avatar/"]')
await p.screenshot({ path: 'T/t6-town.png' })
await test('fake/status', { username: 'bob', status: 'dnd' })
await p.getByRole('button', { name: /Off-Topic/ }).click() // opening a channel polls statuses
await p.getByRole('button', { name: 'bob, Do not disturb' }).waitFor()
await p.screenshot({ path: 'T/t6-offtopic.png' })
return await p.evaluate(() => [...document.querySelectorAll('img[src*="/avatar/"]')].map((i) => [i.getAttribute('src'), i.naturalWidth]))
```
Expected: `t6-town.png` — у постов bob и carol круглые картинки в полоску (не инициалы), у bob зелёная точка, у carol жёлтая; в сайдбаре у DM bob — маленький аватар с точкой; у группы — 👥; всё на тёмном фоне. `t6-offtopic.png` — у bob красная точка. Возвращённый список: у всех картинок `naturalWidth` = 128 (загрузились). Процесс остановить, контексты закрыты.

- [ ] **Step 5: Скриншот desktop (asset handler Wails)**

```bash
make build-desktop
T=$(mktemp -d -p /home/spk/.spk/sawe/ss/Mattermost/.agents/tmp)
SPK_MATTERMOST_HOME=$T build/bin/spk-mattermost-desktop --mm-fake > $T/log 2>&1 & echo $! > $T/pid
for i in $(seq 1 30); do wmctrl -l | grep -q spk-mattermost && break; sleep 1; done
sleep 3; import -window "$(wmctrl -l | awk '/spk-mattermost/{print $1; exit}')" $T/t6-desktop.png
kill $(cat $T/pid)
```
Expected: на `t6-desktop.png` те же аватары и точки, что в браузере (картинки пришли через `wails://localhost/media/…`); в `$T/log` нет ошибок `media`. (`sleep` здесь — ожидание отрисовки окна, условия ждать нечем; окно ищется циклом.)

- [ ] **Step 6: Commit**

```bash
git add frontend
git commit -m "ui: avatars with presence dots in the feed and on DM rows"
```

### Task 7: Файлы в Go — FileView для превью, «Скачать» и «Открыть»

**Files:**
- Modify: `internal/state/view.go` (`FileView`, `postViewLocked`), `internal/state/view_test.go`
- Create: `internal/api/files.go`, `internal/api/files_test.go`
- Modify: `internal/api/api.go` (методы, `SavedFile`, `CodeNoFile`), `internal/api/service.go` (поля `fileOpen`, `filesMu`, `saved`)
- Modify: `internal/api/transport/http.go`, `internal/api/transport/wails.go`, `internal/api/transport/http_test.go`
- Modify: `go.mod` (`github.com/adrg/xdg` — из indirect в прямые, `go mod tidy`)
- Modify: `cmd/spk-mattermost/browser.go` (`RecordingOpener`, test-API `opened-files`, сигнатура `newBrowserHandler`), `cmd/spk-mattermost/browser_test.go`, `cmd/spk-mattermost/run_desktop_wails.go`

**Interfaces:**
- Consumes: Task 1 (`rest.FileInfo`, `Stream`), Task 2 (фейк: файлы, `PostFile`, `Hits`), Task 4 (`Worker.REST`, `Service.transfer`).
- Produces:
  - `state.FileView{ID, Name, Ext, Size, Mime, Width, Height, HasPreview}` — JSON `id, name, ext, size, mime, width, height, has_preview` (ext/width/height/has_preview — omitempty).
  - `api.API`: `DownloadFile(ctx, id int64, fileID string) (SavedFile, error)`, `OpenFile(ctx, id int64, fileID string) (SavedFile, error)`; `type api.SavedFile struct{ Path string \`json:"path"\`; Opened bool \`json:"opened"\` }`; код `api.CodeNoFile = "no_file"`.
  - `(*api.Service).SetFileOpener(fn api.Opener)`; `type api.RecordingOpener` (`Open(path string) error`, `List() []string`).
  - HTTP: `POST /api/DownloadFile`, `POST /api/OpenFile` `{id, file_id}`; Wails: `DownloadFile(id int64, fileID string)`, `OpenFile(id int64, fileID string)`; test-API `GET /api/_test/opened-files`.
  - `newBrowserHandler(svc, em, dist, fake, testAPI, notes, mediaH, opened *api.RecordingOpener)`.

Решение — «Архитектура» п. 5. Каталог загрузок: `SPK_MATTERMOST_DOWNLOADS` (e2e, тесты) → `xdg.UserDirs.Download` (читает `~/.config/user-dirs.dirs`: у русской локали это `~/Загрузки`) → `~/Downloads`. Имя с сервера очищается (`sanitizeName`), занятое имя получает « (1)», « (2)», … (создание с `O_EXCL` — без гонок). Повторное «Скачать»/«Открыть» того же файла в этой сессии не качает заново, если сохранённый файл на месте и того же размера.

- [ ] **Step 1: Падающие тесты**

`internal/state/view_test.go`: в `TestChannelViewComposition` ожидание файлов заменить на
```go
	assert.Equal(t, []FileView{{ID: "f1", Name: "a.pdf", Size: 10, Mime: "application/pdf"}}, v.Posts[2].Files)
```
и дописать тест:
```go
func TestFileViewCarriesWhatPreviewsNeed(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	p := mkPost("p", "off", "u2", 1000)
	p.Metadata = &model.PostMetadata{Files: []model.FileInfo{{ID: "f1", Name: "a.png", Extension: "png", Size: 10, MimeType: "image/png", Width: 640, Height: 480, HasPreviewImage: true}}}
	s.SetWindow("off", []model.Post{p}, true, 5)
	v, _ := s.ChannelView("off")
	assert.Equal(t, []FileView{{ID: "f1", Name: "a.png", Ext: "png", Size: 10, Mime: "image/png", Width: 640, Height: 480, HasPreview: true}}, v.Posts[0].Files)
}
```

`internal/api/files_test.go`:
```go
package api

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func downloadsIn(dir string) func(string) string {
	return func(k string) string {
		if k == "SPK_MATTERMOST_DOWNLOADS" {
			return dir
		}
		return ""
	}
}

func TestDownloadSavesIntoDownloadsWithUniqueNames(t *testing.T) {
	f := newChatFixture(t)
	dl := filepath.Join(t.TempDir(), "Загрузки")
	f.svc.getenv = downloadsIn(dl)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()

	r1, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	assert.Equal(t, SavedFile{Path: filepath.Join(dl, "spec.pdf")}, r1)
	data, err := os.ReadFile(r1.Path)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(data, []byte("%PDF")))

	r2, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	assert.Equal(t, r1, r2, "the same file is not saved twice")
	assert.Equal(t, 1, fake.Hits("GET", "/api/v4/files/f-spec"))

	require.NoError(t, os.WriteFile(r1.Path, []byte("edited"), 0o644))
	r3, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dl, "spec (1).pdf"), r3.Path, "a changed copy is kept, the new one gets a free name")
}

func TestOpenFileOpensSafeTypesOnly(t *testing.T) {
	f := newChatFixture(t)
	dl := t.TempDir()
	f.svc.getenv = downloadsIn(dl)
	opened := &RecordingOpener{}
	f.svc.SetFileOpener(opened.Open)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()

	r, err := f.svc.OpenFile(ctx, id, "f-log")
	require.NoError(t, err)
	assert.True(t, r.Opened)
	assert.Equal(t, []string{filepath.Join(dl, "server.log")}, opened.List())

	p := fake.PostFile("c-offtopic", "bob", "run me", "setup.desktop", "application/x-desktop", []byte("[Desktop Entry]\nExec=rm -rf ~\n"))
	r, err = f.svc.OpenFile(ctx, id, p.Metadata.Files[0].ID)
	require.NoError(t, err)
	assert.False(t, r.Opened, "launchers are saved, never opened")
	assert.FileExists(t, r.Path)
	assert.Len(t, opened.List(), 1)
}

func TestDownloadErrors(t *testing.T) {
	f := newChatFixture(t)
	f.svc.getenv = downloadsIn(t.TempDir())
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	_, err := f.svc.DownloadFile(ctx, id, "f-nope")
	assert.Equal(t, CodeNoFile, codeOf(err))
	_, err = f.svc.DownloadFile(ctx, id, "../etc")
	assert.Equal(t, CodeNoFile, codeOf(err))
	assert.Zero(t, fake.Hits("GET", "/api/v4/files/../etc/info"))
	_, err = f.svc.DownloadFile(ctx, 999, "f-spec")
	assert.Equal(t, CodeNotFound, codeOf(err))
}

func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		"../../.bashrc":         ".bashrc",
		`a\b\c.txt`:             "c.txt",
		"x\x00y.txt":            "x_y.txt",
		`re:port?.pdf`:          "re_port_.pdf",
		"  spaced.txt ":         "spaced.txt",
		"":                      "file",
		"..":                    "file",
		"dir/":                  "file",
		strings.Repeat("я", 150): strings.Repeat("я", 100),
	} {
		assert.Equal(t, want, sanitizeName(in), in)
	}
}

func TestDownloadsDirFollowsEnvThenXDG(t *testing.T) {
	d, err := downloadsDir(func(string) string { return "/x/dl" })
	require.NoError(t, err)
	assert.Equal(t, "/x/dl", d)
	d, err = downloadsDir(func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, xdg.UserDirs.Download, d)
}
```

`internal/api/transport/http_test.go`: в `fakeAPI` добавить
```go
func (f *fakeAPI) DownloadFile(_ context.Context, id int64, fileID string) (api.SavedFile, error) {
	return api.SavedFile{Path: fmt.Sprintf("/dl/%d/%s", id, fileID)}, nil
}
```
и в конец `TestChatRoutes`:
```go
	resp = call(t, h, ts.URL, "DownloadFile", `{"id":3,"file_id":"f1"}`)
	var saved api.SavedFile
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&saved))
	assert.Equal(t, "/dl/3/f1", saved.Path)
```

Run: `go test ./internal/state/ ./internal/api/...`
Expected: FAIL — `FileView.ID undefined`, `DownloadFile undefined`, `sanitizeName undefined`.

- [ ] **Step 2: Реализация — FileView**

`internal/state/view.go`:
```go
// FileView is what the feed needs to show a file: previews come from
// /media/ by id; width/height fix the image box before it loads.
type FileView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Ext        string `json:"ext,omitempty"`
	Size       int64  `json:"size"`
	Mime       string `json:"mime"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	HasPreview bool   `json:"has_preview,omitempty"`
}
```
и в `postViewLocked`:
```go
		for _, f := range p.Metadata.Files {
			v.Files = append(v.Files, FileView{ID: f.ID, Name: f.Name, Ext: f.Extension, Size: f.Size, Mime: f.MimeType,
				Width: f.Width, Height: f.Height, HasPreview: f.HasPreviewImage})
		}
```

- [ ] **Step 3: Реализация — скачивание**

`internal/api/files.go`:
```go
package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/adrg/xdg"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/rest"
)

// SavedFile is where a downloaded file landed and whether it was handed to
// the system to open.
type SavedFile struct {
	Path   string `json:"path"`
	Opened bool   `json:"opened"`
}

const downloadTimeout = 30 * time.Minute

var fileIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// launchers are never handed to the system by "Open": opening them runs
// code (or asks the desktop to), and the file came from someone else. They
// are still saved.
var launchers = map[string]bool{
	".desktop": true, ".sh": true, ".bash": true, ".zsh": true, ".run": true, ".bin": true, ".appimage": true,
	".exe": true, ".msi": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true, ".ps1": true, ".vbs": true,
	".jar": true, ".py": true, ".pl": true, ".rb": true, ".deb": true, ".rpm": true, ".apk": true, ".app": true,
	".command": true, ".lnk": true, ".reg": true,
}

func openable(path string) bool { return !launchers[strings.ToLower(filepath.Ext(path))] }

// RecordingOpener remembers opened files (browser mode, tests): there is no
// system application to hand them to.
type RecordingOpener struct {
	mu    sync.Mutex
	paths []string
}

func (r *RecordingOpener) Open(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
	return nil
}

func (r *RecordingOpener) List() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.paths...)
}

// SetFileOpener sets how "Open" hands a saved file to the system; nil: it
// only saves.
func (s *Service) SetFileOpener(fn Opener) {
	s.mu.Lock()
	s.fileOpen = fn
	s.mu.Unlock()
}

// DownloadFile saves a file of a post into the downloads directory.
func (s *Service) DownloadFile(ctx context.Context, id int64, fileID string) (SavedFile, error) {
	return s.saveFile(ctx, id, fileID, false)
}

// OpenFile saves a file like DownloadFile and opens it with the system
// application, unless it is a launcher.
func (s *Service) OpenFile(ctx context.Context, id int64, fileID string) (SavedFile, error) {
	return s.saveFile(ctx, id, fileID, true)
}

func (s *Service) saveFile(ctx context.Context, id int64, fileID string, open bool) (SavedFile, error) {
	if !fileIDRe.MatchString(fileID) {
		return SavedFile{}, coded(CodeNoFile, nil)
	}
	w, err := s.writer(ctx, id)
	if err != nil {
		return SavedFile{}, err
	}
	rc := w.REST().WithHTTPClient(s.transfer)
	ictx, cancel := s.bounded(ctx)
	info, err := rc.FileInfo(ictx, fileID)
	cancel()
	if err != nil {
		return SavedFile{}, fileError(err)
	}
	dir, err := downloadsDir(s.getenv)
	if err != nil {
		return SavedFile{}, coded(CodeInternal, err)
	}
	key := fmt.Sprintf("%d/%s", id, fileID)
	path, ok := s.savedPath(key, info.Size)
	if !ok {
		dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
		defer cancel()
		if path, err = saveInto(dctx, rc, dir, info); err != nil {
			return SavedFile{}, fileError(err)
		}
		s.filesMu.Lock()
		s.saved[key] = path
		s.filesMu.Unlock()
		slog.Info("file saved", "srv", id, "file", fileID, "path", path)
	}
	res := SavedFile{Path: path}
	s.mu.Lock()
	openFn := s.fileOpen
	s.mu.Unlock()
	if open && openFn != nil && openable(path) {
		if err := openFn(path); err != nil {
			slog.Warn("could not open file", "path", path, "err", err)
		} else {
			res.Opened = true
		}
	}
	return res, nil
}

// savedPath: the file saved earlier this session, if it is still there
// unchanged (same size).
func (s *Service) savedPath(key string, size int64) (string, bool) {
	s.filesMu.Lock()
	p, ok := s.saved[key]
	s.filesMu.Unlock()
	if !ok {
		return "", false
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() != size {
		return "", false
	}
	return p, true
}

func fileError(err error) error {
	var re *rest.Error
	if errors.As(err, &re) && (re.Status == http.StatusNotFound || re.Status == http.StatusBadRequest) {
		return coded(CodeNoFile, err)
	}
	return actionError(err)
}

// downloadsDir: SPK_MATTERMOST_DOWNLOADS (tests, e2e), else the XDG
// download directory (localized, e.g. ~/Загрузки), else ~/Downloads.
func downloadsDir(getenv func(string) string) (string, error) {
	if d := getenv("SPK_MATTERMOST_DOWNLOADS"); d != "" {
		return d, nil
	}
	if d := xdg.UserDirs.Download; d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Downloads"), nil
}

func saveInto(ctx context.Context, rc *rest.Client, dir string, info model.FileInfo) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	resp, err := rc.Stream(ctx, "/api/v4/files/"+url.PathEscape(info.ID), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, path, err := createUnique(dir, sanitizeName(info.Name))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// createUnique creates name in dir, or "stem (N).ext" if taken; O_EXCL
// makes the choice race-free.
func createUnique(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, n)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, p, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("no free file name")
}

// sanitizeName keeps a server-supplied file name inside the downloads
// directory and valid on every OS: the last path element only, no control
// or reserved characters, not empty, "." or "..", at most 200 bytes.
func sanitizeName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	for len(name) > 200 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}
```

`internal/api/service.go`: в `Service` после `notifier Notifier` — `fileOpen Opener // hands saved files to the system; nil: save only`; перед `mu sync.Mutex` —
```go
	filesMu sync.Mutex
	saved   map[string]string // "server/file id" → path saved this session
```
в `NewService` — `saved: map[string]string{},`.

`internal/api/api.go`: в интерфейс `API`:
```go
	DownloadFile(ctx context.Context, id int64, fileID string) (SavedFile, error)
	OpenFile(ctx context.Context, id int64, fileID string) (SavedFile, error)
```
в коды — `CodeNoFile = "no_file"`.

- [ ] **Step 4: Реализация — транспорты и запуск**

`internal/api/transport/http.go`, в `routes()` перед `/api/events`:
```go
	type fileReq struct {
		ID     int64  `json:"id"`
		FileID string `json:"file_id"`
	}
	h.mux.HandleFunc("POST /api/DownloadFile", handle(func(ctx context.Context, r *fileReq) (any, error) {
		return h.api.DownloadFile(ctx, r.ID, r.FileID)
	}))
	h.mux.HandleFunc("POST /api/OpenFile", handle(func(ctx context.Context, r *fileReq) (any, error) {
		return h.api.OpenFile(ctx, r.ID, r.FileID)
	}))
```

`internal/api/transport/wails.go`:
```go
func (w *API) DownloadFile(id int64, fileID string) (api.SavedFile, error) {
	return w.a.DownloadFile(context.Background(), id, fileID)
}
func (w *API) OpenFile(id int64, fileID string) (api.SavedFile, error) {
	return w.a.OpenFile(context.Background(), id, fileID)
}
```

`cmd/spk-mattermost/browser.go`:
- в `buildBrowserServer` после `svc.SetNotifier(notes)`:
  ```go
  	opened := &api.RecordingOpener{} // no system apps in browser mode; e2e reads them via test-API
  	svc.SetFileOpener(opened.Open)
  ```
  и вызов `newBrowserHandler(svc, em, frontendFS(), fake, o.TestAPI, notes, mc, opened)`;
- сигнатура `newBrowserHandler(…, mediaH http.Handler, opened *api.RecordingOpener)`; в test-API после `notifications`:
  ```go
  		tm.HandleFunc("GET /api/_test/opened-files", func(w http.ResponseWriter, _ *http.Request) {
  			writeJSON(w, http.StatusOK, opened.List())
  		})
  ```

`cmd/spk-mattermost/browser_test.go`, в `setupWithService` — `h, token := newBrowserHandler(svc, em, dist, fake, testAPI, notes, mc, &api.RecordingOpener{})`.

`cmd/spk-mattermost/run_desktop_wails.go`, после `svc := api.NewService(...)`:
```go
	// xdg-open (Wails Browser.OpenFile) starts and returns at once.
	svc.SetFileOpener(func(path string) error { return application.Get().Browser.OpenFile(path) })
```

Run: `go mod tidy && go test -race ./... && make build-desktop && make cross-check`
Expected: `github.com/adrg/xdg` — в прямом `require`; всё PASS и собирается.

- [ ] **Step 5: Линт**

Run: `go vet ./... && golangci-lint run && golangci-lint run --build-tags "wails gtk3"`
Expected: без замечаний.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/state internal/api cmd/spk-mattermost
git commit -m "api: download files into the XDG downloads directory with safe unique names, open them with the system app except launchers; file views carry id, size and dimensions"
```

### Task 8: UI — превью картинок, текстовые фрагменты, карточки файлов, просмотрщик

**Files:**
- Create: `frontend/src/components/files.ts`, `frontend/src/components/files.test.ts`
- Create: `frontend/src/components/FileCard.tsx`, `frontend/src/components/textFile.ts`, `frontend/src/components/TextSnippet.tsx`, `frontend/src/components/Attachments.tsx`, `frontend/src/components/Attachments.test.tsx`
- Create: `frontend/src/components/Viewer.tsx`, `frontend/src/components/Viewer.test.tsx`
- Modify: `frontend/src/api/types.ts` (`FileView`, `SavedFile`), `frontend/src/api/client.ts` (+ `downloadFile`, `openFile`), `frontend/src/api/client.test.ts`
- Modify: `frontend/src/store.ts` (`notice`), `frontend/src/App.tsx` (плашка), `frontend/src/chat.ts` (+ `downloadFile`, `openFile`), `frontend/src/chat.test.ts`
- Modify: `frontend/src/components/PostItem.tsx` (`Attachments`, `PostActions.view/download/open`), `frontend/src/components/PostItem.test.tsx`, `frontend/src/components/Feed.test.tsx`, `frontend/src/components/ChannelPane.tsx` (просмотрщик, действия)
- Modify: `frontend/src/i18n.ts`

**Interfaces:**
- Consumes: Task 4 (`/media/{srv}/thumb|feed|full|text/{id}`), Task 6 (`mediaURL`), Task 7 (`FileView` с `id, ext, width, height, has_preview`; `DownloadFile`/`OpenFile` → `SavedFile`).
- Produces:
  - `files.ts`: `type FileKind = 'image' | 'text' | 'other'`; `fileKind(f)`, `imageSrc(f): 'preview' | 'file' | null`, `fitBox(w, h, maxW, maxH): { width, height }`, `TEXT_EXT`, `GIF_INLINE_MAX = 8 MiB`, `IMAGE_FILE_MAX = 25 MiB`.
  - `FileCard.tsx`: `interface FileHandlers { onView(f: FileView): void; onDownload(f: FileView): void; onOpen(f: FileView): void }`, `FileCard({ file, onDownload, onOpen })`, `IconButton({ label, onClick, children })`.
  - `textFile.ts`: `type TextState`, `useTextFile(url): TextState`.
  - `Attachments({ serverId, files, onView, onDownload, onOpen })`; `TextSnippet({ serverId, file, … })`; `Viewer({ serverId, files, index, onIndex, onClose, onDownload, onOpen })`.
  - `PostActions` += `view(post: PostView, fileId: string): void`, `download(file: FileView): void`, `open(file: FileView): void`.
  - `client`: `downloadFile(id, fileId): Promise<SavedFile>`, `openFile(id, fileId): Promise<SavedFile>`; `chat.ts`: `downloadFile(serverId, fileId)`, `openFile(serverId, fileId)`; store: `notice: string | null`, `setNotice(msg)`.

Правила показа (п. 3–4 «Архитектуры»): одна картинка в посте — рамка, вписанная в 480×360 по `width/height` (неизвестные — 240×180, `object-contain`), источник `feed`; две и больше — миниатюры 120×100 (`thumb`, `object-cover`). GIF до 8 МиБ — оригиналом (анимация), больше — карточкой; SVG и прочие не-растровые — карточка. Текстовые файлы — фрагмент фиксированной высоты 8 строк (`h-40`), «Развернуть» — до `max-h-96` с прокруткой, «⤢» — просмотрщик; ошибка загрузки текста (415 — бинарный или не UTF-8) — карточка. Просмотрщик: картинки (`full`) и тексты поста, `Esc` — закрыть, `←/→` — соседний файл (по кругу), фокус на «Закрыть» при открытии и обратно на исходный элемент при закрытии, клик по фону — закрыть. Markdown-файлы (`.md`) — карточка (рендер превью — бэклог).

- [ ] **Step 1: Падающие тесты**

`frontend/src/components/files.test.ts`:
```ts
import type { FileView } from '../api/types'
import { fileKind, fitBox, imageSrc } from './files'

const F = (o: Partial<FileView>): FileView => ({ id: 'f', name: 'x', size: 100, mime: '', ...o })

test('what kind of preview a file gets', () => {
  expect(fileKind(F({ name: 'a.png', ext: 'png', mime: 'image/png', has_preview: true }))).toBe('image')
  expect(fileKind(F({ name: 'a.svg', ext: 'svg', mime: 'image/svg+xml' }))).toBe('other')
  expect(fileKind(F({ name: 'a.gif', ext: 'gif', mime: 'image/gif', size: 1_000_000 }))).toBe('image')
  expect(fileKind(F({ name: 'a.gif', ext: 'gif', mime: 'image/gif', size: 20_000_000 }))).toBe('other')
  expect(fileKind(F({ name: 'a.tiff', ext: 'tiff', mime: 'image/tiff', has_preview: true }))).toBe('image')
  expect(fileKind(F({ name: 'a.tiff', ext: 'tiff', mime: 'image/tiff' }))).toBe('other')
  expect(fileKind(F({ name: 'server.log', ext: 'log', mime: 'text/plain' }))).toBe('text')
  expect(fileKind(F({ name: 'main.go', ext: 'go', mime: 'application/octet-stream' }))).toBe('text')
  expect(fileKind(F({ name: 'Makefile' }))).toBe('text')
  expect(fileKind(F({ name: 'notes.md', ext: 'md', mime: 'text/markdown' }))).toBe('other')
  expect(fileKind(F({ name: 'spec.pdf', ext: 'pdf', mime: 'application/pdf' }))).toBe('other')
})

test('image source: preview, the original when there is none, nothing when too big', () => {
  expect(imageSrc(F({ mime: 'image/jpeg', has_preview: true }))).toBe('preview')
  expect(imageSrc(F({ mime: 'image/png', size: 10_000 }))).toBe('file')
  expect(imageSrc(F({ mime: 'image/png', size: 30_000_000 }))).toBeNull()
  expect(imageSrc(F({ mime: 'image/gif', size: 10_000, has_preview: false }))).toBe('file')
  expect(imageSrc(F({ mime: 'image/svg+xml' }))).toBeNull()
})

test('fitBox keeps the aspect inside the box; unknown size gets a fixed box', () => {
  expect(fitBox(1280, 720, 480, 360)).toEqual({ width: 480, height: 270 })
  expect(fitBox(400, 600, 480, 360)).toEqual({ width: 240, height: 360 })
  expect(fitBox(100, 50, 480, 360)).toEqual({ width: 100, height: 50 })
  expect(fitBox(undefined, undefined, 480, 360)).toEqual({ width: 240, height: 180 })
})
```

`frontend/src/components/Attachments.test.tsx`:
```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { Attachments } from './Attachments'

const handlers = () => ({ onView: vi.fn(), onDownload: vi.fn(), onOpen: vi.fn() })
const png = (o: Partial<FileView> = {}): FileView => ({
  id: 'f-build', name: 'build.png', ext: 'png', size: 5000, mime: 'image/png', width: 1280, height: 720, has_preview: true, ...o,
})

beforeEach(() => setLocale('en'))
afterEach(() => vi.unstubAllGlobals())

test('the image box has its final size before the image loads', async () => {
  const h = handlers()
  const { container } = render(<Attachments serverId={1} files={[png()]} {...h} />)
  const img = container.querySelector('img')!
  expect(img).toHaveAttribute('src', '/media/1/feed/f-build?src=preview')
  expect(img).toHaveAttribute('width', '480')
  expect(img).toHaveAttribute('height', '270')
  expect(img).toHaveAttribute('loading', 'lazy')
  expect(img).toHaveAttribute('alt', 'build.png')
  const box = screen.getByRole('button', { name: 'View build.png' })
  expect(box).toHaveStyle({ width: '480px', height: '270px' })
  await userEvent.click(box)
  expect(h.onView).toHaveBeenCalledWith(png())
})

test('several images are thumbnails; a failed one becomes a card', async () => {
  const h = handlers()
  const { container } = render(
    <Attachments serverId={1} files={[png({ id: 'a', name: 'a.png' }), png({ id: 'b', name: 'b.png', width: undefined, height: undefined })]} {...h} />,
  )
  const imgs = container.querySelectorAll('img')
  expect([...imgs].map((i) => i.getAttribute('src'))).toEqual(['/media/1/thumb/a', '/media/1/thumb/b'])
  expect(imgs[1]).toHaveAttribute('width', '120')
  expect(imgs[1]).toHaveAttribute('height', '100')
  fireEvent.error(imgs[1])
  await userEvent.click(screen.getByRole('button', { name: 'Download b.png' }))
  expect(h.onDownload).toHaveBeenCalledWith(expect.objectContaining({ id: 'b' }))
})

test('text files: a fixed-height snippet, expand, view; unreadable text falls back to a card', async () => {
  const fetchMock = vi.fn(async (url: string) =>
    url.endsWith('/text/f-log')
      ? new Response('line 1\nline 2\n', { headers: { 'X-Truncated': '1' } })
      : new Response('', { status: 415 }),
  )
  vi.stubGlobal('fetch', fetchMock)
  const h = handlers()
  const log: FileView = { id: 'f-log', name: 'server.log', ext: 'log', size: 3000, mime: 'text/plain' }
  const bin: FileView = { id: 'f-bin', name: 'weird.txt', ext: 'txt', size: 10, mime: 'text/plain' }
  const { container } = render(<Attachments serverId={1} files={[log, bin]} {...h} />)
  expect(container.querySelector('pre')).toHaveClass('h-40')
  expect(await screen.findByText(/line 1/)).toBeInTheDocument()
  expect(container.querySelector('pre')).toHaveClass('h-40') // the height does not follow the content
  const expand = screen.getByRole('button', { name: 'Expand' })
  await userEvent.click(expand)
  expect(expand).toHaveAttribute('aria-expanded', 'true')
  await userEvent.click(screen.getByRole('button', { name: 'View server.log' }))
  expect(h.onView).toHaveBeenCalledWith(log)
  expect(await screen.findByRole('button', { name: 'Open weird.txt' })).toBeInTheDocument()
  expect(fetchMock).toHaveBeenCalledWith('/media/1/text/f-log', expect.objectContaining({ credentials: 'same-origin' }))
})

test('other files are cards with download and open', async () => {
  const h = handlers()
  const pdf: FileView = { id: 'f-spec', name: 'spec.pdf', ext: 'pdf', size: 2048, mime: 'application/pdf' }
  render(<Attachments serverId={1} files={[pdf, { id: 's', name: 'logo.svg', ext: 'svg', size: 10, mime: 'image/svg+xml' }]} {...h} />)
  expect(screen.getByText('2.0 KB')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Open spec.pdf' }))
  expect(h.onOpen).toHaveBeenCalledWith(pdf)
  expect(screen.getByRole('button', { name: 'Download logo.svg' })).toBeInTheDocument()
  expect(document.querySelector('img')).toBeNull()
})
```

`frontend/src/components/Viewer.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { Viewer } from './Viewer'

const img: FileView = { id: 'f-build', name: 'build.png', ext: 'png', size: 5000, mime: 'image/png', width: 1280, height: 720, has_preview: true }
const log: FileView = { id: 'f-log', name: 'server.log', ext: 'log', size: 70000, mime: 'text/plain' }
const noop = () => {}

beforeEach(() => setLocale('en'))
afterEach(() => vi.unstubAllGlobals())

test('full image, keyboard navigation around the post, Escape closes', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('hello log', { headers: { 'X-Truncated': '1' } })))
  const onIndex = vi.fn()
  const onClose = vi.fn()
  const { rerender } = render(<Viewer serverId={1} files={[img, log]} index={0} onIndex={onIndex} onClose={onClose} onDownload={noop} onOpen={noop} />)
  const dialog = screen.getByRole('dialog', { name: 'File viewer' })
  expect(screen.getByRole('img', { name: 'build.png' })).toHaveAttribute('src', '/media/1/full/f-build?src=preview')
  expect(dialog).toHaveTextContent('1 of 2')
  expect(screen.getByRole('button', { name: 'Close' })).toHaveFocus()
  await userEvent.keyboard('{ArrowRight}')
  expect(onIndex).toHaveBeenLastCalledWith(1)
  await userEvent.keyboard('{ArrowLeft}')
  expect(onIndex).toHaveBeenLastCalledWith(1) // wraps around
  rerender(<Viewer serverId={1} files={[img, log]} index={1} onIndex={onIndex} onClose={onClose} onDownload={noop} onOpen={noop} />)
  expect(await screen.findByText('hello log')).toBeInTheDocument()
  expect(screen.getByText('Showing the first 64 KB')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalled()
})

test('download and open act on the shown file; focus returns to the opener', async () => {
  const onDownload = vi.fn()
  function Host({ open }: { open: boolean }) {
    return (
      <>
        <button>opener</button>
        {open && <Viewer serverId={1} files={[img]} index={0} onIndex={noop} onClose={noop} onDownload={onDownload} onOpen={noop} />}
      </>
    )
  }
  const { rerender } = render(<Host open={false} />)
  screen.getByRole('button', { name: 'opener' }).focus()
  rerender(<Host open />)
  expect(screen.queryByText('1 of 1')).toBeNull()
  await userEvent.click(screen.getByRole('button', { name: 'Download' }))
  expect(onDownload).toHaveBeenCalledWith(img)
  rerender(<Host open={false} />)
  expect(screen.getByRole('button', { name: 'opener' })).toHaveFocus()
})
```

`frontend/src/chat.test.ts`: в `vi.mock('./api/client', …)` в объект `client` добавить `downloadFile: vi.fn(), openFile: vi.fn(),`; импорт `downloadFile, openFile` из `./chat` (в строку `const { … } = await import('./chat')`) и `import { setLocale } from './i18n'`; тест:
```ts
test('a download says where the file went; an unopened one says why', async () => {
  setLocale('en')
  vi.mocked(client.downloadFile).mockResolvedValue({ path: '/home/a/Downloads/spec.pdf', opened: false })
  await downloadFile(1, 'f-spec')
  expect(useStore.getState().notice).toBe('Saved to /home/a/Downloads/spec.pdf')
  vi.mocked(client.openFile).mockResolvedValue({ path: '/d/run.desktop', opened: false })
  await openFile(1, 'f-x')
  expect(useStore.getState().notice).toBe('Saved to /d/run.desktop. Files of this type are not opened automatically.')
  useStore.getState().setNotice(null)
  vi.mocked(client.openFile).mockResolvedValue({ path: '/d/a.log', opened: true })
  await openFile(1, 'f-log')
  expect(useStore.getState().notice).toBeNull()
})
```

`frontend/src/api/client.test.ts`, тест «http client chat methods post snake_case bodies»: после `await httpClient.sidebar(3, '')` — `await httpClient.downloadFile(3, 'f1')`, в ожидание — `['/api/DownloadFile', { id: 3, file_id: 'f1' }]`.

`frontend/src/components/PostItem.test.tsx`: в фабрику `actions()` добавить `view: vi.fn(), download: vi.fn(), open: vi.fn(),`; в тесте «attachments, files, reactions and reply count» файл — `{ id: 'f1', name: 'report.pdf', size: 2048, mime: 'application/pdf' }`.

`frontend/src/components/Feed.test.tsx`: в `actions` внутри `props(...)` добавить `view: vi.fn(), download: vi.fn(), open: vi.fn(),`.

Run: `cd frontend && pnpm test`
Expected: FAIL — нет модулей `./files`, `./Attachments`, `./Viewer`; `downloadFile` не экспортирован.

- [ ] **Step 2: Реализация — правила и мелкие компоненты**

`frontend/src/components/files.ts`:
```ts
import type { FileView } from '../api/types'

export type FileKind = 'image' | 'text' | 'other'

const RASTER = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/bmp'])

// Text files previewed inline. Markdown is not here on purpose: rendered
// .md previews are in the backlog; until then .md is a card.
export const TEXT_EXT = new Set([
  'txt', 'log', 'csv', 'tsv', 'json', 'yaml', 'yml', 'xml', 'toml', 'ini', 'conf', 'cfg', 'env', 'properties', 'sql',
  'sh', 'bash', 'zsh', 'ps1', 'bat', 'go', 'py', 'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx', 'java', 'kt', 'kts', 'gradle',
  'groovy', 'scala', 'c', 'h', 'cc', 'cpp', 'hpp', 'cs', 'rs', 'rb', 'php', 'swift', 'lua', 'pl', 'r', 'css', 'scss',
  'less', 'html', 'htm', 'vue', 'svelte', 'diff', 'patch', 'proto', 'graphql', 'tf', 'hcl', 'dockerfile', 'makefile',
  'gitignore', 'editorconfig',
])

export const GIF_INLINE_MAX = 8 * 1024 * 1024
export const IMAGE_FILE_MAX = 25 * 1024 * 1024

const extOf = (f: FileView) => (f.ext || (f.name.includes('.') ? f.name.slice(f.name.lastIndexOf('.') + 1) : f.name)).toLowerCase()

// imageSrc: what the feed and the viewer load for an image — the server
// preview, or the original when there is none (GIF keeps its animation;
// small images the server made no preview for). null: shown as a card.
export function imageSrc(f: FileView): 'preview' | 'file' | null {
  const mime = (f.mime || '').toLowerCase()
  if (!mime.startsWith('image/') || mime === 'image/svg+xml') return null
  if (mime === 'image/gif') return f.size <= GIF_INLINE_MAX ? 'file' : null
  if (f.has_preview) return 'preview'
  return RASTER.has(mime) && f.size <= IMAGE_FILE_MAX ? 'file' : null
}

export function fileKind(f: FileView): FileKind {
  if (imageSrc(f)) return 'image'
  const ext = extOf(f)
  const mime = (f.mime || '').toLowerCase()
  if (ext === 'md' || ext === 'markdown' || mime === 'text/markdown') return 'other'
  if (TEXT_EXT.has(ext) || mime === 'text/plain') return 'text'
  return 'other'
}

// fitBox: the box an image is shown in — known before it loads, so the
// feed never shifts. Unknown dimensions get a fixed box (object-contain).
export function fitBox(w: number | undefined, h: number | undefined, maxW: number, maxH: number) {
  if (!w || !h) return { width: Math.min(240, maxW), height: Math.min(180, maxH) }
  const s = Math.min(1, maxW / w, maxH / h)
  return { width: Math.max(1, Math.round(w * s)), height: Math.max(1, Math.round(h * s)) }
}
```

`frontend/src/components/FileCard.tsx`:
```tsx
import type { ReactNode } from 'react'
import type { FileView } from '../api/types'
import { formatSize } from '../format'
import { t } from '../i18n'

export interface FileHandlers {
  onView(file: FileView): void
  onDownload(file: FileView): void
  onOpen(file: FileView): void
}

export function IconButton({ label, onClick, children }: { label: string; onClick(): void; children: ReactNode }) {
  return (
    <button type="button" aria-label={label} title={label} onClick={onClick} className="rounded px-1 text-fg-muted hover:bg-hover hover:text-fg">
      {children}
    </button>
  )
}

export function FileCard({ file, onDownload, onOpen }: { file: FileView } & Pick<FileHandlers, 'onDownload' | 'onOpen'>) {
  return (
    <div className="flex max-w-sm items-center gap-2 rounded border border-line px-2 py-1 text-xs">
      <span aria-hidden>📎</span>
      <span className="min-w-0 truncate" title={file.name}>
        {file.name}
      </span>
      <span className="shrink-0 text-fg-muted">{formatSize(file.size)}</span>
      <IconButton label={t('file.download', { name: file.name })} onClick={() => onDownload(file)}>
        ⬇
      </IconButton>
      <IconButton label={t('file.open', { name: file.name })} onClick={() => onOpen(file)}>
        ↗
      </IconButton>
    </div>
  )
}
```

`frontend/src/components/textFile.ts`:
```ts
import { useEffect, useState } from 'react'

export type TextState = { status: 'loading' } | { status: 'error' } | { status: 'ok'; text: string; truncated: boolean }

// useTextFile loads a text snippet from /media/…/text/<id> (same origin; in
// browser mode the page cookie authorizes it). The response is immutable,
// so the viewer re-reading the same URL hits the webview cache.
export function useTextFile(url: string): TextState {
  const [res, setRes] = useState<{ url: string; state: TextState }>({ url, state: { status: 'loading' } })
  useEffect(() => {
    const ac = new AbortController()
    fetch(url, { signal: ac.signal, credentials: 'same-origin' })
      .then(async (r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return { status: 'ok' as const, text: await r.text(), truncated: r.headers.get('X-Truncated') === '1' }
      })
      .then(
        (state) => setRes({ url, state }),
        () => {
          if (!ac.signal.aborted) setRes({ url, state: { status: 'error' } })
        },
      )
    return () => ac.abort()
  }, [url])
  return res.url === url ? res.state : { status: 'loading' }
}
```

`frontend/src/components/TextSnippet.tsx`:
```tsx
import { useState } from 'react'
import type { FileView } from '../api/types'
import { formatSize } from '../format'
import { t } from '../i18n'
import { mediaURL } from '../media'
import { FileCard, IconButton, type FileHandlers } from './FileCard'
import { useTextFile } from './textFile'

// The collapsed snippet is 8 lines high whether loaded or not: the feed
// row never changes size by itself (only when the user expands it).
export function TextSnippet({ serverId, file, onView, onDownload, onOpen }: { serverId: number; file: FileView } & FileHandlers) {
  const res = useTextFile(mediaURL(serverId, 'text', file.id))
  const [open, setOpen] = useState(false)
  if (res.status === 'error') return <FileCard file={file} onDownload={onDownload} onOpen={onOpen} />
  return (
    <figure className="w-full max-w-3xl overflow-hidden rounded border border-line bg-code-bg text-xs">
      <figcaption className="flex items-center gap-2 border-b border-line px-2 py-1">
        <span aria-hidden>📄</span>
        <span className="min-w-0 truncate font-medium" title={file.name}>
          {file.name}
        </span>
        <span className="shrink-0 text-fg-muted">{formatSize(file.size)}</span>
        <span className="ml-auto flex shrink-0 items-center gap-1">
          <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="rounded px-1.5 text-fg-muted hover:bg-hover hover:text-fg">
            {t(open ? 'file.collapse' : 'file.expand')}
          </button>
          <IconButton label={t('file.view', { name: file.name })} onClick={() => onView(file)}>
            ⤢
          </IconButton>
          <IconButton label={t('file.download', { name: file.name })} onClick={() => onDownload(file)}>
            ⬇
          </IconButton>
          <IconButton label={t('file.open', { name: file.name })} onClick={() => onOpen(file)}>
            ↗
          </IconButton>
        </span>
      </figcaption>
      <pre className={`overflow-x-auto px-2 py-1 font-mono leading-5 text-fg ${open ? 'max-h-96 overflow-y-auto' : 'h-40 overflow-y-hidden'}`}>
        {res.status === 'loading' ? <span className="text-fg-muted">{t('file.loading')}</span> : res.text}
      </pre>
    </figure>
  )
}
```

- [ ] **Step 3: Реализация — вложения и просмотрщик**

`frontend/src/components/Attachments.tsx`:
```tsx
import { useState } from 'react'
import type { FileView } from '../api/types'
import { t } from '../i18n'
import { mediaURL } from '../media'
import { FileCard, type FileHandlers } from './FileCard'
import { fileKind, fitBox, imageSrc } from './files'
import { TextSnippet } from './TextSnippet'

const BIG = { w: 480, h: 360 }
const THUMB = { w: 120, h: 100 }

function ImageTile({ serverId, file, big, onView, onDownload, onOpen }: { serverId: number; file: FileView; big: boolean } & FileHandlers) {
  const [failed, setFailed] = useState(false)
  const src = imageSrc(file)
  if (failed || !src) return <FileCard file={file} onDownload={onDownload} onOpen={onOpen} />
  const box = big ? fitBox(file.width, file.height, BIG.w, BIG.h) : { width: THUMB.w, height: THUMB.h }
  const url = big ? mediaURL(serverId, 'feed', file.id, { src }) : mediaURL(serverId, 'thumb', file.id)
  return (
    <button
      type="button"
      aria-label={t('file.view', { name: file.name })}
      title={file.name}
      onClick={() => onView(file)}
      className="block shrink-0 overflow-hidden rounded border border-line bg-panel focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
      style={{ width: box.width, height: box.height }}
    >
      <img
        src={url}
        alt={file.name}
        width={box.width}
        height={box.height}
        loading="lazy"
        decoding="async"
        draggable={false}
        onError={() => setFailed(true)}
        className={`block h-full w-full ${big ? 'object-contain' : 'object-cover'}`}
      />
    </button>
  )
}

// Attachments: one image big, several as thumbnails, text files as
// snippets, everything else as cards. Every box has its size up front.
export function Attachments({ serverId, files, ...h }: { serverId: number; files: FileView[] } & FileHandlers) {
  const images = files.filter((f) => fileKind(f) === 'image')
  const texts = files.filter((f) => fileKind(f) === 'text')
  const others = files.filter((f) => fileKind(f) === 'other')
  return (
    <div className="mt-1 flex flex-col items-start gap-2">
      {images.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {images.map((f) => (
            <ImageTile key={f.id} serverId={serverId} file={f} big={images.length === 1} {...h} />
          ))}
        </div>
      )}
      {texts.map((f) => (
        <TextSnippet key={f.id} serverId={serverId} file={f} {...h} />
      ))}
      {others.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {others.map((f) => (
            <FileCard key={f.id} file={f} onDownload={h.onDownload} onOpen={h.onOpen} />
          ))}
        </div>
      )}
    </div>
  )
}
```

`frontend/src/components/Viewer.tsx`:
```tsx
import { useEffect, useRef } from 'react'
import type { FileView } from '../api/types'
import { formatSize } from '../format'
import { t } from '../i18n'
import { mediaURL } from '../media'
import { fileKind, imageSrc } from './files'
import { useTextFile } from './textFile'

interface Props {
  serverId: number
  files: FileView[] // the post's previewable files (images and texts)
  index: number
  onIndex(i: number): void
  onClose(): void
  onDownload(file: FileView): void
  onOpen(file: FileView): void
}

function FullText({ serverId, file }: { serverId: number; file: FileView }) {
  const res = useTextFile(mediaURL(serverId, 'text', file.id))
  return (
    <div className="flex h-full w-full max-w-5xl flex-col gap-1">
      {res.status === 'ok' && res.truncated && <p className="text-xs text-fg-muted">{t('file.truncated')}</p>}
      <pre className="min-h-0 flex-1 overflow-auto rounded bg-code-bg p-3 font-mono text-xs leading-5 text-fg">
        {res.status === 'ok' ? res.text : res.status === 'loading' ? t('file.loading') : t('err.no_file')}
      </pre>
    </div>
  )
}

// Viewer is a modal over the app: Escape closes, ←/→ go through the post's
// files (wrapping), focus starts on Close and returns to where it was.
export function Viewer({ serverId, files, index, onIndex, onClose, onDownload, onOpen }: Props) {
  const closeRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
    closeRef.current?.focus()
    return () => opener?.focus()
  }, [])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      } else if (e.key === 'ArrowRight' && files.length > 1) {
        e.preventDefault()
        onIndex((index + 1) % files.length)
      } else if (e.key === 'ArrowLeft' && files.length > 1) {
        e.preventDefault()
        onIndex((index - 1 + files.length) % files.length)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [files.length, index, onClose, onIndex])
  const file = files[index]
  if (!file) return null
  const src = imageSrc(file)
  const closeOnBackdrop = (e: React.MouseEvent) => {
    if (e.target === e.currentTarget) onClose()
  }
  const nav = 'absolute top-1/2 -translate-y-1/2 rounded-full bg-black/50 px-3 py-1 text-2xl text-white hover:bg-black/70'
  return (
    <div role="dialog" aria-modal="true" aria-label={t('viewer.label')} className="fixed inset-0 z-50 flex flex-col bg-black/85 text-fg" onClick={closeOnBackdrop}>
      <header className="flex items-center gap-3 px-4 py-2 text-sm">
        <span className="min-w-0 truncate font-medium">{file.name}</span>
        <span className="shrink-0 text-xs text-fg-muted">{formatSize(file.size)}</span>
        {files.length > 1 && (
          <span className="shrink-0 text-xs text-fg-muted">{t('viewer.counter', { i: String(index + 1), n: String(files.length) })}</span>
        )}
        <span className="ml-auto flex shrink-0 gap-2">
          <button type="button" className="rounded px-2 py-0.5 hover:bg-hover" onClick={() => onDownload(file)}>
            {t('viewer.download')}
          </button>
          <button type="button" className="rounded px-2 py-0.5 hover:bg-hover" onClick={() => onOpen(file)}>
            {t('viewer.open')}
          </button>
          <button ref={closeRef} type="button" aria-label={t('viewer.close')} title={t('viewer.close')} className="rounded px-2 py-0.5 hover:bg-hover" onClick={onClose}>
            ✕
          </button>
        </span>
      </header>
      <div className="relative flex min-h-0 flex-1 items-center justify-center p-4" onClick={closeOnBackdrop}>
        {files.length > 1 && (
          <button type="button" aria-label={t('viewer.prev')} className={`${nav} left-3`} onClick={() => onIndex((index - 1 + files.length) % files.length)}>
            ‹
          </button>
        )}
        {fileKind(file) === 'image' && src ? (
          <img key={file.id} src={mediaURL(serverId, 'full', file.id, { src })} alt={file.name} className="max-h-full max-w-full object-contain" />
        ) : (
          <FullText key={file.id} serverId={serverId} file={file} />
        )}
        {files.length > 1 && (
          <button type="button" aria-label={t('viewer.next')} className={`${nav} right-3`} onClick={() => onIndex((index + 1) % files.length)}>
            ›
          </button>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Реализация — клиент, store, чат, пост, канал, i18n**

`frontend/src/api/types.ts`: `FileView` заменить на
```ts
export interface FileView {
  id: string
  name: string
  ext?: string
  size: number
  mime: string
  width?: number
  height?: number
  has_preview?: boolean
}

export interface SavedFile {
  path: string
  opened: boolean
}
```

`frontend/src/api/client.ts`: импорт `SavedFile`; в `Client` — `downloadFile(id: number, fileId: string): Promise<SavedFile>` и `openFile(id: number, fileId: string): Promise<SavedFile>`; в `httpClient` — `downloadFile: (id, file_id) => post('DownloadFile', { id, file_id }),`, `openFile: (id, file_id) => post('OpenFile', { id, file_id }),`; в `wailsClient` — `downloadFile: (id, fileId) => wcall('DownloadFile', id, fileId),`, `openFile: (id, fileId) => wcall('OpenFile', id, fileId),`.

`frontend/src/store.ts`: в `State` — `notice: string | null // info banner (e.g. where a download went)` и `setNotice(msg: string | null): void`; в начальное состояние — `notice: null,`; `setNotice: (msg) => set({ notice: msg }),`.

`frontend/src/chat.ts`: импорт `import { t } from './i18n'`; в конец:
```ts
export async function downloadFile(serverId: number, fileId: string) {
  try {
    const r = await client.downloadFile(serverId, fileId)
    useStore.getState().setNotice(t('file.saved', { path: r.path }))
  } catch (e) {
    report(e)
  }
}

// openFile saves and opens with the system app; Go refuses to open
// launchers (.desktop, scripts…) — then the user is told where the file is.
export async function openFile(serverId: number, fileId: string) {
  try {
    const r = await client.openFile(serverId, fileId)
    if (!r.opened) useStore.getState().setNotice(t('file.savedNotOpened', { path: r.path }))
  } catch (e) {
    report(e)
  }
}
```

`frontend/src/App.tsx`:
- из `useStore()` дополнительно взять `notice, setNotice`;
- после второго `useEffect`:
  ```tsx
    useEffect(() => {
      if (!notice) return
      const timer = setTimeout(() => {
        if (useStore.getState().notice === notice) setNotice(null)
      }, 8000)
      return () => clearTimeout(timer)
    }, [notice, setNotice])
  ```
- рядом с `banner`:
  ```tsx
    const info = notice && (
      <p role="status" className="flex items-center justify-between bg-hover px-4 py-2 text-sm text-fg">
        <span className="min-w-0 break-all">{notice}</span>
        <button aria-label={t('app.dismiss')} className="px-2" onClick={() => setNotice(null)}>
          ×
        </button>
      </p>
    )
  ```
- в чате: `<main …>{banner}{info}<ChannelPane …/></main>`.

`frontend/src/components/PostItem.tsx`:
- импорты: `import type { Attachment, FileView, PostView } from '../api/types'`, `import { Attachments } from './Attachments'`; `formatSize` больше не нужен — убрать из импорта;
- в `PostActions`:
  ```ts
    view(post: PostView, fileId: string): void
    download(file: FileView): void
    open(file: FileView): void
  ```
- блок `post.files` заменить на
  ```tsx
          {post.files && post.files.length > 0 && (
            <Attachments serverId={serverId} files={post.files} onView={(f) => actions.view(post, f.id)} onDownload={actions.download} onOpen={actions.open} />
          )}
  ```

`frontend/src/components/ChannelPane.tsx`:
- импорты: `useMemo, useState` из `react`; `FileView` из `../api/types`; `downloadFile, openFile` из `../chat`; `fileKind` из `./files`; `Viewer` из `./Viewer`;
- перед `const actions = useMemo…`:
  ```tsx
    // The viewer belongs to the channel it was opened in.
    const [viewer, setViewer] = useState<{ channelId: string; files: FileView[]; index: number } | null>(null)
  ```
- в `actions` (и в зависимости `useMemo` ничего нового — `setViewer` стабилен):
  ```tsx
        view: (p, fileId) => {
          const files = (p.files ?? []).filter((f) => fileKind(f) !== 'other')
          const index = files.findIndex((f) => f.id === fileId)
          if (index >= 0) setViewer({ channelId, files, index })
        },
        download: (f) => void downloadFile(server.id, f.id),
        open: (f) => void openFile(server.id, f.id),
  ```
- в конец `<section>` после `<Composer …/>`:
  ```tsx
        {viewer && viewer.channelId === channel.id && (
          <Viewer
            serverId={server.id}
            files={viewer.files}
            index={viewer.index}
            onIndex={(index) => setViewer({ ...viewer, index })}
            onClose={() => setViewer(null)}
            onDownload={(f) => void downloadFile(server.id, f.id)}
            onOpen={(f) => void openFile(server.id, f.id)}
          />
        )}
  ```

`frontend/src/i18n.ts` — в `ru`:
```ts
  'file.download': 'Скачать {name}',
  'file.open': 'Открыть {name}',
  'file.view': 'Просмотр: {name}',
  'file.expand': 'Развернуть',
  'file.collapse': 'Свернуть',
  'file.loading': 'Загрузка…',
  'file.truncated': 'Показаны первые 64 КБ',
  'file.saved': 'Сохранено: {path}',
  'file.savedNotOpened': 'Сохранено: {path}. Файлы этого типа не открываются автоматически.',
  'viewer.label': 'Просмотр файла',
  'viewer.close': 'Закрыть',
  'viewer.prev': 'Предыдущий файл',
  'viewer.next': 'Следующий файл',
  'viewer.counter': '{i} из {n}',
  'viewer.download': 'Скачать',
  'viewer.open': 'Открыть',
  'err.no_file': 'Файл не найден',
```
в `en`:
```ts
  'file.download': 'Download {name}',
  'file.open': 'Open {name}',
  'file.view': 'View {name}',
  'file.expand': 'Expand',
  'file.collapse': 'Collapse',
  'file.loading': 'Loading…',
  'file.truncated': 'Showing the first 64 KB',
  'file.saved': 'Saved to {path}',
  'file.savedNotOpened': 'Saved to {path}. Files of this type are not opened automatically.',
  'viewer.label': 'File viewer',
  'viewer.close': 'Close',
  'viewer.prev': 'Previous file',
  'viewer.next': 'Next file',
  'viewer.counter': '{i} of {n}',
  'viewer.download': 'Download',
  'viewer.open': 'Open',
  'err.no_file': 'File not found',
```

- [ ] **Step 5: Тесты, типы, линт**

Run: `cd frontend && pnpm test && pnpm lint && pnpm build`
Expected: всё PASS, сборка без ошибок.

- [ ] **Step 6: Скриншоты browser mode**

«Процедура скриншота», сценарий:
```js
await p.getByRole('button', { name: /Off-Topic/ }).click()
const log = p.getByRole('log', { name: 'Messages' })
await log.getByRole('img', { name: 'build.png' }).waitFor()
await p.waitForFunction(() => [...document.querySelectorAll('[aria-label="Messages"] img')].every((i) => i.complete))
await p.screenshot({ path: 'T/t8-feed.png' })
const shift = await log.evaluate((el) => { const a = el.scrollTop; return new Promise((r) => setTimeout(() => r(el.scrollTop - a), 500)) })
await log.getByRole('button', { name: 'View build.png' }).click()
await p.getByRole('dialog', { name: 'File viewer' }).waitFor()
await p.screenshot({ path: 'T/t8-viewer.png' })
await p.keyboard.press('Escape')
await log.getByRole('button', { name: 'View server.log' }).click()
await p.getByRole('dialog').getByText('request #60 handled', { exact: false }).waitFor()
await p.screenshot({ path: 'T/t8-text.png' })
await p.keyboard.press('Escape')
await log.getByRole('button', { name: 'Download spec.pdf' }).click()
await p.getByText(/Saved to .*spec\.pdf/).waitFor()
await p.screenshot({ path: 'T/t8-saved.png' })
const sizes = await log.evaluate((el) => [...el.querySelectorAll('img')].map((i) => `${i.alt} ${i.naturalWidth}x${i.naturalHeight}`))
return { shift, sizes }
```
Expected: `t8-feed.png` — одна крупная картинка 480×270 у «Build screenshot», две миниатюры у «Two diagrams», фрагмент `server.log` (8 строк, моноширинно), карточка `spec.pdf` с ⬇ и ↗; `shift` = 0 (лента не сдвинулась после загрузки картинок); `sizes` — `build.png 960x540` (уменьшения нет — уже ≤ 960), миниатюры ≤ 120×100. `t8-viewer.png` — полноэкранная картинка на тёмном фоне, сверху имя, размер, «Download», «Open», ✕. `t8-text.png` — лог целиком в просмотрщике. `t8-saved.png` — плашка «Saved to …/dl/spec.pdf»; файл есть: `ls $T/dl`. Процесс остановить, контексты закрыты.

- [ ] **Step 7: Commit**

```bash
git add frontend
git commit -m "ui: image previews and thumbnails with fixed boxes, text snippets, file cards with download/open, a keyboard file viewer"
```

### Task 9: Реакции — REST и фейк

**Files:**
- Modify: `internal/mm/rest/write.go`, `internal/mm/rest/write_test.go`
- Create: `internal/mmfake/reactions.go`, `internal/mmfake/reactions_test.go`
- Modify: `internal/mmfake/server.go` (тип `failures`, вызов `reactionRoutes`), `internal/mmfake/net.go` (`FailWith`), `internal/mmfake/seed.go` (реакции у «Welcome to off-topic»)
- Modify: `cmd/spk-mattermost/browser.go` (test-API `fake/react`)

**Interfaces:**
- Consumes: Task 2 (сид Off-Topic, `partyparrot`).
- Produces:
  - `func (c *rest.Client) SaveReaction(ctx, r model.Reaction) (model.Reaction, error)` — `POST /api/v4/reactions {user_id, post_id, emoji_name}`; `func (c *rest.Client) DeleteReaction(ctx, userID, postID, emoji string) error`.
  - Фейк: `POST /api/v4/reactions` (только от своего имени — иначе 403; имя по `^[a-zA-Z0-9+_-]{1,64}$` — иначе 400; архивный канал — 403 `api.reaction.save.archived_channel.app_error`), `DELETE /api/v4/users/{uid}/posts/{pid}/reactions/{name}`; изменение — в `metadata.reactions` поста, `update_at` растёт, событие `reaction_added`/`reaction_removed` членам канала; повтор существующего — без события.
  - Управление: `ReactAs(username, postID, emoji string)`, `UnreactAs(...)`, `Reactions(postID string) []model.Reaction`, `FindPost(channelID, message string) string`, `Preference(username, category, name string) string`, `FailWith(part string, status int, id string)`.
  - Сид: у «Welcome to off-topic» реакции `bob:+1`, `carol:+1`, `carol:tada`, `bob:partyparrot`.
  - test-API: `POST /api/_test/fake/react {channel_id, message, username, emoji, remove}` → `{post_id}`.

Факты — раздел 7.4. Фейк не проверяет, что имя — системное или существующее кастомное (реальный сервер ответит 404): клиент ставит только имена из набора эмодзи Mattermost и кастомные с сервера.

- [ ] **Step 1: Падающие тесты**

В `internal/mm/rest/write_test.go` (импорты + `"io"`):
```go
func TestReactionCalls(t *testing.T) {
	var got []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.EscapedPath()+" "+string(b))
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"user_id":"u1","post_id":"p1","emoji_name":"+1","create_at":5}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	})
	ctx := context.Background()
	r, err := c.SaveReaction(ctx, model.Reaction{UserID: "u1", PostID: "p1", EmojiName: "+1"})
	require.NoError(t, err)
	assert.Equal(t, int64(5), r.CreateAt)
	require.NoError(t, c.DeleteReaction(ctx, "u1", "p1", "+1"))
	assert.Equal(t, []string{
		`POST /api/v4/reactions {"emoji_name":"+1","post_id":"p1","user_id":"u1"}`,
		`DELETE /api/v4/users/u1/posts/p1/reactions/+1 `,
	}, got)
}
```

`internal/mmfake/reactions_test.go`:
```go
package mmfake

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

func reactionNames(s *Server, postID string) []string {
	var out []string
	for _, r := range s.Reactions(postID) {
		out = append(out, r.UserID+":"+r.EmojiName)
	}
	return out
}

func TestReactionsSaveDeleteAndEvents(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	id := s.FindPost("c-offtopic", "Welcome to off-topic")
	require.NotEmpty(t, id)
	assert.Equal(t, []string{"u-bob:+1", "u-carol:+1", "u-carol:tada", "u-bob:partyparrot"}, reactionNames(s, id))

	var r model.Reaction
	require.Equal(t, 200, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, &r))
	assert.Equal(t, "fire", r.EmojiName)
	assert.Contains(t, reactionNames(s, id), "u-alice:fire")
	evs := s.Events()
	assert.Equal(t, "reaction_added", evs[len(evs)-1].Name)
	assert.ElementsMatch(t, []string{"u-alice", "u-bob"}, evs[len(evs)-1].To)

	n := len(s.Events())
	require.Equal(t, 200, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, nil))
	assert.Len(t, s.Events(), n, "an existing reaction changes nothing")
	assert.Equal(t, 403, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-bob", "post_id": id, "emoji_name": "fire"}, nil), "only as yourself")
	assert.Equal(t, 400, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "bad name"}, nil))
	assert.Equal(t, 404, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": "nope", "emoji_name": "fire"}, nil))

	require.Equal(t, 200, a.call("DELETE", "/api/v4/users/u-alice/posts/"+id+"/reactions/fire", nil, nil))
	assert.NotContains(t, reactionNames(s, id), "u-alice:fire")
	evs = s.Events()
	assert.Equal(t, "reaction_removed", evs[len(evs)-1].Name)
	assert.Equal(t, 403, a.call("DELETE", "/api/v4/users/u-bob/posts/"+id+"/reactions/+1", nil, nil), "not someone else's")

	var list model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/channels/c-offtopic/posts?page=0&per_page=60", nil, &list))
	welcome := list.Posts[id]
	require.NotNil(t, welcome.Metadata)
	assert.Len(t, welcome.Metadata.Reactions, 4)
	assert.Greater(t, welcome.UpdateAt, welcome.CreateAt, "reactions bump update_at")
}

func TestReactAsPreferenceAndFailWith(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	id := s.FindPost("c-offtopic", "Welcome to off-topic")
	s.ReactAs("bob", id, "fire") // carol is not in Off-Topic: only members react
	assert.Contains(t, reactionNames(s, id), "u-bob:fire")
	s.UnreactAs("bob", id, "fire")
	assert.NotContains(t, reactionNames(s, id), "u-bob:fire")

	require.Equal(t, 200, a.call("PUT", "/api/v4/users/me/preferences",
		[]model.Preference{{Category: "recent_emojis", Name: "u-alice", Value: `[{"name":"+1","usageCount":1}]`}}, nil))
	assert.Equal(t, `[{"name":"+1","usageCount":1}]`, s.Preference("alice", "recent_emojis", "u-alice"))

	s.FailWith("/api/v4/reactions", 400, "app.reaction.save.save.too_many_reactions")
	var e map[string]any
	require.Equal(t, 400, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, &e))
	s.SetFailure("/api/v4/reactions", 0)
	require.Equal(t, 200, a.call("POST", "/api/v4/reactions", map[string]string{"user_id": "u-alice", "post_id": id, "emoji_name": "fire"}, nil))
}
```
(у `call` тело ответа декодируется только при статусе < 300 — поэтому id ошибки проверяет Task 10 через клиента.)

Run: `go test ./internal/mm/rest/ ./internal/mmfake/`
Expected: FAIL — `SaveReaction undefined`, `FindPost undefined`.

- [ ] **Step 2: Реализация — REST**

В конец `internal/mm/rest/write.go`:
```go
// SaveReaction adds a reaction as r.UserID (must be the session's user).
// Not retried on transport errors, like every POST; adding an existing
// reaction is harmless anyway.
func (c *Client) SaveReaction(ctx context.Context, r model.Reaction) (model.Reaction, error) {
	var out model.Reaction
	_, err := c.do(ctx, http.MethodPost, "/api/v4/reactions",
		map[string]string{"user_id": r.UserID, "post_id": r.PostID, "emoji_name": r.EmojiName}, &out)
	return out, err
}

func (c *Client) DeleteReaction(ctx context.Context, userID, postID, emoji string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/v4/users/"+url.PathEscape(userID)+"/posts/"+url.PathEscape(postID)+
		"/reactions/"+url.PathEscape(emoji), nil, nil)
	return err
}
```

- [ ] **Step 3: Реализация — фейк**

`internal/mmfake/reactions.go`:
```go
package mmfake

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

var emojiNameRe = regexp.MustCompile(`^[a-zA-Z0-9+_-]{1,64}$`)

func (s *Server) reactionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v4/reactions", s.handleAuthed(s.saveReaction))
	mux.HandleFunc("DELETE /api/v4/users/{uid}/posts/{pid}/reactions/{name}", s.handleAuthed(s.deleteReaction))
}

// reactLocked adds or removes userID's reaction like the server: the post's
// update_at moves and the channel gets the event. A no-op (adding what is
// there, removing what is not) changes nothing and sends nothing.
func (s *Server) reactLocked(userID, postID, name string, add bool) *apiErr {
	p := s.chat.byID[postID]
	if p == nil || p.DeleteAt != 0 {
		return &apiErr{404, "app.post.get.app_error"}
	}
	if !s.isMemberLocked(p.ChannelID, userID) {
		return &apiErr{403, "api.context.permissions.app_error"}
	}
	if s.chat.channels[p.ChannelID].DeleteAt != 0 {
		return &apiErr{403, "api.reaction.save.archived_channel.app_error"}
	}
	if !emojiNameRe.MatchString(name) {
		return &apiErr{400, "api.reaction.save_reaction.invalid.app_error"}
	}
	var list []model.Reaction
	var files []model.FileInfo
	if p.Metadata != nil {
		list, files = slices.Clone(p.Metadata.Reactions), p.Metadata.Files
	}
	i := slices.IndexFunc(list, func(r model.Reaction) bool { return r.UserID == userID && r.EmojiName == name })
	if add == (i >= 0) {
		return nil
	}
	now := s.nowLocked()
	r := model.Reaction{UserID: userID, PostID: postID, EmojiName: name, CreateAt: now}
	if add {
		list = append(list, r)
	} else {
		r = list[i]
		list = slices.Delete(list, i, i+1)
	}
	// A fresh Metadata: copies of the post handed out earlier keep theirs.
	p.Metadata = &model.PostMetadata{Files: files, Reactions: list}
	p.UpdateAt = now
	b, _ := json.Marshal(r)
	ev := "reaction_added"
	if !add {
		ev = "reaction_removed"
	}
	s.publishLocked(ev, map[string]any{"reaction": string(b)}, wsBroadcast{ChannelID: p.ChannelID}, s.memberIDsLocked(p.ChannelID), nil)
	return nil
}

func (s *Server) saveReaction(w http.ResponseWriter, r *http.Request, u User) {
	var in model.Reaction
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		appError(w, 400, "api.reaction.save_reaction.invalid.app_error", err.Error())
		return
	}
	if in.UserID != u.ID {
		appError(w, 403, "api.reaction.save_reaction.user_id.app_error", "can only react as yourself")
		return
	}
	s.mu.Lock()
	e := s.reactLocked(u.ID, in.PostID, in.EmojiName, true)
	in.CreateAt = s.chat.lastMs
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 200, in)
}

func (s *Server) deleteReaction(w http.ResponseWriter, r *http.Request, u User) {
	if r.PathValue("uid") != u.ID {
		appError(w, 403, "api.context.permissions.app_error", "not your reaction")
		return
	}
	s.mu.Lock()
	e := s.reactLocked(u.ID, r.PathValue("pid"), r.PathValue("name"), false)
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "OK"})
}

// ---- test controls ----

func (s *Server) ReactAs(username, postID, emoji string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.reactLocked(s.userIDByName(username), postID, emoji, true); e != nil {
		panic("mmfake: ReactAs: " + e.id)
	}
}

func (s *Server) UnreactAs(username, postID, emoji string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.reactLocked(s.userIDByName(username), postID, emoji, false); e != nil {
		panic("mmfake: UnreactAs: " + e.id)
	}
}

func (s *Server) Reactions(postID string) []model.Reaction {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.chat.byID[postID]; p != nil && p.Metadata != nil {
		return slices.Clone(p.Metadata.Reactions)
	}
	return nil
}

// FindPost returns the id of the latest visible post of a channel with
// exactly this message ("" if none).
func (s *Server) FindPost(channelID, message string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	posts := s.chat.posts[channelID]
	for i := len(posts) - 1; i >= 0; i-- {
		if p := posts[i]; visible(p, false) && p.Message == message {
			return p.ID
		}
	}
	return ""
}

// Preference reads a saved preference of username ("" if unset).
func (s *Server) Preference(username, category, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.chat.prefs[s.userIDByName(username)] {
		if p.Category == category && p.Name == name {
			return p.Value
		}
	}
	return ""
}
```

`internal/mmfake/server.go`: в `Start` после `s.mediaRoutes(mux)` — `s.reactionRoutes(mux)`; поле `failures map[string]int` → `failures map[string]failure`.

`internal/mmfake/net.go`:
- тип и выбор отказа:
  ```go
  type failure struct {
  	status int
  	id     string // AppError id in the body
  }
  ```
  в `conditions` цикл по `s.failures` — `var fail failure` и `for part, f := range s.failures { if strings.Contains(r.URL.Path, part) { fail = f } }`; ответ — `if fail.status != 0 { appError(w, fail.status, fail.id, "injected failure"); return }`;
- `SetFailure` и новый `FailWith`:
  ```go
  // SetFailure makes every request whose path contains part fail with the
  // given status (0 removes the failure).
  func (s *Server) SetFailure(part string, status int) {
  	s.FailWith(part, status, "mmfake.injected_failure")
  }

  // FailWith is SetFailure with the AppError id the client gets (e.g.
  // app.reaction.save.save.too_many_reactions).
  func (s *Server) FailWith(part string, status int, id string) {
  	s.mu.Lock()
  	defer s.mu.Unlock()
  	if s.failures == nil {
  		s.failures = map[string]failure{}
  	}
  	if status == 0 {
  		delete(s.failures, part)
  		return
  	}
  	s.failures[part] = failure{status: status, id: id}
  }
  ```

`internal/mmfake/seed.go`: строку `s.seedPostLocked("c-offtopic", "u-bob", "Welcome to off-topic")` заменить на
```go
	welcome := s.seedPostLocked("c-offtopic", "u-bob", "Welcome to off-topic")
	react := func(user, emoji string) model.Reaction {
		return model.Reaction{UserID: user, PostID: welcome.ID, EmojiName: emoji, CreateAt: welcome.CreateAt}
	}
	welcome.Metadata = &model.PostMetadata{Reactions: []model.Reaction{
		react("u-bob", "+1"), react("u-carol", "+1"), react("u-carol", "tada"), react("u-bob", "partyparrot"),
	}}
```

`cmd/spk-mattermost/browser.go`, test-API после `fake/picture`:
```go
		tm.HandleFunc("POST /api/_test/fake/react", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				ChannelID string `json:"channel_id"`
				Message   string `json:"message"`
				Username  string `json:"username"`
				Emoji     string `json:"emoji"`
				Remove    bool   `json:"remove"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			id := fake.FindPost(in.ChannelID, in.Message)
			if id == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "no_post"})
				return
			}
			if in.Remove {
				fake.UnreactAs(in.Username, id, in.Emoji)
			} else {
				fake.ReactAs(in.Username, id, in.Emoji)
			}
			writeJSON(w, http.StatusOK, map[string]string{"post_id": id})
		}))
```

- [ ] **Step 4: Тесты проходят**

Run: `go test -race ./internal/mm/... ./internal/mmfake/ ./internal/mmsync/ ./cmd/...`
Expected: PASS.

- [ ] **Step 5: Линт**

Run: `go vet ./... && golangci-lint run && golangci-lint run --build-tags "wails gtk3"`
Expected: без замечаний.

- [ ] **Step 6: Commit**

```bash
git add internal/mm/rest internal/mmfake cmd/spk-mattermost/browser.go
git commit -m "rest, mmfake: save and delete reactions with events; seeded reactions, error ids for injected failures"
```

### Task 10: Реакции — горячий слой, воркер, API

**Files:**
- Create: `internal/state/reactions.go`, `internal/state/reactions_test.go`
- Modify: `internal/state/server.go` (поле `intents`, `New`), `internal/state/events.go` (реакции)
- Create: `internal/mmsync/react.go`, `internal/mmsync/react_test.go`
- Modify: `internal/mmsync/worker.go` (поля `reactMu`, `reactWant`, `NewWorker`)
- Create: `internal/api/reactions.go`, `internal/api/reactions_test.go`
- Modify: `internal/api/api.go` (методы, `EmojiDTO`, коды), `internal/api/chat.go` (`actionError`)
- Modify: `internal/api/transport/http.go`, `internal/api/transport/wails.go`, `internal/api/transport/http_test.go`

**Interfaces:**
- Consumes: Task 4 (`CustomEmojiNames`, `CustomEmojiEnabled`), Task 9 (`rest.SaveReaction/DeleteReaction`; фейк: `FindPost`, `Reactions`, `ReactAs`, `Preference`, `FailWith`, `SetLatency`).
- Produces:
  - `(*state.Server).ReactLocal(postID, emoji string, add bool) (Change, bool)`, `UndoReactLocal(postID, emoji string, add bool) Change`, `RecentEmojis() []string` (самые частые первыми), `BumpRecentEmoji(name string) model.Preference`.
  - `(*mmsync.Worker).React(ctx, postID, emoji string, add bool) error` (клик во время запроса по той же паре возвращает nil сразу и отправляется после); `mmsync.ErrNoPost`, `mmsync.ErrBadEmoji`.
  - `api.API`: `AddReaction(ctx, id int64, postID, emoji string) error`, `RemoveReaction(…) error`, `EmojiInfo(ctx, id int64) (EmojiDTO, error)`; `type api.EmojiDTO struct{ Recent []string \`json:"recent"\`; Custom []string \`json:"custom"\`; CustomEnabled bool \`json:"custom_enabled"\` }`; коды `api.CodeNoPost = "no_post"`, `api.CodeTooManyReactions = "too_many_reactions"`.
  - HTTP: `POST /api/AddReaction`, `/api/RemoveReaction` `{id, post_id, emoji}`, `POST /api/EmojiInfo {id}`; Wails: `AddReaction(id, postID, emoji)`, `RemoveReaction(…)`, `EmojiInfo(id)`.

Решение — «Архитектура» п. 8. Устаревшее эхо: пользователь поставил 👍 (запрос прошёл), затем снял его, а эхо «добавлено» от первого клика пришло позже — без «намерения» 👍 на мгновение вернулся бы. Намерение живёт до своего эха или 30 с (если WS оборвался и эхо не придёт, оно не мешает чужим изменениям дольше этого).

- [ ] **Step 1: Падающие тесты — state**

`internal/state/reactions_test.go`:
```go
package state

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/ws"
)

func reactionEv(typ, user, post, emoji string) ws.Event {
	rb, _ := json.Marshal(model.Reaction{UserID: user, PostID: post, EmojiName: emoji})
	d, _ := json.Marshal(map[string]any{"reaction": string(rb)})
	return ws.Event{Type: typ, Data: d, Broadcast: ws.Broadcast{ChannelID: "off"}}
}

func reactions(s *Server, postID string) []ReactionView {
	v, _ := s.ChannelView("off")
	for _, p := range v.Posts {
		if p.ID == postID {
			return p.Reactions
		}
	}
	return nil
}

func withPost(s *Server) {
	s.ClearGuard()
	s.SetWindow("off", []model.Post{mkPost("p", "off", "u2", 1000)}, true, 5)
}

func TestReactLocalAppliesAtOnceAndUndoRestores(t *testing.T) {
	s := newFixture()
	withPost(s)
	ch, ok := s.ReactLocal("p", "+1", true)
	require.True(t, ok)
	assert.Equal(t, Change{Channels: []string{"off"}}, ch)
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"))
	assert.Equal(t, Change{Channels: []string{"off"}}, s.UndoReactLocal("p", "+1", true))
	assert.Empty(t, reactions(s, "p"))
	_, ok = s.ReactLocal("nope", "+1", true)
	assert.False(t, ok, "a post not in memory")
}

func TestOwnEchoIsIdempotentAndOthersCount(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocal("p", "+1", true)
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"))
	eff := s.ApplyEvent(reactionEv("reaction_added", "u2", "p", "+1"))
	assert.Equal(t, []string{"off"}, eff.Channels)
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 2, Mine: true}}, reactions(s, "p"))
}

func TestLateEchoOfUndoneReactionIsIgnored(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocal("p", "+1", true)  // click: add (the request succeeded)
	s.ReactLocal("p", "+1", false) // click again: remove
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))
	assert.Empty(t, reactions(s, "p"), "the late echo of the first click is stale")
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1")) // echo of the second click ends the intent
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))   // a real add from another device
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"))
}

func TestIntentExpires(t *testing.T) {
	now := t0
	s := New(func() time.Time { return now })
	s.Bootstrap(fixture())
	withPost(s)
	s.ReactLocal("p", "+1", true)
	now = now.Add(31 * time.Second)
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1")) // removed elsewhere, our echo never came
	assert.Empty(t, reactions(s, "p"))
}

func TestRecentEmojisFollowTheWebapp(t *testing.T) {
	s := newFixture()
	s.BumpRecentEmoji("smile")
	s.BumpRecentEmoji("+1")
	s.BumpRecentEmoji("smile")
	pref := s.BumpRecentEmoji("tada")
	assert.Equal(t, model.Preference{UserID: "u1", Category: "recent_emojis", Name: "u1",
		Value: `[{"name":"+1","usageCount":1},{"name":"tada","usageCount":1},{"name":"smile","usageCount":2}]`}, pref)
	assert.Equal(t, []string{"smile", "tada", "+1"}, s.RecentEmojis())
	for i := 0; i < 40; i++ {
		s.BumpRecentEmoji(fmt.Sprintf("e%d", i))
	}
	assert.Len(t, s.RecentEmojis(), 27)
	assert.Equal(t, "smile", s.RecentEmojis()[0], "the most used stays")
}

func TestRecentEmojisSurviveABadPreference(t *testing.T) {
	s := newFixture()
	d, _ := json.Marshal(map[string]any{"preferences": `[{"user_id":"u1","category":"recent_emojis","name":"u1","value":"not json"}]`})
	s.ApplyEvent(ws.Event{Type: "preferences_changed", Data: d})
	assert.Empty(t, s.RecentEmojis())
	s.BumpRecentEmoji("+1")
	assert.Equal(t, []string{"+1"}, s.RecentEmojis())
}
```

Run: `go test ./internal/state/`
Expected: FAIL — `ReactLocal undefined`.

- [ ] **Step 2: Реализация — state**

`internal/state/reactions.go`:
```go
package state

import (
	"encoding/json"
	"slices"
	"sort"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

const (
	// intentTTL bounds how long our latest click on a post+emoji decides
	// which of our own reaction events are stale echoes.
	intentTTL      = 30 * time.Second
	maxIntents     = 200
	maxRecentEmoji = 27 // the webapp's MAXIMUM_RECENT_EMOJI
)

type intent struct {
	add   bool
	until time.Time
}

func intentKey(postID, emoji string) string { return postID + "/" + emoji }

// channelOfPostLocked finds the channel of a post held in memory.
func (s *Server) channelOfPostLocked(postID string) (string, bool) {
	for id, ch := range s.chans {
		if indexOf(ch.Win.Posts, postID) >= 0 {
			return id, true
		}
	}
	if s.active != "" && indexOf(s.older, postID) >= 0 {
		return s.active, true
	}
	return "", false
}

// ReactLocal applies our own reaction at once — the click lands before the
// server answers — and records it as the intent for the post+emoji; false
// if the post is not in memory.
func (s *Server) ReactLocal(postID, emoji string, add bool) (Change, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channelOfPostLocked(postID)
	if !ok {
		return Change{}, false
	}
	now := s.now()
	if len(s.intents) >= maxIntents {
		for k, in := range s.intents {
			if now.After(in.until) {
				delete(s.intents, k)
			}
		}
	}
	s.intents[intentKey(postID, emoji)] = intent{add: add, until: now.Add(intentTTL)}
	s.reactLocked(ch, model.Reaction{UserID: s.me.ID, PostID: postID, EmojiName: emoji, CreateAt: now.UnixMilli()}, add)
	return Change{Channels: []string{ch}}, true
}

// UndoReactLocal rolls back a ReactLocal the server refused.
func (s *Server) UndoReactLocal(postID, emoji string, add bool) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.intents, intentKey(postID, emoji))
	ch, ok := s.channelOfPostLocked(postID)
	if !ok {
		return Change{}
	}
	s.reactLocked(ch, model.Reaction{UserID: s.me.ID, PostID: postID, EmojiName: emoji}, !add)
	return Change{Channels: []string{ch}}
}

// staleEchoLocked: an event about our own reaction that contradicts our
// latest click is the late echo of an earlier click — drop it. The echo
// that matches the click ends the intent; an expired intent decides nothing.
func (s *Server) staleEchoLocked(r model.Reaction, add bool) bool {
	if r.UserID != s.me.ID {
		return false
	}
	k := intentKey(r.PostID, r.EmojiName)
	in, ok := s.intents[k]
	if !ok {
		return false
	}
	if s.now().After(in.until) || in.add == add {
		delete(s.intents, k)
		return false
	}
	return true
}

type recentEmoji struct {
	Name       string `json:"name"`
	UsageCount int    `json:"usageCount"`
}

func (s *Server) recentLocked() []recentEmoji {
	var list []recentEmoji
	if v := s.prefs[prefKey{"recent_emojis", s.me.ID}]; v != "" {
		if json.Unmarshal([]byte(v), &list) != nil {
			return nil
		}
	}
	return list
}

// RecentEmojis lists recently used emoji, most used first (the webapp keeps
// them sorted ascending and shows the list from its end).
func (s *Server) RecentEmojis() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.recentLocked()
	out := make([]string, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Name != "" {
			out = append(out, list[i].Name)
		}
	}
	return out
}

// BumpRecentEmoji records a use like the webapp's addRecentEmojis: the name
// moves to the end with usageCount+1, only the last 27 stay, the list is
// sorted by usageCount ascending. Returns the preference to save.
func (s *Server) BumpRecentEmoji(name string) model.Preference {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.recentLocked()
	count := 1
	if i := slices.IndexFunc(list, func(e recentEmoji) bool { return e.Name == name }); i >= 0 {
		count = list[i].UsageCount + 1
		list = slices.Delete(list, i, i+1)
	}
	list = append(list, recentEmoji{Name: name, UsageCount: count})
	if len(list) > maxRecentEmoji {
		list = list[len(list)-maxRecentEmoji:]
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].UsageCount < list[j].UsageCount })
	b, _ := json.Marshal(list)
	p := model.Preference{UserID: s.me.ID, Category: "recent_emojis", Name: s.me.ID, Value: string(b)}
	s.prefs[prefKey{p.Category, p.Name}] = p.Value
	s.dirty.meta = true
	return p
}
```

`internal/state/server.go`: в `Server` после `orphans` — `intents map[string]intent // our latest reaction clicks (post/emoji), see staleEchoLocked`; в `New` — `intents: map[string]intent{},`.

`internal/state/events.go`, ветку реакций заменить:
```go
	case "reaction_added", "reaction_removed":
		add := ev.Type == "reaction_added"
		if r, err := ws.DecodeReaction(ev); err == nil && !s.staleEchoLocked(r, add) && s.reactLocked(ev.Broadcast.ChannelID, r, add) {
			eff.Channels = []string{ev.Broadcast.ChannelID}
		}
```

Run: `go test -race ./internal/state/`
Expected: PASS.

- [ ] **Step 3: Падающие тесты — воркер и API**

`internal/mmsync/react_test.go`:
```go
package mmsync

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/state"
)

func reactionOf(h *harness, postID, emoji string) state.ReactionView {
	for _, p := range h.view("c-offtopic").Posts {
		if p.ID == postID {
			for _, r := range p.Reactions {
				if r.Emoji == emoji {
					return r
				}
			}
		}
	}
	return state.ReactionView{}
}

func serverHas(h *harness, postID, user, emoji string) bool {
	return slices.ContainsFunc(h.fake.Reactions(postID), func(r model.Reaction) bool { return r.UserID == user && r.EmojiName == emoji })
}

func welcomeHarness(t *testing.T) (*harness, string) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	return h, h.fake.FindPost("c-offtopic", "Welcome to off-topic")
}

func TestReactionRoundTrip(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	require.NoError(t, h.w.React(ctx, id, "+1", true))
	assert.Equal(t, state.ReactionView{Emoji: "+1", Count: 3, Mine: true}, reactionOf(h, id, "+1"))
	assert.True(t, serverHas(h, id, "u-alice", "+1"))
	h.eventually(func() bool { return strings.Contains(h.fake.Preference("alice", "recent_emojis", "u-alice"), `"name":"+1"`) }, "recent emoji not saved")

	require.NoError(t, h.w.React(ctx, id, "+1", false))
	assert.Equal(t, state.ReactionView{Emoji: "+1", Count: 2}, reactionOf(h, id, "+1"))
	assert.False(t, serverHas(h, id, "u-alice", "+1"))
}

func TestReactionRefusedIsRolledBack(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.SetFailure("/api/v4/reactions", 403)
	err := h.w.React(ctx, id, "tada", true)
	var re *rest.Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, 403, re.Status)
	assert.Equal(t, state.ReactionView{Emoji: "tada", Count: 1}, reactionOf(h, id, "tada"), "rolled back")
	assert.Equal(t, StatusLive, h.w.Status(), "a refused reaction is not a dead session")
	assert.ErrorIs(t, h.w.React(ctx, "nope", "tada", true), ErrNoPost)
	assert.ErrorIs(t, h.w.React(ctx, id, "bad name", true), ErrBadEmoji)
}

func TestReactionClicksWhileInFlightAreQueuedLastWins(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.SetLatency("/api/v4/reactions", 300*time.Millisecond)
	del := "/api/v4/users/u-alice/posts/" + id + "/reactions/"

	done := make(chan error, 1)
	go func() { done <- h.w.React(ctx, id, "fire", true) }()
	h.eventually(func() bool { return reactionOf(h, id, "fire").Mine }, "not applied at once")
	require.NoError(t, h.w.React(ctx, id, "fire", false), "a click while the add is in flight returns at once")
	assert.False(t, reactionOf(h, id, "fire").Mine, "…and shows at once")
	require.NoError(t, <-done)
	assert.False(t, serverHas(h, id, "u-alice", "fire"), "the last click was sent after the first")
	assert.Equal(t, 1, h.fake.Hits("DELETE", del+"fire"))

	go func() { done <- h.w.React(ctx, id, "rocket", true) }()
	h.eventually(func() bool { return reactionOf(h, id, "rocket").Mine }, "not applied at once")
	require.NoError(t, h.w.React(ctx, id, "rocket", false))
	require.NoError(t, h.w.React(ctx, id, "rocket", true))
	require.NoError(t, <-done)
	assert.True(t, serverHas(h, id, "u-alice", "rocket"))
	assert.Zero(t, h.fake.Hits("DELETE", del+"rocket"), "add, remove, add: nothing more to send")
}

func TestOthersReactionsArriveLive(t *testing.T) {
	h, id := welcomeHarness(t)
	h.fake.ReactAs("bob", id, "rocket") // a member of Off-Topic (carol is not)
	h.eventually(func() bool { return reactionOf(h, id, "rocket") == state.ReactionView{Emoji: "rocket", Count: 1} }, "live reaction")
}
```

`internal/api/reactions_test.go`:
```go
package api

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactionsThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") }, "off-topic not loaded")
	post := fake.FindPost("c-offtopic", "Welcome to off-topic")

	require.NoError(t, f.svc.AddReaction(ctx, id, post, "rocket"))
	info, err := f.svc.EmojiInfo(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, []string{"rocket"}, info.Recent)
	assert.True(t, info.CustomEnabled)
	f.eventually(func() bool { i, _ := f.svc.EmojiInfo(ctx, id); return slices.Contains(i.Custom, "partyparrot") }, "custom emoji not listed")
	require.NoError(t, f.svc.RemoveReaction(ctx, id, post, "rocket"))

	fake.FailWith("/api/v4/reactions", 400, "app.reaction.save.save.too_many_reactions")
	assert.Equal(t, CodeTooManyReactions, codeOf(f.svc.AddReaction(ctx, id, post, "fire")))
	fake.FailWith("/api/v4/reactions", 403, "api.reaction.save.archived_channel.app_error")
	assert.Equal(t, CodeForbidden, codeOf(f.svc.AddReaction(ctx, id, post, "fire")))
	assert.Equal(t, "live", f.server(id).State, "403 on a reaction is not an expired session")
	fake.SetFailure("/api/v4/reactions", 0)
	assert.Equal(t, CodeNoPost, codeOf(f.svc.AddReaction(ctx, id, "nope", "fire")))
}
```

`internal/api/transport/http_test.go`: в `fakeAPI` поле `reacted []string` и метод
```go
func (f *fakeAPI) AddReaction(_ context.Context, id int64, postID, emoji string) error {
	f.reacted = append(f.reacted, fmt.Sprintf("%d/%s/%s", id, postID, emoji))
	return nil
}
```
в конец `TestChatRoutes`:
```go
	resp = call(t, h, ts.URL, "AddReaction", `{"id":3,"post_id":"p1","emoji":"+1"}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []string{"3/p1/+1"}, f.reacted)
```

Run: `go test ./internal/mmsync/ ./internal/api/...`
Expected: FAIL — `h.w.React undefined`, `AddReaction undefined`.

- [ ] **Step 4: Реализация — воркер**

`internal/mmsync/react.go`:
```go
package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"regexp"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

var (
	ErrNoPost   = errors.New("mmsync: post not loaded")
	ErrBadEmoji = errors.New("mmsync: invalid emoji name")
)

// The server's rule for reaction names (model.Reaction.IsValid).
var emojiNameRe = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,64}$`)

// React adds or removes our reaction. The post changes at once and is
// rolled back to the server's state if the server refuses; the WS echo is
// idempotent and a late echo of an earlier click is dropped (state
// intents). Clicks on a post+emoji whose request is still in flight are not
// sent in parallel: the last one wins and is sent when the request returns
// (add, remove, add while the first add runs → nothing more to send).
func (w *Worker) React(ctx context.Context, postID, emoji string, add bool) error {
	if !emojiNameRe.MatchString(emoji) {
		return ErrBadEmoji
	}
	ch, ok := w.st.ReactLocal(postID, emoji, add)
	if !ok {
		return ErrNoPost
	}
	w.changed(ch)
	key := postID + "/" + emoji
	w.reactMu.Lock()
	if _, busy := w.reactWant[key]; busy {
		w.reactWant[key] = add // the request in flight sends it afterwards
		w.reactMu.Unlock()
		return nil
	}
	w.reactWant[key] = add
	w.reactMu.Unlock()
	sent := !add // the server's state before this click
	for {
		w.reactMu.Lock()
		want := w.reactWant[key]
		if want == sent {
			delete(w.reactWant, key)
			w.reactMu.Unlock()
			return nil
		}
		w.reactMu.Unlock()
		if err := w.sendReaction(ctx, postID, emoji, want); err != nil {
			w.reactMu.Lock()
			delete(w.reactWant, key)
			w.reactMu.Unlock()
			w.changed(w.st.UndoReactLocal(postID, emoji, want)) // back to what the server has
			slog.Warn("reaction refused", "srv", w.srv.ID, "post", postID, "emoji", emoji, "err", err)
			return w.actionErr(err)
		}
		sent = want
	}
}

func (w *Worker) sendReaction(ctx context.Context, postID, emoji string, add bool) error {
	me := w.st.Me().ID
	if !add {
		return w.rc.DeleteReaction(ctx, me, postID, emoji)
	}
	if _, err := w.rc.SaveReaction(ctx, model.Reaction{UserID: me, PostID: postID, EmojiName: emoji}); err != nil {
		return err
	}
	pref := w.st.BumpRecentEmoji(emoji)
	w.goBG(func(ctx context.Context) {
		if err := w.rc.SavePreferences(ctx, []model.Preference{pref}); err != nil {
			slog.Warn("could not save recent emoji", "srv", w.srv.ID, "err", err)
		}
	})
	return nil
}
```

`internal/mmsync/worker.go`: в `Worker` рядом с `viewing` — `reactMu sync.Mutex` и `reactWant map[string]bool // post/emoji → the state the user wants while a request runs`; в `NewWorker` — `reactWant: map[string]bool{},`.

- [ ] **Step 5: Реализация — API и транспорты**

`internal/api/reactions.go`:
```go
package api

import "context"

// EmojiDTO feeds the emoji picker of one server.
type EmojiDTO struct {
	Recent        []string `json:"recent"` // most used first
	Custom        []string `json:"custom"` // custom emoji names, by name
	CustomEnabled bool     `json:"custom_enabled"`
}

func (s *Service) AddReaction(ctx context.Context, id int64, postID, emoji string) error {
	return s.react(ctx, id, postID, emoji, true)
}

func (s *Service) RemoveReaction(ctx context.Context, id int64, postID, emoji string) error {
	return s.react(ctx, id, postID, emoji, false)
}

func (s *Service) react(ctx context.Context, id int64, postID, emoji string, add bool) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.React(rctx, postID, emoji, add))
}

func (s *Service) EmojiInfo(ctx context.Context, id int64) (EmojiDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return EmojiDTO{}, err
	}
	st := w.State()
	return EmojiDTO{Recent: st.RecentEmojis(), Custom: st.CustomEmojiNames(), CustomEnabled: st.CustomEmojiEnabled()}, nil
}
```

`internal/api/api.go`: в интерфейс `API`:
```go
	AddReaction(ctx context.Context, id int64, postID, emoji string) error
	RemoveReaction(ctx context.Context, id int64, postID, emoji string) error
	EmojiInfo(ctx context.Context, id int64) (EmojiDTO, error)
```
в коды — `CodeNoPost = "no_post"`, `CodeTooManyReactions = "too_many_reactions"`.

`internal/api/chat.go`, в `actionError` после ветки `ErrEmptyMessage`:
```go
	case errors.Is(err, mmsync.ErrNoPost):
		return coded(CodeNoPost, nil)
	case errors.As(err, &re) && re.ID == "app.reaction.save.save.too_many_reactions":
		return coded(CodeTooManyReactions, err)
```

`internal/api/transport/http.go`, в `routes()`:
```go
	type reactReq struct {
		ID     int64  `json:"id"`
		PostID string `json:"post_id"`
		Emoji  string `json:"emoji"`
	}
	h.mux.HandleFunc("POST /api/AddReaction", handle(func(ctx context.Context, r *reactReq) (any, error) {
		return nil, h.api.AddReaction(ctx, r.ID, r.PostID, r.Emoji)
	}))
	h.mux.HandleFunc("POST /api/RemoveReaction", handle(func(ctx context.Context, r *reactReq) (any, error) {
		return nil, h.api.RemoveReaction(ctx, r.ID, r.PostID, r.Emoji)
	}))
	h.mux.HandleFunc("POST /api/EmojiInfo", handle(func(ctx context.Context, r *idReq) (any, error) {
		return h.api.EmojiInfo(ctx, r.ID)
	}))
```

`internal/api/transport/wails.go`:
```go
func (w *API) AddReaction(id int64, postID, emoji string) error {
	return w.a.AddReaction(context.Background(), id, postID, emoji)
}
func (w *API) RemoveReaction(id int64, postID, emoji string) error {
	return w.a.RemoveReaction(context.Background(), id, postID, emoji)
}
func (w *API) EmojiInfo(id int64) (api.EmojiDTO, error) { return w.a.EmojiInfo(context.Background(), id) }
```

- [ ] **Step 6: Тесты, сборки, линт**

Run: `go test -race ./... && make build-desktop && make cross-check && go vet ./... && golangci-lint run && golangci-lint run --build-tags "wails gtk3"`
Expected: всё PASS, без замечаний.

- [ ] **Step 7: Commit**

```bash
git add internal/state internal/mmsync internal/api
git commit -m "reactions: optimistic add/remove with rollback, stale own echoes dropped, recent emoji like the webapp; API and transports"
```

### Task 11: UI — набор эмодзи Mattermost (ленивый чанк) и чипы реакций с переключением

**Files:**
- Create: `scripts/gen-emoji.mjs`
- Create: `frontend/src/emoji/data.ts` (сгенерирован), `frontend/src/emoji/index.ts`, `frontend/src/emoji/index.test.ts`
- Delete: `frontend/src/components/emoji.ts` (таблица переезжает в `emoji/index.ts` как `BUILTIN`)
- Create: `frontend/src/components/EmojiGlyph.tsx`, `frontend/src/components/Reactions.tsx`, `frontend/src/components/Reactions.test.tsx`
- Modify: `frontend/src/api/types.ts` (`EmojiDTO`), `frontend/src/api/client.ts` (+ `addReaction`, `removeReaction`, `emojiInfo`), `frontend/src/api/client.test.ts`
- Modify: `frontend/src/chat.ts` (+ `react`, `emojiInfo`), `frontend/src/components/PostItem.tsx` (`Reactions`, `PostActions.react`), `frontend/src/components/PostItem.test.tsx`, `frontend/src/components/Feed.test.tsx`, `frontend/src/components/ChannelPane.tsx`
- Modify: `frontend/src/i18n.ts`

**Interfaces:**
- Consumes: Task 6 (`mediaURL`), Task 10 (`AddReaction`, `RemoveReaction`, `EmojiInfo` → `{recent, custom, custom_enabled}`), media `emoji/{name}` (Task 4).
- Produces:
  - `emoji/index.ts`: `interface Emoji { char: string; names: string[] }` (`names[0]` — имя, которое ставит веб-клиент); `CATEGORY_IDS = ['smileys','people','nature','food','travel','activities','objects','symbols','flags']`; `type CategoryId`; `interface EmojiIndex { categories: { id: CategoryId; emojis: Emoji[] }[]; byName: Map<string, Emoji> }`; `BUILTIN: Record<string, string>`; `buildIndex(raw: readonly string[]): EmojiIndex`; `loadEmojiIndex(): Promise<EmojiIndex>`; `useEmojiIndex(want = true): EmojiIndex | null`; `emojiChar(name, idx): string | null`; `searchEmoji(idx, q, limit = 200): Emoji[]`.
  - `EmojiGlyph({ serverId, name, size? })`; `Reactions({ serverId, reactions, onToggle, onAdd? })` (кнопку «☺+» добавит Task 12 через `onAdd`).
  - `PostActions.react(post: PostView, emoji: string, add: boolean): void`.
  - `client`: `addReaction(id, postId, emoji)`, `removeReaction(id, postId, emoji)`, `emojiInfo(id): Promise<EmojiDTO>`; `chat.ts`: `react(serverId, postId, emoji, add)`, `emojiInfo(serverId)`.

Набор — «Архитектура» п. 9: генератор кладёт в репозиторий `frontend/src/emoji/data.ts` (41 КБ; 9 категорий: 151, 347, 140, 129, 215, 84, 250, 220, 269 — всего 1805 эмодзи); импортируется динамически — отдельный чанк Vite. Пока он грузится, частые имена (`BUILTIN`) показываются сразу; прочие — `:name:`, затем символ. Имя, которого нет в наборе (после загрузки), — кастомное: картинка `/media/<srv>/emoji/<name>`, при ошибке — снова `:name:`. Оттенки кожи (`+1_light_skin_tone`) выводятся из базового символа и модификатора (у ZWJ-последовательностей — базовый символ).

- [ ] **Step 1: Генератор и данные**

`scripts/gen-emoji.mjs`:
```js
#!/usr/bin/env node
// Generates frontend/src/emoji/data.ts — the emoji set of the Mattermost
// webapp (npm emoji-datasource@6.1.1 + the webapp's
// additional_shortnames.json, see docs/research/2026-09-24-mattermost-api-facts.md
// §7.5) in a compact form: one string per category, one line per emoji —
// "<char> <name> <alias>…" — in the webapp's order. Skin-tone variants are
// not listed: the picker offers the default tone, and emoji/index.ts derives
// toned names for display.
//
// Usage (sources fetched once, the output is committed):
//   node scripts/gen-emoji.mjs <emoji-datasource/emoji.json> <additional_shortnames.json> > frontend/src/emoji/data.ts
import fs from 'node:fs'

const [, , emojiJson, additionalJson] = process.argv
if (!emojiJson || !additionalJson) {
  console.error('usage: node scripts/gen-emoji.mjs <emoji.json> <additional_shortnames.json>')
  process.exit(2)
}
const data = JSON.parse(fs.readFileSync(emojiJson, 'utf8'))
const extra = JSON.parse(fs.readFileSync(additionalJson, 'utf8'))
// emoji-datasource categories in the webapp's order; "Component" (skin tone
// swatches) is not something to pick.
const CATEGORIES = ['Smileys & Emotion', 'People & Body', 'Animals & Nature', 'Food & Drink', 'Travel & Places', 'Activities', 'Objects', 'Symbols', 'Flags']
const char = (unified) => String.fromCodePoint(...unified.split('-').map((h) => parseInt(h, 16)))
const lines = CATEGORIES.map(() => [])
for (const e of [...data].sort((a, b) => a.sort_order - b.sort_order)) {
  const i = CATEGORIES.indexOf(e.category)
  if (i < 0) continue
  lines[i].push([char(e.unified), ...e.short_names, ...(extra[e.short_name] ?? [])].join(' '))
}
process.stdout.write(
  "// Generated by scripts/gen-emoji.mjs from emoji-datasource@6.1.1 and the Mattermost webapp's\n" +
    '// additional_shortnames.json (release-10.11) — do not edit by hand.\n' +
    `const data: readonly string[] = ${JSON.stringify(lines.map((l) => l.join('\n')))}\n` +
    'export default data\n',
)
```

Run:
```bash
T=$(mktemp -d -p /home/spk/.spk/sawe/ss/Mattermost/.agents/tmp)
curl -sfL https://registry.npmjs.org/emoji-datasource/-/emoji-datasource-6.1.1.tgz | tar xz -C $T
curl -sfL https://raw.githubusercontent.com/mattermost/mattermost/release-10.11/webapp/channels/build/emoji/additional_shortnames.json -o $T/add.json
mkdir -p frontend/src/emoji
node scripts/gen-emoji.mjs $T/package/emoji.json $T/add.json > frontend/src/emoji/data.ts
ls -l frontend/src/emoji/data.ts && head -c 200 frontend/src/emoji/data.ts
```
Expected: файл ~41–43 КБ; начинается с комментария и `const data: readonly string[] = ["😀 grinning\n😃 smiley\n😄 smile\n…`.

- [ ] **Step 2: Падающие тесты**

`frontend/src/emoji/index.test.ts`:
```ts
import data from './data'
import { buildIndex, CATEGORY_IDS, emojiChar, searchEmoji } from './index'

const idx = buildIndex(data)

test('the dataset: nine categories in the webapp order, aliases share one emoji', () => {
  expect(idx.categories.map((c) => c.id)).toEqual([...CATEGORY_IDS])
  expect(idx.categories.map((c) => c.emojis.length)).toEqual([151, 347, 140, 129, 215, 84, 250, 220, 269])
  expect(idx.categories[0].emojis[0]).toEqual({ char: '😀', names: ['grinning'] })
  expect(idx.byName.get('+1')?.char).toBe('👍')
  expect(idx.byName.get('thumbsup')).toBe(idx.byName.get('+1'))
  expect(idx.byName.get('+1')?.names[0]).toBe('+1')
})

test('emojiChar: common names before the set loads, skin tones, custom → null', () => {
  expect(emojiChar('+1', null)).toBe('👍')
  expect(emojiChar('rocket_ship_to_nowhere', null)).toBeNull()
  expect(emojiChar('+1_light_skin_tone', idx)).toBe('👍🏻')
  expect(emojiChar('v_medium_dark_skin_tone', idx)).toBe('✌🏾')
  expect(emojiChar('partyparrot', idx)).toBeNull()
})

test('search: exact name first, then prefixes, aliases count', () => {
  expect(searchEmoji(idx, 'smile')[0].names[0]).toBe('smile')
  const thumbs = searchEmoji(idx, 'thumbs')
  expect(thumbs.slice(0, 2).map((e) => e.names[0])).toEqual(['+1', '-1'])
  expect(searchEmoji(idx, '   ')).toEqual([])
})
```

`frontend/src/components/Reactions.test.tsx`:
```tsx
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { setLocale } from '../i18n'
import { Reactions } from './Reactions'

beforeEach(() => setLocale('en'))

test('chips toggle our reaction and say whether it is ours', async () => {
  const onToggle = vi.fn()
  render(<Reactions serverId={1} reactions={[{ emoji: '+1', count: 3, mine: true }, { emoji: 'tada', count: 1, mine: false }]} onToggle={onToggle} />)
  const mine = screen.getByRole('button', { name: '👍 3, you reacted' })
  expect(mine).toHaveAttribute('aria-pressed', 'true')
  await userEvent.click(screen.getByRole('button', { name: '🎉 1' }))
  expect(onToggle).toHaveBeenCalledWith({ emoji: 'tada', count: 1, mine: false })
  await userEvent.click(mine)
  expect(onToggle).toHaveBeenLastCalledWith({ emoji: '+1', count: 3, mine: true })
  expect(screen.queryByRole('button', { name: 'Add reaction' })).toBeNull()
})

test('a custom emoji is a picture from the media endpoint once the set says it is not standard', async () => {
  const { container } = render(<Reactions serverId={4} reactions={[{ emoji: 'partyparrot', count: 1, mine: false }]} onToggle={vi.fn()} />)
  expect(screen.getByRole('button', { name: ':partyparrot: 1' })).toBeInTheDocument()
  await waitFor(() => expect(container.querySelector('img')).toHaveAttribute('src', '/media/4/emoji/partyparrot'))
  fireEvent.error(container.querySelector('img')!)
  expect(container).toHaveTextContent(':partyparrot:')
})

test('a standard emoji outside the common table appears once the set loads', async () => {
  render(<Reactions serverId={1} reactions={[{ emoji: 'avocado', count: 2, mine: false }]} onToggle={vi.fn()} />)
  expect(await screen.findByRole('button', { name: '🥑 2' })).toBeInTheDocument()
})
```

`frontend/src/components/PostItem.test.tsx`:
- в `actions()` добавить `react: vi.fn(),`;
- в тесте «attachments, files, reactions and reply count» строки с `getByTitle(':+1:')` и `getByTitle(':custom_party:')` заменить на
  ```tsx
    expect(screen.getByRole('button', { name: '👍 2, you reacted' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: ':custom_party: 1' })).toBeInTheDocument()
  ```
  и после клика по ссылке дописать
  ```tsx
    await userEvent.click(screen.getByRole('button', { name: '👍 2, you reacted' }))
    expect(a.react).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }), '+1', false)
  ```

`frontend/src/components/Feed.test.tsx`: в `actions` добавить `react: vi.fn(),`.

`frontend/src/api/client.test.ts`, тест «http client chat methods…»: после `downloadFile` — `await httpClient.addReaction(3, 'p1', '+1')` и `await httpClient.emojiInfo(3)`; в ожидание — `['/api/AddReaction', { id: 3, post_id: 'p1', emoji: '+1' }]`, `['/api/EmojiInfo', { id: 3 }]`.

Run: `cd frontend && pnpm test`
Expected: FAIL — нет `./emoji/index`, `./Reactions`.

- [ ] **Step 3: Реализация — набор эмодзи**

`frontend/src/emoji/index.ts`:
```ts
import { useEffect, useSyncExternalStore } from 'react'

export interface Emoji {
  char: string
  names: string[] // names[0] is what the webapp posts (getEmojiName)
}

export const CATEGORY_IDS = ['smileys', 'people', 'nature', 'food', 'travel', 'activities', 'objects', 'symbols', 'flags'] as const
export type CategoryId = (typeof CATEGORY_IDS)[number]

export interface EmojiIndex {
  categories: { id: CategoryId; emojis: Emoji[] }[]
  byName: Map<string, Emoji>
}

// BUILTIN: the most common reaction names, shown before the set loads.
export const BUILTIN: Record<string, string> = {
  '+1': '👍', thumbsup: '👍', '-1': '👎', thumbsdown: '👎', heart: '❤️', smile: '😄', slightly_smiling_face: '🙂',
  grinning: '😀', laughing: '😆', joy: '😂', wink: '😉', tada: '🎉', eyes: '👀', fire: '🔥', pray: '🙏',
  clap: '👏', ok_hand: '👌', rocket: '🚀', thinking_face: '🤔', white_check_mark: '✅', heavy_check_mark: '✔️',
  x: '❌', '100': '💯', warning: '⚠️', muscle: '💪', wave: '👋', sob: '😭', cry: '😢', raised_hands: '🙌',
}

// buildIndex parses data.ts: one string per category, one line per emoji,
// "<char> <name> <alias>…".
export function buildIndex(raw: readonly string[]): EmojiIndex {
  const byName = new Map<string, Emoji>()
  const categories = raw.map((block, i) => ({
    id: CATEGORY_IDS[i],
    emojis: block
      .split('\n')
      .filter(Boolean)
      .map((line) => {
        const [char, ...names] = line.split(' ')
        const e = { char, names }
        for (const n of names) if (!byName.has(n)) byName.set(n, e)
        return e
      }),
  }))
  return { categories, byName }
}

let index: EmojiIndex | null = null
let loading: Promise<EmojiIndex> | null = null
const listeners = new Set<() => void>()

// loadEmojiIndex loads the set — a lazy chunk (~16 KB gzip) — once.
export function loadEmojiIndex(): Promise<EmojiIndex> {
  loading ??= import('./data').then((m) => {
    index = buildIndex(m.default)
    listeners.forEach((l) => l())
    return index
  })
  return loading
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

// useEmojiIndex returns the set once loaded and re-renders then;
// want=false only reads it (no load is triggered).
export function useEmojiIndex(want = true): EmojiIndex | null {
  const idx = useSyncExternalStore(subscribe, () => index)
  useEffect(() => {
    if (want && !index) void loadEmojiIndex()
  }, [want])
  return idx
}

const TONES: Record<string, string> = {
  light: '\u{1F3FB}', medium_light: '\u{1F3FC}', medium: '\u{1F3FD}', medium_dark: '\u{1F3FE}', dark: '\u{1F3FF}',
}
const TONE_RE = /^(.+?)_(light|medium_light|medium|medium_dark|dark)_skin_tone$/

function withTone(base: string, tone: string): string {
  const cps = [...base]
  if (cps.includes('‍')) return base // ZWJ sequence: the default tone is close enough
  return cps[0] + tone + cps.slice(1).filter((c) => c !== '️').join('')
}

// emojiChar is the character for a reaction name: aliases (+1, thumbsup)
// and skin tones (+1_light_skin_tone) included. null: not a standard emoji
// — a custom one, or the set is not loaded and the name is not common.
export function emojiChar(name: string, idx: EmojiIndex | null): string | null {
  const e = idx?.byName.get(name)
  if (e) return e.char
  if (BUILTIN[name]) return BUILTIN[name]
  const m = TONE_RE.exec(name)
  if (m) {
    const base = emojiChar(m[1], idx)
    if (base) return withTone(base, TONES[m[2]])
  }
  return null
}

// searchEmoji ranks an exact name first, then names starting with the
// query, then names containing it; the set's order within each rank.
export function searchEmoji(idx: EmojiIndex, q: string, limit = 200): Emoji[] {
  const query = q.trim().toLowerCase()
  if (!query) return []
  const ranked: { rank: number; order: number; e: Emoji }[] = []
  let order = 0
  for (const c of idx.categories) {
    for (const e of c.emojis) {
      let rank = 3
      for (const n of e.names) rank = Math.min(rank, n === query ? 0 : n.startsWith(query) ? 1 : n.includes(query) ? 2 : 3)
      if (rank < 3) ranked.push({ rank, order, e })
      order++
    }
  }
  ranked.sort((a, b) => a.rank - b.rank || a.order - b.order)
  return ranked.slice(0, limit).map((r) => r.e)
}
```

Удалить `frontend/src/components/emoji.ts`.

- [ ] **Step 4: Реализация — глиф, чипы, пост, клиент**

`frontend/src/components/EmojiGlyph.tsx`:
```tsx
import { useState } from 'react'
import { emojiChar, useEmojiIndex } from '../emoji'
import { mediaURL } from '../media'

// EmojiGlyph draws an emoji by name: a character for standard emoji, the
// server's picture for custom ones (via /media/, by name), ":name:" while
// unknown or if the picture fails.
export function EmojiGlyph({ serverId, name, size = 16 }: { serverId: number; name: string; size?: number }) {
  const common = emojiChar(name, null)
  const idx = useEmojiIndex(!common)
  const [broken, setBroken] = useState(false)
  const ch = common ?? emojiChar(name, idx)
  if (ch) return <span aria-hidden="true">{ch}</span>
  if (!idx || broken) return <span aria-hidden="true">:{name}:</span>
  return (
    <img
      src={mediaURL(serverId, 'emoji', name)}
      alt=""
      aria-hidden="true"
      width={size}
      height={size}
      loading="lazy"
      onError={() => setBroken(true)}
      className="inline-block align-text-bottom"
    />
  )
}
```

`frontend/src/components/Reactions.tsx`:
```tsx
import type { ReactionView } from '../api/types'
import { emojiChar, useEmojiIndex } from '../emoji'
import { t } from '../i18n'
import { EmojiGlyph } from './EmojiGlyph'

interface Props {
  serverId: number
  reactions: ReactionView[]
  onToggle(r: ReactionView): void
  onAdd?(trigger: HTMLElement): void // the "☺+" chip opens the picker next to itself
}

// Reactions: a chip per emoji; clicking toggles our own reaction with the
// chip's own name (aliases stay separate, as on the server).
export function Reactions({ serverId, reactions, onToggle, onAdd }: Props) {
  const idx = useEmojiIndex(reactions.some((r) => !emojiChar(r.emoji, null)))
  return (
    <div className="mt-1 flex flex-wrap items-center gap-1">
      {reactions.map((r) => {
        const label = emojiChar(r.emoji, idx) ?? `:${r.emoji}:`
        return (
          <button
            key={r.emoji}
            type="button"
            aria-pressed={r.mine}
            aria-label={t(r.mine ? 'reaction.chipMine' : 'reaction.chip', { emoji: label, n: String(r.count) })}
            title={`:${r.emoji}:`}
            onClick={() => onToggle(r)}
            className={`flex h-6 items-center gap-1 rounded-full border px-1.5 text-xs ${r.mine ? 'border-accent bg-accent/15 text-fg' : 'border-line text-fg-muted hover:border-fg-subtle'}`}
          >
            <EmojiGlyph serverId={serverId} name={r.emoji} />
            <span>{r.count}</span>
          </button>
        )
      })}
      {onAdd && (
        <button
          type="button"
          aria-label={t('reaction.add')}
          title={t('reaction.add')}
          onClick={(e) => onAdd(e.currentTarget)}
          className="flex h-6 items-center rounded-full border border-line px-1.5 text-xs text-fg-muted hover:bg-hover"
        >
          ☺+
        </button>
      )}
    </div>
  )
}
```

`frontend/src/api/types.ts`:
```ts
export interface EmojiDTO {
  recent: string[] // most used first
  custom: string[]
  custom_enabled: boolean
}
```
`frontend/src/api/client.ts`: импорт `EmojiDTO`; в `Client`:
```ts
  addReaction(id: number, postId: string, emoji: string): Promise<void>
  removeReaction(id: number, postId: string, emoji: string): Promise<void>
  emojiInfo(id: number): Promise<EmojiDTO>
```
в `httpClient`:
```ts
  addReaction: (id, post_id, emoji) => done(post('AddReaction', { id, post_id, emoji })),
  removeReaction: (id, post_id, emoji) => done(post('RemoveReaction', { id, post_id, emoji })),
  emojiInfo: (id) => post('EmojiInfo', { id }),
```
в `wailsClient`:
```ts
  addReaction: (id, postId, emoji) => wcall('AddReaction', id, postId, emoji),
  removeReaction: (id, postId, emoji) => wcall('RemoveReaction', id, postId, emoji),
  emojiInfo: (id) => wcall('EmojiInfo', id),
```

`frontend/src/chat.ts`:
```ts
export const react = (serverId: number, postId: string, emoji: string, add: boolean) => {
  ;(add ? client.addReaction(serverId, postId, emoji) : client.removeReaction(serverId, postId, emoji)).catch(report)
}

export const emojiInfo = (serverId: number) => client.emojiInfo(serverId)
```

`frontend/src/components/PostItem.tsx`:
- убрать `import { emojiFor } from './emoji'`, добавить `import { Reactions } from './Reactions'`;
- в `PostActions` — `react(post: PostView, emoji: string, add: boolean): void`;
- блок `post.reactions` заменить на
  ```tsx
          {post.reactions && post.reactions.length > 0 && (
            <Reactions serverId={serverId} reactions={post.reactions} onToggle={(r) => actions.react(post, r.emoji, !r.mine)} />
          )}
  ```

`frontend/src/components/ChannelPane.tsx`: импорт `react` из `../chat`; в `actions` — `react: (p, emoji, add) => react(server.id, p.id, emoji, add),`.

`frontend/src/i18n.ts`: в `ru` — `'reaction.chip': '{emoji} {n}'`, `'reaction.chipMine': '{emoji} {n}, ваша реакция'`, `'reaction.add': 'Добавить реакцию'`, `'err.too_many_reactions': 'У сообщения слишком много разных реакций'`, `'err.no_post': 'Сообщение не найдено'`; в `en` — `'reaction.chip': '{emoji} {n}'`, `'reaction.chipMine': '{emoji} {n}, you reacted'`, `'reaction.add': 'Add reaction'`, `'err.too_many_reactions': 'This message has too many different reactions'`, `'err.no_post': 'Message not found'`.

- [ ] **Step 5: Тесты, линт, размер бандла**

Run: `cd frontend && pnpm test && pnpm lint && pnpm build && for f in dist/assets/*.js; do echo "$f $(wc -c < $f) $(gzip -9c $f | wc -c)"; done`
Expected: тесты PASS; в `dist/assets` появился отдельный чанк `data-*.js` (~16 КБ gzip); основной `index-*.js` — около 148 КБ gzip (весь UI этапа 3 — ≈ +6 КБ к 142 КБ этапа 2; замер прогона плана на копии репозитория). Цифры записать в отчёт задачи.

- [ ] **Step 6: Скриншот browser mode**

«Процедура скриншота», сценарий:
```js
await p.getByRole('button', { name: /Off-Topic/ }).click()
const post = p.locator('article', { hasText: 'Welcome to off-topic' })
await post.getByRole('button', { name: '👍 2' }).waitFor()
await post.locator('img[src*="/emoji/partyparrot"]').waitFor()
await p.screenshot({ path: 'T/t11-before.png' })
await post.getByRole('button', { name: '👍 2' }).click()
await post.getByRole('button', { name: '👍 3, you reacted' }).waitFor()
await test('fake/react', { channel_id: 'c-offtopic', message: 'Welcome to off-topic', username: 'bob', emoji: 'avocado' })
await post.getByRole('button', { name: '🥑 1' }).waitFor()
await p.screenshot({ path: 'T/t11-after.png' })
await post.getByRole('button', { name: '👍 3, you reacted' }).click()
await post.getByRole('button', { name: '👍 2' }).waitFor()
return 'ok'
```
Expected: `t11-before.png` — у поста чипы 👍 2, 🎉 1 и картинка partyparrot с 1; `t11-after.png` — 👍 3 выделен рамкой акцента, появился 🥑 1 (пришёл по WS от bob). Процесс остановить, контексты закрыты.

- [ ] **Step 7: Commit**

```bash
git add scripts/gen-emoji.mjs frontend
git commit -m "ui: Mattermost's emoji set as a lazy chunk, reaction chips toggle our reaction, custom emoji pictures via media"
```

### Task 12: UI — пикер эмодзи

**Files:**
- Create: `frontend/src/components/EmojiPicker.tsx`, `frontend/src/components/EmojiPicker.test.tsx`
- Modify: `frontend/src/components/PostItem.tsx` (кнопка «Добавить реакцию», «☺+» после чипов, пикер в портале, `PostActions.emojiInfo`), `frontend/src/components/PostItem.test.tsx`, `frontend/src/components/Feed.test.tsx`, `frontend/src/components/ChannelPane.tsx`
- Modify: `frontend/src/i18n.ts` (`picker.*`)

**Interfaces:**
- Consumes: Task 11 (`useEmojiIndex`, `searchEmoji`, `CATEGORY_IDS`, `Reactions.onAdd(trigger: HTMLElement)`, `chat.emojiInfo`, `EmojiDTO`), Task 6 (`mediaURL`).
- Produces:
  - `EmojiPicker` — **default export** (для `React.lazy`): `({ serverId, anchor: DOMRect, info: EmojiDTO | null, onPick(name: string), onClose() })`; `placePicker(anchor, vw, vh): { left, top }`; `COLS = 9`.
  - `PostActions.emojiInfo(): Promise<EmojiDTO>`.

Поведение: окно 360×380 под кнопкой (над ней, если снизу нет места), в пределах экрана; строка поиска в фокусе. Без запроса — «Недавние» (из `EmojiInfo.recent`, самые частые первыми), 9 категорий Mattermost, «Эмодзи сервера» (кастомные; только при `custom_enabled`); с запросом — результаты (`searchEmoji`: точное имя, затем начало, затем вхождение) и подходящие кастомные. Клавиатура: `↓` из поиска — к первому эмодзи, стрелки — по строкам и столбцам сетки (строки нумеруются сквозь разделы), `↑` из первой строки — обратно в поиск, `Enter` в поиске — первый результат, `Enter`/пробел на эмодзи — выбрать, `Esc` — закрыть; клик мимо — закрыть. Выбор эмодзи, который уже стоит от меня, ничего не отправляет. Пикер рендерится **в портал `document.body`**: строки ленты сдвинуты `transform`, а `position: fixed` внутри трансформированного предка позиционируется от него и обрезается прокруткой ленты. Пикер и набор эмодзи — ленивые чанки: основной бандл не растёт.

- [ ] **Step 1: Падающие тесты**

`frontend/src/components/EmojiPicker.test.tsx`:
```tsx
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { setLocale } from '../i18n'
import EmojiPicker, { placePicker } from './EmojiPicker'

const anchor = { top: 100, bottom: 120, left: 500, right: 520, width: 20, height: 20, x: 500, y: 100, toJSON() {} } as DOMRect

beforeEach(() => setLocale('en'))

test('recent, standard and custom sections; custom emoji are server pictures', async () => {
  render(<EmojiPicker serverId={2} anchor={anchor} info={{ recent: ['tada', 'partyparrot', 'gone_custom'], custom: ['partyparrot'], custom_enabled: true }} onPick={vi.fn()} onClose={vi.fn()} />)
  const recent = await screen.findByRole('region', { name: 'Recently used' })
  expect(within(recent).getAllByRole('button').map((b) => b.getAttribute('aria-label'))).toEqual([':tada:', ':partyparrot:'])
  expect(within(recent).getByRole('button', { name: ':partyparrot:' }).querySelector('img')).toHaveAttribute('src', '/media/2/emoji/partyparrot')
  expect(screen.getByRole('region', { name: 'Smileys & Emotion' })).toBeInTheDocument()
  expect(within(screen.getByRole('region', { name: 'Custom' })).getAllByRole('button')).toHaveLength(1)
  expect(screen.getByRole('textbox', { name: 'Search emoji' })).toHaveFocus()
})

test('search and keyboard: arrows through results, Enter picks, Escape closes', async () => {
  const onPick = vi.fn()
  const onClose = vi.fn()
  render(<EmojiPicker serverId={1} anchor={anchor} info={{ recent: [], custom: [], custom_enabled: false }} onPick={onPick} onClose={onClose} />)
  await screen.findByRole('region', { name: 'Smileys & Emotion' })
  await userEvent.type(screen.getByRole('textbox', { name: 'Search emoji' }), 'thumbs')
  await userEvent.keyboard('{ArrowDown}')
  expect(screen.getByRole('button', { name: ':+1:' })).toHaveFocus()
  await userEvent.keyboard('{ArrowRight}')
  expect(screen.getByRole('button', { name: ':-1:' })).toHaveFocus()
  await userEvent.keyboard('{ArrowUp}')
  expect(screen.getByRole('textbox', { name: 'Search emoji' })).toHaveFocus()
  await userEvent.keyboard('{ArrowDown}{ArrowRight}{Enter}')
  expect(onPick).toHaveBeenCalledWith('-1')
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalled()
})

test('Enter in the search box picks the first result; nothing found says so', async () => {
  const onPick = vi.fn()
  render(<EmojiPicker serverId={1} anchor={anchor} info={null} onPick={onPick} onClose={vi.fn()} />)
  await screen.findByRole('region', { name: 'Smileys & Emotion' })
  const box = screen.getByRole('textbox', { name: 'Search emoji' })
  await userEvent.type(box, 'zzzqqq')
  expect(screen.getByText('No emoji found')).toBeInTheDocument()
  await userEvent.clear(box)
  await userEvent.type(box, 'rocket{Enter}')
  expect(onPick).toHaveBeenCalledWith('rocket')
})

test('the picker opens below its button, or above when there is no room', () => {
  expect(placePicker({ top: 100, bottom: 120, right: 520 }, 1400, 900)).toEqual({ left: 160, top: 124 })
  expect(placePicker({ top: 800, bottom: 820, right: 50 }, 1400, 900)).toEqual({ left: 8, top: 416 })
})
```

`frontend/src/components/PostItem.test.tsx`:
- в `actions()` добавить `emojiInfo: vi.fn().mockResolvedValue({ recent: [], custom: [], custom_enabled: false }),`;
- новый тест (импорт `within` из `@testing-library/react`):
  ```tsx
  test('the reaction button opens the picker; picking reacts, one already ours is not sent again', async () => {
    const a = actions()
    a.emojiInfo = vi.fn().mockResolvedValue({ recent: ['tada'], custom: [], custom_enabled: false })
    render(<PostItem serverId={1} post={post({ reactions: [{ emoji: 'tada', count: 1, mine: true }] })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
    await userEvent.click(screen.getAllByRole('button', { name: 'Add reaction' })[0])
    const dialog = await screen.findByRole('dialog', { name: 'Emoji picker' })
    expect(a.emojiInfo).toHaveBeenCalled()
    await userEvent.click(await within(dialog).findByRole('button', { name: ':rocket:' }))
    expect(a.react).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }), 'rocket', true)
    expect(screen.queryByRole('dialog')).toBeNull()

    await userEvent.click(screen.getAllByRole('button', { name: 'Add reaction' })[0])
    const again = await screen.findByRole('dialog', { name: 'Emoji picker' })
    const recent = await within(again).findByRole('region', { name: 'Recently used' })
    await userEvent.click(within(recent).getByRole('button', { name: ':tada:' }))
    expect(a.react).toHaveBeenCalledTimes(1)
  })

  test('system and pending posts offer no reaction button', () => {
    const { rerender } = render(<PostItem serverId={1} post={post({ system: true })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
    expect(screen.queryByRole('button', { name: 'Add reaction' })).toBeNull()
    rerender(<PostItem serverId={1} post={post({ pending: true })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
    expect(screen.queryByRole('button', { name: 'Add reaction' })).toBeNull()
  })
  ```

`frontend/src/components/Feed.test.tsx`: в `actions` — `emojiInfo: vi.fn().mockResolvedValue({ recent: [], custom: [], custom_enabled: false }),`.

Run: `cd frontend && pnpm test src/components/EmojiPicker.test.tsx src/components/PostItem.test.tsx`
Expected: FAIL — нет `./EmojiPicker`, нет кнопки «Add reaction».

- [ ] **Step 2: Реализация — пикер**

`frontend/src/components/EmojiPicker.tsx`:
```tsx
import { useEffect, useMemo, useRef, useState } from 'react'
import type { EmojiDTO } from '../api/types'
import { searchEmoji, useEmojiIndex } from '../emoji'
import { t, type I18nKey } from '../i18n'
import { mediaURL } from '../media'

export const COLS = 9
const W = 360
const H = 380

interface Props {
  serverId: number
  anchor: DOMRect
  info: EmojiDTO | null // recent and custom; null while loading
  onPick(name: string): void
  onClose(): void
}

interface Item {
  name: string
  char?: string // absent: a custom emoji, drawn from /media/
}

interface Cell extends Item {
  row: number
  col: number
}

interface Section {
  id: string
  title: string
  cells: Cell[]
}

// placePicker puts the picker under its button, or above it when there is
// no room below, always inside the viewport.
export function placePicker(anchor: Pick<DOMRect, 'top' | 'bottom' | 'right'>, vw: number, vh: number) {
  const left = Math.max(8, Math.min(anchor.right - W, vw - W - 8))
  const below = anchor.bottom + 4
  const top = below + H <= vh - 8 ? below : Math.max(8, anchor.top - H - 4)
  return { left, top }
}

// layout numbers rows through all sections, so arrow keys move by the
// grid's visible rows and columns; empty sections are dropped.
function layout(sections: { id: string; title: string; items: Item[] }[]): Section[] {
  let row = 0
  return sections
    .filter((s) => s.items.length > 0)
    .map((s) => {
      const cells = s.items.map((it, i) => ({ ...it, row: row + Math.floor(i / COLS), col: i % COLS }))
      row += Math.ceil(s.items.length / COLS)
      return { id: s.id, title: s.title, cells }
    })
}

export default function EmojiPicker({ serverId, anchor, info, onPick, onClose }: Props) {
  const idx = useEmojiIndex()
  const [query, setQuery] = useState('')
  const root = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) onClose()
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [onClose])

  const sections = useMemo(() => {
    if (!idx) return []
    const custom = info?.custom_enabled ? info.custom : []
    const q = query.trim().toLowerCase()
    if (q) {
      return layout([{
        id: 'results', title: '',
        items: [
          ...searchEmoji(idx, q).map((e) => ({ name: e.names[0], char: e.char })),
          ...custom.filter((n) => n.toLowerCase().includes(q)).map((name) => ({ name })),
        ],
      }])
    }
    const recent: Item[] = []
    for (const n of info?.recent ?? []) {
      const e = idx.byName.get(n)
      if (e) recent.push({ name: n, char: e.char })
      else if (custom.includes(n)) recent.push({ name: n })
    }
    return layout([
      { id: 'recent', title: t('picker.recent'), items: recent },
      ...idx.categories.map((c) => ({
        id: c.id, title: t(`picker.cat.${c.id}` as I18nKey), items: c.emojis.map((e) => ({ name: e.names[0], char: e.char })),
      })),
      { id: 'custom', title: t('picker.custom'), items: custom.map((name) => ({ name })) },
    ])
  }, [idx, info, query])

  const cellAt = (row: number, col: number) => root.current?.querySelector<HTMLButtonElement>(`[data-row="${row}"][data-col="${col}"]`) ?? null
  const rowCells = (row: number) => [...(root.current?.querySelectorAll<HTMLButtonElement>(`[data-row="${row}"]`) ?? [])]

  const onGridKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const el = e.target as HTMLElement
    if (el.dataset.row === undefined) return
    const row = Number(el.dataset.row)
    const col = Number(el.dataset.col)
    let next: HTMLElement | null | undefined
    switch (e.key) {
      case 'ArrowRight':
        next = cellAt(row, col + 1) ?? rowCells(row + 1)[0]
        break
      case 'ArrowLeft':
        next = col > 0 ? cellAt(row, col - 1) : rowCells(row - 1).at(-1)
        break
      case 'ArrowDown': {
        const r = rowCells(row + 1)
        next = r[Math.min(col, r.length - 1)]
        break
      }
      case 'ArrowUp': {
        const r = rowCells(row - 1)
        next = row === 0 ? input.current : r[Math.min(col, r.length - 1)]
        break
      }
      default:
        return
    }
    e.preventDefault()
    next?.focus()
  }

  const pos = placePicker(anchor, window.innerWidth, window.innerHeight)
  return (
    <div
      ref={root}
      role="dialog"
      aria-label={t('picker.label')}
      className="fixed z-50 flex flex-col rounded-lg border border-line bg-panel text-fg shadow-xl"
      style={{ left: pos.left, top: pos.top, width: W, height: H }}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.preventDefault()
          e.stopPropagation()
          onClose()
        }
      }}
    >
      <input
        ref={input}
        autoFocus
        aria-label={t('picker.search')}
        placeholder={t('picker.search')}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') {
            e.preventDefault()
            rowCells(0)[0]?.focus()
          } else if (e.key === 'Enter') {
            e.preventDefault()
            const first = sections[0]?.cells[0]
            if (first) onPick(first.name)
          }
        }}
        className="m-2 rounded border border-line bg-app px-2 py-1 text-sm text-fg focus:border-accent focus:outline-none"
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2" onKeyDown={onGridKey}>
        {!idx && <p className="p-2 text-xs text-fg-muted">{t('picker.loading')}</p>}
        {idx && sections.length === 0 && <p className="p-2 text-xs text-fg-muted">{t('picker.empty')}</p>}
        {sections.map((s) => (
          <section key={s.id} aria-label={s.title || t('picker.results')}>
            {s.title && <h3 className="sticky top-0 bg-panel py-1 text-xs font-semibold text-fg-muted">{s.title}</h3>}
            <div className="grid grid-cols-9 gap-0.5">
              {s.cells.map((c) => (
                <button
                  key={`${s.id}:${c.name}`}
                  type="button"
                  data-row={c.row}
                  data-col={c.col}
                  tabIndex={c.row === 0 && c.col === 0 ? 0 : -1}
                  aria-label={`:${c.name}:`}
                  title={`:${c.name}:`}
                  onClick={() => onPick(c.name)}
                  className="flex h-9 w-9 items-center justify-center rounded text-xl hover:bg-hover focus:bg-hover focus:outline-none"
                >
                  {c.char ?? <img src={mediaURL(serverId, 'emoji', c.name)} alt="" width={24} height={24} loading="lazy" />}
                </button>
              ))}
            </div>
          </section>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Реализация — пост и канал**

`frontend/src/components/PostItem.tsx`:
- импорты:
  ```tsx
  import { lazy, memo, Suspense, useRef, useState } from 'react'
  import { createPortal } from 'react-dom'
  import type { Attachment, EmojiDTO, FileView, PostView } from '../api/types'
  ```
  и после импортов — `const EmojiPicker = lazy(() => import('./EmojiPicker'))`;
- в `PostActions` — `emojiInfo(): Promise<EmojiDTO>`;
- `ToolButton` принимает событие: `onClick(e: React.MouseEvent<HTMLButtonElement>): void` (существующие вызовы `() => …` совместимы);
- в начало тела `PostItem`:
  ```tsx
    const [picker, setPicker] = useState<{ anchor: DOMRect; info: EmojiDTO | null } | null>(null)
    const trigger = useRef<HTMLElement | null>(null)
    const canReact = !post.system && !post.pending && !post.failed
    const openPicker = (el: HTMLElement) => {
      trigger.current = el
      setPicker({ anchor: el.getBoundingClientRect(), info: null })
      actions.emojiInfo().then(
        (info) => setPicker((p) => (p ? { ...p, info } : p)),
        () => {},
      )
    }
    const closePicker = () => {
      setPicker(null)
      trigger.current?.focus()
    }
    const pick = (name: string) => {
      closePicker()
      if (!post.reactions?.some((r) => r.emoji === name && r.mine)) actions.react(post, name, true)
    }
  ```
- у `Reactions` — `onAdd={canReact ? openPicker : undefined}`;
- в панели действий первой кнопкой:
  ```tsx
            {canReact && (
              <ToolButton label={t('reaction.add')} onClick={(e) => openPicker(e.currentTarget)}>
                ☺
              </ToolButton>
            )}
  ```
- перед закрывающим `</article>`:
  ```tsx
        {picker &&
          createPortal(
            <Suspense fallback={null}>
              <EmojiPicker serverId={serverId} anchor={picker.anchor} info={picker.info} onPick={pick} onClose={closePicker} />
            </Suspense>,
            document.body,
          )}
  ```

`frontend/src/components/ChannelPane.tsx`: импорт `emojiInfo` из `../chat`; в `actions` — `emojiInfo: () => emojiInfo(server.id),`.

`frontend/src/i18n.ts` — в `ru`:
```ts
  'picker.label': 'Выбор эмодзи',
  'picker.search': 'Поиск эмодзи',
  'picker.recent': 'Недавние',
  'picker.custom': 'Эмодзи сервера',
  'picker.results': 'Результаты',
  'picker.empty': 'Ничего не найдено',
  'picker.loading': 'Загрузка…',
  'picker.cat.smileys': 'Смайлы и эмоции',
  'picker.cat.people': 'Люди и тело',
  'picker.cat.nature': 'Животные и природа',
  'picker.cat.food': 'Еда и напитки',
  'picker.cat.travel': 'Путешествия и места',
  'picker.cat.activities': 'Занятия',
  'picker.cat.objects': 'Предметы',
  'picker.cat.symbols': 'Символы',
  'picker.cat.flags': 'Флаги',
```
в `en`:
```ts
  'picker.label': 'Emoji picker',
  'picker.search': 'Search emoji',
  'picker.recent': 'Recently used',
  'picker.custom': 'Custom',
  'picker.results': 'Results',
  'picker.empty': 'No emoji found',
  'picker.loading': 'Loading…',
  'picker.cat.smileys': 'Smileys & Emotion',
  'picker.cat.people': 'People & Body',
  'picker.cat.nature': 'Animals & Nature',
  'picker.cat.food': 'Food & Drink',
  'picker.cat.travel': 'Travel & Places',
  'picker.cat.activities': 'Activities',
  'picker.cat.objects': 'Objects',
  'picker.cat.symbols': 'Symbols',
  'picker.cat.flags': 'Flags',
```

- [ ] **Step 4: Тесты, линт, бандл**

Run: `cd frontend && pnpm test && pnpm lint && pnpm build && for f in dist/assets/*.js; do echo "$f $(gzip -9c $f | wc -c)"; done`
Expected: всё PASS; появился чанк `EmojiPicker-*.js` (~2 КБ gzip); основной чанк почти не изменился относительно Task 11.

- [ ] **Step 5: Скриншоты browser mode**

«Процедура скриншота», сценарий:
```js
await p.getByRole('button', { name: /Off-Topic/ }).click()
const post = p.locator('article', { hasText: 'Welcome to off-topic' })
await post.hover()
await post.getByRole('toolbar').getByRole('button', { name: 'Add reaction' }).click()
const picker = p.getByRole('dialog', { name: 'Emoji picker' })
await picker.getByRole('region', { name: 'Smileys & Emotion' }).waitFor()
await p.screenshot({ path: 'T/t12-picker.png' })
await p.keyboard.type('rock')
await p.screenshot({ path: 'T/t12-search.png' })
await p.keyboard.press('Enter')
await post.getByRole('button', { name: '🚀 1, you reacted' }).waitFor()
await post.getByRole('button', { name: 'Add reaction' }).first().click()
await picker.getByRole('region', { name: 'Recently used' }).getByRole('button', { name: ':rocket:' }).waitFor()
const box = await picker.boundingBox()
await p.screenshot({ path: 'T/t12-recent.png' })
await p.keyboard.press('Escape')
await picker.waitFor({ state: 'detached' })
return box
```
Expected: `t12-picker.png` — тёмное окно пикера под кнопкой, не обрезано лентой: поиск, «Smileys & Emotion» сеткой 9 колонок, внизу раздел «Custom» с partyparrot; `t12-search.png` — 🚀 первым; после Enter у поста чип 🚀 1 с рамкой; `t12-recent.png` — «Recently used» с 🚀. `box` — целиком в пределах 1400×900. Процесс остановить, контексты закрыты.

- [ ] **Step 6: Commit**

```bash
git add frontend
git commit -m "ui: emoji picker with search, recent and custom emoji, keyboard navigation, rendered in a portal"
```

### Task 13: E2E, скриншоты, память, документация, бэклог, живая проверка

**Files:**
- Create: `tests/e2e/media.spec.ts`
- Modify: `tests/e2e/chat.spec.ts` (имя строки DM теперь «bob, <статус>»), `tests/e2e/playwright.config.ts` (`SPK_MATTERMOST_DOWNLOADS`)
- Modify: `docs/specs/2026-09-24-spk-mattermost-design.md`, `AGENTS.md`, `README.md`, `docs/backlog.md`, `docs/spikes/2026-09-24-stage1-spikes.md` (S4, «Этап 3»)

**Interfaces:**
- Consumes: всё из Tasks 1–12; test-API: `fake/status`, `fake/picture`, `fake/react`, `opened-files` (Tasks 2, 7, 9); сид Off-Topic (Tasks 2, 9).
- Produces: e2e-покрытие аватаров, статусов, превью, просмотрщика, скачивания/открытия и реакций; замеры памяти этапа 3; документация, совпадающая с кодом; бэклог с тем, что не вошло.

Как и в этапе 2: один экземпляр приложения и один фейк на прогон, состояние фейка копится между тестами — каждый тест возвращает то, что поменял (статус bob, свои реакции), и опирается на сид, а не на счётчики «с нуля».

- [ ] **Step 1: Сценарии**

`tests/e2e/playwright.config.ts`: в `webServer.env` добавить `SPK_MATTERMOST_DOWNLOADS: join(home, 'downloads'),`.

`tests/e2e/chat.spec.ts`: в первом тесте регулярку DM `/^@\s*bob$/` заменить на `/^bob(,|$)/` (строка DM теперь — аватар + «bob, <статус>»; группа «👥 alice, bob, carol» с `bob` не начинается).

`tests/e2e/media.spec.ts`:
```ts
import { expect, test } from '@playwright/test'
import { channel, feed, removeServerFromMenu, signInAlice, testGet, testPost } from './helpers'

const naturalWidth = (img: import('@playwright/test').Locator) => img.evaluate((i: HTMLImageElement) => i.naturalWidth)

test('avatars and presence in the sidebar and the feed', async ({ page }) => {
  await signInAlice(page)
  const bob = channel(page, /^bob, Online$/)
  await expect(bob).toBeVisible()
  const avatar = bob.locator('img')
  await expect(avatar).toHaveAttribute('src', /^\/media\/\d+\/avatar\/u-bob\?v=/)
  await expect.poll(() => naturalWidth(avatar)).toBeGreaterThan(0)
  await expect(feed(page).locator('article img[src*="/avatar/u-carol"]').first()).toBeVisible()

  await testPost(page, 'fake/status', { username: 'bob', status: 'dnd' })
  await channel(page, /Off-Topic/).click() // opening a channel polls statuses
  await expect(channel(page, /^bob, Do not disturb$/)).toBeVisible()
  await testPost(page, 'fake/status', { username: 'bob', status: 'online' })

  // the row's name follows bob's status: find it by the name's start from here on
  const picture = channel(page, /^bob,/).locator('img')
  const before = await picture.getAttribute('src')
  await testPost(page, 'fake/picture', { username: 'bob' })
  await expect(picture).not.toHaveAttribute('src', before!)
  await removeServerFromMenu(page)
})

test('image preview, viewer, text snippet, download and open', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  const shot = feed(page).getByRole('button', { name: 'View build.png' })
  await expect(shot).toBeVisible()
  const img = shot.locator('img')
  await expect(img).toHaveAttribute('width', '480')
  await expect(img).toHaveAttribute('height', '270')
  await expect.poll(() => naturalWidth(img)).toBe(960)
  await expect(feed(page).getByRole('button', { name: 'View flow.png' }).locator('img')).toHaveAttribute('src', /\/thumb\/f-diag1$/)
  await expect(feed(page).getByText('request #1 handled', { exact: false })).toBeVisible()

  await shot.click()
  const viewer = page.getByRole('dialog', { name: 'File viewer' })
  await expect(viewer.getByRole('img', { name: 'build.png' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(viewer).toHaveCount(0)

  await feed(page).getByRole('button', { name: 'Download spec.pdf' }).click()
  await expect(page.getByText(/Saved to .*spec\.pdf/)).toBeVisible()
  await feed(page).getByRole('button', { name: 'Open server.log' }).click()
  await expect
    .poll(async () => ((await testGet(page, 'opened-files')) as string[]).some((p) => p.endsWith('server.log')))
    .toBe(true)
  await removeServerFromMenu(page)
})

test('reactions: chips toggle, the picker adds, others arrive live', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  const post = feed(page).locator('article', { hasText: 'Welcome to off-topic' })
  await post.getByRole('button', { name: '👍 2' }).click()
  await expect(post.getByRole('button', { name: '👍 3, you reacted' })).toBeVisible()
  await post.getByRole('button', { name: '👍 3, you reacted' }).click()
  await expect(post.getByRole('button', { name: '👍 2' })).toBeVisible()

  await post.hover()
  await post.getByRole('toolbar').getByRole('button', { name: 'Add reaction' }).click()
  const picker = page.getByRole('dialog', { name: 'Emoji picker' })
  await picker.getByRole('textbox', { name: 'Search emoji' }).fill('avocado')
  await page.keyboard.press('Enter')
  await expect(picker).toHaveCount(0)
  await expect(post.getByRole('button', { name: '🥑 1, you reacted' })).toBeVisible()

  await testPost(page, 'fake/react', { channel_id: 'c-offtopic', message: 'Welcome to off-topic', username: 'bob', emoji: 'fire' })
  await expect(post.getByRole('button', { name: '🔥 1' })).toBeVisible()
  await expect(post.locator('img[src*="/emoji/partyparrot"]')).toBeVisible()

  // the fake is shared by all tests: put things back
  await post.getByRole('button', { name: '🥑 1, you reacted' }).click()
  await expect(post.getByRole('button', { name: /🥑/ })).toHaveCount(0)
  await testPost(page, 'fake/react', { channel_id: 'c-offtopic', message: 'Welcome to off-topic', username: 'bob', emoji: 'fire', remove: true })
  await expect(post.getByRole('button', { name: /🔥/ })).toHaveCount(0)
  await removeServerFromMenu(page)
})
```

- [ ] **Step 2: Прогон e2e**

Run: `make test-e2e`
Expected: все спеки (login, chat, reconnect, media) — PASS. Упавший сценарий разбирать по trace (`pnpm exec playwright show-trace`), таймауты вслепую не подбирать.

- [ ] **Step 3: Полный набор ворот**

Run: `make lint && golangci-lint run --build-tags "wails gtk3" && make test-go && make test-front && make build-desktop && make cross-check`
Expected: всё зелёное.

- [ ] **Step 4: Итоговые скриншоты (browser mode и desktop)**

1. «Процедура скриншота», сценарий — полный путь пользователя: Town Square (аватары, точки) → Off-Topic (картинка, миниатюры, лог, карточка, чипы с partyparrot) → просмотрщик → пикер с поиском. Снимки `T/t13-*.png`; посмотреть каждый.
2. Desktop dev-режим с фейком:
   ```bash
   T=$(mktemp -d -p /home/spk/.spk/sawe/ss/Mattermost/.agents/tmp)
   SPK_MATTERMOST_HOME=$T build/bin/spk-mattermost-desktop --mm-fake > $T/log 2>&1 & echo $! > $T/pid
   for i in $(seq 1 30); do wmctrl -l | grep -q spk-mattermost && break; sleep 1; done
   W=$(wmctrl -l | awk '/spk-mattermost/{print $1; exit}'); sleep 3; import -window "$W" $T/t13-desktop-town.png
   ```
   По снимку найти строку Off-Topic в сайдбаре, кликнуть (`xdotool windowactivate --sync $W mousemove --window $W <x> <y> click 1`), через 2 с `import -window "$W" $T/t13-desktop-off.png`, затем `kill $(cat $T/pid)`. Ожидается: картинка, миниатюры, фрагмент лога и чипы с partyparrot на месте (пришли через `wails://localhost/media/…`), в `$T/log` нет предупреждений `media`.

- [ ] **Step 5: Память**

Ориентир и правило — «Global Constraints» (Private_Dirty ~150 МБ — ориентир, не предел; обязательно — без роста).
1. Browser mode, 100 каналов (порт `P` — свободный, см. «Процедуру скриншота»): `SPK_MATTERMOST_HOME=$T build/bin/spk-mattermost --browser --port $P --mm-fake --mm-fake-channels 100 --test-api &`; войти alice:
   ```bash
   TOKEN=$(curl -s http://127.0.0.1:$P/ | sed -n 's/.*api-token" content="\([^"]*\)".*/\1/p')
   FAKE=$(curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:$P/api/_test/fake-url | sed -n 's/.*"url":"\([^"]*\)".*/\1/p')
   ID=$(curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Origin: http://127.0.0.1:$P" -d "{\"url\":\"$FAKE\"}" http://127.0.0.1:$P/api/AddServer | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
   curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Origin: http://127.0.0.1:$P" -d "{\"id\":$ID,\"login\":\"alice\",\"password\":\"secret\"}" http://127.0.0.1:$P/api/LoginWithPassword
   ```
   дождаться предзагрузки (`GetChannel` по каналам → `loaded && !syncing`); `scripts/pss.sh <pid Go-процесса>` — сравнить с «Этап 2, задача 16» (S4): Go-часть не должна вырасти больше чем на несколько МБ (новое — статусы, эмодзи сервера, индекс кэша).
2. Desktop dev-режим, 3 фейка по 100 каналов, churn 2 с (как в этапе 2: `--mm-fake --mm-fake-servers 3 --mm-fake-channels 100 --mm-fake-churn 2s`): открыть Off-Topic, 20 раз открыть и закрыть просмотрщик `build.png` (координаты картинки — со снимка окна: `for i in $(seq 20); do xdotool mousemove --window $W <x> <y> click 1; sleep 0.5; xdotool key Escape; sleep 0.5; done`), открыть пикер; `scripts/pss.sh <pid>` сразу и через 30 минут. Ожидается: без устойчивого роста; разница с этапом 2 — в пределах того, что стоят декодированные картинки на экране (единицы МБ). Если ориентир заметно превышен — разобрать, откуда (WebKit web process vs Go), и записать; удобство не жертвовать.
3. Размер `$T/media` после сценария — единицы МБ; лимит кэша 256 МиБ не достигнут.
4. Итог — в `docs/spikes/2026-09-24-stage1-spikes.md`, S4, подраздел «Этап 3, часть 1» (что мерили, цифры, вывод).

- [ ] **Step 6: Живая проверка на mm.citeck.ru вместе с пользователем — выполняет контроллер**

Реализатор задачи этот шаг пропускает (как Step 5 Task 16 этапа 2): контроллер после шагов 1–5 и 7 собирает `make build-desktop` и просит пользователя перезапустить его dev-клиент (PID в `/tmp/spkmm-live.pid`; самим не убивать, официальный клиент `/opt/Mattermost` не трогать). Сценарий для пользователя и что смотреть в логе (`slog` в stderr — предупреждения `media`, `reaction refused`, `statuses unavailable`) и памяти:
- аватары в ленте и у DM совпадают с официальным веб-клиентом; точки статусов (свой и собеседников) — те же, смена статуса собеседника видна в течение минуты или сразу при открытии канала;
- картинки в реальных каналах: одна — крупно, несколько — миниатюрами, лента не прыгает при догрузке; просмотрщик (Esc, ←/→); текстовые вложения (`.log`, `.json`, `.yaml`) — фрагмент и просмотр; `.md` — карточкой;
- «Скачать» кладёт файл в `~/Загрузки` (или что указано в `user-dirs.dirs`), «Открыть» открывает PDF/картинку системным приложением;
- реакции: клик по чипу ставит/снимает (видно в официальном клиенте), пикер с поиском, «Недавние» совпадают с официальным клиентом, кастомные эмодзи сервера видны и ставятся;
- через час работы — `scripts/pss.sh <pid>`: без роста (ориентир ~150 МБ).
Найденные проблемы — чинить в рамках этапа (TDD, отдельные коммиты), мелочи вне объёма — в `docs/backlog.md`.

- [ ] **Step 7: Документация и бэклог**

- `docs/specs/…`: статус — «Этап 3, часть 1 (аватары и статусы, превью картинок и текста, реакции) реализована и проверена e2e (дата)»; в «Архитектура» уточнить: `/media/…` обслуживает `internal/media` — в desktop в asset handler Wails, в browser mode — за cookie `spk_media`; дисковый кэш с лимитом 256 МиБ; в «Хранение» — статусы присутствия не пишутся в снимок; в «Этапы», п. 3 — отметить сделанное и оставшееся (видео/аудио/markdown-превью, треды, поиск, загрузка файлов — после согласования с пользователем).
- `AGENTS.md`, «Правила» (правило — причина — тест):
  - картинки и фрагменты файлов UI берёт только с `/media/<srv>/<kind>/<key>`; путь к API строится из проверенных ключей, открытого прокси нет — `internal/media` (`TestBadRequests`);
  - browser mode: `/media/` — за cookie `spk_media` (HttpOnly, SameSite=Strict) или bearer — `TestMediaNeedsThePageCookie`;
  - SVG и не-растровые типы не отображаются, размер/пиксели ограничены — `TestSVGIsRefused`, `TestHugeDimensionsAreRefused`;
  - рамка картинки и текстового фрагмента имеет окончательный размер до загрузки (лента со скролл-якорем не прыгает) — `Attachments.test`;
  - чужие статусы приходят только опросом (`status_change` — только о себе) — `TestStatusesArePolledForDMPartnersAndOpenChannelAuthors`;
  - реакция применяется сразу и откатывается при отказе, клики во время запроса — «последний побеждает», устаревшее эхо своего клика отбрасывается — `TestLateEchoOfUndoneReactionIsIgnored`, `TestReactionRefusedIsRolledBack`, `TestReactionClicksWhileInFlightAreQueuedLastWins`;
  - «Открыть» не запускает `.desktop`/скрипты/исполняемые — `TestOpenFileOpensSafeTypesOnly`.
- `AGENTS.md`, «Things that bite»: `position: fixed` внутри строки ленты (у неё `transform`) позиционируется от строки и обрезается — всплывающее рендерить порталом в `document.body` (`PostItem` → `EmojiPicker`); `nil *media.Cache` в `http.Handler` — не nil-интерфейс (`run_desktop_wails.go`); два шаблона ServeMux `/emoji/name/{name}` и `/emoji/{id}/image` конфликтуют (паника при регистрации) — один шаблон `/emoji/{a}/{b}` в фейке; реальный `POST /users/status/ids` отвергает весь запрос, если хоть один id не 26 символов (фейк мягче); набор эмодзи — `scripts/gen-emoji.mjs` (источники и команда — в шапке скрипта), `frontend/src/emoji/data.ts` руками не править; новые test-API (`fake/status`, `fake/picture`, `fake/react`, `opened-files`) и `SPK_MATTERMOST_DOWNLOADS`.
- `README.md`: что умеет клиент после этапа 3, ч. 1 (аватары, статусы, превью, просмотрщик, скачивание/открытие, реакции и пикер), каталог загрузок и `SPK_MATTERMOST_DOWNLOADS`, дисковый кэш `media/` (≤ 256 МиБ) в каталоге данных.
- `docs/backlog.md` — новый раздел «Этап 3, не вошло в часть 1»:
  - превью видео, аудио и markdown-файлов (решение пользователя — «в todo»);
  - выбор оттенка кожи в пикере; имена отреагировавших во всплывающей подсказке чипа; число участников у GM в сайдбаре (нужна статистика канала); иконки вебхуков (`override_icon_url` — внешний URL);
  - уменьшенные превью в Go для не-JPEG/PNG (WebP/BMP идут как есть) и EXIF-поворот оригиналов без серверного превью;
  - и отдельной строкой — «Позже, после согласования с пользователем: треды (CRT, панель), поиск, загрузка файлов, автодополнение `@ ~ :`, Ctrl+K».

- [ ] **Step 8: Commit**

```bash
git add tests/e2e docs AGENTS.md README.md
git commit -m "e2e: avatars, previews, downloads and reactions; docs: stage 3 part 1 status, media rules, memory numbers, backlog"
```
Готово, когда: все ворота зелёные, e2e проходят, замеры памяти записаны, документация совпадает с кодом, `git status` чистый; после Step 6 (контроллер с пользователем) — пуш в `main`.
