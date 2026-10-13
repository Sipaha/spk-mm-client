# Исправления аудита: рабочее состояние

Цель пользователя: исправить **все A1–A6**. Работа выполняется поверх
базового commit `77b77102b027de1346ff32597ebdf44dbfec802c` и ранее внесённого
исправления reconnect. Изменения пока не закоммичены; исходное изменение
README.md сохранено. Установленный клиент не заменялся.

## Этап 1 — A1, проверен 2026-10-13

Исправлено завершение уже начавшегося SSO/парольного входа после Logout,
RemoveServer, нового входа, Close или отмены вызывающего.

- `internal/api/sessions.go` владеет сменой сессии на сервер. Вход получает
  поколение и отменяемый контекст. Network authentication/revoke выполняются
  вне локов, а перед остановкой старого воркера и перед локальной фиксацией
  проверяется, что попытка всё ещё актуальна. Старая SSO-попытка завершается
  тихо; старый парольный вход отвечает `cancelled`.
- `auth.SSO.BeginAttempt` сохраняет поколение начала flow и передаёт его
  через `Result.Generation` в callback, включая callback с `Me` уже в пути.
- `internal/api/lifecycle.go` регистрирует auth-вызовы и отменяет/ожидает их
  при Service.Close; новые auth-вызовы и Start после Close запрещены.
  Sync Manager теперь также принадлежит жизни Service. Эта инфраструктура
  **на этапе 1 ещё не была подключена к downloads**; подключение завершено ниже.
- Store.ReplaceSession атомарно сохраняет токен и удаляет снимок другого
  пользователя. Store.EndSession атомарно очищает credentials и снимок.
  Повторный вход того же пользователя сохраняет snapshot. Старый воркер
  завершает flush до транзакции. Проверены откаты при ошибке очистки.
- Отмена UI-waiter после начала локальной фиксации не оставляет записанный
  токен без запуска воркера: этот короткий шаг ограничен контекстом Service.
  При отмене до фиксации восстанавливается прежний воркер, если поколение
  не было заменено; прежний контракт восстановления после ошибки БД сохранён.

Регрессии:
`TestSessionChangeCancelsPendingAuthentication` — 20 комбинаций
(SSO/password × authenticate/revoke × logout/remove/new-login/close/caller-cancel).
Проверяется отмена старого HTTP-запроса и результат в БД/менеджере.
Дополнительно: запрет нового auth после Close, перенос поколения SSO,
атомарность смены credentials/snapshot и сохранение своего snapshot.

В POST-barrier тест сначала читает тело и восстанавливает его для fake:
net/http не может надёжно заметить закрытие соединения, пока тело запроса
не прочитано. Токены/пароли в вывод не попадают.

Проверки завершённого этапа:

- `go test -race -timeout 120s ./...` — PASS, все пакеты;
  API 50.106 с, mmsync 57.797 с.
- Целевые регрессии API/auth/store с `-race` — PASS.
- `make lint cross-check` — PASS, `0 issues`, Windows build/vet.
- `make build-desktop build-go` — PASS, frontend + browser + Linux desktop.
- Playwright `login.spec.ts chat.spec.ts network-recovery.spec.ts reconnect.spec.ts`
  — **19 passed (28.0s)**, включая GitLab callback, duplicate callback,
  password sign-in/sign-out и session expiration.
- Скриншот просмотрен: `.tmp/mm-fixes/shots-phase1/network-recovered.png`
  от Solution; UI показывает восстановленный чат.

Логи — `.tmp/mm-fixes/` от корня Solution: `session-regressions3.log`,
`go-phase1.log`, `build-phase1.log`, `lint-phase1.log`, `e2e-phase1.log`.

Пауза на границе этапа сделана по явному уведомлению пользователя о контексте.
Исходная цель исправления A1–A6 остаётся незавершённой.

## Этап 2 — A2–A6 исправлены и проверены 2026-10-13

**A2.** URL WebView и disk/negative/inflight keys включают `media_scope` —
не-секретный SHA-256 account id; токен остаётся в Go. Cache до lookup получает
immutable worker identity/lifetime и проверяет scope URL. Logout/смена аккаунта
отменяют fetch/stream; перед чтением готового файла scope проверяется снова,
а чтение самого body учитывает отмену. Signed-in офлайн-сессия читает свой кэш,
`needs_reauth` и чужой/старый URL отказывают. Stream type cache также scoped.
Staged, PDF, emoji, posticon и остальные URL проходят ту же проверку.
Прежние unscoped disk entries не переиспользуются; остаются под общим лимитом
кэша и вытесняются обычным LRU. Уже скачанные пользователем файлы не удаляются.
Account switch очищает старые UI views и перемонтирует области с различными
React keys. Нативный скриншот обнаружил и помог исправить коллизию этих keys.

**A3.** Каждый save/Open waiter регистрируется в Service lifetime, а общий
transfer работает в отдельной зарегистрированной операции с worker context.
Отмена первого waiter не отменяет общий transfer. Close отменяет и ждёт
fetch, итоговые записи в Store, cleanup и вызовы opener до возвращения;
новые вызовы после Close запрещены. `deactivate` сначала останавливает worker,
затем ждёт его downloads; регистрация под `filesMu` отвергает уже отменённого
владельца. Saved/inflight keys разделены по аккаунтам. Перед публикацией
готового файла и перед opener проверяется отмена. Неизменённые пользовательские
файлы сохраняются при выходе.

**A4.** `refreshServers` имеет поколение запроса. Поздние списки и ошибки
отбрасываются до изменения store; выбор, badges и поведение баннеров сохраняются.

**A5.** Навигация уведомления продолжает канал→тред только при успешном
приземлении channel response и неизменённом navigation generation, выбранном
сервере и wanted/показанном канале. Смена канала/сервера, открытие другого
треда, закрытие панели и ошибка отменяют устаревшее продолжение.

**A6.** `OpenChannel` проверяет membership без полной проекции. Полный
`ChannelView` проходит history/window без промежуточной копии raw posts и
предвыделяет DTO slice. Для обычных UI refresh добавлен `GetChannelDelta`:
явный acknowledged baseline, изменённые posts, removed IDs, полный порядок ID
только при его изменении, актуальные headers/gaps/drafts на каждом ответе.
Несовпавший revision/channel получает reset, поэтому потерянный/поздний ответ
не ломает историю. Сохраняются все удержанные посты, история не ограничена.
Один projection cache на worker относится к последнему запрошенному каналу;
это дополнительная память в обмен на малый recurring IPC. Metadata reducers
заменяют `PostMetadata` целиком: его files/reactions можно переиспользовать.
Author/presence/avatar/preferences/root context пересчитываются, edits/deletes/
reactions/pending и перестановки передаются. UI сохраняет ссылки неизменённых
rows и всего posts array при metadata-only update; Feed не перестраивает rows
из-за изменений draft/header/sync, не участвующих в grouping.

Обход Go history и JS сборка изменившегося массива остаются O(n). Первый reset
остаётся полным; смена состава передаёт O(n) строк ID. Это не обещание постоянной
стоимости всех операций и не оценка retained heap. Непрерывная прокрутка,
hist/gap generations, grouping и якоря остаются прежними.

Регрессии: `media_accounts_test.go`, `media/accounts_test.go`,
`download_lifecycle_test.go`, `projection_test.go`, `channelPatch.test.ts`,
новые тесты `chat.test.ts`/`media.test.ts` и `audit-fixes.spec.ts`.
Runner `reproduce.py all` теперь запускает реальные desired-contract tests;
исходные API/UI bug-presence probes остаются только архивным свидетельством.

Итоговая проверка:

- Полный `go test -race -timeout 120s ./...` — PASS; дополнительный полный
  state `-race` с новым heap-регрессионным тестом — PASS (3.308 с).
- Frontend — **1135 tests / 70 files PASS**; текущий audit runner — PASS,
  в том числе 116 целевых frontend regressions и auth/media/download probes.
- Полный Playwright — **109 passed (4.6m)**, включая account isolation,
  login/reconnect, медиа/PDF/downloads, notifications/threads/search и walkFeed.
- Lint — **0 issues**, frontend lint PASS; Windows build/vet PASS.
- Последовательные browser/Linux desktop builds — PASS.
- Нативный smoke в отдельном Xvfb: fake-профиль, 3 сервера × 100 каналов,
  churn каждые 2 с, 90 с — **exit 0**. Скриншоты account-isolation и native
  просмотрены: текущий аккаунт, одна sidebar, чат и composer без регрессии.
  Private_Dirty дерева 189.05 / 176.26 / 204.15 MiB на 15/45/90 с;
  fake серверы входят в процесс. 90 с не доказывают плато или отсутствие утечки.

Промежуточные провалы исправлены: Playwright browser-cache path в окружении,
устаревшие URL-expectations после добавления scope, коллизия sibling React keys
(выявлена скриншотом). Heap-тест при запуске после JSON-тестов первоначально
считал чужой sync.Pool; две GC-итерации удаляют pool victims до каждого замера.
Production cache после смены канала не остаётся удержанным.

Логи от корня Solution `.tmp/mm-fixes/`: `go-verified-all.log`,
`state-complete3.log`, `checks-verified.log`, `lint-verified-all.log`,
`build-verified.log`, `e2e-complete.log`, `runner-final.log`,
`native-complete/smoke.log`. Просмотренные скриншоты:
`shots-complete/account-isolation.png`, `native-complete/native-smoke.png`.
Архивные исходные probes сохраняются; runner больше не ожидает наличие дефектов.

Замеры Ryzen 7 260, Go 1.26.9, Linux amd64, 500-byte text posts, без `-race`:

| Удержано posts | Исходный full view+JSON | Новый full view+JSON | Recurring delta+JSON |
|---:|---:|---:|---:|
| 10 000 | 16.60 ms / 40.06 MB/op | 10.37 ms / 13.64 MB/op | 2.56 ms / 1.8 KB/op |
| 50 000 | 77.95 ms / 218.17 MB/op | 61.30 ms / 80.04 MB/op | 19.70 ms / 1.9 KB/op |

Recurring benchmark — стабильный состав history, не whole UI frame или RSS.
При 50k reset = 30 317 065 bytes; edit самого старого post = **483 bytes**.
Projection cache дополнительно удерживает **~19.4 MiB на 50k** простых постов;
после навигации замер возвращается к baseline (~0.00–0.01 MiB).
Это осознанный компромисс памяти и стоимости повторных ответов, не снижение RSS.
Исходные baseline и scripts сохранены; новые результаты:
[full benchmarks](fixed-full-benchmarks.log),
[delta benchmarks](fixed-delta-benchmarks.log),
[отдельный retained heap](fixed-projection-heap.log),
[native metrics](fixed-native-metrics.json).
Установленный `/usr/bin/spk-mm-client --version` отдельно проверен: **1.0.0**;
никаких claims о deployed исправлении, установки, commit или release нет.

Все шесть находок закрыты локальным кодом и проверками. Выпуск v1.0.1
публикуется штатным GitHub Actions release pipeline; локальные operational
receipts остаются вне публичного исходного дерева. Первый combined heap-test
считал чужие sync.Pool victims: методика исправлена двумя GC-итерациями,
после чего весь state race и весь Go race прошли. Это ошибка тестовой методики,
не дополнительный production-дефект.
