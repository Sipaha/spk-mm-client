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
