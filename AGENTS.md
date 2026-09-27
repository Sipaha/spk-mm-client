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
- `make build-desktop` — desktop-бинарь (теги `wails gtk3`).
- `make test-go`, `make test-front`, `make test-e2e`, `make lint`, `make cross-check`.
- `make run-browser` — UI на http://127.0.0.1:5180 с фейковым сервером MM (данные во временном каталоге).

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
- Browser-режим: доступ к `/media/` закрыт за cookie `spk_media` (HttpOnly, SameSite=Strict)
  или bearer-токеном — иначе картинку мог бы утащить любой сайт в браузере пользователя. —
  `TestMediaNeedsThePageCookie`.
- SVG и не-растровые типы не отображаются, размер файла и число пикселей ограничены (пиксельный
  гард отказывает закрыто, если конфиг картинки не читается). — `TestSVGIsRefused`,
  `TestHugeDimensionsAreRefused`.
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
  открывает файл по своему allowlist. UI сразу показывает «Скачивается <имя>…» (липкое
  уведомление без авто-скрытия), его заменяет «Сохранено…» или ошибка. Запасной путь без жёстких
  ссылок не удаляет после переименования путь `.part` — его уже может занять чужая одноимённая
  загрузка. — `TestConcurrentSavesOfOneFileShareTheDownload`,
  `TestNoLinkFallbackLeavesAnotherDownloadsPartAlone`, `frontend/src/chat.test.ts` («a download in
  progress is shown at once and stays until it is replaced»).
- Рамка картинки и текстового фрагмента имеет окончательный размер до загрузки содержимого —
  лента со скролл-якорем не прыгает при догрузке превью. —
  `frontend/src/components/Attachments.test.tsx` («the image box has its final size before the
  image loads»).
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
  через ref, а не переподписываются на каждое изменение масштаба. — `frontend/src/components/
  ImageZoom.test.tsx` («wheel zooms in around the cursor and reports a growing percentage; wheel
  out returns toward it», «the original is requested immediately, and the preview is shown as a
  placeholder until it loads», «wheel and keyboard zoom subscribe once, not on every scale
  change»), `frontend/src/components/imageZoom.test.ts`.
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
  drop. — `TestForgedDropIsRefused`, `TestNativeDropOnAChannelAttachesItsPaths`, `TestHugeDropIsCapped`,
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
- Browser-маршрут загрузки `POST /api/attachments/{srv}/{channel}` — те же защиты, что у `/api/`
  (bearer только заголовком — не query-токен на POST, `OriginGuard`, `LoopbackHostGuard`), сырое тело через
  `http.MaxBytesReader(MaxFileSize)` (413 `too_large` и по `Content-Length`, и по потоку), имя очищается:
  как у всех вложений (`attach.cleanName`). — `TestBrowserUploadAttachesTheRawBody`,
  `TestBrowserUploadNeedsTokenOriginAndLoopbackHost`, `TestBrowserUploadOverMaxFileSizeIs413`,
  `TestBrowserUploadNameIsCleaned`, `TestBrowserUploadErrors`.
- Каждый созданный Playwright-контекст/страница (в `browser_run_code_unsafe` или в скриптах)
  закрывается в том же вызове (`try`/`finally` → `ctx.close()`); окна не оставляются открытыми.
  После работы с браузером проверить, что не осталось висящих контекстов — пользователь уже
  жаловался на «наплодил pw окон» (Task 14, ledger).

## Things that bite

- **A `fetch()` to `wails://` with a `Blob`, `File` or `FormData` body crashes the whole desktop app** (SIGSEGV in `webkit_uri_scheme_request_get_http_body`, WebKitGTK 2.52 — Wails reads every scheme request's body). Only `Uint8Array`/`ArrayBuffer` bodies are safe there. The desktop UI never sends file bytes at all (Go reads the clipboard/dialog/drop itself); browser mode posts a `File` only to `/api/attachments` over plain HTTP. — `docs/spikes/2026-09-27-attachments-spike.md` §2.6.
- WebKitGTK never gives the page pasted or dropped file contents (`clipboardData.files`/`dataTransfer.files` are always empty — `DataTransfer::allowsFileAccess()` is `false` off Cocoa): a pasted picture is an empty `paste`, copied files a hidden `text/uri-list` whose default action inserts the paths as text. Hence the native sources in Go. — spike doc §2.1, §3.
- WebKitGTK's own paste (Ctrl+V into the page) reads the clipboard synchronously in the UI process: with a clipboard owner that never answers, the whole window is frozen for ~30 s (measured 31 s on Xvfb, Task 4 fix-round smoke) — before and regardless of our `AttachFromClipboard`, whose own reads are async and bounded. Nothing in our code can shorten it.
- Driving the desktop app from a script (smoke): `WEBKIT_INSPECTOR_HTTP_SERVER=127.0.0.1:<port>` + `Runtime.evaluate` over `ws://…/socket/1/1/WebPage` (`Target.sendMessageToTarget`); WebKit ignores `awaitPromise` — park the promise's result on `window` and poll it. Bindings can be called from there with a raw `fetch('/wails/runtime', {method:'POST', body: JSON.stringify({object: 0, method: 0, args: {'call-id', methodName: 'github.com/spk/spk-mm-client/internal/api/transport.API.<M>', args}})})`. Point `DBUS_SESSION_BUS_ADDRESS` at a dead socket so the smoke instance adds no tray icon to the user's session. The GTK file chooser's location bar autocompletes typed text (XTest typing mangles paths) — paste the path from a clipboard owner on the same Xvfb instead.
- Vitest: `@wailsio/runtime` is globally mocked in `frontend/vitest.setup.ts` (its import-time drag/resize code touches `window` after jsdom teardown); tests of `wailsClient` override the mock locally.
- Wails v3 beta.25 on Linux touches the D-Bus session bus with no timeout in several places: `SingleInstance` (inside `application.New()`, `os.Exit(1)` on failure — uninterceptable), the notifications service startup, `SystemTray.Run` (`InvokeSync(dbus.SessionBus())` on the GTK main thread — a hung bus freezes the UI), and GLib itself (`GApplication` registration in `g_application_run` — with a hung bus the window never appears). Rule: probe the bus **once** with a 2 s bound (`probeBus`) and let `integrationsFor` decide; unless it answered, disable single-instance, notifications and tray, point `DBUS_SESSION_BUS_ADDRESS` at a dead address before GTK starts, and make window close quit (no tray = no way back). Wails' theme listener runs on its own goroutine/connection and does not block the main thread. — `internal/desktop/integrations.go` (`TestIntegrationsFor`, `TestProbeBus`), `internal/desktop/busprobe_linux.go`, `internal/desktop/run.go`.
- `EventManager.Emit(name, data ...any)` with exactly one non-slice argument sets `event.Data = data[0]` — the frontend receives the payload directly, not wrapped in a 1-element array. Since this app always calls `app.Event.Emit(ev.Type, ev.Payload)` (one argument), the array-unwrap branch in `wailsClient.subscribeEvents` is defensive but currently dead code. — `internal/desktop/run.go`, `frontend/src/api/client.ts`.
- Frontend toolchain resolved newer than the stage-1 briefs assumed (TypeScript 6, Vite 8, Vitest 4): bare CSS side-effect imports need `"vite/client"` in `tsconfig.json`'s `"types"`, and `vite.config.ts` must `import { defineConfig } from 'vitest/config'` (not `'vite'`) — a triple-slash `vitest` types reference no longer reliably pulls in the `UserConfig.test` augmentation.
- Memory budget is **Private_Dirty** of all app processes (a guideline target of ~150 MB with 2–3 servers/~100 channels — usability comes first, no growth over time is the hard requirement), not PSS: PSS includes a share of WebKit/GTK/ICU libraries shared with other apps and swings with what else runs (150–196 MB PSS vs ~73–80 MB Private_Dirty for the empty shell; release build and WebKit GPU policy don't change it). Measure with `scripts/pss.sh <pid>` (prints both). — `docs/spikes/2026-09-24-stage1-spikes.md` S4.
- Wails beta.25 Linux tray: `SystemTray.SetTooltip` is a no-op and the StatusNotifierItem `Id`/`ToolTip` are frozen to the label when the tray starts (default "Wails"); only `SetLabel` (SNI `Title`) and `SetIcon` update live. Tray/notification calls reach GTK/D-Bus with no timeout — keep them off service goroutines (`offerLatest`, `asyncSender`). — `internal/desktop/tray.go` (`setTrayText`, `trayBadge`), `internal/desktop/async.go` (`TestAsyncSenderDetachesAHungSendAndResumes`).
- Fake-server test API (dev/e2e only, gated by `--test-api`): `/api/_test/fake/post`, `/api/_test/fake/drop` (simulate a lost WS connection — `{lose:true}` drops the server's dead-letter buffer too, forcing a resync instead of a resume), `/api/_test/fake/revoke` (expire the session), `/api/_test/fake/status` (set a user's presence status), `/api/_test/fake/picture` (bump a user's avatar version), `/api/_test/fake/react` (react as another user, `remove:true` to undo), `/api/_test/fake/throttle-file` (`{bytes_per_sec}`, `0` restores full speed — slows every plain `GET /api/v4/files/{id}` so a screenshot/test can catch a download mid-progress; does not affect `/thumbnail`, `/preview` or `/info`, but it also slows the media stream and text-snippet fetches, since both go through that same plain-file endpoint upstream, and it ignores `Range` (always answers the full body with `200`) — `internal/mmfake/media.go` (`streamThrottled`)), `/api/_test/opened-files` (files the fake file opener was asked to open), `/api/_test/revealed-files` (files "Show in folder" was asked to reveal) and `SPK_MM_CLIENT_DOWNLOADS` (downloads directory override for e2e), plus `/api/_test/notifications` and `/api/_test/notification-click` for asserting on desktop-notification delivery/click without a real OS notifier. — `cmd/spk-mm-client/browser.go`.
- Go-level fake network controls for `mmsync` tests: `mmfake.Server.SetDown` (every request 503 — server unreachable), `SetLatency(pathPart, d)` (slow endpoint, e.g. keep a resync in flight), `RejectResumes` (close resumed sockets without a hello), `SetFailure(pathPart, status)` (inject an HTTP error), `UsersSince` (the `since=` of every
`POST /users/ids` that carried one — the fake honours it: a fake user's `update_at` is its picture
time); `harness.tune` adjusts the worker `Config` (unexported seams `refreshTimeout`, `refreshRetry`, `sinceLimit`). The harness `useClock()` gives the worker a clock the test can jump forward while the fake keeps real time. — `internal/mmfake/net.go`, `internal/mmsync/harness_test.go`.
- `--mm-fake-channels N` (browser mode) seeds N extra open channels (`c-load-001`…, 20 posts each) in the fake server for memory/perf checks; the desktop `--mm-fake` flag (dev builds only) starts the same fake server in-process and signs in as alice automatically — always point it at its own `SPK_MM_CLIENT_HOME` (a fresh temp dir), never at the live client's data dir, and it clears any stale fake-mode server entries from a previous dev run before adding the live one. — `cmd/spk-mm-client/main.go`, `cmd/spk-mm-client/run_desktop_wails.go`.
- Testing the virtualized feed under Vitest/jsdom needs a manual layout stub: jsdom never computes real layout, so `HTMLElement.prototype.offsetHeight` (row height for the virtualizer) and `scrollTo` (history-load anchor) must be overridden in `beforeEach`/restored in `afterEach`, not left at jsdom's defaults (0 / no-op that doesn't move `scrollTop`). — `frontend/src/components/Feed.test.tsx`.
- `ServerRail`'s per-server button folds the unread/mention badge into its own accessible name ("`<name> — Mentions: N`" or "`<name> — Unread messages`"), not just the sibling badge `<span>` (Task 12 fix). Playwright's `getByLabel`/`getByRole(name:)` match is a substring by default, so `page.getByRole('navigation').getByLabel('Mentions: 1')` also matches that button (whose name contains "Mentions: 1") in addition to the intended badge — two hits trip strict mode. e2e assertions on these badge labels need `{ exact: true }`. — `frontend/src/components/ServerRail.tsx`, `tests/e2e/chat.spec.ts`.
- Wails beta.25 exposes no WebKit memory knobs: it uses the default `WebKitWebContext` (`webkit_web_view_new_with_user_content_manager`), so the cache model and — in the 4.1 API — the web process's memory-pressure settings (a construct-only property of a web context) are out of reach; `webkit_website_data_manager_set_memory_pressure_settings` only covers the network process. The only lever over the web process engine is its environment (`JSC_*` options, read by JavaScriptCore at start; set with `os.Setenv` before the webview exists — WebKit launches the web process with our environment). Measured JIT-tier options all cost UI speed and were rejected (spike doc S4).
- Memory soak runs: `--mm-fake-servers N` (dev desktop, `--mm-fake`) starts N in-process fakes (each seeded the same, `--mm-fake-channels` each) and signs alice in on all; `--mm-fake-churn 2s` makes bob post to a random channel every interval (no @mentions — no OS notifications) and asks the UI to open a random channel every 5th tick (the notification-click path), and logs Go heap figures (`go memory …`) once a minute. The fakes live in the main process; a churn run therefore starts them in a steady state (`fakeOptions`): every load channel is seeded with a full client window (`state.WindowSize` = 60 posts) and the fake keeps at most that many posts per channel and no event log — before, the fake stored every churn post (~0.5 KB each) and the client's windows kept filling from 20 to 60 posts for hours, and both read as main-process "growth". The main process still creeps ~2–3 MB for about 3 h at `2s` while churn posts (longer than the seed's) replace the seeded ones in the fake and the windows; `--mm-fake-churn 200ms` (10× faster) reaches the steady state in ~20 min (main) / ~80 min (web process) — use it for ≥ 60 min to check for a plateau, and judge `WebKitWebProcess` by its median/lower envelope: neighbouring samples differ by up to ±40 MB (large transient allocations), while its JS heap and DOM stay flat (180 min at `200ms`: main 66 MB, web median ~130 MB, both flat). `SPK_MM_CLIENT_SOAK_PROFILES=<dir>` makes a churn run write `heap-NNN.pb.gz` there every minute for `go tool pprof -base` diffs. Measure with nothing attached: a WebKit inspector session (`WEBKIT_INSPECTOR_HTTP_SERVER`, and especially `Heap.snapshot`, which the inspector keeps) inflates the web and main processes by tens of MB — use `JSC_logGC=1` (sizes after each JS GC on stderr) for the JS heap instead. Run the soak **headless**, under `xvfb-run -a` (`/usr/bin/Xvfb`/`/usr/bin/xvfb-run`) — no window on the user's real display for an hour-long run; set `TMPDIR` to a scratch dir before invoking it (`xvfb-run`'s own `mktemp -d` for its `Xauthority` defaults to `/tmp` otherwise). — `cmd/spk-mm-client/devchurn.go`, `devfake.go` (`fakeOptions`), `TestFakeChurnPostsAndSwitchesChannels`, `TestFakeOptionsForSoak`, `TestKeepPostsCapsHistoryAndEventLog`; spike doc S4 «Рост памяти в soak этапа 3 ч.1б».
- Engine-specific frontend timing (JIT tiers etc.): Playwright's own WebKit build does not start on this host (missing `libavif16`/`libjxl` without sudo) and is not the system engine anyway; drive the system WebKitGTK 4.1 through PyGObject (`gi.require_version('WebKit2', '4.1')`, a `WebView` in a `Gtk.Window`, `evaluate_javascript` — an async IIFE's Promise result is unsupported, stash results on `window` and poll). `JSC_*` variables in the script's environment reach its web process.
- `position: fixed` inside a feed row does not position against the viewport: the row has a `transform` (the virtualizer), which turns `fixed` into `absolute`-like behaviour relative to that row, so a popover clips to the row's box. The emoji picker opened from a `PostItem` renders through a portal into `document.body` instead. — `frontend/src/components/PostItem.tsx`, `frontend/src/components/EmojiPicker.tsx`.
- A `nil *media.Cache` stored in an `http.Handler` interface variable is not a nil interface (the classic Go gotcha) — when the media cache fails to open, the desktop runner keeps the interface itself `nil` (never assigns the typed nil pointer to it), so `withMedia` can tell "no cache" apart from "a broken cache handle". — `cmd/spk-mm-client/run_desktop_wails.go`.
- Two `http.ServeMux` patterns `/emoji/name/{name}` and `/emoji/{id}/image` overlap on `/emoji/name/image` and Go's `ServeMux` panics at registration time, not at request time — the fake server merges them into one pattern `/emoji/{a}/{b}` and dispatches by shape inside the handler. — `internal/mmfake/media.go`.
- The real Mattermost `POST /users/status/ids` rejects the **whole** request with 400 if even one id in the array isn't exactly 26 characters (`docs/research/2026-09-24-mattermost-api-facts.md` §7.2); the fake is more lenient (short ids like `u-bob` are accepted) — don't rely on the fake's leniency when writing new status tests, and expect real-server integration to need real 26-char ids.
- A hidden WebKitGTK window (closed to the tray — `Hide()` unmaps it; a window merely on another workspace kept painting in our runs) never paints, and everything WebKit defers to "the next rendering update" piles up while JS keeps running: (1) `requestAnimationFrame` callbacks never run, and each keeps its closure (rows, virtualizer, channel) alive; (2) every element scrolled by script waits, with its whole subtree, in the document's pending scroll-event list; (3) every `<img loading="lazy">` not yet loaded waits for its first intersection check, again with its whole detached tree. With the feed remounted per channel and scrolled to its end, and avatars of other servers never loaded before the window hid, each switch kept an entire old feed: WebKitWebProcess +2.5–3.5 MB/min under the churn soak while hidden, flat while visible — a soak with the window on screen does not show it. Rules: frames go through `useFrames` (one pending frame per purpose, cancelled on unmount); a scrolled container empties itself when unmounted; images in feed rows and the sidebar load eagerly (the feed is virtualized anyway) — `loading="lazy"` only where the UI is necessarily visible (the emoji picker). Diagnose in the system WebKitGTK: a PyGObject `WebView` + `win.hide()` harness, and `WEBKIT_INSPECTOR_HTTP_SERVER=127.0.0.1:<port>` (works for the dev desktop build too) for heap snapshots over the inspector WebSocket (`Heap.snapshot`, `Heap.getRemoteObject` → `Runtime.callFunctionOn` to find a detached node's tree root). Chromium's background tab is a weaker model (extra Blink-internal paint-timing retention). — `frontend/src/components/Feed.tsx` (`useFrames`), `Feed.test.tsx`, `Avatar.tsx`, spike doc S4 «Этап 3, часть 1».
- The Mattermost webapp's emoji set (`frontend/src/emoji/data.ts`) is generated, not hand-written — regenerate with `scripts/gen-emoji.mjs` (sources and the exact command are in the script's header comment); editing `data.ts` by hand will be overwritten by the next regeneration and drift from the webapp's names/order.
- Never raise Wails' `LogLevel` to `Debug` in a build that ships (or in a screenshot/soak session with a real server): Wails logs binding results at Debug (`messageprocessor_call.go`), and `MediaStreamBase`'s result is the loopback stream server's base URL — which carries its session token. Default (Info) is safe; only raise it briefly with a fake server if you must, never with a live one. — `internal/desktop/run.go`.
- The header's downloads button (`⬇`) folds the active-download count into its own accessible name, the same trap as `ServerRail`'s badge (see above): `t('downloads.button')` is exactly `"Downloads"`, `t('downloads.buttonActive', {n})` is `"Downloads — active: N"` — both change at once as downloads start/finish, so an e2e `getByRole('button', { name: 'Downloads' })` with `exact: true` can miss the button mid-download, and a loose substring match (no `exact`) can also hit unrelated buttons whose name merely contains "Downloads". Match with `{ name: /^Downloads/ }` (or query right after the panel is already known to be idle). — `frontend/src/components/ChannelPane.tsx` (`downloadsLabel`), `frontend/src/i18n.ts`, `tests/e2e/media.spec.ts`.
