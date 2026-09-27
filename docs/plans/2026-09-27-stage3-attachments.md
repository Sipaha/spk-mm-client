# Этап 3, часть 2 — вложения: Ctrl+V, перетаскивание, 📎

Источник — запрос пользователя 2026-09-27: «важно ещё, чтобы была возможность через Ctrl+V вставлять как картинки, так и файлы. Хочется делать скрины в буфер обмена и вставлять в сообщение через Ctrl+V». На вопрос об объёме — «давай полный спектр делать»: Ctrl+V + перетаскивание файлов + кнопка 📎 с системным диалогом. Загрузка файлов на сервер снята с ограничения «нужно одобрение» этим запросом.

Спайк — `docs/spikes/2026-09-27-attachments-spike.md` (WebKitGTK 2.52.3, Wails v3.0.0-beta.25, проверено на Xvfb). Спека — `docs/specs/2026-09-24-spk-mattermost-design.md` (строка «Редактор: … вставка/перетаскивание файлов с прогрессом»); правила — `AGENTS.md`.

## Дизайн

**Главный факт спайка:** в desktop страница не получает содержимого вставленных/перетащенных файлов (WebKitGTK на Linux: `allowsFileAccess()==false`). Скриншот в буфере приходит пустым `paste`, файлы из файлового менеджера — скрытым `text/uri-list` (по умолчанию WebKit вставляет пути текстом). Поэтому в desktop источники — **нативные**: Go сам читает буфер GTK (cgo, главный поток, асинхронно), перетаскивание — Wails `EnableFileDrop` → `WindowFilesDropped` с путями, 📎 — диалог Wails. В browser mode (Chromium) страница получает настоящие `File` и шлёт байты сырым телом в Go.

```
desktop:  Ctrl+V → UI paste-обработчик → Go AttachFromClipboard(srv,ch) → пути (uri-list / gnome-copied-files) или PNG из буфера → спул
          перетаскивание → Wails WindowFilesDropped (data-srv/data-channel цели) → пути
          📎 → Go PickAttachments(srv,ch) → диалог Wails (множественный выбор) → пути
browser:  paste / drop / <input type=file multiple> → File[] → POST /api/attachments/{srv}/{channel}?name&mime (сырое тело) → спул
                 ▼
   attach: вложение {id, srv, ch, name, mime, size, source: path|spool, state: staged|uploading|uploaded|failed, progress, fileID?, error?}
                 ▼ загрузчик сервера (транспорт transfer, общий лимитер, 2 параллельно, только live)
   POST /api/v4/files?channel_id&filename&client_id (сырое тело, Content-Length) → 201 {file_infos, client_ids}
                 ▼
   SendPost(srv, ch, message, attachmentIDs) → ожидающий пост (с локальными файлами) → ждёт загрузок → POST /posts {file_ids, pending_post_id}
```

Решения контроллера (пользователь не уточнял — в реестре SDD как Ruling):
- **Одна модель вложений, два вида источника** (путь на диске / спул-файл), **один конвейер загрузки в Go**. Перетаскивание и 📎 — лишь новые источники путей; браузерный режим — источник байтов. Всё, что выше `attach`, про источник не знает.
- **Пути не копируются**: запоминаем путь, размер, mtime; перед загрузкой — повторный `Stat`; изменился/исчез — ошибка этого вложения. Только обычные файлы; каталоги — отказ с сообщением. Память не растёт: тело идёт потоком с диска, целиком в куче Go файл не держится.
- **Картинка из буфера и байты браузера** — спул в `~/.spk/mm-client/tmp/attach-<id>` (имя для сервера — `Screenshot YYYY-MM-DD HH-MM-SS.png` для скриншотов, исходное имя `File` — для браузера). Спул удаляется после успешной отправки, отмены вложения или отмены поста; при старте — асинхронная зачистка (старт не блокирует). Неподдерживаемый формат картинки в буфере (без `image/png`) — перекодировать в PNG через gdk-pixbuf.
- **Загрузка сразу после добавления** (как официальный веб-клиент), пока воркер live; офлайн — ждёт. Прогресс ~4 Гц; зависание «нет байтов 60 с» — ошибка. **Без автоповтора** POST (правило «POST не повторяются на сетевых ошибках»): ошибка → чип «Ошибка» с «Повторить».
- **Отправка:** Enter с вложениями → ожидающий пост сразу виден в ленте с локальными файлами (превью картинок из Go) → после загрузки всех → `CreatePost` c `file_ids`. Ошибка загрузки → пост «не отправлен» с «Повторить» (повторяет только неудачные загрузки; уже полученные `fileID` сохраняются) и «Удалить». Разрешено отправить вложения без текста.
- **Лимиты:** `EnableFileAttachments` и `MaxFileSize` (по умолчанию 100 МиБ) из `config/client?format=old`; не больше 10 вложений на пост (`MAX_UPLOAD_FILES` веб-клиента; `file_ids` ≤ 300 рун). Превышение — сообщение у композера, вложение не добавляется.
- **Вложения — по каналу**, в памяти (как черновик до отправки). Перезапуск их теряет (ожидающие посты тоже не переживают перезапуск — как сейчас у текста); запись в бэклог. Смена канала не теряет вложения другого канала.
- **Превью вложений** — через `/media/<srv>/staged/<attachmentID>` (новый вид; ключ проверяется как у прочих; растровые картинки в пределах медиа-лимитов, иначе иконка типа). В ленте ожидающий пост показывает те же превью.
- **Вставка текста не ломается:** `preventDefault` только когда в буфере есть скрытый список файлов (`text/uri-list` пуст для страницы); ссылки/текст/HTML вставляются как обычно (проверено спайком). HTML+картинка — текст вставляется, картинка тоже прикладывается (как в веб-клиенте).
- **Безопасность:** UI не передаёт в Go пути ни в одном API (Go сам читает буфер и открывает диалог). Wails пропускает пути перетаскивания через JS (`FilesDropped`) — подделка скриптом возможна; защита: вложения — видимые чипы, отправка только явным действием пользователя; только обычные файлы. Ни одного `Blob`/`File`/`FormData` в `fetch` к `wails://` — это роняет приложение (SIGSEGV, спайк) — правило в AGENTS.md и тест.
- **Browser mode:** маршрут `POST /api/attachments/{srv}/{channel}` — те же защиты, что у `/api/` (bearer, `OriginGuard`, `LoopbackHostGuard`), `http.MaxBytesReader(MaxFileSize)`, сырое тело (без JSON/base64/multipart).

## Global Constraints

Те же, что у части 1б (`constraints.md` рабочего каталога SDD этого плана — копия): Go 1.26, Wails v3.0.0-beta.25; `go test -race`, `go vet` и `golangci-lint` для обоих наборов тегов (`""` и `"wails gtk3"`), `gofmt`; `go build ./...` без тегов проходит (cgo GTK — за тегами, со стабом); фронтенд `pnpm test && pnpm lint && pnpm build`; i18n ru/en с паритетом ключей; доступность (aria-label у кнопок-иконок, клавиатура: чип вложения удаляется Delete/Backspace с фокусом на нём, «×» — кнопка); UI ходит только в `api.API`, `/media/` и (browser mode) `/api/attachments`; UI никогда не ходит к серверу MM; токен не покидает Go; все кэши, спулы и списки ограничены, без роста памяти; ничего блокирующего на старте (буфер, D-Bus, диалог — только по действию пользователя); `Emitter.Emit` не блокирует, события склеиваются; хуки `mmsync.Worker` не блокируют; POST не повторяются автоматически; коммиты — репозиторная идентичность, без amend и Co-Authored-By, только явные пути; Playwright-контексты закрываются в том же вызове; живой dev-клиент пользователя и `/opt/Mattermost` не трогать, pkill/killall запрещены; desktop-бинарь `build/bin/spk-mm-client-desktop` не пересобирать — для проверки сборки desktop собирать во временный путь под `.agents/tmp`; любые проверки буфера обмена/перетаскивания/диалога — только на своём Xvfb (`xvfb-run -a`, `TMPDIR` в scratch): буфер и экран пользователя не трогать.

---

### Task 1: Фейк и REST — загрузка файлов, file_ids, лимиты

**Files:** `internal/mmfake/media.go`, `chat.go`, `server.go` (+ тесты), `internal/mm/rest/` (новый `upload.go` + тест, `write.go`, `endpoints.go`), `internal/mmsync/worker.go` (конфиг), `internal/state` (`Config`), `docs/research/2026-09-24-mattermost-api-facts.md` (новый §7.7 «Загрузка файлов»).

- Фейк: `POST /api/v4/files` простой режим (`channel_id`, `filename`, `client_id` в query, сырое тело, `Content-Length` обязателен → 400 при 0) — проверка членства в канале, `EnableFileAttachments` (403 `api.file.attachments.disabled.app_error`), `MaxFileSize` (413); ответ 201 `{file_infos:[FileInfo], client_ids:[…]}`, `FileInfo` с `user_id`, `post_id=""`, превью/миниатюра/размеры как у сида (`newFileLocked`). `createPostLocked` — проверка `file_ids` как у сервера: молча отбросить неизвестные, чужого канала/пользователя, уже прикреплённые; дубликаты убрать; проставить `post_id`. `clientConfig` — `MaxFileSize`, `EnableFileAttachments`. Тест-API: сделать загрузки медленными/падающими (по аналогии с `throttle-file`), настроить `MaxFileSize`/`EnableFileAttachments` — для e2e прогресса, ошибки, лимитов.
- REST: `UploadFile(ctx, channelID, filename, clientID string, body io.Reader, size int64, progress func(sent int64)) (FileInfo, error)` — простой режим, сырое тело, `Content-Length`, через клиент `transfer` (без общего таймаута) и общий лимитер, классификация ошибок как у прочих (401/403 → auth, 413 → свой код «слишком большой», 5xx → network), без повторов. `CreatePost` — поле `file_ids`. `ClientConfig` — `MaxFileSize` (строка в `format=old` → int64), `EnableFileAttachments`; прокинуть в `state.Config`.
- Факты API: маршрут, режимы (простой/multipart, порядок `channel_id`), коды, лимиты (`MaxFileSize` 100 МиБ по умолчанию, `MaxImageResolution`, `file_ids` ≤ 300 рун ≈ 10), молчаливое отбрасывание неприкрепляемых id, upload sessions (`/api/v4/uploads`) — не используются.
- Тесты: фейк (201/400/403/413/не член канала, проверка `file_ids`), REST (тело и заголовки, прогресс, коды → ошибки, без повторов на сетевой ошибке), конфиг.

### Task 2: Go — вложения: модель, спул, загрузчик, превью

**Files:** новый пакет `internal/attach` (+ тесты), `internal/api` (методы и события), `internal/media` (вид `staged`), `internal/paths` (каталог `tmp`), `cmd/spk-mm-client` (подключение, асинхронная зачистка спула).

- `attach.Store`: `AddPath(srv, ch, path)`, `AddBytes(srv, ch, name, mime, r io.Reader, limit)` (спул с `MaxBytesReader`-лимитом), `Remove(id)`, `List(srv, ch)`, `Retry(id)`; проверки: обычный файл, размер ≤ `MaxFileSize`, `EnableFileAttachments`, ≤ 10 на канал (ошибки с кодами для UI: `too_large`, `too_many`, `attachments_disabled`, `not_a_file`); повторный `Stat` (размер+mtime) перед загрузкой.
- Загрузчик на сервер: очередь, 2 параллельно, только пока воркер live (ждёт live), `rest.UploadFile` с `client_id` = id вложения, прогресс ~4 Гц, «нет байтов 60 с» → ошибка, 401 → сигнал авторизации, без автоповторов. Удаление вложения во время загрузки — отмена запроса. Остановка сервиса — отмена всех и чистка спула.
- События `attachments_changed` `{srv, ch, items:[{id, name, size, mime, state, sent, error}]}` — склеиваются как прочие; API: `Attachments(srv, ch)`, `RemoveAttachment`, `RetryAttachment`.
- `/media/<srv>/staged/<id>`: отдаёт файл вложения только растровых типов в пределах медиа-лимитов (как превью), для прочих — 404 (UI покажет иконку типа); ключ — существующий id этого сервера.
- Спул: `~/.spk/mm-client/tmp/attach-*`, удаление при удалении вложения/после поста; при старте — зачистка в фоне.
- Тесты: добавление/лимиты/коды ошибок, изменённый файл → ошибка, загрузка с прогрессом (фейк), отмена при удалении, офлайн ждёт live, 401, спул удаляется, зачистка не блокирует старт, `staged`-маршрут (картинка/не картинка/чужой id).

### Task 3: Go — отправка поста с вложениями

**Files:** `internal/api/chat.go`, `api.go`, `internal/mmsync/actions.go`, `internal/state` (ожидающий пост с файлами, `view.go`), транспорт (`internal/transport` wails/http) (+ тесты).

- `SendPost(ctx, srv, ch, message string, attachmentIDs []string)` (пустой список — как раньше; пустой текст разрешён при непустых вложениях): ожидающий пост сразу в ленте с `files` из вложений (`FileView` с превью-URL `staged`, имя, размер, mime); ждёт загрузок; все загружены → `CreatePost` с `file_ids` и `pending_post_id`; ошибка загрузки → пост «не отправлен». Вложения, ушедшие в пост, уходят из композера (событие). `RetryPost` — перезапускает неудачные загрузки и затем создание; `DiscardPost` — отменяет загрузки, удаляет вложения и спулы.
- После подтверждения сервером (эхо WS с тем же `pending_post_id`) — обычный пост с серверными файлами; спулы удаляются.
- Тесты: отправка с вложениями (фейк: пост с файлами), только вложения без текста, ошибка загрузки → «не отправлен» → «Повторить» не перезагружает уже загруженные, «Удалить» чистит, офлайн, `session_expired`.

### Task 4: Go desktop — источники: буфер GTK, перетаскивание, диалог; browser-маршрут

**Files:** новый `internal/desktop/clipboard_gtk.go` (cgo, `//go:build wails && gtk3`) + стаб, `internal/desktop/run.go` (`EnableFileDrop`, `WindowFilesDropped`), `internal/desktop` (диалог), `internal/api` (интерфейсы `Clipboard`, `FilePicker` и методы `AttachFromClipboard`, `PickAttachments`), `cmd/spk-mm-client/browser.go` (маршрут `/api/attachments`), `AGENTS.md` (правила) (+ тесты).

- `AttachFromClipboard(ctx, srv, ch) (int, error)`: Go читает TARGETS буфера на главном потоке GTK **асинхронно** (`gtk_clipboard_request_*` + таймаут в Go ~2 с — зависший владелец буфера ничего не блокирует); порядок — список файлов (`text/uri-list`, только `file://`; или `x-special/gnome-copied-files`, «cut» — как copy) → `AddPath` каждого; иначе `image/*` (PNG как есть, прочие — в PNG через gdk-pixbuf) → спул → вложение; иначе 0. Возвращает число добавленных; результат — событием. В browser mode и тестах — интерфейс `Clipboard` (браузер: «не поддерживается», UI там берёт `File` сам).
- Перетаскивание: `EnableFileDrop: true`; обработчик `WindowFilesDropped` берёт `data-srv`/`data-channel` цели (`DropTargetDetails`) и пути → `AddPath`. Каталоги — отказ (сообщение). Подделка через JS — см. дизайн (видимые чипы; только обычные файлы).
- 📎: `PickAttachments(ctx, srv, ch)`: диалог Wails `OpenFile().CanChooseFiles(true).AttachToWindow(win).PromptForMultipleSelection()` — не с главного потока; через интерфейс `FilePicker` (desktop — Wails, browser/тесты — «не поддерживается»/фейк).
- Browser: `POST /api/attachments/{srv}/{channel}?name=&mime=` — bearer + `OriginGuard` + `LoopbackHostGuard`, сырое тело → `AddBytes` с лимитом `MaxFileSize` (413 сверх), имя очищается (без путей, управляющих символов).
- AGENTS.md: «`Blob`/`File`/`FormData` в `fetch` к `wails://` роняет приложение — только `Uint8Array`», «пути в Go из UI не передаются», правила буфера (главный поток, асинхронно, таймаут), `EnableFileDrop` и подделка drop.
- Тесты: разбор `uri-list`/`gnome-copied-files` (Go, без GTK), выбор источника по TARGETS (фейковый `Clipboard`), обработчик drop (фейковый контекст), `PickAttachments` через фейк, маршрут browser (защиты, лимит, имя). Проверка настоящего GTK-буфера — смоук на своём Xvfb (скрипт из спайка, не в CI): скриншот PNG и файл из «файлового менеджера» доходят до вложения.

### Task 5: UI — вложения в композере, вставка, перетаскивание, 📎

**Files:** `frontend/src/components/Composer.tsx` (+ тест), новый `Attachments­Tray.tsx` (или `ComposerAttachments.tsx`) (+ тест), `ChannelPane.tsx`, `chat.ts`, `store.ts`, `api/client.ts`, `api/types.ts` (+ тесты), `PostItem.tsx`/`Attachments.tsx` (ожидающий пост с локальными файлами), `i18n.ts`.

- Полоса вложений над полем ввода: чип — миниатюра (`/media/<srv>/staged/<id>`) или иконка типа, имя, размер; состояние: прогресс-полоса / «Ошибка: …» с «Повторить» / готово; «×» удаляет (кнопка с aria-label), с фокусом на чипе — Delete/Backspace. Сообщения лимитов (`too_large`, `too_many`, `attachments_disabled`, `not_a_file`) — у композера, как ошибка отправки.
- Вставка: desktop — если в буфере скрытый список файлов (`types` содержит `text/uri-list` и `getData('text/uri-list') === ''`) → `preventDefault` + `attachFromClipboard`; если нет `text/plain` (голая картинка/HTML+картинка) → `attachFromClipboard` без `preventDefault`. Browser — `clipboardData.files.length > 0` → `preventDefault` + загрузка каждого `File` сырым телом в `/api/attachments`.
- Перетаскивание: область канала (лента+композер) — `data-file-drop-target data-srv data-channel`, подсветка `.file-drop-target-active` (рамка/подложка «Отпустите, чтобы прикрепить»); browser — DOM `dragover`/`drop` → `File[]`.
- 📎: кнопка-иконка слева/справа в композере (aria-label «Прикрепить файлы»); desktop — `pickAttachments`; browser — скрытый `<input type=file multiple>`.
- Отправка: Enter с вложениями (в т.ч. без текста) → `sendPost(…, attachmentIDs)`; пока идут загрузки — пост в ленте «Отправляется…» с превью. Ожидающий пост рендерит `files` как обычный (превью `staged`).
- Никаких `Blob`/`File`/`FormData` в `fetch` к `wails://` (тест: desktop-путь не шлёт байты вообще; browser — `fetch` с `File` только к `/api/attachments` HTTP-транспорта).
- Тесты (vitest): вставка — список файлов → `attachFromClipboard` и `preventDefault`; обычный текст/ссылка → не перехватывается; голая картинка → `attachFromClipboard`; browser `files` → загрузка; чипы по событию (прогресс, ошибка, повтор, удаление, клавиатура); лимиты; отправка только вложений; drop в browser; 📎 в обоих режимах; ожидающий пост с превью.
- Скриншоты browser mode: чипы (картинка с прогрессом, файл готов, ошибка), подсветка перетаскивания, ожидающий пост, отправленный пост с картинкой и файлом.

### Task 6: e2e и документация

- e2e (`tests/e2e/`, browser mode): вставка картинки (синтетический `ClipboardEvent` с `DataTransfer` + `File` PNG) → чип → Enter → пост с картинкой в ленте (скриншот вставки и отправки); вставка файла (не картинки) → карточка файла в посте; перетаскивание (синтетический `DragEvent`) → чип; 📎 (`setInputFiles`) → чип; ошибка загрузки (тест-API фейка) → «Повторить» → отправлено; лимит `MaxFileSize` → сообщение; только вложения без текста.
- Смоук desktop на своём Xvfb (не в CI, результат — в спайк/отчёт): временная сборка desktop под `.agents/tmp`, свой `SPK_MM_CLIENT_HOME`, `--mm-fake`; буфер Xvfb заполняется PNG и списком файлов, вставка через `execute_editing_command`/Xvfb-локальный ввод (только на своём дисплее) — вложение появилось, пост с файлом создан в фейке.
- Проверка памяти: вставка/отправка нескольких картинок по 10–20 МБ — главный процесс и WebKit не растут после отправки (спулы удалены, куча Go без роста) — `scripts/pss.sh`, headless.
- Документы: спека (редактор: вложения, три источника, лимиты, поведение офлайн/ошибок, безопасность), `AGENTS.md` (правила с именами тестов, «Things that bite»: Blob-в-`wails://`, буфер GTK на главном потоке, подделка drop), `README.md`, `docs/backlog.md` (вложения не переживают перезапуск; upload sessions для больших файлов/нестабильной сети; Wayland не проверен; `MaxImageResolution` не проверяется клиентом; всё, что осталось «minor (deferred)» в реестре).
- Все ворота зелёные.
