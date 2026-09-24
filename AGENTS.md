# spk-mattermost — гид для агентов

Лёгкий десктопный клиент Mattermost (Wails v3 + React). Спецификация:
`docs/specs/2026-09-24-spk-mattermost-design.md`. Планы: `docs/plans/`.
Результаты спайков: `docs/spikes/`.

## Сборка и тесты

- `make build` — фронт + бинарь для browser-режима (`build/bin/spk-mattermost`).
- `make build-desktop` — desktop-бинарь (теги `wails gtk3`).
- `make test-go`, `make test-front`, `make test-e2e`, `make lint`, `make cross-check`.
- `make run-browser` — UI на http://127.0.0.1:5180 с фейковым сервером MM (данные во временном каталоге).

## Правила (правило — причина — тест)

- `go build ./...` без тега `wails` обязан проходить: desktop-код за тегом.
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
- Действия записи (`SendPost`, `EditPost`, `DeletePost`, `MarkUnread`, …) при `needs_reauth`
  отказывают сразу кодом `session_expired`, не уходя в воркер — see `s.writer()` seam. —
  `internal/api/chat.go`.

## Things that bite

- Vitest: `@wailsio/runtime` is globally mocked in `frontend/vitest.setup.ts` (its import-time drag/resize code touches `window` after jsdom teardown); tests of `wailsClient` override the mock locally.
- Wails v3 beta.25 on Linux touches the D-Bus session bus with no timeout in several places: `SingleInstance` (inside `application.New()`, `os.Exit(1)` on failure — uninterceptable), the notifications service startup, `SystemTray.Run` (`InvokeSync(dbus.SessionBus())` on the GTK main thread — a hung bus freezes the UI), and GLib itself (`GApplication` registration in `g_application_run` — with a hung bus the window never appears). Rule: probe the bus **once** with a 2 s bound (`probeBus`) and let `integrationsFor` decide; unless it answered, disable single-instance, notifications and tray, point `DBUS_SESSION_BUS_ADDRESS` at a dead address before GTK starts, and make window close quit (no tray = no way back). Wails' theme listener runs on its own goroutine/connection and does not block the main thread. — `internal/desktop/integrations.go` (`TestIntegrationsFor`, `TestProbeBus`), `internal/desktop/busprobe_linux.go`, `internal/desktop/run.go`.
- `EventManager.Emit(name, data ...any)` with exactly one non-slice argument sets `event.Data = data[0]` — the frontend receives the payload directly, not wrapped in a 1-element array. Since this app always calls `app.Event.Emit(ev.Type, ev.Payload)` (one argument), the array-unwrap branch in `wailsClient.subscribeEvents` is defensive but currently dead code. — `internal/desktop/run.go`, `frontend/src/api/client.ts`.
- Frontend toolchain resolved newer than the stage-1 briefs assumed (TypeScript 6, Vite 8, Vitest 4): bare CSS side-effect imports need `"vite/client"` in `tsconfig.json`'s `"types"`, and `vite.config.ts` must `import { defineConfig } from 'vitest/config'` (not `'vite'`) — a triple-slash `vitest` types reference no longer reliably pulls in the `UserConfig.test` augmentation.
- Memory budget is **Private_Dirty** of all app processes (≤150 MB with 2–3 servers/~100 channels), not PSS: PSS includes a share of WebKit/GTK/ICU libraries shared with other apps and swings with what else runs (150–196 MB PSS vs ~73–80 MB Private_Dirty for the empty shell; release build and WebKit GPU policy don't change it). Measure with `scripts/pss.sh <pid>` (prints both). — `docs/spikes/2026-09-24-stage1-spikes.md` S4.
- Wails beta.25 Linux tray: `SystemTray.SetTooltip` is a no-op and the StatusNotifierItem `Id`/`ToolTip` are frozen to the label when the tray starts (default "Wails"); only `SetLabel` (SNI `Title`) and `SetIcon` update live. Tray/notification calls reach GTK/D-Bus with no timeout — keep them off service goroutines (`offerLatest`, `asyncSender`). — `internal/desktop/tray.go` (`setTrayText`, `trayBadge`), `internal/desktop/async.go` (`TestAsyncSenderDetachesAHungSendAndResumes`).
- Fake-server test API (dev/e2e only, gated by `--test-api`): `/api/_test/fake/post`, `/api/_test/fake/drop` (simulate a lost WS connection — `{lose:true}` drops the server's dead-letter buffer too, forcing a resync instead of a resume), `/api/_test/fake/revoke` (expire the session), plus `/api/_test/notifications` and `/api/_test/notification-click` for asserting on desktop-notification delivery/click without a real OS notifier. — `cmd/spk-mattermost/browser.go`.
- Go-level fake network controls for `mmsync` tests: `mmfake.Server.SetDown` (every request 503 — server unreachable), `SetLatency(pathPart, d)` (slow endpoint, e.g. keep a resync in flight), `RejectResumes` (close resumed sockets without a hello). The harness `useClock()` gives the worker a clock the test can jump forward while the fake keeps real time. — `internal/mmfake/net.go`, `internal/mmsync/harness_test.go`.
- `--mm-fake-channels N` (browser mode) seeds N extra open channels (`c-load-001`…, 20 posts each) in the fake server for memory/perf checks; the desktop `--mm-fake` flag (dev builds only) starts the same fake server in-process and signs in as alice automatically — always point it at its own `SPK_MATTERMOST_HOME` (a fresh temp dir), never at the live client's data dir, and it clears any stale fake-mode server entries from a previous dev run before adding the live one. — `cmd/spk-mattermost/main.go`, `cmd/spk-mattermost/run_desktop_wails.go`.
- Testing the virtualized feed under Vitest/jsdom needs a manual layout stub: jsdom never computes real layout, so `HTMLElement.prototype.offsetHeight` (row height for the virtualizer) and `scrollTo` (history-load anchor) must be overridden in `beforeEach`/restored in `afterEach`, not left at jsdom's defaults (0 / no-op that doesn't move `scrollTop`). — `frontend/src/components/Feed.test.tsx`.
- `ServerRail`'s per-server button folds the unread/mention badge into its own accessible name ("`<name> — Mentions: N`" or "`<name> — Unread messages`"), not just the sibling badge `<span>` (Task 12 fix). Playwright's `getByLabel`/`getByRole(name:)` match is a substring by default, so `page.getByRole('navigation').getByLabel('Mentions: 1')` also matches that button (whose name contains "Mentions: 1") in addition to the intended badge — two hits trip strict mode. e2e assertions on these badge labels need `{ exact: true }`. — `frontend/src/components/ServerRail.tsx`, `tests/e2e/chat.spec.ts`.
