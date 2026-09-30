# spk-mm-client — гид для агентов

Лёгкий десктопный клиент Mattermost (Wails v3 + React). Спецификация:
`docs/specs/2026-09-24-spk-mattermost-design.md`. Планы: `docs/plans/`.
Результаты спайков: `docs/spikes/`.

## Переименование (Task 0, 2026-09-25)

Проект был переименован из spk-mattermost в **spk-mm-client** по запросу
пользователя: Go-модуль `github.com/spk/spk-mm-client`, каталог
`cmd/spk-mm-client`, бинарники `spk-mm-client`/`spk-mm-client-desktop`/
`spk-mm-client-release`, переменные окружения `SPK_MM_CLIENT_*`, app id
`ru.spk.spk-mm-client`. Каталог данных стал `~/.spk/mm-client` (короче, чем
`~/.spk/spk-mm-client`, — он и так уже под `.spk`); `internal/paths.Resolve`
один раз переносит `~/.spk/spk-mattermost` в `~/.spk/mm-client`
(`os.Rename`, только для дефолтного расположения, ошибки не блокируют
старт — см. `internal/paths/paths.go`, `migrate`). Имя файла спецификации
(`docs/specs/2026-09-24-spk-mattermost-design.md`) и завершённые планы
(`docs/plans/2026-09-24-stage1-skeleton.md`,
`docs/plans/2026-09-24-stage2-chat-core.md`) намеренно не переименованы —
это история; читать в них старые имена как новые.

## Сборка и тесты

- `make build` — фронт + бинарь для browser-режима (`build/bin/spk-mm-client`).
- `make build-desktop` — desktop-бинарь (теги `wails gtk3`); собирает во временный файл в
  `build/bin` и атомарно переносит (`mv`) на место — уже запущенный экземпляр держит старый
  (отвязанный) inode, пока сам не завершится, а неудачная сборка не трогает существующий бинарь.
- `make run` — `build-desktop` + запуск свежего бинаря в foreground (как `make run` в spk-mail).
  Не останавливает уже запущенный экземпляр — сначала завершить его самому (Ctrl+C/SIGINT или
  SIGTERM: оба гасят приложение чисто и быстро, `cmd/spk-mm-client/main.go` заводит
  `signal.NotifyContext` на оба сигнала, а `internal/desktop/run.go` реагирует на отмену контекста
  тем же путём, что и «Quit» из трея — `app.Quit()`, с закрытием store/media/стримов через defer).
- `make test-go`, `make test-front`, `make test-e2e`, `make lint`, `make cross-check`.
- `make run-browser` — UI на http://127.0.0.1:5180 с фейковым сервером MM (данные во временном каталоге).
- `E2E_BIN=<путь> E2E_PORT=<порт>` для `pnpm exec playwright test` в `tests/e2e` — прогнать e2e против
  другой browser-сборки (например, собранной в scratch-каталог), не трогая `build/bin`.

## Правила (правило — причина — тест)

- `go build ./...` без тега `wails` обязан проходить: desktop-код за тегом.
- `gofmt` — часть `make lint` (`golangci-lint` с `formatters: gofmt` в `.golangci.yml`), отдельно
  гонять `gofmt -l .` не обязательно, но неотформатированный файл роняет линт.
- Токены сервера не покидают Go: нет в DTO, логах, событиях. — `TestServerDTOHasNoToken`, `TestServerLogValueHidesToken`.
- Никаких блокирующих системных вызовов на старте (урок официального клиента, завис на gnome-keyring). Токены — в SQLite.
- Browser-режим отвечает только на loopback-`Host` (127.0.0.1, localhost, ::1), иначе 403 — защита от DNS rebinding (иначе `/` отдаст API-токен чужому сайту). — `TestNonLoopbackHostIsRejected`, `transport.LoopbackHostGuard`.
- `events.Emitter.Emit` не блокирует: полный буфер подписчика — событие отбрасывается.
- SQLite — одно соединение; многошаговые записи только через `Store.WithTx`.
- Версии `github.com/wailsapp/wails/v3` и `@wailsio/runtime` совпадают.
- `mmsync.Worker` хуки (`Changed`/`Status`/`Notify`) выполняются синхронно на горутинах воркера:
  они не должны блокировать и не должны звать `Manager.Start/Stop/Close` синхронно (`Stop` ждёт
  воркер под локом менеджера — дедлок); тяжёлая работа уходит в отдельную горутину. —
  `internal/mmsync/worker.go` (`Hooks`).
- UI-события схлопываются на 100 мс (`events.Coalescer`) — очередь бурстов сводится к одному
  вызову с последним состоянием. — `internal/api/sync.go` (`coalesceDelay`), `internal/events/coalesce.go`.
- `servers_changed` приходит часто (любое изменение состояния воркера, бейджей, непрочитанного);
  фронт при его обработке обновляет только список серверов и не сбрасывает `lastError` или
  выбранный сервер — иначе баннер ошибки/выбор мигали бы на каждый чих. —
  `frontend/src/store.ts` (`setServers`).
- Начало дыры в потоке событий — конец последнего **доказанного** потока: `Worker.live` (и
  `live/at` в снимке) не двигаются, пока новый поток не доказан (завершён bootstrap, событие с
  ожидаемым seq после resume или `resumeSettle` без `hello` и без удержанных
  refresh-ом событий; до первого доказательства — `live/at` из снимка); иначе отказ в resume после долгого
  офлайна догонял бы каналы только с момента переподключения и терял посты из дыры. —
  `TestLongOfflineGapWithLossCatchesUpFromStreamEnd`, `TestLiveMarkMovesOnlyWhileProven`,
  `TestSettleDoesNotProveWhileRefreshHoldsTheHello`, `TestRestoreSeedsGapStart`.
- Обновление метаданных применяет цикл событий сессии (`startRefresh`/`finishRefresh`), события
  на время REST-чтения копятся и применяются после `Bootstrap` под его guard — иначе `Bootstrap`
  откатывает счётчики, поднятые событиями. Уведомление зависит от новизны поста (seen-set), не от
  guard; пост неизвестного канала хранится (`TakeOrphans`) до обновления. —
  `TestMetaRefreshKeepsCountersRaisedByEvents`, `TestFirstPostInNewChannelNotifies`,
  `TestPostInsideGuardNotifiesWithoutDoubleCount`.
- Частичное чтение метаданных не применяется: ошибка контекста (дедлайн refresh-а) в `fetchMeta`
  фатальна, а команда с неудачным чтением категорий попадает в `Bootstrap.CategoriesFailed` и
  сохраняет прежние — иначе `Bootstrap` затёр бы «Избранное» и свои категории пользователя. —
  `TestRefreshDeadlineDuringCategoriesKeepsThemAndRetries`, `TestCategoriesFailureKeepsPreviousCategories`,
  `TestBootstrapKeepsCategoriesOfFailedTeams`.
- Действия записи (`SendPost`, `EditPost`, `DeletePost`, `MarkUnread`, …) при `needs_reauth`
  отказывают сразу кодом `session_expired`, не уходя в воркер — see `s.writer()` seam. —
  `internal/api/chat.go`.
- Go-рантайм стартует с `GOGC=50` и мягким лимитом 64 MiB (`tuneGoMemory`, первым делом в
  `main`; `GOGC`/`GOMEMLIMIT` из окружения приоритетнее): живая куча клиента 5–10 МБ, и при
  GOGC=100 RSS кучи держится около её удвоения — это запас GC, не данные. Soak-тесты на
  3 серверах × 100 каналов показали кучу ~20 МБ, дающую ~3× запас; если куча приближается
  к 64 MiB, Go runtime учащает сборки мусора и ограничивает CPU GC до ~50%, замедляя app вместо
  краша; обход — задать `GOMEMLIMIT`/`GOGC` в окружении. Настройка применяется до выбора
  режима, т.е. и в desktop, и в `--browser`. Удобство важнее экономии: настройки, замедляющие UI
  (напр. `JSC_useDFGJIT=false` — −13 МБ web process, но открытие канала с тяжёлым markdown
  ~120 мс вместо ~75), отвергнуты.
  — `TestTuneGoMemoryDefaults`, `TestTuneGoMemoryRespectsEnvironment`;
  замеры — `docs/spikes/2026-09-24-stage1-spikes.md` S4.
- Картинки и фрагменты файлов UI берёт только с `/media/<srv>/<kind>/<key>`; путь строится
  только из проверенных ключей, открытого прокси к серверу Mattermost нет — обслуживает
  `internal/media`. — `TestBadRequests`.
- Своя картинка webhook-поста (`override_icon_url`) — только `/media/<srv>/posticon/<post id>?v=<версия>`:
  ключ — id поста, никогда URL со страницы (`v` — хэш URL иконки, `PostView.IconVersion`: правка
  иконки — новый URL у WebView и новый шанс после отказа в UI); Go сам находит пост в state и берёт
  URL, только если сервер разрешает (`EnablePostIconOverride`) и пост подходит (`from_webhook`, не
  системный, без `use_user_icon`; `override_icon_emoji` рисуется в UI через `EmojiGlyph`, без
  загрузки). Откуда качать, решает `media.routeIcon`:
  (1) **origin сервера** (точно: схема, хост, порт; под базовым путём) — только пути картинок сервера
  (`<base>/static/…`, `<base>/api/v4/emoji/<id>/image`, `<base>/api/v4/image`), без `..`/`%2e`/`%2f`/
  `%5c`/`\` в пути (прокси перед сервером прочёл бы их иначе, чем мы проверяем); качается с сессией
  через `PostIcons.GetIcon`: редирект — только на такой же путь того же origin (`iconRedirect`), иначе
  отказ без повтора (Go сам пересылает `Authorization` на тот же hostname с другим портом/схемой —
  токен ушёл бы открытым текстом), 401/403 — ответ пути, а не сессии: без `CheckAuth`/повторного
  входа, в негативный кэш; (2) **хост сервера с другой схемой или портом** — отказ (иначе прокси
  картинок сервера отредиректил бы туда с токеном); (3) внешний при `HasImageProxy` —
  `/api/v4/image?url=` по (1); редирект **самого** `/api/v4/image` за пределы origin — это передача
  прокси atmos/camo: ответ не следуется с сессией (`http.ErrUseLastResponse`), а его цель качается
  как (4) — без токена, с её гардами адресов, 2 слотами (слот общего пула меняется на внешний),
  таймаутом, лимитом редиректов и негативным кэшем, как браузер webapp (редирект без cookie); любой
  другой редирект пути сессии — правила (1); (4) иначе — напрямую **без** токена/cookie, только http(s) без
  userinfo, ≤ 3 редиректов, без прокси окружения (`HTTP(S)_PROXY` игнорируется — `docs/backlog.md`),
  таймаут 10 с на всё, свои 2 слота (`extSem`, не 6 общих `c.sem` — зависший хост не держит аватары),
  любой отказ внешнего хоста — в негативный кэш на 5 мин. Каждый dial (редиректы тоже) проверяет
  реальный IP (`allowedAddr`, зона адреса отбрасывается): никогда — loopback, link-local (и
  169.254.169.254), метаданные облаков в частных диапазонах (`fd00:ec2::254`, `100.100.100.200`),
  unspecified, multicast, служебные диапазоны (документация, бенчмарки, 6to4,
  Teredo, ORCHID, IPv4-compatible/translated, local-use NAT64, site-local, discard; well-known NAT64 —
  по вложенному IPv4; NAT64 с префиксом оператора не отличить от публичного адреса); частные
  (RFC 1918, ULA, CGNAT) — **только** если хост самого сервера резолвится в частный адрес
  (интранет, где браузер webapp их тоже видит; резолв — лениво при первой внешней иконке, кэш 10 мин,
  сбой — «нет»). Прямая загрузка раскрывает IP пользователя хосту webhook — как webapp без прокси.
  Дальше — те же растровые/размерные/пиксельные проверки, дисковый кэш (одна копия на URL для всех
  постов webhook); офлайн не качает (`PostIcon.Live`). Имя (`override_username`) — только при
  `EnablePostUsernameOverride` и `from_webhook` (и в уведомлениях: `NotifyCandidate.SenderName` —
  `authorLocked`); такой пост всегда BOT и никогда не группируется с соседями. Аватар владельца
  webhook-пост **не показывает** (выглядело бы, будто написал владелец; webapp —
  `DEFAULT_WEBHOOK_LOGO`): без своей иконки (`from_webhook`, не `use_user_icon`) — `PostView.Icon == "webhook"`, в UI общая иконка webhook (`IconWebhook` на
  круглом фоне темы, `PostAvatar`); она же — пока своя картинка не загрузилась (у нового поста новый
  URL, первая загрузка с внешнего хоста или отказ — секунды пустого круга) и при отказе. Картинка, уже
  лежащая в кэше WebView (`complete` при монтировании), показывается сразу, без подмены. При выключенном
  `EnablePostIconOverride` своя иконка/эмодзи игнорируются, но пост тоже получает общую иконку webhook —
  **намеренное отличие от webapp** (там аватар владельца; решение пользователя: аватар автора сбивает с
  толку). Аватар аккаунта остаётся только у бот-аккаунта без `from_webhook` и при `use_user_icon`
  (интеграция сама его попросила). —
  `TestPostIconIsKeyedByPostID`, `TestRouteIcon`, `TestIconRedirectPolicy`,
  `TestPostIconServerRedirectsNeverCarryTheToken`, `TestPostIconServerRedirectToPlainHTTPIsRefused`,
  `TestPostIconUnauthorizedIsRememberedNotSignIn`, `TestPostIconExternalSendsNoCredentials`,
  `TestPostIconExternalRefusesPrivateAddresses`, `TestAllowedAddr`, `TestPrivateHost`,
  `TestPostIconPrivateTargetsOnlyForAnIntranetServer`, `TestPostIconExternalRedirects`,
  `TestPostIconStalledExternalHostDoesNotBlockOtherPictures`, `TestPostIconExternalFailureIsRememberedLonger`,
  `TestPostIconExternalRasterOnlySizeCapAndNegativeCache`, `TestPostIconThroughTheImageProxy`,
  `TestPostIconFetchesOnlyWhileLive`, `TestPostIconAcceptsAVersion`,
  `TestPostIconCamoRedirectIsFetchedWithoutTheSession`, `TestPostIconCamoRedirectToARefusedAddressIsRefused`,
  `TestPostIconStalledCamoDoesNotBlockOtherPictures`, `TestPostIconRedirectWithoutLocationIsRefused` (3xx без
  `Location` с любого пути — 403, без повторного запроса без сессии),
  `TestPostIconCamoToAPrivateAddressOnlyForAnIntranetServer`,
  `TestPostViewWebhookOverridesFollowTheServerConfig`, `TestPostViewWebhookOverridesOffInConfig`,
  `TestPostViewIconVersionFollowsTheIconURL`, `TestNotifySenderFollowsTheUsernameOverride`,
  `TestMediaPostIconThroughTheService`, `TestMediaPostIconDoesNotSignOutOrFollowTheTokenAway`,
  `frontend/src/components/PostAvatar.test.tsx`, `PostItem.test.tsx` («a webhook icon that fails to
  load falls back to the generic webhook icon…»), `TestPostViewWebhookWithoutAnIconShowsTheGenericOne`, `feedRows.test.ts`, `tests/e2e/webhook.spec.ts`. Фейк:
  `Options.ImageProxy`, `Options.DisablePostOverrides`, `WebhookPostAs`, `WebhookIconPath`,
  `UnauthorizedIconPath`, `RedirectIconPath`, `SetProxiedImage`; test-API `/api/_test/fake/webhook`
  `{channel_id, username, message, override_username, override_icon_url, override_icon_emoji}` → `{id}`.
- Browser-режим: доступ к `/media/` закрыт за cookie `spk_media` (HttpOnly, SameSite=Strict)
  или bearer-токеном — иначе картинку мог бы утащить любой сайт в браузере пользователя. —
  `TestMediaNeedsThePageCookie`.
- SVG и не-растровые типы не отображаются, размер файла и число пикселей ограничены (пиксельный
  гард отказывает закрыто, если конфиг картинки не читается). — `TestSVGIsRefused`,
  `TestHugeDimensionsAreRefused`.
- `internal/media` не читает картинку в память целиком (`io.ReadAll` в пути картинок запрещён —
  вложения в куче Go не держим): скачанное тело сначала пишется во временный файл кэша (`*.tmp`,
  `Cache.writeImageFrom`) — **вне** `decodeSem`, чтобы медленная загрузка не держала очередь декодов
  и слоты `c.sem`; staged-файл (`*os.File`) читается как есть. Затем `writeImage` работает с файлом
  (`io.ReadSeeker`): размер — `Seek`, заголовок (`DecodeConfig`) читается из файла и не копится в
  памяти (длинные APPn у JPEG тоже), влезающая в `FeedMax` картинка копируется потоком, а
  масштабируемая декодируется из файла под `decodeSem` (одна за раз) и кодируется сразу в файл кэша.
  Нужны только битмапы (исходный + ≤960 px). — `internal/media/memory_test.go`
  (`TestWriteImagePassesAFittingPictureThroughWithoutReadingItWhole`,
  `TestWriteImageScalesWithoutReadingTheFileWhole`, `TestWriteImageStreamedStillEnforcesTheByteCap`,
  `TestStagedPreviewRetainsNothing`, `TestWriteImageDoesNotKeepALongHeader`,
  `TestSlowFeedDownloadDoesNotBlockOtherPictures`), `TestDownscalesRunOneAtATime`.
- PDF для просмотрщика — только `/media/<srv>/pdf/<file id>` (`media.KindPDF`, `internal/media/pdf.go`):
  весь файл до `media.PDFMax` = 50 МиБ (= `PDF_MAX` в UI; больше — 413, по `Content-Length` — не
  скачивая), потоком во временный файл кэша (`writePDF`, без `io.ReadAll` — вложения в куче Go не
  держим); `%PDF-` в первых 1024 байтах — и при загрузке, и при отдаче из кэша (иначе 415, испорченный
  файл кэша удаляется); только ответ `200` апстрима (206/204 — 502, частичный файл не кэшируется);
  таймаут загрузки — 5× обычного (файл вдвое больше картинки), поэтому у PDF **свои 2 слота**
  (`pdfSem`, не 6 общих `c.sem` — зависшие PDF не держат аватары и миниатюры), а загрузка, которую
  больше никто не ждёт (просмотрщик закрыт; у PDF один читатель), **отменяется** и не попадает в
  негативный кэш (`call.cancel`; ждущая слот — уходит из очереди сразу; следующий запрос начинает свою загрузку, не присоединяясь к
  отменённой). Проверка `%PDF-` — это сниффинг, **не** валидация: HTML с `%PDF-` в комментарии её
  проходит; настоящая защита — заголовки ответа и то, что pdf.js получает байты, а не URL. Любой ответ
  на pdf-URL (успех, HEAD, Range и ошибки, включая 416, где `ServeContent` снимает `Cache-Control`) —
  через `pdfWriter`: `nosniff`, CSP `default-src 'none'; sandbox`, `Content-Disposition: attachment`,
  `Cache-Control: no-store`; у успешного ещё `Content-Type: application/pdf`. Причина: разбирает PDF только pdf.js
  в ленивом чанке UI (по байтам из `fetch`); сам WebView его показывать не должен — встроенный в
  WebKitGTK 2.52 просмотрщик (pdf.js 4.1.392, eval и скрипты PDF включены, класс CVE-2024-4367) не
  настраивается, а sandbox-CSP не даёт ему запуститься, даже если фрейм сюда перейдёт; `no-store` —
  копия в кэше WebView стоила бы только памяти web process (файл уже на диске у Go). Ходит к серверу
  только «живой» воркер, как остальные виды. — `internal/media/pdf_test.go`
  (`TestPDFServedAsSandboxedAttachment`, `TestPDFOverCapIs413`, `TestNotAPDFIs415`,
  `TestPDFHeaderWithinTheFirstKilobyte`, `TestPDFFetchHasALongerTimeout`, `TestPDFErrorsCarryTheGuardHeaders`,
  `TestPDFNeedsTheWholeFile`, `TestStalledPDFsDoNotBlockOtherPictures`, `TestAbandonedPDFFetchIsCancelled`,
  `TestAbandonedQueuedPDFLeavesAtOnce`, `TestRequestAfterACancelStartsAFreshPDFFetch`), `TestPDFIsNotReadWhole`
  (`memory_test.go`), `TestBadRequests`, `internal/api/media_test.go` (`TestPDFOnlyWhileLive`).
- PDF в просмотрщике (фронтенд) — `PdfView.tsx`, собственный ленивый чанк `Viewer`'а (`React.lazy` в
  `Viewer.tsx`): pdf.js и его воркер (`pdf.worker.min.mjs`) целиком в этом чанке и его ассетах, ни
  байта — в начальном чанке и ни в каком другом; `frontend/scripts/check-pdf-bundle.mjs` (шаг
  `pnpm build`) грепает **каждый** JS-чанк, кроме `PdfView-*`/`pdf.worker*`, на `pdfjs`/
  `GlobalWorkerOptions` — не только входной (финальное ревью M7: старая версия проверяла только его,
  и общий чанк с pdf.js прошёл бы). `pdfjs-dist` закреплён точной версией (без `^`, `package.json` и
  `pnpm-lock.yaml`) и обновляется осознанно, не транзитивно. Опциональные ассеты для сканов/CJK/ICC
  (финальное ревью I3) — `wasmUrl`/`cMapUrl` (с `cMapPacked: true`)/`standardFontDataUrl`,
  указывающие на `/pdfjs/{wasm,cmaps,standard_fonts}/`: `vite.config.ts`'s `copyPdfjsAssets`
  (хук `writeBundle` — после `emptyOutDir`, иначе рискует быть стёртым) копирует их из закреплённого
  `pdfjs-dist` при сборке на тот же origin, что и остальные ассеты приложения (Wails asset server в
  десктопе, `/` в browser-режиме — оба раздают один `dist`); без них скан молча остаётся пустой
  страницей — не ошибка, не карточка-фолбэк (`onFail` срабатывает только на неразбираемый документ
  целиком). Никогда не копируется `quickjs-eval.*` — это сэндбокс скриптов pdf.js, запрещённый
  правилом плана (никакого PDF-скриптинга). `.wasm` отдаётся с `Content-Type: application/wasm`
  (`mime.AddExtensionType` в `cmd/spk-mm-client/embed.go`, `init()`) — иначе вебвью получил бы
  `application/octet-stream` (в built-in таблице Go нет `.wasm`, а `WebAssembly.instantiateStreaming`
  требует точный тип). `PdfView` не рендерит ни annotation-, ни form-, ни scripting-слой
  (`enableXfa:false`, только текстовый слой `TextLayer`); при размонтировании — `loadingTask.destroy()`
  (гасит воркер), отмена всех render/text-задач, обнуление **каждого** канваса — включая тот, чей
  первый рендер был отменён/провалился (взят из пула или создан, но не успел стать `s.canvas`: иначе
  он утёк бы мимо пула — canvas-буферы, не JS-куча, драйвер роста памяти по спайку). `Viewer` никогда
  не кладёт PDF в `iframe`/`embed`/`object`. Масштаб по умолчанию — как у webapp (`DEFAULT_SCALE` = 1.75,
  `ZoomSettings.DEFAULT_SCALE`: страница Letter ~1071 px), но не шире панели (у каждой страницы — свой
  предел fit-width), страница по центру; «По ширине» — отдельный режим, Ctrl+0 — назад к умолчанию
  (раньше по умолчанию был fit-width: чек на окне 1920 px открывался на 301%). У коробки страницы нет
  `box-shadow`/`filter`/blur: WebKitGTK перерисовывает её на каждом шаге прокрутки колесом, и размытая
  тень на коробке размером со страницу стоила 125–200 мс на шаг вместо ~32 мс (рваная прокрутка;
  Chromium не страдает) — отчёт `.superpowers/sdd/fixes-2026-09-28/pdf-lag-report.md`. —
  `frontend/src/components/PdfView.test.tsx`,
  `frontend/src/components/Viewer.test.tsx`, `tests/e2e/pdf.spec.ts`.
- Картинка, которая не загрузилась (`/media/` ответил 404/413/415), в ленте/миниатюрах/просмотрщике
  откатывается на карточку файла, а не остаётся сломанной. —
  `frontend/src/components/Attachments.test.tsx` («a single image that fails to load (404/413/415
  from /media/) becomes a card»).
- `/media/` ходит к серверу только через «живой» воркер (`StatusLive`): офлайн, при переподключении
  и при `needs_reauth` `api.Service` как `media.Origin` отвечает `media.ErrNoServer` (404, в
  негативный кэш не попадает) — иначе запрос уходил бы в сеть и падал 502, который UI запоминает;
  имя кастомного эмодзи, уже известное воркеру, резолвится и офлайн (картинка может быть на диске).
  401 на картинку или имя эмодзи тоже не запоминается и ведёт воркер в повторный вход
  (`Worker.CheckAuth` → `signalAuth`), как любой другой запрос. — `TestMediaOriginFetchesOnlyWhileLive`,
  `TestMediaUnauthorizedAsksForSignIn`, `TestUnauthorizedIsNotRemembered`, `TestNotSignedInIsNotRemembered`.
- UI помнит неудачную загрузку картинки/фрагмента/кастомного эмодзи только до следующего перехода
  сервера в `live`: store держит «эпоху живости» на сервер (`liveEpochs`, растёт на каждом
  переходе в `live` в `setServers`), `useLoadFailure`/`useTextFile(url, epoch)` сбрасывают отказ
  при смене эпохи — иначе клиент, стартовавший офлайн, показывал бы инициалы и карточки весь сеанс.
  — `frontend/src/store.test.ts` («a server going live bumps its live epoch; other updates do not»),
  `Avatar.test.tsx` («a failed picture is tried again once the server goes live again»),
  `Attachments.test.tsx` («a failed image and a failed snippet are tried again once the server
  goes live again»).
- После каждого bootstrap воркер один раз (в фоне, через общий лимитер) перечитывает профили,
  которые держал до него (из снимка или с прошлого потока): `POST users/ids?since=<начало дыры −
  sinceMargin>`, без метки — все пачками по 100; `state.RefreshUsers` не откатывает более свежий
  профиль из события (`update_at`) и шлёт `changed()` только при видимом изменении. Иначе
  аватар (версия — часть immutable-URL) и имя, изменённые в офлайне, не обновились бы до живого
  `user_updated`. — `TestKnownUsersAreRefreshedOnceAfterBootstrap`,
  `TestRefreshUsersKeepsNewerProfilesAndReportsChanges`.
- Скачивание/«Открыть» одного файла (`сервер/id`), пока он качается, присоединяются к той же
  загрузке (`Service.shared`): один запрос, одна копия; присоединившееся «Открыть» после неё
  открывает файл по своему allowlist. Запасной путь без жёстких ссылок не удаляет после
  переименования путь `.part` — его уже может занять чужая одноимённая загрузка. —
  `TestConcurrentSavesOfOneFileShareTheDownload`, `TestNoLinkFallbackLeavesAnotherDownloadsPartAlone`.
- Обратная связь о скачивании — только на самой карточке файла и в плавающем тосте, **никаких
  баннеров** над лентой (баннер «Сохранено в … / Показать в папке» сдвигал ленту — жалоба
  2026-09-29; удалён). Кнопка скачивания (`DownloadButton` — карточка, текстовый/markdown-фрагмент,
  заголовок просмотрщика) при клике сразу крутит кольцо (прогресс записи списка загрузок, пока её
  нет — спиннер), по готовности ~2 с показывает ✓ (подсказка — путь), и до конца сессии рядом
  остаётся «Показать в папке» (`downloads.reveal`/`downloads.showInFolder`); её место в карточке
  зарезервировано с самого начала (невидимая, `aria-hidden`, `inert`, `tabIndex -1` до сохранения),
  иконка во всех состояниях одного размера — ни ширина, ни высота карточки не меняются (карточки
  нескольких файлов лежат в переносимом ряду: подросшая карточка переносилась бы на новую строку и
  увеличивала строку ленты). Готовое скачивание озвучивается для скринридеров («Файл сохранён:
  <имя>», `Announcer` — невидимый `aria-live="polite"`); кольцо-спиннер крутится только при
  `motion-safe`. Состояние — `fileSaves` в
  store по ключу `<сервер>/<id файла>` (не больше `FILE_SAVES_CAP` = 200, старые вытесняются); сбой
  возвращает файлу прежнее сохранённое состояние (и при повторном клике во время скачивания, и при
  двух сбоях подряд «Показать в папке» прежней копии не пропадает). Ошибка (и «сохранено, но такие
  файлы не открываются») — тост: один компонент (`Toast` в `App`, одна live-область), который
  порталом рисуется в лучший из «хостов» (`useToastHost`): открытый просмотрщик (модальный
  `fixed z-50` — под ним тост не виден, а заголовок просмотрщика — главная точка скачивания), иначе
  самая правая лента (панель треда над каналом; `Feed`'s `toastHost`) — `absolute` внизу справа в её
  рамке, всегда над композером любой высоты и левее кнопки «к последним» (`right-16`), иначе (ленты
  ещё нет: экран добавления сервера, канал грузится) — `fixed` над окном. Переезд между хостами
  (смена канала, открытие/закрытие треда) не перезапускает 6 с. Над лентой карточка тоста пропускает
  указатель (кроме кнопки закрытия) — тулбар последнего поста под ней остаётся доступен; в
  просмотрщике — нет (клик сквозь неё закрыл бы просмотрщик). Вне потока вёрстки,
  `aria-live="polite"`, сам скрывается через 6 с, закрывается кнопкой. Ошибки действий и обновления
  панели загрузок — тоже тост. —
  `frontend/src/components/DownloadButton.test.tsx`, `Toast.test.tsx`, `frontend/src/chat.test.ts`
  («a download is "saving" on its file at once…», «a failed download…»), `store.test.ts`,
  `tests/e2e/media.spec.ts`, `tests/e2e/stick-bottom.spec.ts`.
- Рамка картинки и текстового фрагмента имеет окончательный размер до загрузки содержимого —
  лента со скролл-якорем не прыгает при догрузке превью. —
  `frontend/src/components/Attachments.test.tsx` («the image box has its final size before the
  image loads»).
- Компенсация ленты за строку, впервые измеренную выше видимой области (картинку виртуализатор
  оценивает в 28–64 px, а в ней ~300), во время прокрутки пользователем (колесо или тач — до 150 мс
  покоя) не пишется в `scrollTop`: WebKitGTK анимирует каждый щелчок колеса и отменяет анимацию на
  любую запись `scrollTop` скриптом — шаг обрывался (замер: 19–57 px вместо 86, на серии из 45
  щелчков терялось ~30% хода), лента в каналах с картинками и файлами дёргалась. Сдвиг копится в
  отрицательном `margin-top` контейнера строк (`ScrollShift`) — именно margin, не `transform`:
  transform не уменьшает `scrollHeight`, и под последним постом оставалась пустота размером со
  сдвиг, а проверка «внизу ли лента» ошибалась на сдвиг (ревью 649189b). Виртуализатор считает в
  координатах строк (`scrollTop + shift`); в `scrollTop` сдвиг переносится одним шагом вместе со
  снятием margin — после покоя и у самого верха. Любая программная прокрутка (`scrollToFn`: якорь
  истории, прокрутка к концу, следование за новым постом) сначала переносит сдвиг и **завершает
  жест**: дальше, до следующего колеса/тача, компенсация сразу, как у виртуализатора по умолчанию
  (отложенный сдвиг у верха при приземлении отодвигал бы `scrollTo(0)` и стопорил подгрузку
  истории). Прокрутка клавишами не покрыта (лента не фокусируемая, клавиши приходят из любого
  фокуса) и компенсируется сразу. Нативный scroll anchoring на ленте выключен
  (`overflow-anchor: none`) — позицию ведут виртуализатор и `ScrollShift`. Chromium применяет
  колесо Playwright мгновенно и обрыва не показывает — e2e проверяет причину (ни одной записи
  `scrollTop` посреди жеста, при этом сдвиг действительно был) и всё, что отложенный сдвиг не
  должен ломать: нет скачка, нет пустоты под последним постом при развороте жеста вниз, новый пост
  внизу подхватывается, история догружается. — `frontend/src/components/scrollShift.test.ts`,
  `tests/e2e/feed-scroll.spec.ts`.
- Ресайз ленты/треда (2026-09-30): только весь смонтированный диапазон строк имеет
  `translateY(first.start)`; строки внутри — обычный поток с `display: flow-root` (включает
  margin статьи в измеренную высоту). Нельзя возвращать отдельный absolute/transform каждой
  строке: после изменения ширины перенос уже произошёл, а старые позиции до ResizeObserver
  дают наложения на долю секунды. При этом виртуализация и overscan сохранены, весь канал
  не монтируется. `useFlushSync: false` позволяет React объединить пакет замеров вместо
  синхронного коммита на каждую строку выше viewport; normal flow уже сдвинул контент вместе
  с компенсацией scrollTop. Не добавлять общий `onChange => flushSync` (даже через microtask):
  при смене диапазона внутри доставки RO WebKitGTK получает loop errors. Синхронная microtask
  остаётся только у `ScrollShift` для согласования margin во время жеста колеса. Тесты:
  `Feed.test.tsx` (пакет из 8 замеров — не больше 2 коммитов), `resize.spec.ts` (48 промежуточных
  ширин двух панелей, затем размеры окна), `feed-scroll`, `stick-bottom`, `threads`.
  Исходный repro: 49 наложений до 60 px. Финальный WebKitGTK/Xvfb: 0 наложений в 707 проверках
  соседних строк за 80 кадров, 0 ошибок RO; медиана кадра 17 ms, p95 32 ms (локальный smoke,
  не гарантия для любого железа). Скриншот/метрики: `.agents/tmp/theme-refresh/webkit-resize*`
  в корне Solution.
- Якорь догрузки истории — верхний видимый пост **из тех, над которыми ляжет страница**. Корень треда
  стоит в панели первым и до, и после страницы (старые ответы ложатся под него и под строку «N
  ответов»), поэтому якорем быть не может: удержание корня оставляло ленту наверху, только что
  загруженная страница пропускалась и сразу запрашивалась следующая (`threads.spec.ts` «a long thread
  … without gaps», 77 из 150 ответов — проявилось, когда выросший композер укоротил ленту панели и
  загрузка стала начинаться с корнем на экране). — `Feed.tsx` (`captureAnchor`),
  `frontend/src/components/Feed.test.tsx` («thread: history that lands under the root…»).
- Лента, стоящая внизу (`atBottom` — по последнему событию `scroll`), остаётся внизу при **любом**
  изменении размера, не только при новых строках: `ResizeObserver` на скроллере (`Feed.tsx`,
  «Bottom-stick»; колбэк — после layout, до paint, тот же кадр) и layout-эффект на общей высоте
  строк виртуализатора (в том же коммите, где выросли строки) пишут `scrollTop` = конец. Контейнер
  строк **не** наблюдается `ResizeObserver`'ом: раньше виртуализатор перерисовывал его синхронно из
  наблюдателя строк, и более мелкий по глубине элемент меняется внутри рассылки — «ResizeObserver
  loop completed with undelivered notifications» (ловилось в e2e; `stick-bottom.spec.ts` проверяет во
  всех тестах, что таких ошибок нет). По той же причине новый размер скроллера виртуализатор получает
  в следующем кадре (`observeElementRect` в `Feed.tsx` — через `useFrames`), а не внутри рассылки: посреди
  прокрутки его notify — `flushSync`, и укоротившийся скроллер (растущий композер) размонтировал
  вышедшую из диапазона строку прямо в колбэке; её всё ещё наблюдает наблюдатель строк, а отсоединённая
  она мельче скроллера по глубине — Chromium пропускал её (замер: 28 строк до колбэка, 27 после, ошибка в
  конце той же рассылки; 5–11 из 16 прогонов «away» до фикса; отключение наблюдателей композера после
  453aad0 не помогало, отключение этого — 0 из 16).
  Причина: эффект «следовать за низом» срабатывает только на смену `rows`, а уменьшившийся
  скроллер (баннер/статус над лентой, выросший композер с многострочным текстом или чипами вложений,
  окно, сплиттер, открытая панель треда) сохраняет `scrollTop` и не шлёт `scroll` — последний пост
  уезжал под композер и так и оставался (жалоба 2026-09-29: баннер скачивания; замер до фикса: окно
  800→600 px — лента в 200 px от низа, композер +5 строк — 100 px). Не внизу — ничего не пишется
  (верх вьюпорта держит `scrollTop`, строки на экране не двигаются). Посреди жеста колеса/тача
  (`ScrollShift.gesturing`) — тоже ничего: запись `scrollTop` обрывает анимацию колеса WebKitGTK
  (правило выше); прижатие откладывается до покоя жеста (`ScrollShift`'s `idle`) и выполняется, только
  если лента всё ещё внизу. «Внизу» теряется только движением вверх: событие `scroll` от нашего же
  прижатия приходит кадром позже и может меряться по уже снова уменьшившемуся скроллеру (композер
  вырос сразу больше чем на `NEAR_BOTTOM`) — расстояние велико, но `scrollTop` не уменьшился, значит лента
  всё ещё внизу (`bottomTop`). Автовысота композера меряет `textarea` с `height: auto` при
  удержанной высоте его рамки (`min-height` на время замера): иначе на этот принудительный layout
  лента вырастала, браузер зажимал её `scrollTop`, и каждая новая строка «отлипала» ленту от низа
  (e2e, 78daeed; `Composer.test.tsx` «auto-grow measures…»). Рост содержимого (перемеренная последняя строка) может быть виден один кадр: новый общий размер
  виртуализатор коммитит асинхронно. — `frontend/src/components/Feed.test.tsx` («bottom-stick on
  size changes», «the scroll event of our own pin…»), `scrollShift.test.ts` («the idle hook…»),
  `tests/e2e/stick-bottom.spec.ts`.
- Кэш тредов (`internal/state/threads.go`) ограничен: не больше `ThreadCacheSize` = 3 тредов на
  сервер (открытый в панели + 2 недавних, LRU; вытесненный освобождается целиком), в каждом — корень
  и не больше `ThreadMaxReplies` = 200 ответов (прокрутка вверх останавливается на потолке — `Capped`;
  живой ответ на потолке вытесняет самый старый); закрытие панели (или открытие другого треда)
  обрезает тред до последних `ThreadPage` = 60 в свежем срезе. Кэш только в памяти: не пишется в
  снимок и не восстанавливается на старте; `ResetThreads` (смена CRT) его очищает, но при
  открытой панели оставляет пустую запись открытого треда — воркер её перечитывает; остановка
  воркера сначала закрывает панель (`CloseThread`), так что после неё кэш пуст; уход из канала
  убирает треды канала. Закрытый тред остаётся на последней странице и при живых ответах,
  страница, прилетевшая после закрытия, тоже обрезается. Страницы треда применяются с эпохой
  (`ResetThreads`/`MarkStale` её двигают) — поздняя страница не воскрешает сброшенный или
  вытесненный тред. `GET /posts/{id}/thread` — всегда с `perPage` (без него сервер отдаёт тред
  целиком). — `TestThreadCacheStaysBounded`, `TestClosingTrimsTheThread`,
  `TestLateThreadPageAfterResetIsIgnored`, `TestForgottenChannelDropsItsThreads`,
  `TestStoppedWorkerLetsTheThreadsGo`, `TestOlderRepliesStopAtTheCap`,
  `TestThreadClosedWhilePageInFlightIsTrimmed`, `TestOlderPageAppliesOnlyToItsCursor`.
- Тред при CRT помечается прочитанным на сервере (`PUT …/threads/{root}/read/{ts}`) только пока
  его панель открыта над его каналом, окно в фокусе (фокус есть только у активного сервера) и мы на
  него подписаны: `state.Server.ThreadReadTarget` — единственная проверка, `Worker.readThread` —
  один запрос на тред за раз с перепроверкой, без автоматических повторов. Поводы — открытие,
  фокус, чужой ответ, `thread_updated` открытого треда, пришедшая страница; читается только
  загруженный тред, `ts` — `create_at` новейшего показанного ответа (свои часы — только у треда
  без ответов: спешащие часы не должны помечать непоказанное прочитанным); повтор без нового
  чужого ответа не шлётся; результат (`ThreadReadDone`/`ThreadNotFollowing`) несёт номер открытия
  и после закрытия и нового открытия игнорируется.
  404 (не подписан) — больше не слать в этом открытии, пока не придёт свой ответ или
  `thread_updated` по треду; иначе запрос на каждый ответ. Без CRT треды отдельно не читаются. —
  `TestThreadReadOnlyWhileOpenAndFocused`, `TestThreadReadGoesOnlyToTheActiveServer`,
  `TestThreadIsNotReadWithoutCRT`, `TestThreadReadNotFollowingIsNotRepeated`, `TestThreadReadTarget`,
  `TestThreadReadResultOfAnEarlierOpeningIsIgnored`.
- Упоминания в тредах (CRT, `internal/state/threadcounts.go`) — итоги по командам (`""` — DM/GM)
  читаются в `fetchMeta` и двигаются дельтами `unread_mentions − previous_unread_mentions`; ключ —
  команда канала события, не `broadcast.team_id` (у `thread_read_changed` это команда, через
  которую читали, — у DM-треда не `""`). Событие с дельтой, пришедшее под guard обновления
  (`Bootstrap` → `ClearGuard`), может уже быть в итогах: не прибавляется, а вызывает
  перечитывание итогов (`Effects.RereadThreadCounts`), которое заменяет их целиком, только если за
  время запроса их не сдвинуло другое событие (`ThreadCountsToken`; третья попытка — как есть,
  но не поверх более свежего `Bootstrap`; перечитывания, запрошенные во время текущего,
  схлопываются в одно). Так же, без дельты, — `thread_read_changed` без `thread_id`,
  `thread_follow_changed` (отписка на другом устройстве шлёт только его), `thread_updated` без
  `previous_unread_mentions` (`MarkChannelAsUnreadFromPost` шлёт один `thread`: отсутствие ключа ≠ 0,
  иначе бейдж ползёт вверх) и включение CRT; выключение CRT итоги очищает. Неудачное
  перечитывание (или чтение итогов в refresh) помечает их грязными (`ThreadCountsDirty`) —
  воркер перечитывает при следующем переходе в `live`. Ответ `PUT read` счётчики не трогает (иначе
  двойной учёт с событием). Поднимают бейдж сервера и строку команды, не строку канала;
  непрочитанные треды без упоминаний ничего не поднимают. Итоги только в памяти, одно число на
  команду. — `TestThreadMentionsSurviveRefreshWithoutDoubleCount`, `TestAllThreadsReadRereadsTotals`,
  `TestThreadMentionsVanishWhenCRTTurnsOff`, `TestDMThreadMentionsCountOnce`,
  `TestThreadMentionsUnderGuardAreRereadNotCounted`, `TestThreadCountsRereadInstallsOnlyWhenSettled`,
  `TestThreadUpdatedWithoutPreviousAsksForReread`, `TestThreadFollowChangedRereads`,
  `TestThreadCountsDirtyUntilReadAgain`, `TestFailedRereadIsRetriedWhenLiveAgain`.
- Уведомление об ответе (оба режима) несёт `root_id`, его ID — `mm-<srv>-<канал>-<корень>`
  (серия ответов треда склеивается отдельно от канала); клик шлёт `open_channel {server_id,
  channel_id, root_id}`; CRT-ответ в треде, открытом в панели, при фокусе не уведомляет
  (`thread_is_open`). — `TestNotificationText`, `TestNotificationClickOpensChannel`, `TestClickTarget`,
  `TestDecide`.
- Счётчик ответов корня (`reply_count`/`last_reply_at`) не двоится и не теряется: свой ответ приходит
  дважды (REST `CreatePost` и эхо `posted`), удаление — тоже (REST и `post_deleted`). Число берётся из
  `reply_count` самого `posted`-ответа (сервер кладёт туда актуальное) только для нового id и только если
  его `create_at` ≥ `LastReplyAt` корня, иначе `++`; удаление уменьшает ровно один раз на id (кольцо
  `gone`, по `DeleteAt`: `post_deleted` несёт копию **до** удаления), не ниже 0; `SetWindow`/страницы
  сохраняют локальный счётчик, если он новее `UpdateAt` пришедшего корня (сервер двигает `UpdateAt` корня
  на каждый ответ). При CRT свой ответ не трогает `Member.MsgCount*`/`LastViewedAt`, ожидающий ответ
  виден только в панели, `NeedsView` смотрит только корневые счётчики; смена CRT (`Bootstrap` сравнивает
  режим до/после) — `ResetWindows` + `ResetThreads` + перечитывание всех каналов. —
  `TestReplyCountComesFromPostedOnce`, `TestReplyDeleteDecrementsOnce`,
  `TestReplyDeleteAfterFresherRootIsNotCountedTwice`, `TestSetWindowKeepsNewerLocalReplyCount`,
  `TestPageReadBeforeReplyDeletionKeepsTheLowerCount`, `TestCRTReplyAlreadyReflectedByRESTDoesNotDoubleBump`,
  `TestOwnCRTReplyDoesNotMarkTheChannelRead`, `TestPendingReplyIsNotInTheCRTFeed`,
  `TestNeedsViewUnderCRTCountsRootsOnly`, `TestBootstrapReportsCRTChange`, `TestBootstrapWithOtherCRTResetsWindows`,
  `TestReplyDeleteLowersTheRootOnce`.
- Панель треда (UI, `frontend/src/components/ThreadPane.tsx`): справа от ленты 420 px, уже 1024 px —
  поверх ленты с «← К каналу»; корень, разделитель «Replies: N» (из `reply_count` корня, не из числа
  загруженных строк), ответы и свой `Composer` с `key` по (канал, корень) и `autoFocus` — фокус в нём при
  каждом открытии; вся панель — drop-цель с `data-root` и сама `relative` (иначе оверлей drop растягивается
  на весь ряд); Esc закрывает, только если фокус в панели и не открыт поповер; закрытие возвращает фокус в
  ленту канала (`[data-feed="channel"]`). В ленте: под корнем с ответами — строка «Replies: N · last reply
  <время>», у горячего поста — «Reply in thread» (у ответа открывает тред корня; нет у ожидающих/
  неотправленных/системных), без CRT у первого ответа серии — строка «Reply to <автор>: <фрагмент>»
  (`isFirstReply` веб-клиента: ответ сразу под своим корнем или под ответом того же треда её не
  получает). Клик по уведомлению с `root_id` открывает канал, затем панель. —
  `frontend/src/components/ThreadPane.test.tsx`, `PostItem.test.tsx`, `feedRows.test.ts`,
  `tests/e2e/threads.spec.ts` (9 сценариев: CRT on/off, «Reply in thread», живой ответ и `PUT read`,
  правка/удаление ответа и корня, вставка картинки в композер треда, 150 ответов до корня без дыр и
  повторов, упоминание в треде → уведомление с `root_id` → панель и погасший бейдж, смена CRT на лету).
- Чужие статусы присутствия приходят только опросом (`status_change` рассылается только самому
  пользователю, не наблюдателям) — опрос идёт по участникам DM и авторам открытого канала, а
  не по всем пользователям сразу. — `TestStatusesArePolledOnLiveAndOnOpenChannel`,
  `TestStatusesArePolledWhenTheOpenChannelLoads`.
- Реакция применяется сразу (оптимистично) и откатывается немедленно только при явном отказе
  сервера (4xx); клики во время запроса — «последний побеждает»; устаревшее эхо своего отменённого
  клика отбрасывается. Сохранение/снятие реакции — идемпотентные запросы на сервере Mattermost и
  документированное исключение из правила «POST не повторяются при сетевых ошибках» (решение
  пользователя 2026-09-25): при неоднозначном исходе (сетевая ошибка, таймаут, 5xx) оптимистичное
  состояние сохраняется и повторяется с бэкоффом 2/5/15 с, откат — только после 4 попыток;
  переключение обратно во время неуверенного ожидания сразу шлёт второй, тоже идемпотентный,
  запрос. Бэкофф-таймеры тикают только пока воркер «живой» (`StatusLive`) — в офлайне ожидающая
  попытка не расходуется впустую; когда воркер снова становится живым, все ожидающие пары
  повторяются сразу, а не ждут истечения своего таймера — офлайн-время не срезает 4 попытки.
  — `TestLateEchoOfUndoneReactionIsIgnored`, `TestReactionRefusedIsRolledBack`,
  `TestReactionClicksWhileInFlightAreQueuedLastWins`,
  `TestReactionNetworkFailureIsRetriedKeepingTheClick`,
  `TestReactionFailingOnIsRolledBackAfterTheLastAttempt`,
  `TestReactionToggledBackWhileWaitingIsSentAtOnce`, `TestReactionRetriedWhenTheWorkerIsLiveAgain`.
- Список загрузок (`downloads` в SQLite, последние 100) читается только по запросу UI (`Downloads`);
  на старте — лишь один UPDATE, помечающий прерванные загрузки `failed`/`interrupted`. Одна запись на
  реальную загрузку (присоединившиеся к общей её не дублируют; повторное скачивание уже сохранённого
  файла поднимает запись наверх). Прогресс — событие `downloads_changed` не чаще ~4 раз в секунду на
  загрузку. «Показать в папке» — D-Bus `FileManager1.ShowItems` с таймаутом 2 с только по действию
  пользователя, при ошибке — каталог через тот же opener, что у «Открыть». Сбой до начала загрузки
  (FileInfo, каталог загрузок) тоже попадает в список как `failed` с кодом. id записей не
  переиспользуются (`AUTOINCREMENT`), поднимается только запись того же файла (сервер, id, путь);
  путь из БД доверяется, только если он абсолютный и ведёт на обычный файл (`os.Lstat`, не симлинк). —
  `TestDownloadsKeepTheLatestHundred`, `TestInterruptedDownloadsFailOnOpen`, `TestDownloadIDsAreNeverReused`,
  `TestSharedDownloadIsOneEntry`, `TestDownloadsAreListedNewestFirst`, `TestRemovedNewestEntryIsNotConfusedWithTheNext`,
  `TestProgressIsThrottled`, `TestFailureBeforeTheDownloadIsListed`, `TestListedPathMustBeARegularAbsoluteFile`,
  `TestOpenDownloadOpensSafeTypesOnly`, `TestRevealFallsBackToOpeningTheFolder`, `TestShowItemGivesUpOnAHungBus`.
- «Открыть» запускает системным приложением только инертные типы из allowlist (растровые
  картинки, PDF, текст/лог/csv/json/md, макро-свободные офисные документы, аудио/видео,
  распространённые архивы); всё остальное (включая `.html`/`.svg`/лаунчеры) — только сохраняется.
  — `TestOpenFileOpensSafeTypesOnly`.
- Текстовый просмотрщик (`TextView`) занимает всю ширину панели, грузит файл целиком до
  `TextFullLimit` (1 МиБ, `?full=1`) — не 64-КиБ фрагмент ленты — и даёт поиск по нему:
  постоянной длины сворачивание регистра (`foldForSearch`, гарантированно той же длины, что и
  исходная строка, иначе сместились бы смещения совпадений), честный кап в 5000 совпадений,
  Ctrl+F перехватывается на `window` (не открывает системный поиск webview), Escape в поле с
  текстом чистит поиск сразу, не всплывая до просмотрщика (пустое поле — всплывает и закрывает
  просмотрщик как раньше). — `frontend/src/components/TextView.test.tsx` («search: counter,
  Enter/Shift+Enter cycle through matches, current match is marked», «a length-expanding case
  fold (İ → i + combining dot) does not misalign later matches», «Ctrl+F focuses the search
  field from anywhere in the viewer», «Escape with text in the search field clears it instead
  of bubbling up, right away (no 150 ms wait)»).
- Зум картинки в просмотрщике (`ImageZoom`) — колесо вокруг курсора, перетаскивание — пан
  (только при масштабе > 1), двойной клик и `+`/`-`/`0` — переключение вписать/100%; кап —
  8× натурального размера. Оригинал запрашивается сразу вместе с превью (оба `<img>` смонтированы
  с самого начала), превью остаётся видимым до готовности оригинала — не наоборот; колёсный и
  клавиатурный обработчики подписываются один раз при монтировании и читают свежее состояние
  через ref, а не переподписываются на каждое изменение масштаба. Корневой `<div>` `ImageZoom`
  занимает всю панель (`data-viewer-empty`) — Viewer.tsx считает клик по нему (не по `<img>`)
  кликом по пустой области и закрывает просмотрщик, как и клик по самой панели; двойной клик
  переключает вписать/100% только когда его цель — сам `<img>` (двойной клик по пустой области —
  это для браузера сначала обычный `click`, который уже закрывает просмотрщик — до `dblclick`
  дело не доходит). — `frontend/src/components/
  ImageZoom.test.tsx` («wheel zooms in around the cursor and reports a growing percentage; wheel
  out returns toward it», «the original is requested immediately, and the preview is shown as a
  placeholder until it loads», «wheel and keyboard zoom subscribe once, not on every scale
  change», «double-click on the image zooms from fit to natural size, and again back to fit»,
  «double-click on the empty area around the image (not on the `<img>`) does not toggle zoom»),
  `frontend/src/components/imageZoom.test.ts`, `frontend/src/components/Viewer.test.tsx`
  («clicking the empty area around the image closes the viewer, while loading and after load;
  clicking the image itself does not», «at zoom > 1, clicking the visible image still does not
  close; clicking the uncovered area around it still does»).
- Клавиатурные шорткаты на букву/цифру/знак пунктуации (не только с модификатором — тот же урок
  для одиночных `+`/`-`/`0`) сравниваются по физической клавише (`KeyboardEvent.code`, например
  `KeyF`/`Digit0`/`Equal`), а не по `KeyboardEvent.key` — на нелатинской раскладке (русской и т.п.)
  `key` для физической F — `'а'`, так что `e.key === 'f'` там молча никогда не сработает; `code`
  от раскладки не зависит. Общий хелпер — `frontend/src/keyboard.ts` (`isShortcut(e, code(s),
  {ctrl?, shift?})`); клавиши без раскладочной зависимости (Escape, Enter, стрелки, Delete,
  Backspace, Tab) по-прежнему сравниваются через `key` напрямую — хелпер им не нужен. `+` на
  многих раскладках — это Shift+Equal: код `Equal` матчится с Shift и без него (плюс `NumpadAdd`
  отдельно, `Digit0`/`Minus` — с `Numpad0`/`NumpadSubtract`). Разобрано на всех буквенных/
  цифровых/пунктуационных шорткатах фронтенда (Ctrl+F в `TextView` и в `Viewer` — переключение
  markdown на Source, `+`/`-`/`0` в `ImageZoom`); Go-сторона (`internal/desktop/observe_gtk.go`,
  жест вставки) уже сравнивает физическую клавишу через `hardware_keycode`/группы раскладки
  (`spk_key_is`) — менять не нужно. — `frontend/src/keyboard.test.ts`, `frontend/src/components/
  TextView.test.tsx` («Ctrl+F works on a Russian keyboard layout (key is "а", not "f" — matched by
  the physical key instead)»), `frontend/src/components/Viewer.test.tsx` («markdown: Ctrl+F works
  on a Russian keyboard layout…»), `frontend/src/components/ImageZoom.test.tsx` («+/- and 0 keys
  work regardless of keyboard layout (matched by the physical key, not the character it
  produces)»).
- Видео и аудио стримятся, не кэшируются на диске и не буферизуются в памяти: `MediaPlayer`
  берёт URL у `streamURL()`/`MediaStreamBase` (Task 7) и рендерит `<video controls>`/
  `<audio controls preload="none">`. Играет одновременно только один элемент во всём приложении
  (`mediaSession.registerMediaElement` ставит остальные на паузу); элемент, ушедший из DOM
  (виртуализация ленты, смена канала) или переставший показываться (сорвался стрим — карточка
  вместо плеера, без размонтирования `MediaPlayer`), останавливается и освобождается
  (`removeAttribute('src'); load()`) — иначе WebKit держит декодер и буферы (урок 13b). Видео в
  ленте — `preload="none"` и постер до клика; `play()` вызывается синхронно внутри обработчика
  клика, иначе WebKitGTK молча отказывает в воспроизведении вне пользовательского жеста. —
  `frontend/src/components/MediaPlayer.test.tsx` («video: fixed box before load, preload="none",
  poster placeholder; clicking it calls play() synchronously inside the click», «only one player
  plays at a time: starting one pauses the other», «unmounting releases the element: paused, src
  dropped, reloaded (Task 13b)», «a stream failure releases the element right away, not just on
  unmount (it's swapped for a card, not unmounted as a whole)»), `frontend/src/components/
  mediaSession.test.ts`.
- Медиа-поток десктопа — отдельный loopback HTTP-сервер (`internal/media.Loopback`, Task 7,
  вариант A пользователя 2026-09-27), потому что WebKitGTK/GStreamer не умеет читать `wails://`
  (спайк S6): слушает `127.0.0.1:0`, поднимается лениво по первому вызову `MediaStreamBase` и
  живёт до выхода приложения; URL несёт сессионный токен (32 случайных байта, новый при каждом
  запуске, сравнение за постоянное время, никогда не логируется) —
  `http://127.0.0.1:<port>/<token>/<srv>/stream/<id>`. Каждый запрос проверяет `Host` (защита от
  DNS rebinding), метод (только `GET`/`HEAD`), не отдаёт CORS-заголовков и белый список
  медиатипов; ответы — `Cache-Control: no-store`, чтобы токенизированный URL и байты не осели в
  дисковом кэше WebKit. В browser-режиме тот же вид `stream` отдаётся по обычному
  `/media/<srv>/stream/<id>` на уже существующем loopback-сервере UI — отдельного сервера там не
  нужно, вебвью и так браузер. — `internal/media/loopback_test.go`
  (`TestLoopbackStartsLazilyOnItsOwnLoopbackPort`, `TestLoopbackRejectsWrongTokenHostAndMethod`,
  `TestLoopbackCloseStopsServingAndCancelsStreams`, `TestLoopbackTokenNeverReachesTheLogs`),
  `internal/media/stream_test.go` (`TestStreamPassesRangeThroughAs206`,
  `TestStreamWritesNothingToTheCache`, `TestStreamCancelClosesTheUpstream`).
- Markdown-файлы (`.md`/`.markdown`) в ленте — фрагмент, отрендеренный тем же компонентом
  `Markdown`, что и текст поста (удалённые картинки — ссылками, никогда не загружаются; ссылки —
  в системный браузер), фиксированной высоты, как у текстового фрагмента, пока не развёрнут; в
  просмотрщике — рендер на всю ширину с переключателем «Оформление»/«Исходный текст»
  (`aria-pressed` на обеих кнопках группы), источник тот же `text?full=1`, что и у `TextView`. —
  `frontend/src/components/MarkdownSnippet.test.tsx` («renders a heading, a list and a code
  block; fixed height until expanded»), `frontend/src/components/MarkdownView.test.tsx`
  («renders heading, list, code and table; a remote image is a link, not an <img>»),
  `frontend/src/components/Viewer.test.tsx`.
- Вложения — модель угроз: скрипт страницы (XSS в отрендеренном контенте) может вызвать **любую**
  привязку, в т.ч. `SendPost`, а вложения грузятся на сервер сразу — значит, всё, что скрипт сумел
  поставить во вложения, он может и отправить. Поэтому: (1) UI **никогда не передаёт в Go пути** — ни в
  одном методе API; источники вложений принимают только сервер и канал (`AttachFromClipboard`,
  `PickAttachments`); общее правило держится ревью (рефлексией путь от строки не отличить);
  (2) каждый источник требует **нативного** действия пользователя, которое страница подделать не может
  (буфер — нажатая клавиша вставки, drop — нативный drop на webview только из другого приложения,
  📎 — клик в модальном системном диалоге); (3) файлы каталога данных приложения (`~/.spk/mm-client`: база с токенами, кэши, спулы) не
  ставятся никогда, симлинк сначала разрешается (`EvalSymlinks`) — и ставится как свой target (с его
  именем). Остаётся: файл, который пользователь действительно вставил/уронил/выбрал, скрипт может
  отправить без спроса. — `TestAttachmentSourcesTakeNoPaths`, `TestAppDataIsNeverStaged`,
  `TestClipboardWithoutAPasteKeyReadsNothing`, `TestForgedDropIsRefused`,
  `TestNativeDropOnAChannelAttachesItsPaths`, `TestPickAttachmentsAttachesTheChosenFiles`.
- Буфер обмена GTK (`internal/desktop/clipboard_gtk.go`, cgo за тегами `wails && gtk3`, заглушка —
  `clipboard_other.go`): читается **только в течение 1,5 с после нативного Ctrl+V / Ctrl+Shift+V /
  Shift+Insert** в окне (`key-press-event` на GtkWindow в `observe_gtk.go`, событие не поглощается; для
  нелатинских раскладок сверяется физическая клавиша), нажатие расходуется одним чтением, иначе
  `no_paste_gesture` и ничего не читается; вставка из контекстного меню не поддерживается (решение
  реестра). GTK зовётся только на главном потоке (`application.InvokeAsync`), только асинхронно
  (`gtk_clipboard_request_*`, не `wait_*`), весь вызов — под одним дедлайном в Go (`clipboardTimeout` =
  2 с): зависший владелец не блокирует наш вызов (`clipboard_failed`), поздний ответ освобождается
  (`pending`). Ждать ответа на главном потоке нельзя — он и отвечает. Байты буфера копируются в память C
  и читаются оттуда в спул, в кучу Go целиком не попадают. Порядок: список файлов (`text/uri-list` —
  только `file://` без хоста или `localhost`, с percent-декодированием; иначе
  `x-special/gnome-copied-files`, «cut» — как copy) → каждый путь; иначе картинка: `image/png` как есть,
  иной формат — в PNG через gdk-pixbuf (вне главного потока), имя `Screenshot YYYY-MM-DD HH-MM-SS.png`;
  иначе ничего. — `TestClipboardWithoutAPasteKeyReadsNothing`, `TestPasteGateTakesARecentKeyOnce`,
  `TestHungClipboardOwnerTimesOut`, `TestClipboardDeadlineCoversTheWholeCall`, `TestPendingLateAnswerIsFreed`,
  `TestPendingRacingAnswerAndTimeoutNeitherLeaksNorLoses`, `TestParseURIListKeepsLocalFilesOnly`,
  `TestParseGnomeCopiedFilesTreatsCutAsCopy`, `TestClipboardPNGIsSpooledAsAScreenshot`,
  `TestClipboardOtherImageIsConvertedToPNG`, `TestClipboardGnomeCopiedFilesWithoutURIList`,
  `TestClipboardTextAttachesNothing`, `TestClipboardFolderIsRefusedOthersAttached`; настоящий буфер —
  смоук на своём Xvfb (отчёт Task 4 плана `docs/plans/2026-09-27-stage3-attachments.md`).
- Перетаскивание файлов: окно создаётся с `EnableFileDrop: true` — Wails забирает drag с путями на уровне
  GTK (страница drag-событий не видит), отдаёт пути через JS (`handlePlatformFileDrop` → `FilesDropped`)
  и зовёт `WindowFilesDropped` только для элемента с `data-file-drop-target`; его `data-srv`/`data-channel`
  выбирают канал (`filesDropped`). Путь через JS подделываем, поэтому наш наблюдатель
  `drag-data-received` на самом webview (`observe_gtk.go`, подключён после обработчика Wails и ничего не
  останавливает) записывает пути **нативного** drop только из другого приложения (drag, начатый в нашем процессе —
  `gtk_drag_get_source_widget` ≠ NULL — не записывается: `text/uri-list` своего drag страница задаёт
  сама через `dataTransfer.setData`), и `dropGate` пропускает путь из `WindowFilesDropped`
  только если нативный drop нёс его за последние 5 с — один раз; прочие — отказ `not_dropped`. Не больше
  100 путей за drop (остальное — `too_many`). Отказы приходят событием `attachment_refused {server_id,
  channel_id, code}` — у drop нет вызывающего. Нет наблюдателя (не нашёлся webview, не-GTK) — отказ всем
  drop. **Принятый остаточный риск** (ревью Task 4, решение реестра): нативный drag из **другого**
  приложения (например, перетаскиваемый элемент страницы во внешнем браузере с подделанным
  `text/uri-list`, `dataTransfer.setData` на чужой странице) — это настоящий внешний drop, и он
  принимается как обычный: наблюдатель различает только «начался ли drag в нашем процессе», а не
  «доверенное ли содержимое». Использовать это для эксфильтрации всё равно нужен XSS в **нашем**
  приложении (drop только складывает вложение, отправка — отдельное явное действие или вызов
  `SendPost` тем же XSS); Wayland с этим наблюдателем не проверялся (`docs/backlog.md`). —
  `TestForgedDropIsRefused`, `TestNativeDropOnAChannelAttachesItsPaths`, `TestHugeDropIsCapped`,
  `TestDropGateStaysBounded`, `TestDropWithoutAChannelTargetIsIgnored`, `TestDropRefusalIsReported`,
  `TestDroppedFilesAreAttachedAndRefusalsReported`.
- Имена вложений из любого источника (путь или страница) чистятся одинаково (`attach.cleanName`):
  последний элемент пути, без управляющих и format-символов (bidi, zero-width), `.`/`..`/пусто →
  `attachment`, ≤ 200 рун с коротким расширением. — `TestCleanNameKeepsWhatAFileNameShows`,
  `TestAddPathCleansTheName`, `TestBrowserUploadNameIsCleaned`.
- Диалог 📎 (`internal/desktop/picker.go`) — единственный модальный диалог: только по клику
  пользователя (привязка `PickAttachments`, не на старте), никогда не с главного потока GTK (Wails крутит
  его `gtk_dialog_run`-ом на главном потоке, горутина ждёт), не держит локов, которых ждёт кто-то ещё
  (его `TryLock` — только его собственный), один за раз (второй клик ничего не делает); до открытия
  проверяются канал, `EnableFileAttachments` и лимит 10. `gtk_dialog_run` — вложенный цикл, который выход
  из приложения не завершает: задача завершения Wails отменяет открытый диалог и каждый, что откроется
  после неё (`cancelFileDialogs` + emission hook на `map`), иначе выход из трея/SIGTERM висел бы до
  закрытия диалога (смоук Task 4). — `TestPickAttachmentsChecksBeforeTheDialog`,
  `TestNoClipboardOrPickerIsUnsupported`.
- Browser-маршрут загрузки `POST /api/attachments/{srv}/{channel}?root=&name=&mime=` — те же защиты,
  что у `/api/` (bearer только заголовком — не query-токен на POST, `OriginGuard`, `LoopbackHostGuard`),
  сырое тело через `http.MaxBytesReader(MaxFileSize)` (413 `too_large` и по `Content-Length`, и по потоку),
  имя очищается: как у всех вложений (`attach.cleanName`). `root` пустой — вложение в композер канала;
  непустой — композер ответа, admitted только если `root` уже открыт в панели (см. следующий пункт). —
  `TestBrowserUploadAttachesTheRawBody`, `TestBrowserUploadNeedsTokenOriginAndLoopbackHost`,
  `TestBrowserUploadOverMaxFileSizeIs413`, `TestBrowserUploadNameIsCleaned`, `TestBrowserUploadErrors`,
  `TestBrowserUploadWithRootGoesToTheThreadsComposer`.
- Вложения и черновик ответа (Task 4) — по ключу (сервер, канал, корень): `attach.Store`'s `key{srv, ch,
  root}` (`root=""` — композер канала). Все источники вложений (`Attachments`, `AttachFromClipboard`,
  `PickAttachments`, `AttachDropped`, drop, browser-маршрут) и `SendReply`/`SaveThreadDraft` принимают
  `rootID`; непустой `rootID` допускается только если это тред, который воркер держит в кэше тредов этого
  канала (`state.Server.ThreadHeld` — открытый или недавний), иначе `no_post` — панель треда не может
  прилететь раньше своего `OpenThread`. `attach.Store.Take` (её использует и `SendPost`, и `SendReply`) —
  all-or-nothing по тому же ключу: вложение из ответа не уйдёт в пост канала, и наоборот. Загрузка файла
  на сервер всё равно идёт с `channel_id` канала (сервер Mattermost не знает про "композер ответа") —
  корень влияет только на локальный список и `Take`. Лимит 10 вложений — на ключ, а не на канал целиком:
  у канала и у каждого его треда — свои десять. `api.Service.onAttachments(srv, ch, root)`, когда
  прогресс ожидающего поста реально изменился, шлёт **и** `channel_changed`, **и** (при `root != ""`)
  `thread_changed` — без CRT ответ виден и в ленте, и в панели, обоим нужно обновление (ревью Task 4,
  раньше слался только один); события `attachments_changed`/`attachment_refused` несут `root_id`.
  Desktop drop: цель несёт `data-root` (нет атрибута — `""`), `internal/desktop/drop.go` читает его как и
  `data-srv`/`data-channel`. Черновики треда — `state.Server.
  SetThreadDraft`, отдельная от `s.drafts` карта `s.threadDrafts` (+ `s.threadDraftOrder` для вытеснения —
  порядок по времени последнего обновления, не вставки: вытесняется тот, кого дольше всего не трогали),
  ограничена `ThreadDraftCap` = 50, только в памяти (не в снимке), удаляется с уходом из канала (вместе
  с его тредами), пустой текст — как обычный `SetDraft`. `Worker.Retry`/`Discard` пересылают `Change` от
  `state.RetryPending`/`DropPending`, которая несёт `Threads` так же, как `FailPending` — иначе повтор
  или отмена ответа обновляли бы только (скрытую под CRT) ленту канала, не панель. — `TestThreadAttachmentsAreNotSentToTheChannel`,
  `TestAttachToUnknownRootIsRefused`, `TestThreadDraftsAreBounded`, `TestThreadDraftEvictionIsByLastUpdate`,
  `TestReplyIsCreatedWithRootID`, `TestRetryOfAReplyRefreshesTheThreadToo`, `TestDiscardOfAReplyRefreshesTheThreadToo`.
- 400 `api.post.create_post.root_id.app_error` на `SendReply` — неоднозначный: сервер отдаёт его и когда
  корень удалён, и на непричастную ошибку стора, и (в защитных целях) когда «корень» сам оказался
  ответом (mm-10.11 `post.go:296,305` — одна и та же строка id на все три случая). Поэтому сам этот код
  не помечает тред `RootDeleted` — только помечает `state.Server.MarkThreadStale` (без сдвига общей эпохи
  кэша) и просит перечитать тред (`Worker.loadThread`); удалённым тред становится лишь по-настоящему,
  если перечитывание само вернёт 404 (`FailThread` → `rootGoneLocked`, тот же путь, что и обычный опрос
  панели) — успешный ответ вместо этого либо ничего не делает, либо (если «корень» и правда был ответом)
  включает redirect на настоящий корень (Task 3). — `TestRootDeletedBeforeSendFailsThePending` (тред
  «молчит» — `mmfake.Server.DeleteAsQuiet`, без `post_deleted` в WS; так проверяется именно этот путь, а
  не обычный live-апдейт: ревью нашло, что со старым тестом `DeleteAs`'а хватало одного WS-события, и код
  подтверждения можно было выключить без сбоя теста), `TestAmbiguousRootErrorConfirmsInsteadOfAssumingDeleted`.
- Вложения композера треда, оставшегося без композера (уход из канала уносит с собой и его открытые/
  недавние треды — `forgetChannelLocked`/`forgetThreadsLocked`), освобождаются: `state.Server.
  TakeForgottenComposers` копит покинутые ключи (канал `Root:""` и каждый его тред), `Worker.
  releaseForgottenComposers` разбирает их так же лениво, как `releaseFiles` — `TakeReleased` (при
  следующем вызове `changed()`, не обязательно сразу), вызывая `attach.Store.ReleaseComposer` (снимает
  вообще всё, включая `StateUploading` — та загрузка отменяется так же, как при `Remove`, а не донашивается
  оставленной в списке: до fix round 2 незавершённая загрузка оставалась в списке и могла «воскреснуть»,
  если пользователь вернётся в канал/тред раньше, чем она сама себя разрешит). Тред, просто вытесненный из
  LRU без ухода из канала, этим не покрыт (`docs/backlog.md`). — `TestTakeForgottenComposersOnChannelLeave`,
  `TestReleaseComposerDropsEverythingIncludingUploading`, `TestChannelLeaveReleasesForgottenComposers`.
  Drop: `TestNativeDropOnAThreadPanelAttachesItsPaths`.
- UI (Task 5): staged (pending, не отправленные) картинки превьюются с `/media/<srv>/staged/<id>`
  (`mediaURL(serverId, 'staged', id)`), **не** `feed`/`thumb` — те требуют настоящий id файла поста,
  которого у вложения ещё нет; `ImageTile` (`Attachments.tsx`) различает по `FileView.staged` и рендерит
  staged-картинку как обычный блок (не кнопку — просмотрщик на такой id не расcчитан) с прогрессом/ошибкой
  под ней (`FileCard.tsx` → `StagedProgress`, общий для картинок и карточек файлов). `state.FileView` несёt
  `state/sent/error` только пока `staged`; `api.Service.onAttachments` обновляет их у ожидающих постов через
  `state.Server.RefreshPendingProgress` (клонирует `Pending.Files` целиком при реальном изменении — точечная
  мутация элемента раньше гонялась с уже отданным вызывающему срезом `ChannelView`, `-race` поймал) и шлёт
  `channel_changed` по тому же пути, что и обычное обновление окна постов — отдельного события для прогресса
  нет. Все ошибки вложений композера (лимиты, `no_paste_gesture`, отказ drop, ошибка загрузки в браузере)
  сведены в одно поле стора `attachError` (не в локальный `error` Composer'а, который остаётся только для
  неудачной отправки сообщения) — так `ChannelPane`'шный drop и глобальное событие `attachment_refused`
  могут показать текст в композере без проброса колбэков; сбрасывается со сменой канала, как `attachments`.
  Вставка/drop реализованы ровно по правилу спайка (desktop: `text/uri-list` пуст → `attachFromClipboard` с
  `preventDefault`; нет `text/plain` → без `preventDefault`; иначе не трогается; browser: `clipboardData.files`/
  `dataTransfer.files`) — тесты `Composer.test.tsx`, `ChannelPane.test.tsx` явно проверяют, что desktop-ветка
  никогда не читает `clipboardData.files` и что скрытый `<input type=file>` не рендерится в desktop-режиме
  (структурная гарантия «никаких байт с десктопа», не только доверие к поведению WebKitGTK).
- Оптимистичная отправка (2026-09-30): локальный pending-пост сразу выглядит как обычный —
  без `opacity-60` и строки «Отправка…». `PostItem` показывает `role=status` только через
  3 с от локального `create_at`, если подтверждения ещё нет; это ожидание, не ошибка и
  не таймаут запроса. Таймер очищается при подтверждении/ошибке/размонтировании; повторный
  mount при виртуализации не начинает отсчёт заново. Явная ошибка показывается сразу с
  Retry/Discard. Кнопка отмены ожидающих вложений остаётся вместе с отложенным статусом;
  прогресс самих файлов сохраняется. Ключ `pending_post_id` удерживает строку при ack.
  Тесты: `PostItem.test.tsx`, `optimistic-send.spec.ts` — канал и тред, реальные задержки
  фейка 2 с (одинаковая высота до/после ack) и 4.5 с (статус после 3 с, без ложной ошибки).
  В e2e отсутствие «Отправка…» больше не означает ack: `expectSent` ждёт доступных действий
  отправленного поста. Вложенный locator `filter({has:…})` должен быть относительным,
  без `[data-feed]` в нём, иначе пустая выборка с `toHaveCount(0)` скрывает ошибку теста.
  Dev-only `/api/_test/fake/post-latency` `{ms:0..10000}` задаёт задержку пути `/api/v4/posts`,
  0 снимает её; доступен только с `--test-api`, проверяет входной диапазон.
- Пост с файлами ждёт своих загрузок **без дедлайна**: офлайн — пока сервер не станет «живым»;
  проваливается только при ошибке загрузки, отмене (discard) или остановке воркера; `createTimeout`
  (30 с) начинается лишь после загрузок, на сам `CreatePost` — в отличие от текстового поста, который
  проваливается через 30 с. — `internal/mmsync/actions.go` (`Worker.create`),
  `internal/mmsync/files_test.go` (`TestSendWithFilesWaitsForTheUploadsThenCreates` — «not failed by the
  create timeout's clock», `TestFailedUploadFailsThePostAndRetryUploadsAgain`,
  `TestDiscardLetsTheFilesGo`, `TestStoppingWorkerLetsTheFilesOfItsPendingPostsGo`),
  `internal/attach/attach_test.go` (`TestOfflineAttachmentsWaitForLive`, `TestWaitReportsTheOutcome`).
- UI (Task 6): после удаления чипа Delete/Backspace фокус переходит на чип, занявший его место
  (следующий), иначе на предыдущий, иначе в textarea композера — `AttachmentsTray` запоминает id и индекс
  удаляемого чипа (удаление идёт через store/API, не синхронно) и решает, куда ставить фокус, только
  когда чипа с этим id в `items` больше нет — неудавшееся удаление (чип остался) не двигает фокус,
  когда позже уходит другой чип («a keyboard removal that fails does not move focus when another chip
  goes later»). —
  `frontend/src/components/AttachmentsTray.test.tsx` («Delete on a middle chip moves focus to the
  chip that took its place», «Delete on the last chip moves focus to the previous one», «Delete on
  the only chip focuses the textarea»).
- e2e (Task 6, `tests/e2e/attachments.spec.ts`, browser mode): вставка картинки/файла (синтетический
  `ClipboardEvent`+`DataTransfer`+`File`), перетаскивание (синтетический `DragEvent`), 📎
  (`setInputFiles`), ошибка загрузки → «Повторить» (`fake/fail-uploads`), лимит `MaxFileSize`
  (`fake/max-file-size` — см. ниже), пост из одних вложений без текста. — `paste an image: a chip
  appears, Enter sends a post with the image`, `paste a non-image file: a file card appears in the
  sent post`, `drag-and-drop a file over the channel: a chip appears`, `📎 opens the file picker: a
  chip appears`, `a failed upload shows Retry; retrying sends it`, `MaxFileSize limit: attaching an
  over-limit file shows an error, nothing is attached`, `attachments only, no text, is a valid post`.
- Каждый созданный Playwright-контекст/страница (в `browser_run_code_unsafe` или в скриптах)
  закрывается в том же вызове (`try`/`finally` → `ctx.close()`); окна не оставляются открытыми.
  После работы с браузером проверить, что не осталось висящих контекстов — пользователь уже
  жаловался на «наплодил pw окон» (Task 14, ledger).
- UI pass (2026-09-28): панель действий поста рендерится только для «горячего» поста (наведение/
  фокус или открытое своё меню «…»/пикер реакций) — не скрытый CSS-ом тулбар в каждой строке ленты,
  иначе с картинками быстрых реакций каждая скрытая панель грузила бы чужие эмодзи-картинки.
  `PostItem` держит локальный `hot` (`pointerenter`/`focusin` → true, `pointerleave`/`focusout` → false,
  если фокус ушёл из статьи и не открыто меню/пикер); состояние не трогает виртуализатор ленты. —
  `frontend/src/components/PostItem.test.tsx` («the toolbar is not rendered before hover/focus; it
  stays visible while the "…" menu is open after the pointer leaves», «a pending or failed post shows
  no toolbar at all, hovered or not»).
- UI pass (2026-09-28): любая иконка кнопки/ссылки/чипа/строки списка/бейджа — инлайн-SVG-компонент
  из `frontend/src/components/icons.tsx` (`viewBox="0 0 24 24"`, `fill="currentColor"`, `aria-hidden`),
  не сырой символ эмодзи/dingbat/стрелка; библиотеку иконок не подключаем (25 путей дешевле бандла и
  рантайм-памяти). Настоящие эмодзи сообщений/реакций (`EmojiGlyph.tsx`, `frontend/src/emoji/`) —
  исключение, они вычисляются из данных сервера в рантайме и не матчатся сканом. —
  `frontend/src/components/noEmojiIcons.test.ts` (сканирует все `.ts`/`.tsx` под `frontend/src` на
  символы из широкого диапазона «похоже на иконку», не только `components/*.tsx` — узкий скан уже
  однажды пропустил каналовые маркеры приватности).
- «Сохранить» пост (панель действий, `IconBookmark`/`IconBookmarkFilled`) — предпочтение
  `flagged_post` (имя — id поста, значение `"true"`; снятие — `DELETE`-подобный
  `POST /api/v4/users/{user_id}/preferences/delete`, у нас всегда `me`, как и у сохранения). Пишет
  `internal/mmsync.Worker.SetSaved` (не повторяется при ошибке, как остальные `POST`; успешный ответ
  сразу обновляет `state.Server` — `SetPostSaved` — не дожидаясь эха `preferences_changed`/
  `preferences_deleted`); `needs_reauth` отказывает сразу `session_expired`, как и другие действия
  записи (`s.writer()`). `PostView.Saved` вычисляется только из хранимых предпочтений
  (`prefKey{"flagged_post", postID}`), никакого оптимистичного локального состояния в UI — кнопка не
  может «залипнуть». Приход `flagged_post` по WS (`preferences_changed`/`preferences_deleted`, любое
  устройство) помечает канал этого поста изменённым — так же, как реакция, — не только
  Sidebar/Badge, которые обновляет любое предпочтение. —
  `internal/state/view_test.go` (`TestPostViewSavedComesFromFlaggedPostPref`),
  `internal/state/events_test.go` (`TestFlaggedPostPrefEventMarksTheChannelChanged`),
  `internal/mmsync/saved_test.go`, `internal/api/saved_test.go`
  (`TestSetPostSavedSessionExpired`), `frontend/src/components/PostItem.test.tsx` («save button:
  not-saved and saved states, and the click it sends»).
- Клик по тексту существующего треда в ленте (2026-09-30): `PostItem` открывает панель
  для ответа (`root_id`) и корня с `reply_count > 0`. Раньше обработчик был только у
  «Ответов…», строки контекста и кнопки тулбара; текст ответа прямо под корнем ничего
  не делал. Обработчик только на тексте сообщения: не на вложениях/реакциях/шапке.
  Ссылки, элементы управления, код, выделение текста, клики с модификаторами, режим
  редактирования, ожидающие/ошибочные/системные/эфемерные посты и сама панель исключены.
  Кнопки открытия остаются для клавиатуры. — `PostItem.test.tsx`, `threads.spec.ts`
  («clicking thread message text…»). Проверка чтения треда в e2e допускает `null`
  от фейка до первого PUT read, но всё равно ждёт ненулевой счётчик чтений.
- Тема — одна, нейтральный тёмный графит (обновление 2026-09-30 по просьбе пользователя:
  прежние сине-фиолетовые цвета не понравились; подтверждена тёмная тема). Фон `#202225`,
  текст `#d3d6db`, акцент `#82b4f2`; Catppuccin Macchiato заменена. Все 32 цветовых токена
  находятся в `frontend/src/index.css` в одном `@theme`-блоке, включая 4 сайдбар-токена
  и 9 цветов типов файлов. Прочитанные каналы остаются хорошо читаемыми, непрочитанные —
  ярче и жирным. `danger-fg` используется также на бейджах упоминаний.
  Компоненты не хардкодят hex; исключение — предзагрузочный `<style>` в `frontend/index.html`:
  фон `html,body` должен совпадать с `--color-app`, чтобы при запуске не мелькала другая тема.
  Без переключателя и `prefers-color-scheme`; свитчер или тема сервера — `docs/backlog.md`.
  Шрифт — встроенный Open Sans (обычный и курсивный variable TrueType, кириллица),
  `frontend/src/fonts/`; лицензия поставляется в `frontend/public/licenses/`. `text-sm` —
  14 px при высоте строки 20 px, `text-xs` — 12/16 px, как в оригинальном клиенте.
  Source Sans 3 отвергнут пользователем как сжатый по горизонтали. Аватар поста — 32 px
  (был 36 px); правое выравнивание в прежней колонке сохраняет положение текста. Не включать CSS-сглаживание принудительно: эффект зависит от
  платформы; проверять именно системный WebKitGTK, не только Chromium.
  Контраст: текст/фон 10.94:1, вторичный текст/фон 8.72:1, подписи/панель 5.96:1,
  прочитанный канал/сайдбар 10.08:1, текст кнопки/акцент 8.20:1.
- Сохранение ширины панелей: `httpClient` и `wailsClient` округляют ширину через
  `Math.round` непосредственно перед `SetSidebarWidth`/`SetThreadWidth` (Go принимает
  `int`). Координаты PointerEvent при GTK/HiDPI бывают дробными: без округления Wails
  отклонял аргумент (`cannot unmarshal number 537.968… into Go value of type int`), и
  ширина не сохранялась. Live CSS остаётся дробным для плавности. —
  `api/client.test.ts`, `api/wailsClient.test.ts` (четыре значения из пользовательского
  лога), `layout.spec.ts` (дробные pointerdown/up → сохранение целых → reload).
- Ширина сайдбара и панели треда — сплиттеры (`frontend/src/components/Splitter.tsx`, `role=
  separator`, стрелки на 16 px, Home/End, двойной клик — сброс), сохраняются один раз на всё
  приложение (не на сервер): `internal/store`'ная таблица `ui_prefs` (плоский key-value,
  `Get/SetUIPref`) и биндинги `API.GetLayout/SetSidebarWidth/SetThreadWidth`. Во время
  перетаскивания в React-состояние ничего не пишется — только CSS-переменная
  (`--spk-sidebar-width`/`--spk-thread-width` на `document.documentElement`) через
  `requestAnimationFrame`; коммит (в состояние и в БД) — один раз на `pointerup`/шаг
  клавиатурой/двойной клик, не на каждый кадр. Границы (сайдбар 180–480 px, панель
  320–min(800, 50vw), лента не уже 360 px) пересчитываются при ресайзе окна, но сам пересчёт не
  сохраняется — тем самым виджет никогда не проваливается ниже своего минимума, но после
  перезапуска на более широком окне вернётся к сохранённому (не урезанному) значению
  (`docs/backlog.md`). Строки ленты перемеряет их собственный `ResizeObserver` (`v.measureElement`,
  `@tanstack/react-virtual`), а перетаскивание сплиттера — не колёсный/тач-жест, так что
  `ScrollShift` не откладывает компенсацию (см. «Компенсация ленты» выше) — она уходит сразу в
  `scrollTop`; ленту, стоящую внизу, держит внизу bottom-stick (правило выше). — `frontend/src/components/splitter.test.ts`, `Splitter.test.tsx`,
  `internal/store/uiprefs_test.go`, `internal/api/layout_test.go`, `tests/e2e/layout.spec.ts`.
- Автодополнение композера `@ ~ : /` (brief 2026-09-29): все запросы к серверу — в Go
  (`api.Autocomplete(srv, kind, channel, root, prefix)`, `mmsync.Worker.Autocomplete`); UI передаёт
  только вид, id и слово после триггера. Проверки: вид из четырёх, id — `[A-Za-z0-9_-]{1,64}`, префикс
  ≤ 64 рун без пробелов/управляющих (иначе пустой ответ без запроса), канал — наш. Запрос идёт под
  контекстом вызова: UI дебаунсит 150 мс, у каждого запроса номер (поздний ответ отбрасывается) и
  `AbortSignal` — более новое слово отменяет старый запрос и в Go (Wails-метод принимает
  `context.Context`, отмена промиса отменяет его; HTTP — контекст запроса). Ответы — в LRU воркера
  (50 записей, 60 с; очищается, когда воркер останавливается — выход, новый вход, удаление); офлайн —
  без запросов (эмодзи — только из индекса); на старте ничего. Триггеры как у веб-клиента: `@`/`~` сразу
  (не внутри слова/адреса — `a@b` не срабатывает), `:` с 2 символов в начале слова, `/` только в самом
  начале сообщения; пока всплывашка открыта, Enter вставляет и **не** отправляет, Esc закрывает её
  для этого слова и не доходит до панели треда. `/`-сообщение выполняется **только** явной отправкой
  (`ExecuteCommand`, не ретраится, в треде — с `root_id`, корень должен быть в кэше тредов); неизвестная
  команда оставляет текст и предлагает отправить его сообщением. Команды, опасные в обход веб-клиента
  (fix round 1 ревью безопасности): `/leave` с `root_id` — отказ `command_unsupported_in_thread` без
  запроса (сервер игнорирует `root_id` и выходит из всего канала); `/leave` в закрытом канале — только
  после встроенного подтверждения; успешный `/logout` — `Service.Logout` (сервер лишь отвечает
  `goto_location`); сетевая ошибка/таймаут выполнения — `command_uncertain` («команда могла
  выполниться»), не повтор. Запросы автодополнения и команд привязаны и к жизни воркера
  (`Worker.withLife`), ключ кэша `@` содержит команду. Тела `/api/*` ограничены `MaxAPIBody` = 1 МиБ
  (загрузки — свой маршрут). Эфемерные посты (`ephemeral_message`) живут отдельно от окон (не в снимке,
  ≤ 50 на сервер, уход из канала их убирает; правка/удаление по id применяются, id настоящего поста не
  показывается дважды); ответ самого сервера (не бота/webhook) — автор «Система» (`system_author`). `~имя` в сообщении —
  ссылка на канал, если его знает сайдбар выбранного сервера (`ChannelItem.slug`). — `internal/mm/rest/
  autocomplete_test.go`, `internal/mmfake/autocomplete_test.go`, `internal/mmsync/aclru_test.go`
  (`TestAutocompleteCacheIsBoundedLRUWithTTL`), `internal/mmsync/autocomplete_test.go`
  (`TestAutocompleteIsCachedPerPrefixForAMinute`, `TestAutocompleteRequestIsCancelledWithItsContext`,
  `TestAutocompleteOfflineIsLocalOnly`, `TestAutocompleteCacheIsDroppedWhenTheWorkerStops`,
  `TestExecuteCommandCarriesTeamAndThreadRoot`, `TestEphemeralAnswerShowsInTheChannelAndTheThread`,
  `TestLeaveInAThreadIsRefusedWithoutASend`, `TestAutocompleteAndCommandsEndWithTheWorker`,
  `TestDMUsersCacheIsPerTeam`), `TestLeaveInAThreadIsACodedRefusal`, `TestLogoutCommandSignsTheServerOut`,
  `TestExecuteCommandTimeoutIsUncertain`, `TestAPIBodyIsCapped`, `TestEphemeralPostsAreEditedDeletedAndDeduped`,
  `TestEphemeralAuthorIsTheSystem`,
  `internal/api/autocomplete_test.go` (`TestAutocompleteValidatesItsInput`,
  `TestExecuteCommandThroughService`, `TestSignOutEndsTheAutocompleteSession`),
  `internal/api/transport/http_test.go` (`TestAbortedAutocompleteCancelsItsContext`),
  `internal/state/ephemeral_test.go`, `frontend/src/autocomplete.test.ts`,
  `frontend/src/components/ComposerAutocomplete.test.tsx`, `ComposerCommand.test.tsx`,
  `ChannelMention.test.tsx`, `tests/e2e/autocomplete.spec.ts` (пишет только в «Secret» и возвращает
  alice статус online после `/away`: фейк общий для всех спеков, а `chat.spec.ts` ждёт на экране
  последние посты Town Square). Фейк: `/users/autocomplete`, `/teams/{id}/channels/autocomplete`,
  `/emoji/autocomplete`, `/teams/{id}/commands/autocomplete`, `/commands/execute` (`/echo`, `/shrug`,
  `/away` — эфемерный ответ; остальное — 404 `not_found`), `ExecutedCommands()`; публичный канал
  «Offices», где alice нет. Поиск по сообщениям и Ctrl+K не согласованы (`docs/backlog.md`).

## Things that bite

- Для UI-smoke на Xvfb задавать `LANG=en_US.UTF-8 LANGUAGE=en_US LC_ALL=en_US.UTF-8`:
  при локали `C` системный WebKitGTK отдаёт её как язык, и форматирование дат падает
  с `RangeError: invalid language tag: C` — пустое окно ещё до проверки темы. Найдено
  при обновлении темы 2026-09-30; окружение со штатной локалью отображается нормально.

- **A `fetch()` to `wails://` with a `Blob`, `File` or `FormData` body crashes the whole desktop app** (SIGSEGV in `webkit_uri_scheme_request_get_http_body`, WebKitGTK 2.52 — Wails reads every scheme request's body). Only `Uint8Array`/`ArrayBuffer` bodies are safe there. The desktop UI never sends file bytes at all (Go reads the clipboard/dialog/drop itself); browser mode posts a `File` only to `/api/attachments` over plain HTTP. — `docs/spikes/2026-09-27-attachments-spike.md` §2.6.
- WebKitGTK never gives the page pasted or dropped file contents (`clipboardData.files`/`dataTransfer.files` are always empty — `DataTransfer::allowsFileAccess()` is `false` off Cocoa): a pasted picture is an empty `paste`, copied files a hidden `text/uri-list` whose default action inserts the paths as text. Hence the native sources in Go. — spike doc §2.1, §3.
- WebKitGTK's own paste (Ctrl+V into the page) reads the clipboard synchronously in the UI process: with a clipboard owner that never answers, the whole window is frozen for ~30 s (measured 31 s on Xvfb, Task 4 fix-round smoke) — before and regardless of our `AttachFromClipboard`, whose own reads are async and bounded. Nothing in our code can shorten it. Smoke consequence: a clipboard owner process killed right after sending Ctrl+V leaves WebKit's synchronous paste waiting ~31 s for an answer that never comes, and by the time the page's paste event reaches us the 1.5 s paste-gesture window (`internal/desktop/paste.go`, `pasteWindow`) has expired (`no_paste_gesture`) — keep the owner alive until the chip appears (the memcheck/smoke scripts wait for the chip before `terminate()`).
- Driving the desktop app from a script (smoke): `WEBKIT_INSPECTOR_HTTP_SERVER=127.0.0.1:<port>` + `Runtime.evaluate` over `ws://…/socket/1/1/WebPage` (`Target.sendMessageToTarget`); WebKit ignores `awaitPromise` — park the promise's result on `window` and poll it. Bindings can be called from there with a raw `fetch('/wails/runtime', {method:'POST', body: JSON.stringify({object: 0, method: 0, args: {'call-id', methodName: 'github.com/spk/spk-mm-client/internal/api/transport.API.<M>', args}})})`. Point `DBUS_SESSION_BUS_ADDRESS` at a dead socket so the smoke instance adds no tray icon to the user's session. The GTK file chooser's location bar autocompletes typed text (XTest typing mangles paths) — paste the path from a clipboard owner on the same Xvfb instead.
- Vitest: `@wailsio/runtime` is globally mocked in `frontend/vitest.setup.ts` (its import-time drag/resize code touches `window` after jsdom teardown); tests of `wailsClient` override the mock locally.
- Wails v3 beta.25 on Linux touches the D-Bus session bus with no timeout in several places: `SingleInstance` (inside `application.New()`, `os.Exit(1)` on failure — uninterceptable), the notifications service startup, `SystemTray.Run` (`InvokeSync(dbus.SessionBus())` on the GTK main thread — a hung bus freezes the UI), and GLib itself (`GApplication` registration in `g_application_run` — with a hung bus the window never appears). Rule: probe the bus **once** with a 2 s bound (`probeBus`) and let `integrationsFor` decide; unless it answered, disable single-instance, notifications and tray, point `DBUS_SESSION_BUS_ADDRESS` at a dead address before GTK starts, and make window close quit (no tray = no way back). Wails' theme listener runs on its own goroutine/connection and does not block the main thread. — `internal/desktop/integrations.go` (`TestIntegrationsFor`, `TestProbeBus`), `internal/desktop/busprobe_linux.go`, `internal/desktop/run.go`.
- `EventManager.Emit(name, data ...any)` with exactly one non-slice argument sets `event.Data = data[0]` — the frontend receives the payload directly, not wrapped in a 1-element array. Since this app always calls `app.Event.Emit(ev.Type, ev.Payload)` (one argument), the array-unwrap branch in `wailsClient.subscribeEvents` is defensive but currently dead code. — `internal/desktop/run.go`, `frontend/src/api/client.ts`.
- Frontend toolchain resolved newer than the stage-1 briefs assumed (TypeScript 6, Vite 8, Vitest 4): bare CSS side-effect imports need `"vite/client"` in `tsconfig.json`'s `"types"`, and `vite.config.ts` must `import { defineConfig } from 'vitest/config'` (not `'vite'`) — a triple-slash `vitest` types reference no longer reliably pulls in the `UserConfig.test` augmentation.
- Memory budget is **Private_Dirty** of all app processes (a guideline target of ~150 MB with 2–3 servers/~100 channels — usability comes first, no growth over time is the hard requirement), not PSS: PSS includes a share of WebKit/GTK/ICU libraries shared with other apps and swings with what else runs (150–196 MB PSS vs ~73–80 MB Private_Dirty for the empty shell; release build and WebKit GPU policy don't change it). Measure with `scripts/pss.sh <pid>` (prints both). — `docs/spikes/2026-09-24-stage1-spikes.md` S4.
- Wails beta.25 Linux tray: `SystemTray.SetTooltip` is a no-op and the StatusNotifierItem `Id`/`ToolTip` are frozen to the label when the tray starts (default "Wails"); only `SetLabel` (SNI `Title`) and `SetIcon` update live. Tray/notification calls reach GTK/D-Bus with no timeout — keep them off service goroutines (`offerLatest`, `asyncSender`). — `internal/desktop/tray.go` (`setTrayText`, `trayBadge`), `internal/desktop/async.go` (`TestAsyncSenderDetachesAHungSendAndResumes`).
- Raising the window on X11 (tray click, second instance, deep link, notification click): Wails beta.25's `Show()`+`Focus()` is `gtk_widget_show_all` + `gtk_window_present`, and GTK3 presents with `GDK_CURRENT_TIME` → the display's last *user* time, i.e. the client's own last input. A tray `Activate` arrives over D-Bus with no X event, so that time is older than the input in the window the user works in, and Muffin/Mutter/Metacity's focus-stealing prevention maps the window **behind** it and sets `_NET_WM_STATE_DEMANDS_ATTENTION` (the taskbar entry blinks). A client with no input at all has user time 0, which WMs treat as a legacy app and let through — a check that never clicks into the client first does **not** reproduce the bug. Fix: `raiseWindow` → `nativeRaise` (`raise_gtk.go`, one main-thread call) stamps the GDK window with `gdk_x11_get_server_time` via `gdk_x11_window_set_user_time` before the map, then `gtk_window_present_with_time(server time)` (`_NET_ACTIVE_WINDOW`, source 1, current timestamp — honoured); Wayland keeps `gtk_window_present`, non-GTK builds keep `Show`/`UnMinimise`/`Focus`. Tray click rule (user 2026-09-29): hide only a window that is visible, focused and not minimized; a hidden, minimized or visible-but-behind window is raised — `trayClickHides`, `TestTrayClickHidesOnlyTheActiveWindow`. Checked on a private Xvfb with `muffin --x11` and `metacity` (own `dbus-daemon --session`, `GSETTINGS_BACKEND=memory`, tray driven by `gdbus call … org.kde.StatusNotifierItem.Activate 0 0` on `org.kde.StatusNotifierItem-<pid>-1` `/StatusNotifierItem`, a `zenity --entry` window focused by an XTEST click as the "editor"; `_NET_ACTIVE_WINDOW`, `_NET_CLIENT_LIST_STACKING` top, `_NET_WM_STATE`). Wails also calls the tray's click handler on the dbusmenu `opened` Event (`systemtray_linux.go` `Event`), so on hosts that send it for the root menu (e.g. KDE) opening the right-click menu toggles too; Cinnamon's `xapp-sn-watcher` does not — a right-click there only opens the menu and leaves the window alone (checked 2026-09-29 on Xvfb with the real `xapp-sn-watcher`, driven by `org.x.StatusIcon.ButtonPress/ButtonRelease` like the `xapp-status` applet). — `internal/desktop/raise.go`, `raise_gtk.go`, `run.go` (`raiseWindow`, `toggle`); report `.superpowers/sdd/fixes-2026-09-28/tray-raise-report.md`.
- Fake-server test API (dev/e2e only, gated by `--test-api`): `/api/_test/fake/post`, `/api/_test/fake/drop` (simulate a lost WS connection — `{lose:true}` drops the server's dead-letter buffer too, forcing a resync instead of a resume), `/api/_test/fake/revoke` (expire the session), `/api/_test/fake/status` (set a user's presence status), `/api/_test/fake/picture` (bump a user's avatar version), `/api/_test/fake/react` (react as another user, `remove:true` to undo), `/api/_test/fake/throttle-file` (`{bytes_per_sec, file_id?}`, `0` restores full speed — slows every plain `GET /api/v4/files/{id}` so a screenshot/test can catch a download mid-progress; does not affect `/thumbnail`, `/preview` or `/info`, but it also slows the media stream and text-snippet fetches, since both go through that same plain-file endpoint upstream, and it ignores `Range` (always answers the full body with `200`) — `internal/mmfake/media.go` (`streamThrottled`); an optional `file_id` scopes the slowdown to just that one file via `SetFileThrottleFor`, leaving every other plain download at full speed — added for the PDF final review's I2/M1: throttling *every* file also slowed Off-Topic's own text-snippet fetches, saturating Chromium's connection pool and aborting the PDF request client-side before Go's media cache ever saw it, which made the mid-load-cancel e2e test pass even against a build that never cancels — `TestFileThrottleForScopesToOneFile`), `/api/_test/fake/file-get-status` (GET, `?id=<file id>` — exposes `FileGetStatus`: whether that file's most recent plain `GET` has actually started, and/or been cancelled by the client (`streamThrottled`'s `onCancel` callback, wired to `r.Context().Done()`); `pdf.spec.ts`'s mid-load-cancel test polls this real server-side signal instead of inferring cancellation from request timing, which removed a race too (a fresh request could beat Go's own async notice of the abort) — `TestFileGetStatusRecordsStartAndCancel`, mutation-checked: reverting the `onCancel` wiring fails it), `/api/_test/fake/throttle-upload` (`{bytes_per_sec}`, `0` restores full speed — the upload-side counterpart: slows the fake's reading of `POST /api/v4/files`'s request body, so a staged attachment can be caught mid-upload — `mmfake.Server.SetUploadThrottle`), `/api/_test/fake/fail-uploads` (`{n}` — the next n `POST /api/v4/files` fail with 500, like `FailPosts` does for posts — `mmfake.Server.FailUploads`, for an attachment's error chip + Retry), `/api/_test/fake/max-file-size` (`{bytes}`, `0` restores the 100 MiB default — changes the fake's advertised/enforced `MaxFileSize` at runtime so an e2e test can hit the "too large" limit without uploading megabytes; the client only sees it after its next metadata refresh, e.g. `fake/drop {lose:true}` — `mmfake.Server.SetMaxFileSize`), `/api/_test/fake/file-attachments-enabled` (`{enabled}` — flips `EnableFileAttachments` at runtime, same refresh caveat — `mmfake.Server.SetFileAttachmentsEnabled`), `/api/_test/opened-files` (files the fake file opener was asked to open), `/api/_test/revealed-files` (files "Show in folder" was asked to reveal) and `SPK_MM_CLIENT_DOWNLOADS` (downloads directory override for e2e), plus `/api/_test/notifications` and `/api/_test/notification-click` for asserting on desktop-notification delivery/click without a real OS notifier (`notification-click` also accepts an optional `root_id`: passed on as `open_channel`'s `root_id`, like a click on a reply's desktop notification — `TestTestAPINotificationClickCarriesTheRoot`). Threads (Task 1, `internal/mmfake/threads.go`): `/api/_test/fake/post` now takes an optional `root_id` (posts a reply instead of a new root — same underlying `fake.ReplyAs`); `/api/_test/fake/thread` `{channel_id, username, replies}` → `{root_id}` seeds a root by `username` plus `replies` replies alternating bob/carol as authors (**both must be members of `channel_id`, or the fake panics like every other `...As` helper** — use `c-town`/`c-gm`, not `c-offtopic`/`c-dm-bob`); `/api/_test/fake/edit` `{post_id, message}` and `/api/_test/fake/delete` `{post_id}` are thin HTTP wrappers over `fake.EditAs`/`fake.DeleteAs`; `/api/_test/fake/crt` `{mode}` (`always_on|default_on|default_off|disabled`) is a runtime `CollapsedThreads` switch (`fake.SetCollapsedThreads`), visible to a signed-in client only after its next bootstrap, like `max-file-size`; `/api/_test/fake/thread-reads` (GET) lists every `PUT .../threads/{id}/read/{ts}` call (`[]{root_id, team_id, ts}`). — `cmd/spk-mm-client/browser.go`.
- Go-level fake network controls for `mmsync` tests: `mmfake.Server.SetDown` (every request 503 — server unreachable), `SetLatency(pathPart, d)` (slow endpoint, e.g. keep a resync in flight), `RejectResumes` (close resumed sockets without a hello), `SetFailure(pathPart, status)` (inject an HTTP error), `UsersSince` (the `since=` of every
`POST /users/ids` that carried one — the fake honours it: a fake user's `update_at` is its picture
time); `harness.tune` adjusts the worker `Config` (unexported seams `refreshTimeout`, `refreshRetry`, `sinceLimit`). The harness `useClock()` gives the worker a clock the test can jump forward while the fake keeps real time. — `internal/mmfake/net.go`, `internal/mmsync/harness_test.go`.
- `--mm-fake-channels N` (browser mode) seeds N extra open channels (`c-load-001`…, 20 posts each) in the fake server for memory/perf checks; the desktop `--mm-fake` flag (dev builds only) starts the same fake server in-process and signs in as alice automatically — always point it at its own `SPK_MM_CLIENT_HOME` (a fresh temp dir), never at the live client's data dir, and it clears any stale fake-mode server entries from a previous dev run before adding the live one. — `cmd/spk-mm-client/main.go`, `cmd/spk-mm-client/run_desktop_wails.go`.
- PDF memory check: `--mm-fake --mm-fake-pdf-cycles N` (dev desktop builds only; refused without `--mm-fake`/in a release build) opens Off-Topic, samples a baseline, then N times opens the seeded `manual.pdf` (50 pages, a 480×360 JPEG per page, ~4 MB, generated once per process — `mmfake.seededManual`), reads 8 s, scrolls 30 × 1200 px, reads, presses Esc and samples WebKitWebProcess Private_Dirty for 25 s (the close's figure is the minimum), idles 2 min, logs `pdf memory cycle …` per close and a `pdf memory result …` summary (baseline, every close's delta, the largest, the least-squares slope in MB/open — raw numbers, no pass/fail) and quits; a run that does not finish (page error, no web process) exits non-zero. The scripts run through `WebviewWindow.ExecJS` and answer through Wails raw messages (`window._wails.invoke('spk-dev-pdf:…')`); `desktop.Options.DevDriver`/`DevMessage` are nil otherwise, so a normal run has no raw message handler. Run it under your own Xvfb with `DBUS_SESSION_BUS_ADDRESS` pointing nowhere (no tray in the user's session) and a fresh `SPK_MM_CLIENT_HOME`. Decision 2026-09-29 (user): the +20 MB gate is **withdrawn** — no hard memory limit, usability and speed come first, so PdfView is not tuned *further*: `KEEP` stays 1 (visible-page-only-plus-one rendering already exists, `PdfView.tsx`), not reduced to `KEEP=0`; no `isOffscreenCanvasSupported:false`; no malloc env (final review M9: the old wording read as if no page windowing existed at all). Measured on WebKitGTK 2.52.3/Xvfb: after heavy PDF use the web process sits **+30…+40 MB** over the pre-PDF baseline, bounded (a sawtooth over 40 opens); the JS heap is released on close, and the residue is native WebKit memory from pdf.js painting into canvases that the allocator keeps. The criterion is **no unbounded growth**: re-check with `--mm-fake-pdf-cycles 40` (or more) and judge that the closes stay in a band. Longer re-check of the bound — `docs/backlog.md`; levers and isolation — spike doc S4 «PDF: гейт памяти». Don't edit a runner shell script while it runs: bash reads scripts incrementally and the edit kills the run. — `cmd/spk-mm-client/devpdf.go`, `devpdf_test.go`.
- Testing the virtualized feed under Vitest/jsdom needs a manual layout stub: jsdom never computes real layout, so `HTMLElement.prototype.offsetHeight` (row height for the virtualizer) and `scrollTo` (history-load anchor) must be overridden in `beforeEach`/restored in `afterEach`, not left at jsdom's defaults (0 / no-op that doesn't move `scrollTop`). — `frontend/src/components/Feed.test.tsx`.
- `ServerRail`'s per-server button folds the unread/mention badge into its own accessible name ("`<name> — Mentions: N`" or "`<name> — Unread messages`"), not just the sibling badge `<span>` (Task 12 fix). Playwright's `getByLabel`/`getByRole(name:)` match is a substring by default, so `page.getByRole('navigation').getByLabel('Mentions: 1')` also matches that button (whose name contains "Mentions: 1") in addition to the intended badge — two hits trip strict mode. e2e assertions on these badge labels need `{ exact: true }`. — `frontend/src/components/ServerRail.tsx`, `tests/e2e/chat.spec.ts`.
- Wails beta.25 exposes no WebKit memory knobs: it uses the default `WebKitWebContext` (`webkit_web_view_new_with_user_content_manager`), so the cache model and — in the 4.1 API — the web process's memory-pressure settings (a construct-only property of a web context) are out of reach; `webkit_website_data_manager_set_memory_pressure_settings` only covers the network process. The only lever over the web process engine is its environment (`JSC_*` options, read by JavaScriptCore at start; set with `os.Setenv` before the webview exists — WebKit launches the web process with our environment). Measured JIT-tier options all cost UI speed and were rejected (spike doc S4).
- Memory soak runs: `--mm-fake-servers N` (dev desktop, `--mm-fake`) starts N in-process fakes (each seeded the same, `--mm-fake-channels` each) and signs alice in on all; `--mm-fake-churn 2s` makes bob post to a random channel every interval (no @mentions — no OS notifications; every 3rd post is a reply to one of the channel's 5 newest roots) and asks the UI to open a random channel every 5th tick (the notification-click path; every 5th of those with a `root_id` — channel plus thread panel, so the thread cache and panel churn too), and logs Go heap figures (`go memory …`) once a minute. A churn run's fakes alternate CRT — on for the first, off for the second (`fakeOptions`), so 2+ servers drive the thread panel and the inline replies with their context line, and `KeepPosts` also drops the thread records and subscriptions of trimmed roots and keeps no thread-read log (`TestKeepPostsDropsTheThreadsOfTrimmedRoots`). The fakes live in the main process; a churn run therefore starts them in a steady state (`fakeOptions`): every load channel is seeded with a full client window (`state.WindowSize` = 60 posts) and the fake keeps at most that many posts per channel and no event log — before, the fake stored every churn post (~0.5 KB each) and the client's windows kept filling from 20 to 60 posts for hours, and both read as main-process "growth". The main process still creeps ~2–3 MB for about 3 h at `2s` while churn posts (longer than the seed's) replace the seeded ones in the fake and the windows; `--mm-fake-churn 200ms` (10× faster) reaches the steady state in ~20 min (main) / ~80 min (web process) — use it for ≥ 60 min to check for a plateau, and judge `WebKitWebProcess` by its median/lower envelope: neighbouring samples differ by up to ±40 MB (large transient allocations), while its JS heap and DOM stay flat (before threads, 180 min at `200ms`: main 66 MB, web median ~130 MB, both flat; with replies and thread panels, CRT on one server and off on the other, Task 7 threads re-run, 96 min at `200ms`: main ~68–69 MB flat from minute 15 (slope after minute 60 −0.3 MB/h), Go heap flat from minute 30 (`next_gc` 23; the client's `internal/state` 2.75–4.5 MB without a trend, only the in-process fake creeps), web median ~150 MB from minute ~60 (slope over the last 35 min −10 MB/h), total ~225 MB — spike doc `docs/spikes/2026-09-28-threads-spike.md` §6.3). `SPK_MM_CLIENT_SOAK_PROFILES=<dir>` makes a churn run write `heap-NNN.pb.gz` there every minute for `go tool pprof -base` diffs. Both the `go memory` line and these profiles are **as of the last GC**, and an idle client (e.g. `--mm-fake-churn 1h`, used only to get the ticker) may run no GC for up to 2 min (the runtime's forced GC): garbage from a burst — a decoded 16 MB picture — then reads as a "flat" heap. Compare `num_gc` between ticks before calling anything retained. The in-process fakes store uploads (`POST /api/v4/files`) and their previews on disk (`mmfake.Options.FilesDir` = `<data dir>/tmp/mmfake`, cleared at start, each fake's own subdir removed on `Close`) — before, every pasted 16 MB picture stayed in the fake's memory (5 → +91 MB Go heap in the main process), which read as a client leak in the Task 6 memory check. — `internal/mmfake/files_test.go`, `TestFakeOptionsForSoak`. Measure with nothing attached: a WebKit inspector session (`WEBKIT_INSPECTOR_HTTP_SERVER`, and especially `Heap.snapshot`, which the inspector keeps) inflates the web and main processes by tens of MB — use `JSC_logGC=1` (sizes after each JS GC on stderr) for the JS heap instead. Run the soak **headless**, under `xvfb-run -a` (`/usr/bin/Xvfb`/`/usr/bin/xvfb-run`) — no window on the user's real display for an hour-long run; set `TMPDIR` to a scratch dir before invoking it (`xvfb-run`'s own `mktemp -d` for its `Xauthority` defaults to `/tmp` otherwise). — `cmd/spk-mm-client/devchurn.go`, `devfake.go` (`fakeOptions`), `TestFakeChurnPostsRepliesAndOpensThreads`, `TestFakeOptionsForSoak`, `TestKeepPostsCapsHistoryAndEventLog`; spike doc S4 «Рост памяти в soak этапа 3 ч.1б».
- Engine-specific frontend timing (JIT tiers etc.): Playwright's own WebKit build does not start on this host (missing `libavif16`/`libjxl` without sudo) and is not the system engine anyway; drive the system WebKitGTK 4.1 through PyGObject (`gi.require_version('WebKit2', '4.1')`, a `WebView` in a `Gtk.Window`, `evaluate_javascript` — an async IIFE's Promise result is unsupported, stash results on `window` and poll). `JSC_*` variables in the script's environment reach its web process.
- `position: fixed` inside a feed row does not position against the viewport: the mounted range has a `transform` (the virtualizer), which turns `fixed` into `absolute`-like behaviour relative to that row, so a popover clips to the row's box. The emoji picker opened from a `PostItem` renders through a portal into `document.body` instead. — `frontend/src/components/PostItem.tsx`, `frontend/src/components/EmojiPicker.tsx`.
- WebKitGTK 2.52 renders `application/pdf` itself: a frame navigated to a PDF gets its built-in viewer (the `PDFJSViewer` feature, on by default — `webkit-pdfjs-viewer://`, pdf.js 4.1.392 bundled in `libwebkit2gtk`, with `isEvalSupported` and PDF scripting on: an `/OpenAction` `app.alert` pops a real script dialog; the eval path is the CVE-2024-4367 class), and embedders cannot configure or turn it off per view. The `/media` sandbox CSP applies to that viewer's own frame and keeps it from running (an empty box) — so `/media/…/pdf/…` keeps the sandbox, and the UI never puts a PDF in an `iframe`/`embed`/`object`. The fakes seed two PDFs in `c-offtopic`: `manual.pdf` (`mmfake.PDFFileID`, a real `mmfake.PDFPages` = 50-page document generated in Go, ~4 MB — text, vector shapes and a 480x360 JPEG of its own per page; `mmfake.LandscapePage` is turned sideways, and `mmfake.ScannedPage` carries a CCITT Group 4 scan instead of a JPEG, decoded through pdf.js's `jbig2.wasm`; final review I3/M9, stale since fbe527b/a4d4a4a — it used to be ~66 KB of text and vector shapes only) and `spec.pdf` (`mmfake.JunkPDFFileID`: a `%PDF-1.4` header and nothing else — passes Go's magic check, pdf.js refuses it: the UI's fallback-to-card case). — spike report `.superpowers/sdd/fixes-2026-09-28/pdf-spike-report.md` (A), `internal/mmfake/pdf.go`, `TestSeededPDFIsARealMultiPageDocument`, `TestSeededPDFHasOneLandscapePage`, `TestSeededPDFHasAScannedCCITTPage`.
- A `nil *media.Cache` stored in an `http.Handler` interface variable is not a nil interface (the classic Go gotcha) — when the media cache fails to open, the desktop runner keeps the interface itself `nil` (never assigns the typed nil pointer to it), so `withMedia` can tell "no cache" apart from "a broken cache handle". — `cmd/spk-mm-client/run_desktop_wails.go`.
- Two `http.ServeMux` patterns `/emoji/name/{name}` and `/emoji/{id}/image` overlap on `/emoji/name/image` and Go's `ServeMux` panics at registration time, not at request time — the fake server merges them into one pattern `/emoji/{a}/{b}` and dispatches by shape inside the handler. — `internal/mmfake/media.go`.
- The real Mattermost `POST /users/status/ids` rejects the **whole** request with 400 if even one id in the array isn't exactly 26 characters (`docs/research/2026-09-24-mattermost-api-facts.md` §7.2); the fake is more lenient (short ids like `u-bob` are accepted) — don't rely on the fake's leniency when writing new status tests, and expect real-server integration to need real 26-char ids.
- WebKitGTK (2.52, measured on Xvfb — software rendering, no accelerated compositing; a GPU session may scroll overflow containers asynchronously and differ, not verified) repaints a scrolled overflow container's visible content on every wheel-scroll step — the Timeline shows a viewport-sized `Paint` per `scroll` event, for the feed as much as the PDF viewer — so anything expensive to paint *inside* scrolled content costs on every step. A Tailwind `shadow` (blurred box-shadow) on a page-sized box made each step of the PDF viewer 125–200 ms instead of ~32 ms (Chromium: smooth either way). No blurred box-shadow/filter on large boxes inside scrollers; spread-only shadows (Tailwind `ring-*`, `0 0 0 1px`) measured cheap (~32 ms steps), and small popups are fine. Measure with real XTest wheel clicks on your own Xvfb plus the inspector (`Timeline.start` — its record times are 0 here, but `Paint` clips and counts work; frame pacing from an in-page `requestAnimationFrame` loop). — `.superpowers/sdd/fixes-2026-09-28/pdf-lag-report.md`.
- A hidden WebKitGTK window (closed to the tray — `Hide()` unmaps it; a window merely on another workspace kept painting in our runs) never paints, and everything WebKit defers to "the next rendering update" piles up while JS keeps running: (1) `requestAnimationFrame` callbacks never run, and each keeps its closure (rows, virtualizer, channel) alive; (2) every element scrolled by script waits, with its whole subtree, in the document's pending scroll-event list; (3) every `<img loading="lazy">` not yet loaded waits for its first intersection check, again with its whole detached tree. With the feed remounted per channel and scrolled to its end, and avatars of other servers never loaded before the window hid, each switch kept an entire old feed: WebKitWebProcess +2.5–3.5 MB/min under the churn soak while hidden, flat while visible — a soak with the window on screen does not show it. Rules: frames go through `useFrames` (one pending frame per purpose, cancelled on unmount); a scrolled container empties itself when unmounted; images in feed rows and the sidebar load eagerly (the feed is virtualized anyway) — `loading="lazy"` only where the UI is necessarily visible (the emoji picker). Diagnose in the system WebKitGTK: a PyGObject `WebView` + `win.hide()` harness, and `WEBKIT_INSPECTOR_HTTP_SERVER=127.0.0.1:<port>` (works for the dev desktop build too) for heap snapshots over the inspector WebSocket (`Heap.snapshot`, `Heap.getRemoteObject` → `Runtime.callFunctionOn` to find a detached node's tree root). Chromium's background tab is a weaker model (extra Blink-internal paint-timing retention). — `frontend/src/components/Feed.tsx` (`useFrames`), `Feed.test.tsx`, `Avatar.tsx`, spike doc S4 «Этап 3, часть 1».
- The Mattermost webapp's emoji set (`frontend/src/emoji/data.ts`) is generated, not hand-written — regenerate with `scripts/gen-emoji.mjs` (sources and the exact command are in the script's header comment); editing `data.ts` by hand will be overwritten by the next regeneration and drift from the webapp's names/order.
- Never raise Wails' `LogLevel` to `Debug` in a build that ships (or in a screenshot/soak session with a real server): Wails logs binding results at Debug (`messageprocessor_call.go`), and `MediaStreamBase`'s result is the loopback stream server's base URL — which carries its session token. Default (Info) is safe; only raise it briefly with a fake server if you must, never with a live one. — `internal/desktop/run.go`.
- The header's downloads button (`⬇`) folds the active-download count into its own accessible name, the same trap as `ServerRail`'s badge (see above): `t('downloads.button')` is exactly `"Downloads"`, `t('downloads.buttonActive', {n})` is `"Downloads — active: N"` — both change at once as downloads start/finish, so an e2e `getByRole('button', { name: 'Downloads' })` with `exact: true` can miss the button mid-download, and a loose substring match (no `exact`) can also hit unrelated buttons whose name merely contains "Downloads". Match with `{ name: /^Downloads/ }` (or query right after the panel is already known to be idle). — `frontend/src/components/ChannelPane.tsx` (`downloadsLabel`), `frontend/src/i18n.ts`, `tests/e2e/media.spec.ts`.
- e2e-only trap, not an app bug: `Composer` is `key={channel.id}` (a fresh instance per channel), so a synthetic `paste`/`drop` built with a raw `document.querySelector('textarea…')`/`'[data-file-drop-target]'` right after `channel(...).click()` can still find the **outgoing** channel's not-yet-unmounted node — Playwright's `.click()` resolving doesn't guarantee React has committed the channel switch first. The attachment lands on the wrong channel with no error (both channels have a composer, so nothing looks broken until you check which channel the upload's `channel_id` query param named). Fix: wait for the new channel's heading to be visible before dispatching the synthetic event — `tests/e2e/attachments.spec.ts` (`openOffTopic`). Locators like `post.locator('img')` are a second trap in the same file: a post's own avatar is also an `<img>` in the same `<article>` — match by accessible name (`post.getByRole('img', { name })`) instead.
- `GET /api/v4/posts/{id}/thread` **without `perPage` returns the whole thread** (every reply, however many) — always pass `perPage=60&direction=up` (newest first; `order[0]` is the root itself, `has_next` means "older exist"); older pages need `fromCreateAt` **and** `fromPost` (the server ignores `fromPost` alone). With `collapsedThreads=true` the id must be a root: for a reply the server's `RootId = id` filter matches nothing (the fake answers 400 to catch client bugs). — `internal/mm/rest` (`PostThread`), `internal/mmfake/threads.go`, `docs/research/2026-09-24-mattermost-api-facts.md` §8.
- `GET /users/me/teams/unread?include_collapsed_threads=true` gives `thread_mention_count` per team **without DM/GM threads** — their part is `GET …/teams/{team}/threads?totalsOnly=true` minus the same with `excludeDirect=true` (not below 0). Counting only `teams/unread` silently drops mentions in DM threads from the badge. — `internal/mmsync` (`fetchMeta`), `TestDMThreadMentionsCountOnce`.
- e2e trap for the thread panel, same as the channel composer's (above): the panel's `Composer` is keyed by `channel+root`, so a synthetic `paste`/`drop` right after opening a thread can land in the previous thread's (or the channel's) composer. Wait for `[data-file-drop-target][data-root="<root>"]` and query the textarea inside it. And the channel feed and the panel both have a `role="log"` named "Messages": `feed(page)` in `tests/e2e/helpers.ts` is `[data-feed="channel"]`, the panel's is `threadFeed(page)`. Under CRT, alice follows only threads she started, replied to or was mentioned in — a `PUT read` of any other thread is a 404 (and the client stops reading it), so a test that waits for `fake/thread-reads` must use a thread alice follows. — `tests/e2e/threads.spec.ts`.
- The virtualized feed's "follow the bottom" runs in a layout effect on every rows change while `atBottom` is set, and `atBottom` is only updated by `onScroll` — which fires asynchronously, one frame after the scroll. A rows update committed in between undoes the scroll (right after sign-in several updates arrive in a row). e2e that scrolls away from the bottom right after a channel opens must retry the scroll until the effect it waits for shows (`chat.spec.ts`, "jump-to-latest button…"); the product side is in `docs/backlog.md`. The opposite end bit too: at `scrollTop` 0 neither `scrollTo(0)` nor the wheel fires a `scroll` event, so a history page that landed without moving the feed off the top (anchor restore that could not apply) left it stuck with `has_more` forever — `fillViewportIfShort` now applies `onScroll`'s "within `NEAR_TOP` → load" rule after every rows change (`Feed.test.tsx`, "a page that lands with the feed still at the top…"; it was the `chat.spec.ts` "history loads up to the first message" flake). Thread-cache memory is measured, not only counted: `SPK_MM_CLIENT_MEMCHECK=1 go test ./internal/api -run TestThreadCacheHeapReturnsToBaseline -count=1 -v` (30 threads × 200 replies opened and closed — heap back to baseline ±0.5 MB; skipped otherwise).
