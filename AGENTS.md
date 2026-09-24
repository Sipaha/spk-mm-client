# spk-mattermost — гид для агентов

Лёгкий десктопный клиент Mattermost (Wails v3 + React). Спецификация:
`docs/specs/2026-09-24-spk-mattermost-design.md`. Планы: `docs/plans/`.
Результаты спайков: `docs/spikes/`.

## Сборка и тесты

- `make build` — фронт + бинарь для browser-режима (`build/bin/spk-mattermost`).
- `make build-desktop` — desktop-бинарь (теги `wails gtk3`).
- `make test-go`, `make test-front`, `make test-e2e`, `make lint`, `make cross-check`.
- `make run-browser` — UI на http://127.0.0.1:5180 с фейковым сервером MM.

## Правила (правило — причина — тест)

- `go build ./...` без тега `wails` обязан проходить: desktop-код за тегом.
- Токены сервера не покидают Go: нет в DTO, логах, событиях. — `TestServerDTOHasNoToken`, `TestServerLogValueHidesToken`.
- Никаких блокирующих системных вызовов на старте (урок официального клиента, завис на gnome-keyring). Токены — в SQLite.
- `events.Emitter.Emit` не блокирует: полный буфер подписчика — событие отбрасывается.
- SQLite — одно соединение; многошаговые записи только через `Store.WithTx`.
- Версии `github.com/wailsapp/wails/v3` и `@wailsio/runtime` совпадают.

## Things that bite

- Vitest: `@wailsio/runtime` is globally mocked in `frontend/vitest.setup.ts` (its import-time drag/resize code touches `window` after jsdom teardown); tests of `wailsClient` override the mock locally.
- Wails v3 beta.25 `SingleInstance` on Linux dials D-Bus synchronously, with no timeout, inside `application.New()`, and calls `os.Exit(1)` internally on failure — uninterceptable from calling code. Rule: probe `dbus.ConnectSessionBus()` with a bounded timeout first and pass `SingleInstance: nil` when unreachable, degrading to "no single-instance lock" instead of crashing. — `internal/desktop/singleinstance_linux.go`.
- Wails v3 beta.25 notifications service `ServiceStartup` on Linux also does a synchronous, uncancellable `dbus.ConnectSessionBus()`. Rule: wrap it in a bounded `startWithTimeout` and degrade sends to logged no-ops on failure/timeout, never let it block or fail startup. — `internal/desktop/notify.go`, `internal/desktop/notify_startup.go`.
- `EventManager.Emit(name, data ...any)` with exactly one non-slice argument sets `event.Data = data[0]` — the frontend receives the payload directly, not wrapped in a 1-element array. Since this app always calls `app.Event.Emit(ev.Type, ev.Payload)` (one argument), the array-unwrap branch in `wailsClient.subscribeEvents` is defensive but currently dead code. — `internal/desktop/run.go`, `frontend/src/api/client.ts`.
- Frontend toolchain resolved newer than the stage-1 briefs assumed (TypeScript 6, Vite 8, Vitest 4): bare CSS side-effect imports need `"vite/client"` in `tsconfig.json`'s `"types"`, and `vite.config.ts` must `import { defineConfig } from 'vitest/config'` (not `'vite'`) — a triple-slash `vitest` types reference no longer reliably pulls in the `UserConfig.test` augmentation.
- Stage-1 memory spike (S4): an empty shell (0 servers, "Add server" screen) already measures ~150 MB total PSS (WebKitGTK web+network process + main binary) — right at the ≤150 MB budget that the spec sets for 2–3 servers with ~100 channels. Budget must stay in focus from the first sync/rendering work in stage 2, not deferred to stage 4. — `docs/spikes/2026-09-24-stage1-spikes.md`.
