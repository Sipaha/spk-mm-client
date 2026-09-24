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

## Things that bite

- Vitest: `@wailsio/runtime` is globally mocked in `frontend/vitest.setup.ts` (its import-time drag/resize code touches `window` after jsdom teardown); tests of `wailsClient` override the mock locally.
- Wails v3 beta.25 on Linux touches the D-Bus session bus with no timeout in several places: `SingleInstance` (inside `application.New()`, `os.Exit(1)` on failure — uninterceptable), the notifications service startup, `SystemTray.Run` (`InvokeSync(dbus.SessionBus())` on the GTK main thread — a hung bus freezes the UI), and GLib itself (`GApplication` registration in `g_application_run` — with a hung bus the window never appears). Rule: probe the bus **once** with a 2 s bound (`probeBus`) and let `integrationsFor` decide; unless it answered, disable single-instance, notifications and tray, point `DBUS_SESSION_BUS_ADDRESS` at a dead address before GTK starts, and make window close quit (no tray = no way back). Wails' theme listener runs on its own goroutine/connection and does not block the main thread. — `internal/desktop/integrations.go` (`TestIntegrationsFor`, `TestProbeBus`), `internal/desktop/busprobe_linux.go`, `internal/desktop/run.go`.
- `EventManager.Emit(name, data ...any)` with exactly one non-slice argument sets `event.Data = data[0]` — the frontend receives the payload directly, not wrapped in a 1-element array. Since this app always calls `app.Event.Emit(ev.Type, ev.Payload)` (one argument), the array-unwrap branch in `wailsClient.subscribeEvents` is defensive but currently dead code. — `internal/desktop/run.go`, `frontend/src/api/client.ts`.
- Frontend toolchain resolved newer than the stage-1 briefs assumed (TypeScript 6, Vite 8, Vitest 4): bare CSS side-effect imports need `"vite/client"` in `tsconfig.json`'s `"types"`, and `vite.config.ts` must `import { defineConfig } from 'vitest/config'` (not `'vite'`) — a triple-slash `vitest` types reference no longer reliably pulls in the `UserConfig.test` augmentation.
- Stage-1 memory spike (S4): an empty shell (0 servers, "Add server" screen) already measures ~150 MB total PSS (WebKitGTK web+network process + main binary) — right at the ≤150 MB budget that the spec sets for 2–3 servers with ~100 channels. Budget must stay in focus from the first sync/rendering work in stage 2, not deferred to stage 4. — `docs/spikes/2026-09-24-stage1-spikes.md`.
