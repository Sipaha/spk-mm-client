# spk-mattermost — Этап 1: каркас, вход, спайки. План реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Рабочий каркас клиента: приложение запускается в desktop- и browser-режиме, хранит серверы в SQLite, добавляет сервер Mattermost, входит по паролю и через GitLab SSO (`mmauth://`), имеет окно, трей, single-instance и проверенные спайки S1 (вход), S2 (уведомления), S4 (память).

**Architecture:** Один процесс Wails v3. Go-ядро (`internal/*`) за интерфейсом `api.API`; два транспорта — Wails-биндинги (desktop) и HTTP+SSE (browser-режим для e2e). Фейк-сервер Mattermost (`internal/mmfake`) обслуживает тесты и browser-режим. Фронт — React-SPA, встроенная в бинарь.

**Tech Stack:** Go 1.26, Wails v3.0.0-beta.25, cobra, modernc.org/sqlite, testify; React 19, Vite 8, TypeScript 6, Tailwind 4, Zustand 5, Vitest, Playwright; pnpm.

**Spec:** `docs/specs/2026-09-24-spk-mattermost-design.md` (читать вместе с этим планом).

Этот план покрывает этап 1 спецификации плюс вход (из этапа 2 — нужен для спайка S1). Этапы 2–4 получат отдельные планы после результатов спайков.

## Global Constraints

- Go-модуль: `github.com/spk/spk-mattermost`; Go `1.26`.
- Wails: Go `github.com/wailsapp/wails/v3 v3.0.0-beta.25` и npm `@wailsio/runtime` **ровно** `3.0.0-beta.25` (версии обязаны совпадать).
- Linux-сборка desktop: build tags `wails gtk3` (webkit2gtk-4.1, как в citeck-launcher). Без тега `wails` `go build ./...` и `go test ./...` обязаны проходить (desktop-код за тегом).
- Фронт: React 19, Vite 8, TypeScript 6, Tailwind 4, Zustand 5, без UI-библиотеки и без роутера; менеджер пакетов **pnpm**.
- SQLite: `modernc.org/sqlite`, WAL, одно соединение (`MaxOpenConns=1`), все многошаговые записи через `WithTx`.
- Данные: `~/.spk/spk-mattermost/` (переопределяется `SPK_MATTERMOST_HOME`); каталог 0700, файлы БД 0600.
- Токены: хранятся в таблице `servers` SQLite; **никогда** не уходят во фронт, в логи, в JSON DTO. Без keyring и мастер-пароля.
- На пути старта нет блокирующих обращений к системным сервисам (keyring, D-Bus без таймаута и т.п.).
- Вход GitLab: `/oauth/gitlab/mobile_login?redirect_to=mmauth://callback`; ответ — `mmauth://callback?MMAUTHTOKEN=…&MMCSRF=…&srv=…`.
- UI на русском и английском; все строки — через словарь i18n; паритет ключей проверяется тестом.
- Коммиты: без `Co-Authored-By`, без amend.

## Review Focus

1. **Повторная доставка `mmauth://`** (событие запуска Wails + второй экземпляр, двойной клик по ссылке) — вторая доставка молча игнорируется, UI не показывает ошибку после успешного входа. Тесты: Task 7 (`TestCompleteTwiceIsAlreadyCompleted`), Task 8 (`TestHandleDeepLinkDuplicateIsSilent`).
2. **URL сервера введён иначе, чем `SiteURL`** (без схемы, со слэшем, другой регистр, другой хост в `SiteURL`) — колбэк с `srv` всё равно сопоставляется с сервером. Тесты: Task 5 (`TestNormalizeURL`), Task 7 (`TestCompleteMatchesSiteURLAlias`).
3. **Утечка токена** через логи/JSON — `store.Server` в slog маскирует токен; `ServerDTO` токена не содержит. Тесты: Task 3 (`TestServerLogValueHidesToken`), Task 8 (`TestServerDTOHasNoToken`).
4. **Сервер недоступен / это не Mattermost / медленный** при добавлении — понятный код ошибки, без зависания дольше таймаута. Тесты: Task 8 (`TestAddServerNotMattermost`, `TestAddServerUnreachable`).
5. **Сессия отозвана на сервере** — `Me()` отдаёт 401: завершение SSO падает с кодом `auth_failed`; «Выйти» при уже мёртвом токене всё равно очищает локальную сессию. Тесты: Task 5 (`TestLogoutTreats401AsSuccess`), Task 8 (`TestLogoutClearsEvenIfServerRejects`).

---

## File Structure

```
spk-mattermost/
├── AGENTS.md                      # гид для агентов (CLAUDE.md — симлинк)
├── CLAUDE.md -> AGENTS.md
├── Makefile
├── go.mod / go.sum
├── .gitignore
├── .golangci.yml
├── cmd/spk-mattermost/
│   ├── main.go                    # cobra: --browser --port --mm-fake --test-api
│   ├── main_test.go
│   ├── embed.go                   # go:embed all:dist
│   ├── dist/.gitkeep
│   ├── browser.go                 # browser-режим (HTTP + SSE + test-api)
│   ├── static.go                  # index.html с мета-токеном
│   ├── run_desktop_wails.go       # //go:build wails
│   └── run_desktop_nowails.go     # //go:build !wails
├── internal/
│   ├── paths/paths.go             # каталоги данных
│   ├── store/                     # SQLite: open, migrate, WithTx, servers
│   │   ├── store.go
│   │   ├── migrations/0001_servers.sql
│   │   └── servers.go
│   ├── events/events.go           # неблокирующий Emitter (из spk-mail)
│   ├── mm/rest/                   # REST v4 клиент
│   │   ├── errors.go
│   │   ├── url.go
│   │   ├── client.go
│   │   └── endpoints.go
│   ├── mmfake/server.go           # фейк Mattermost + фейк GitLab
│   ├── auth/sso.go                # mmauth:// — начало/завершение входа
│   ├── api/                       # API приложения
│   │   ├── api.go                 # интерфейс, DTO, коды ошибок, события
│   │   └── service.go
│   ├── api/transport/
│   │   ├── http.go                # browser-режим: POST /api/<Method>, SSE
│   │   ├── guard.go               # OriginGuard + bearer (из spk-mail)
│   │   └── wails.go               # //go:build wails
│   ├── appfiles/                  # иконки (embed)
│   │   ├── embed.go
│   │   └── icons/icon.png
│   └── desktop/                   # //go:build wails
│       ├── run.go                 # окно, single-instance, deep link
│       ├── tray.go
│       ├── notify.go              # спайк S2
│       ├── devtools_dev.go
│       └── devtools_prod.go
├── frontend/                      # React SPA
│   ├── package.json, vite.config.ts, tsconfig.json, index.html
│   └── src/{main.tsx,App.tsx,index.css,i18n.ts,errors.ts,store.ts,
│            api/{types.ts,client.ts},
│            components/{ServerRail,AddServerForm,ServerPanel}.tsx}
├── tests/e2e/                     # Playwright против browser-режима + mmfake
├── scripts/{gen-icon.go,pss.sh,install-dev-linux.sh}
└── docs/{specs,plans,spikes}/
```

---

### Task 1: Каркас репозитория и CLI

**Files:**
- Create: `go.mod`, `.gitignore`, `.golangci.yml`, `Makefile`, `AGENTS.md`, `CLAUDE.md` (симлинк), `cmd/spk-mattermost/main.go`, `cmd/spk-mattermost/main_test.go`, `cmd/spk-mattermost/embed.go`, `cmd/spk-mattermost/dist/.gitkeep`, `cmd/spk-mattermost/run_desktop_nowails.go`

**Interfaces:**
- Produces: `newRootCmd(run runners) *cobra.Command`; `type runners struct{ browser func(ctx context.Context, o browserOpts) error; desktop func(ctx context.Context) error }`; `type browserOpts struct{ Port int; MMFake bool; TestAPI bool }`; `frontendFS() fs.FS`; `runDesktop(ctx) error` (в `!wails` — возвращает ошибку-подсказку).

- [ ] **Step 1: Инициализировать модуль и зависимости**

```bash
cd spk-mattermost
go mod init github.com/spk/spk-mattermost
go mod edit -go=1.26
go get github.com/spf13/cobra@v1.10.2 github.com/stretchr/testify@v1.11.1
```

`.gitignore`:
```
/build/
/frontend/node_modules/
/frontend/dist/
/tests/e2e/node_modules/
/tests/e2e/test-results/
/tests/e2e/playwright-report/
/cmd/spk-mattermost/dist/*
!/cmd/spk-mattermost/dist/.gitkeep
coverage.*
```

`.golangci.yml` — скопировать из `../spk-mail/.golangci.yml` без изменений.

- [ ] **Step 2: Написать падающий тест CLI**

`cmd/spk-mattermost/main_test.go`:
```go
package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserFlagsRouteToBrowserRunner(t *testing.T) {
	var got browserOpts
	var desktopCalled bool
	cmd := newRootCmd(runners{
		browser: func(_ context.Context, o browserOpts) error { got = o; return nil },
		desktop: func(context.Context) error { desktopCalled = true; return nil },
	})
	cmd.SetArgs([]string{"--browser", "--port", "5199", "--mm-fake", "--test-api"})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	assert.Equal(t, browserOpts{Port: 5199, MMFake: true, TestAPI: true}, got)
	assert.False(t, desktopCalled)
}

func TestNoFlagsRouteToDesktopRunner(t *testing.T) {
	var desktopCalled bool
	cmd := newRootCmd(runners{
		browser: func(context.Context, browserOpts) error { t.Fatal("browser runner called"); return nil },
		desktop: func(context.Context) error { desktopCalled = true; return nil },
	})
	cmd.SetArgs(nil)
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	assert.True(t, desktopCalled)
}

// Linux/Windows pass a custom-scheme URL as the only argument when the OS
// launches us for mmauth://. Cobra must not reject it as an unknown command.
func TestPositionalDeepLinkArgIsAccepted(t *testing.T) {
	var desktopCalled bool
	cmd := newRootCmd(runners{
		browser: func(context.Context, browserOpts) error { return nil },
		desktop: func(context.Context) error { desktopCalled = true; return nil },
	})
	cmd.SetArgs([]string{"mmauth://callback?MMAUTHTOKEN=x&srv=https://mm.example.com"})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	assert.True(t, desktopCalled)
}
```

- [ ] **Step 3: Запустить — убедиться, что падает**

Run: `go test ./cmd/...`
Expected: FAIL — `undefined: newRootCmd`.

- [ ] **Step 4: Реализовать main, embed, заглушку desktop**

`cmd/spk-mattermost/main.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

type browserOpts struct {
	Port    int
	MMFake  bool
	TestAPI bool
}

type runners struct {
	browser func(ctx context.Context, o browserOpts) error
	desktop func(ctx context.Context) error
}

func newRootCmd(run runners) *cobra.Command {
	var o browserOpts
	var browser bool
	root := &cobra.Command{
		Use:           "spk-mattermost [mmauth://callback?...]",
		Short:         "Lightweight Mattermost desktop client",
		SilenceUsage:  true,
		SilenceErrors: true,
		// The OS hands a deep link (mmauth://...) over as a positional arg;
		// Wails reads it from os.Args itself, cobra just has to accept it.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if browser {
				return run.browser(cmd.Context(), o)
			}
			return run.desktop(cmd.Context())
		},
	}
	root.Flags().BoolVar(&browser, "browser", false, "Serve the UI over HTTP on localhost instead of opening a window")
	root.Flags().IntVar(&o.Port, "port", 5180, "HTTP port for --browser")
	root.Flags().BoolVar(&o.MMFake, "mm-fake", false, "Start an in-process fake Mattermost server (browser mode, development/e2e only)")
	root.Flags().BoolVar(&o.TestAPI, "test-api", false, "Expose /api/_test/* automation routes (development/e2e only)")
	return root
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd := newRootCmd(runners{browser: runBrowser, desktop: runDesktop})
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

Временная заглушка browser-раннера до Task 10 — `cmd/spk-mattermost/browser.go`:
```go
package main

import (
	"context"
	"errors"
)

func runBrowser(context.Context, browserOpts) error {
	return errors.New("browser mode is not implemented yet")
}
```

`cmd/spk-mattermost/run_desktop_nowails.go`:
```go
//go:build !wails

package main

import (
	"context"
	"errors"
)

func runDesktop(context.Context) error {
	return errors.New("desktop mode requires building with: go build -tags \"wails gtk3\" ./cmd/spk-mattermost")
}
```

`cmd/spk-mattermost/embed.go`:
```go
package main

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// frontendFS returns the built SPA rooted at dist/. In a plain `go build`
// (no frontend built) dist holds only .gitkeep and the UI is empty — the
// Makefile copies frontend/dist here before building.
func frontendFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
```

```bash
mkdir -p cmd/spk-mattermost/dist && touch cmd/spk-mattermost/dist/.gitkeep
```

- [ ] **Step 5: Makefile**

```make
.PHONY: build build-frontend build-go build-desktop release test test-go test-front test-e2e lint fmt tidy clean run run-browser cross-check install-dev-linux pss

BIN_DIR := build/bin
BIN     := $(BIN_DIR)/spk-mattermost
DIST    := cmd/spk-mattermost/dist
DESKTOP_TAGS := wails gtk3

define with_dist
	rm -rf $(DIST) && mkdir -p $(DIST) && cp -r frontend/dist/. $(DIST)/
	$(1)
	rm -rf $(DIST) && mkdir -p $(DIST) && touch $(DIST)/.gitkeep
endef

build: build-frontend build-go

build-frontend:
	cd frontend && pnpm install --frozen-lockfile --silent && pnpm build

build-go:
	mkdir -p $(BIN_DIR)
	$(call with_dist,CGO_ENABLED=1 go build -trimpath -ldflags="-w -s" -o $(BIN) ./cmd/spk-mattermost)

build-desktop: build-frontend
	mkdir -p $(BIN_DIR)
	$(call with_dist,CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS)" -trimpath -ldflags="-w -s" -o $(BIN_DIR)/spk-mattermost-desktop ./cmd/spk-mattermost)

release: build-frontend
	mkdir -p $(BIN_DIR)
	$(call with_dist,CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS) production" -trimpath -ldflags="-w -s" -o $(BIN_DIR)/spk-mattermost-release ./cmd/spk-mattermost)

# Windows desktop build needs no cgo (WebView2 via pure Go) — cross-compiles
# from Linux and catches Windows-only compile errors early. macOS needs cgo +
# SDK and is covered by CI in stage 4.
cross-check:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags wails -o /dev/null ./cmd/spk-mattermost
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet -tags wails ./...

test: test-go test-front test-e2e

test-go:
	go test -race -timeout 120s ./...

test-front:
	cd frontend && pnpm test

test-e2e: build
	cd tests/e2e && pnpm install --silent && pnpm exec playwright install chromium && pnpm exec playwright test

lint:
	go vet ./...
	golangci-lint run
	cd frontend && pnpm lint

fmt:
	go fmt ./...

tidy:
	go mod tidy

clean:
	rm -rf build frontend/dist

run: build-desktop
	$(BIN_DIR)/spk-mattermost-desktop

run-browser: build
	$(BIN) --browser --port=5180 --mm-fake --test-api

install-dev-linux: build-desktop
	bash scripts/install-dev-linux.sh $(abspath $(BIN_DIR)/spk-mattermost-desktop)

pss:
	bash scripts/pss.sh $(PID)
```

- [ ] **Step 6: AGENTS.md и симлинк**

`AGENTS.md`:
```markdown
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
```

```bash
ln -s AGENTS.md CLAUDE.md
```

- [ ] **Step 7: Проверить**

Run: `go test ./cmd/... && go build ./... && go vet ./...`
Expected: PASS, сборка без ошибок.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "chore: scaffold spk-mattermost module, CLI and Makefile"
```

---

### Task 2: Пути к данным

**Files:**
- Create: `internal/paths/paths.go`, `internal/paths/paths_test.go`

**Interfaces:**
- Produces: `type Paths struct{ DataDir, DBFile, MediaDir string }`; `func Resolve() (Paths, error)`; `func (p Paths) Ensure() error` (создаёт `DataDir` с правами 0700).

- [ ] **Step 1: Падающий тест**

```go
package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveHonoursEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SPK_MATTERMOST_HOME", dir)
	p, err := Resolve()
	require.NoError(t, err)
	assert.Equal(t, dir, p.DataDir)
	assert.Equal(t, filepath.Join(dir, "db.sqlite"), p.DBFile)
	assert.Equal(t, filepath.Join(dir, "media"), p.MediaDir)
}

func TestResolveDefaultsUnderHomeDotSpk(t *testing.T) {
	t.Setenv("SPK_MATTERMOST_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p, err := Resolve()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".spk", "spk-mattermost"), p.DataDir)
}

func TestEnsureCreatesOwnerOnlyDir(t *testing.T) {
	p := Paths{DataDir: filepath.Join(t.TempDir(), "a", "b")}
	require.NoError(t, p.Ensure())
	st, err := os.Stat(p.DataDir)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o700), st.Mode().Perm())
	}
}
```

- [ ] **Step 2: Run — FAIL** (`go test ./internal/paths/`, `undefined: Resolve`)

- [ ] **Step 3: Реализация**

```go
// Package paths resolves where spk-mattermost keeps its data.
package paths

import (
	"os"
	"path/filepath"
)

const appName = "spk-mattermost"

type Paths struct {
	DataDir  string
	DBFile   string
	MediaDir string
}

// Resolve returns ~/.spk/spk-mattermost (house convention shared with
// spk-mail/spk-cockpit), overridable via SPK_MATTERMOST_HOME for tests and
// e2e runs.
func Resolve() (Paths, error) {
	dir := os.Getenv("SPK_MATTERMOST_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		dir = filepath.Join(home, ".spk", appName)
	}
	return Paths{
		DataDir:  dir,
		DBFile:   filepath.Join(dir, "db.sqlite"),
		MediaDir: filepath.Join(dir, "media"),
	}, nil
}

// Ensure creates DataDir owner-only. The dir holds session tokens in the DB.
func (p Paths) Ensure() error {
	if err := os.MkdirAll(p.DataDir, 0o700); err != nil {
		return err
	}
	return os.Chmod(p.DataDir, 0o700)
}
```

- [ ] **Step 4: Run — PASS** (`go test ./internal/paths/`)

- [ ] **Step 5: Commit**

```bash
git add internal/paths && git commit -m "feat(paths): resolve data dir under ~/.spk/spk-mattermost"
```

---

### Task 3: SQLite-хранилище и таблица серверов

**Files:**
- Create: `internal/store/store.go`, `internal/store/migrations/0001_servers.sql`, `internal/store/servers.go`, `internal/store/store_test.go`, `internal/store/servers_test.go`

**Interfaces:**
- Produces:
  - `func Open(ctx context.Context, path string) (*Store, error)`; `func (s *Store) Close() error`; `func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error`
  - `type Server struct{ ID int64; URL, SiteURL, Name string; Sort int; Token, UserID, Username string; GitLab bool; CreatedAt int64 }` + `func (s Server) SignedIn() bool` + `func (s Server) LogValue() slog.Value`
  - `var ErrNotFound, ErrServerExists error`
  - `func (s *Store) AddServer(ctx, Server) (Server, error)`; `ListServers(ctx) ([]Server, error)`; `GetServer(ctx, id int64) (Server, error)`; `SetSession(ctx, id int64, token, userID, username string) error`; `ClearSession(ctx, id int64) error`; `DeleteServer(ctx, id int64) error`

- [ ] **Step 1: Зависимость**

```bash
go get modernc.org/sqlite@v1.53.0
```

- [ ] **Step 2: Падающие тесты**

`internal/store/store_test.go`:
```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestOpenCreatesOwnerOnlyFileAndMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	st, err := Open(context.Background(), path)
	require.NoError(t, err)
	defer st.Close()
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
	var v int
	require.NoError(t, st.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v))
	assert.Equal(t, 1, v)
}

func TestOpenTwiceIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	st, err := Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, st.Close())
	st, err = Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, st.Close())
}

func TestWithTxRollsBackOnError(t *testing.T) {
	st := openTest(t)
	boom := errors.New("boom")
	err := st.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO servers(url, site_url, name, created_at) VALUES('https://a', '', 'a', 1)`)
		require.NoError(t, err)
		return boom
	})
	assert.ErrorIs(t, err, boom)
	list, err := st.ListServers(context.Background())
	require.NoError(t, err)
	assert.Empty(t, list)
}
```

`internal/store/servers_test.go`:
```go
package store

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerCRUD(t *testing.T) {
	ctx := context.Background()
	st := openTest(t)

	a, err := st.AddServer(ctx, Server{URL: "https://mm.example.com", SiteURL: "https://mm.example.com", Name: "Example", GitLab: true})
	require.NoError(t, err)
	assert.NotZero(t, a.ID)
	assert.NotZero(t, a.CreatedAt)
	assert.False(t, a.SignedIn())

	_, err = st.AddServer(ctx, Server{URL: "https://mm.example.com", Name: "dup"})
	assert.ErrorIs(t, err, ErrServerExists)

	require.NoError(t, st.SetSession(ctx, a.ID, "tok-1", "u1", "alice"))
	got, err := st.GetServer(ctx, a.ID)
	require.NoError(t, err)
	assert.True(t, got.SignedIn())
	assert.Equal(t, "tok-1", got.Token)
	assert.Equal(t, "alice", got.Username)
	assert.True(t, got.GitLab)

	require.NoError(t, st.ClearSession(ctx, a.ID))
	got, err = st.GetServer(ctx, a.ID)
	require.NoError(t, err)
	assert.False(t, got.SignedIn())
	assert.Empty(t, got.UserID)

	b, err := st.AddServer(ctx, Server{URL: "https://b.example.com", Name: "B"})
	require.NoError(t, err)
	list, err := st.ListServers(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, a.ID, list[0].ID)
	assert.Equal(t, b.ID, list[1].ID)
	assert.Equal(t, 1, list[1].Sort, "new servers go to the end")

	require.NoError(t, st.DeleteServer(ctx, a.ID))
	_, err = st.GetServer(ctx, a.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, st.SetSession(ctx, a.ID, "t", "u", "n"), ErrNotFound)
	assert.ErrorIs(t, st.DeleteServer(ctx, a.ID), ErrNotFound)
}

func TestServerLogValueHidesToken(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("server", "srv", Server{ID: 7, URL: "https://mm", Token: "super-secret-token"})
	assert.NotContains(t, buf.String(), "super-secret-token")
	assert.Contains(t, buf.String(), `"signed_in":true`)
	assert.Contains(t, buf.String(), "https://mm")
}
```

- [ ] **Step 3: Run — FAIL** (`go test ./internal/store/`)

- [ ] **Step 4: Реализация**

`internal/store/migrations/0001_servers.sql`:
```sql
-- servers holds the user's Mattermost servers and their session tokens.
-- Kept apart from cache tables (added in stage 2) so "reset cache" and a
-- rebuilt cache snapshot never sign the user out.
CREATE TABLE servers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    url        TEXT    NOT NULL UNIQUE,
    site_url   TEXT    NOT NULL DEFAULT '',
    name       TEXT    NOT NULL,
    sort       INTEGER NOT NULL DEFAULT 0,
    token      TEXT    NOT NULL DEFAULT '',
    user_id    TEXT    NOT NULL DEFAULT '',
    username   TEXT    NOT NULL DEFAULT '',
    gitlab     INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);
```

`internal/store/store.go`:
```go
// Package store is spk-mattermost's SQLite persistence.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps a single-connection SQLite handle. MaxOpenConns=1 serialises
// every statement; mixing s.db and a *sql.Tx inside one logical operation
// deadlocks, so multi-statement writes go through WithTx only.
type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	// The DB holds session tokens: owner-only regardless of umask. WAL/SHM
	// sidecars carry the same data; they may not exist yet.
	_ = os.Chmod(path, 0o600)
	for _, side := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(side); err == nil {
			_ = os.Chmod(side, 0o600)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// WithTx runs fn in one transaction: rollback on error or panic, commit otherwise.
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// migrate applies migrations/NNNN_*.sql in order, each in its own tx,
// recording the version in schema_migrations.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %q", name)
		}
		if v <= current {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := s.WithTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, v)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
```

`internal/store/servers.go`:
```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrServerExists = errors.New("server already exists")
)

// Server is one configured Mattermost server. Token is the session token —
// it must never leave the Go side; LogValue masks it for slog.
type Server struct {
	ID        int64
	URL       string // normalized URL the user entered
	SiteURL   string // normalized SiteURL reported by the server ('' if unset)
	Name      string
	Sort      int
	Token     string
	UserID    string
	Username  string
	GitLab    bool
	CreatedAt int64
}

func (s Server) SignedIn() bool { return s.Token != "" }

func (s Server) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("id", s.ID),
		slog.String("url", s.URL),
		slog.String("name", s.Name),
		slog.Bool("signed_in", s.SignedIn()),
		slog.String("username", s.Username),
	)
}

const serverCols = `id, url, site_url, name, sort, token, user_id, username, gitlab, created_at`

func scanServer(row interface{ Scan(...any) error }) (Server, error) {
	var s Server
	var gitlab int
	err := row.Scan(&s.ID, &s.URL, &s.SiteURL, &s.Name, &s.Sort, &s.Token, &s.UserID, &s.Username, &gitlab, &s.CreatedAt)
	s.GitLab = gitlab != 0
	return s, err
}

func (s *Store) AddServer(ctx context.Context, srv Server) (Server, error) {
	srv.CreatedAt = time.Now().UnixMilli()
	gitlab := 0
	if srv.GitLab {
		gitlab = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO servers(url, site_url, name, sort, gitlab, created_at)
		 VALUES (?, ?, ?, (SELECT COALESCE(MAX(sort)+1, 0) FROM servers), ?, ?)`,
		srv.URL, srv.SiteURL, srv.Name, gitlab, srv.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return Server{}, ErrServerExists
		}
		return Server{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Server{}, err
	}
	return s.GetServer(ctx, id)
}

func (s *Store) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverCols+` FROM servers ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

func (s *Store) GetServer(ctx context.Context, id int64) (Server, error) {
	srv, err := scanServer(s.db.QueryRowContext(ctx, `SELECT `+serverCols+` FROM servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return srv, err
}

func (s *Store) SetSession(ctx context.Context, id int64, token, userID, username string) error {
	return s.execOne(ctx, `UPDATE servers SET token = ?, user_id = ?, username = ? WHERE id = ?`, token, userID, username, id)
}

func (s *Store) ClearSession(ctx context.Context, id int64) error {
	return s.execOne(ctx, `UPDATE servers SET token = '', user_id = '', username = '' WHERE id = ?`, id)
}

func (s *Store) DeleteServer(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM servers WHERE id = ?`, id)
}

func (s *Store) execOne(ctx context.Context, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 5: Run — PASS** (`go test -race ./internal/store/`)

- [ ] **Step 6: Commit**

```bash
git add internal/store go.mod go.sum && git commit -m "feat(store): SQLite store with migrations and servers table"
```

---

### Task 4: Шина событий

**Files:**
- Create: `internal/events/events.go`, `internal/events/events_test.go`

**Interfaces:**
- Produces: `type Event struct{ Type string \`json:"type"\`; Payload map[string]any \`json:"payload,omitempty"\` }`; `func NewEmitter() *Emitter`; `func (e *Emitter) Subscribe() (<-chan Event, func())`; `func (e *Emitter) Emit(ev Event)` (неблокирующий, nil-safe).

- [ ] **Step 1: Падающий тест**

```go
package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmitDeliversToAllSubscribers(t *testing.T) {
	e := NewEmitter()
	a, unsubA := e.Subscribe()
	b, unsubB := e.Subscribe()
	defer unsubA()
	defer unsubB()
	e.Emit(Event{Type: "x", Payload: map[string]any{"n": 1}})
	assert.Equal(t, "x", (<-a).Type)
	assert.Equal(t, "x", (<-b).Type)
}

func TestEmitNeverBlocksOnFullSubscriber(t *testing.T) {
	e := NewEmitter()
	_, unsub := e.Subscribe() // never drained
	defer unsub()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10*subBuffer; i++ {
			e.Emit(Event{Type: "flood"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked on a full subscriber")
	}
}

func TestUnsubscribeClosesChannelAndIsIdempotent(t *testing.T) {
	e := NewEmitter()
	ch, unsub := e.Subscribe()
	unsub()
	unsub()
	_, ok := <-ch
	require.False(t, ok)
	e.Emit(Event{Type: "after"}) // must not panic
}

func TestNilEmitterIsNoop(t *testing.T) {
	var e *Emitter
	e.Emit(Event{Type: "x"})
}
```

- [ ] **Step 2: Run — FAIL** (`go test ./internal/events/`)

- [ ] **Step 3: Реализация** (перенос из `spk-mail/internal/events/events.go`, буфер — константа)

```go
// Package events is the in-process fan-out bus from the Go core to the UI
// transports (Wails events, SSE). Emit never blocks.
package events

import (
	"log/slog"
	"sync"
)

const subBuffer = 256

type Event struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
}

type subscription struct {
	ch     chan Event
	mu     sync.Mutex
	closed bool
}

// send drops the event when the buffer is full: a wedged subscriber (a
// backgrounded SSE tab) must never stall the sync goroutine that emits.
func (s *subscription) send(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- ev:
	default:
		slog.Warn("event dropped: subscriber buffer full", "type", ev.Type)
	}
}

func (s *subscription) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

type Emitter struct {
	mu   sync.Mutex
	subs []*subscription
}

func NewEmitter() *Emitter { return &Emitter{} }

func (e *Emitter) Subscribe() (<-chan Event, func()) {
	sub := &subscription{ch: make(chan Event, subBuffer)}
	e.mu.Lock()
	e.subs = append(e.subs, sub)
	e.mu.Unlock()
	var once sync.Once
	return sub.ch, func() {
		once.Do(func() {
			e.mu.Lock()
			for i, s := range e.subs {
				if s == sub {
					e.subs = append(e.subs[:i], e.subs[i+1:]...)
					break
				}
			}
			e.mu.Unlock()
			sub.close()
		})
	}
}

func (e *Emitter) Emit(ev Event) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sub := range e.subs {
		sub.send(ev)
	}
}
```

- [ ] **Step 4: Run — PASS** (`go test -race ./internal/events/`)

- [ ] **Step 5: Commit**

```bash
git add internal/events && git commit -m "feat(events): non-blocking event emitter"
```

---

### Task 5: REST-клиент Mattermost (ядро + эндпоинты входа)

**Files:**
- Create: `internal/mm/rest/errors.go`, `internal/mm/rest/url.go`, `internal/mm/rest/client.go`, `internal/mm/rest/endpoints.go`, `internal/mm/rest/url_test.go`, `internal/mm/rest/client_test.go`, `internal/mm/rest/endpoints_test.go`

**Interfaces:**
- Produces:
  - `type ErrKind int` с `KindNetwork`, `KindAuth`, `KindAPI`; `type Error struct{ Kind ErrKind; Status int; ID, Message string; Err error }`; `func IsAuth(err error) bool`; `func IsNetwork(err error) bool`
  - `func NormalizeURL(raw string) (string, error)`
  - `func New(baseURL, token string, hc *http.Client) *Client`; `func (c *Client) WithToken(token string) *Client`
  - `func (c *Client) Ping(ctx) error`; `ClientConfig(ctx) (ClientConfig, error)`; `Me(ctx) (User, error)`; `Login(ctx, loginID, password string) (token string, u User, err error)`; `Logout(ctx) error`
  - `type ClientConfig struct{ SiteName, SiteURL, Version, EnableSignUpWithGitLab string }` + `func (c ClientConfig) GitLabEnabled() bool`
  - `type User struct{ ID, Username, FirstName, LastName, Nickname string }` (json: `id`, `username`, `first_name`, `last_name`, `nickname`)

- [ ] **Step 1: Падающие тесты**

`internal/mm/rest/url_test.go`:
```go
package rest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeURL(t *testing.T) {
	ok := map[string]string{
		"https://mm.example.com":        "https://mm.example.com",
		"https://mm.example.com/":       "https://mm.example.com",
		"  HTTPS://MM.Example.COM//  ":  "https://mm.example.com",
		"mm.example.com":                "https://mm.example.com",
		"http://127.0.0.1:8065":         "http://127.0.0.1:8065",
		"https://example.com/mattermost/": "https://example.com/mattermost",
		"https://mm.example.com/?x=1#f": "https://mm.example.com",
	}
	for in, want := range ok {
		got, err := NormalizeURL(in)
		if assert.NoError(t, err, in) {
			assert.Equal(t, want, got, in)
		}
	}
	for _, bad := range []string{"", "   ", "ftp://mm.example.com", "https://", "://nohost"} {
		_, err := NormalizeURL(bad)
		assert.Error(t, err, bad)
	}
}
```

`internal/mm/rest/client_test.go`:
```go
package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "tok", srv.Client())
	var slept []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	return c, &slept
}

func TestRequestCarriesBearerAndXRequestedWith(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		assert.Equal(t, "XMLHttpRequest", r.Header.Get("X-Requested-With"))
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	})
	require.NoError(t, c.Ping(context.Background()))
}

func TestRetries429HonouringRetryAfter(t *testing.T) {
	var n atomic.Int32
	c, slept := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	})
	require.NoError(t, c.Ping(context.Background()))
	assert.Equal(t, int32(2), n.Load())
	assert.Equal(t, []time.Duration{3 * time.Second}, *slept)
}

func TestGives429UpAfterMaxAttempts(t *testing.T) {
	var n atomic.Int32
	c, slept := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	err := c.Ping(context.Background())
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, http.StatusTooManyRequests, e.Status)
	assert.Equal(t, int32(maxAttempts), n.Load())
	assert.Equal(t, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}, *slept)
}

func TestClassifies401AsAuthWithServerErrorID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"id":"api.context.session_expired.app_error","message":"Invalid or expired session","status_code":401}`))
	})
	_, err := c.Me(context.Background())
	assert.True(t, IsAuth(err))
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "api.context.session_expired.app_error", e.ID)
}

func TestClassifies502AsNetwork(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	err := c.Ping(context.Background())
	assert.True(t, IsNetwork(err))
}

func TestTransportErrorIsNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := New(url, "", &http.Client{Timeout: time.Second})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	assert.True(t, IsNetwork(c.Ping(context.Background())))
}

func TestPostIsNotRetriedOnTransportError(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close() // transport error for the client
	})
	_, _, err := c.Login(context.Background(), "a", "b")
	assert.True(t, IsNetwork(err))
	assert.Equal(t, int32(1), n.Load(), "non-idempotent POST must not be replayed")
}

func TestContextCancelStopsRetries(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }
	err := c.Ping(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}
```

`internal/mm/rest/endpoints_test.go`:
```go
package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientConfig(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/config/client", r.URL.Path)
		assert.Equal(t, "old", r.URL.Query().Get("format"))
		_, _ = w.Write([]byte(`{"SiteName":"MM","SiteURL":"https://mm.example.com","Version":"10.11.22","EnableSignUpWithGitLab":"true"}`))
	})
	cfg, err := c.ClientConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "MM", cfg.SiteName)
	assert.True(t, cfg.GitLabEnabled())
}

func TestPingRejectsNonOKStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"status":"FAIL"}`)) })
	assert.Error(t, c.Ping(context.Background()))
}

func TestLoginReturnsTokenFromHeader(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/users/login", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"), "login must not send a stale token")
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, map[string]string{"login_id": "alice", "password": "pw"}, body)
		w.Header().Set("Token", "new-token")
		_, _ = w.Write([]byte(`{"id":"u1","username":"alice"}`))
	})
	tok, u, err := c.Login(context.Background(), "alice", "pw")
	require.NoError(t, err)
	assert.Equal(t, "new-token", tok)
	assert.Equal(t, "alice", u.Username)
}

func TestLoginWithoutTokenHeaderFails(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"id":"u1"}`)) })
	_, _, err := c.Login(context.Background(), "alice", "pw")
	assert.Error(t, err)
}

func TestLogoutTreats401AsSuccess(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	assert.NoError(t, c.Logout(context.Background()))
}

func TestMe(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/users/me", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":"u1","username":"alice","first_name":"Alice"}`))
	})
	u, err := c.Me(context.Background())
	require.NoError(t, err)
	assert.Equal(t, User{ID: "u1", Username: "alice", FirstName: "Alice"}, u)
}
```

- [ ] **Step 2: Run — FAIL** (`go test ./internal/mm/rest/`)

- [ ] **Step 3: Реализация**

`internal/mm/rest/errors.go`:
```go
package rest

import (
	"errors"
	"fmt"
)

type ErrKind int

const (
	KindNetwork ErrKind = iota + 1 // transport failure, timeout, 5xx
	KindAuth                        // 401/403: token missing, expired or revoked
	KindAPI                         // any other 4xx
)

// Error is every failure the client returns. ID/Message come from the
// Mattermost AppError body when present.
type Error struct {
	Kind    ErrKind
	Status  int
	ID      string
	Message string
	Err     error
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil:
		return fmt.Sprintf("mattermost: %v", e.Err)
	case e.ID != "":
		return fmt.Sprintf("mattermost: HTTP %d %s: %s", e.Status, e.ID, e.Message)
	default:
		return fmt.Sprintf("mattermost: HTTP %d", e.Status)
	}
}

func (e *Error) Unwrap() error { return e.Err }

func kindOf(err error) ErrKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

func IsAuth(err error) bool    { return kindOf(err) == KindAuth }
func IsNetwork(err error) bool { return kindOf(err) == KindNetwork }
```

`internal/mm/rest/url.go`:
```go
package rest

import (
	"errors"
	"net/url"
	"strings"
)

// NormalizeURL canonicalises a server URL so the same server always compares
// equal: https:// is assumed when no scheme is given; scheme and host are
// lower-cased; trailing slashes, query and fragment are dropped. A subpath
// install (https://host/mattermost) keeps its path.
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("empty URL")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("URL must be http or https")
	}
	if u.Host == "" {
		return "", errors.New("URL has no host")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String(), nil
}
```

`internal/mm/rest/client.go`:
```go
// Package rest is a typed client for the Mattermost REST API v4.
package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	maxAttempts   = 5
	baseBackoff   = 100 * time.Millisecond
	maxBackoff    = 5 * time.Second
	maxRetryAfter = 60 * time.Second
	errBodyLimit  = 64 << 10
)

type Client struct {
	base  string
	token string
	hc    *http.Client
	sleep func(context.Context, time.Duration) error
}

// New builds a client for baseURL (already normalized). A nil hc gets a 30s
// timeout — http.DefaultClient has none, and a hung server once stalled a
// whole sync loop in spk-cockpit.
func New(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{base: baseURL, token: token, hc: hc, sleep: sleepCtx}
}

// WithToken returns a copy that authenticates with token ("" = anonymous).
func (c *Client) WithToken(token string) *Client {
	cp := *c
	cp.token = token
	return &cp
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func backoff(attempt int) time.Duration {
	d := baseBackoff << (attempt - 1)
	if d > maxBackoff || d <= 0 {
		return maxBackoff
	}
	return d
}

func retryAfter(h http.Header, fallback time.Duration) time.Duration {
	secs, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil || secs <= 0 {
		return fallback
	}
	d := time.Duration(secs) * time.Second
	if d > maxRetryAfter {
		return maxRetryAfter
	}
	return d
}

// do sends one API call. 429 is retried for every method (the server
// rejected it before doing anything); transport errors only for GET, since
// replaying a POST could post a message twice. out may be nil.
func (c *Client) do(ctx context.Context, method, path string, in, out any) (http.Header, error) {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return nil, err
		}
	}
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, &Error{Kind: KindNetwork, Err: ctx.Err()}
			}
			if method == http.MethodGet && attempt < maxAttempts {
				if serr := c.sleep(ctx, backoff(attempt)); serr != nil {
					return nil, &Error{Kind: KindNetwork, Err: serr}
				}
				continue
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
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return resp.Header, classify(resp)
		}
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
				return resp.Header, &Error{Kind: KindAPI, Status: resp.StatusCode, Err: err}
			}
		}
		return resp.Header, nil
	}
}

func classify(resp *http.Response) error {
	e := &Error{Status: resp.StatusCode}
	var body struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
	if json.Unmarshal(raw, &body) == nil {
		e.ID, e.Message = body.ID, body.Message
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		e.Kind = KindAuth
	case resp.StatusCode >= 500:
		e.Kind = KindNetwork
	default:
		e.Kind = KindAPI
	}
	return e
}
```

Примечание: `TestGives429UpAfterMaxAttempts` — последний ответ 429 не ретраится (attempt == maxAttempts) и классифицируется как `KindAPI` со статусом 429; ожидания теста это учитывают (`Status` = 429).

`internal/mm/rest/endpoints.go`:
```go
package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type ClientConfig struct {
	SiteName               string `json:"SiteName"`
	SiteURL                string `json:"SiteURL"`
	Version                string `json:"Version"`
	EnableSignUpWithGitLab string `json:"EnableSignUpWithGitLab"`
}

func (c ClientConfig) GitLabEnabled() bool { return c.EnableSignUpWithGitLab == "true" }

type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
}

// Ping checks the URL is a live Mattermost server.
func (c *Client) Ping(ctx context.Context) error {
	var out struct {
		Status string `json:"status"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/api/v4/system/ping", nil, &out); err != nil {
		return err
	}
	if out.Status != "OK" {
		return &Error{Kind: KindAPI, Status: http.StatusOK, Err: fmt.Errorf("ping status %q", out.Status)}
	}
	return nil
}

func (c *Client) ClientConfig(ctx context.Context) (ClientConfig, error) {
	var cfg ClientConfig
	_, err := c.do(ctx, http.MethodGet, "/api/v4/config/client?format=old", nil, &cfg)
	return cfg, err
}

func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	_, err := c.do(ctx, http.MethodGet, "/api/v4/users/me", nil, &u)
	return u, err
}

// Login signs in with login ID + password; the session token arrives in the
// "Token" response header.
func (c *Client) Login(ctx context.Context, loginID, password string) (string, User, error) {
	var u User
	h, err := c.WithToken("").do(ctx, http.MethodPost, "/api/v4/users/login",
		map[string]string{"login_id": loginID, "password": password}, &u)
	if err != nil {
		return "", User{}, err
	}
	tok := h.Get("Token")
	if tok == "" {
		return "", User{}, &Error{Kind: KindAPI, Status: http.StatusOK, Err: errors.New("login response has no Token header")}
	}
	return tok, u, nil
}

// Logout revokes the session server-side. 401/403 mean it is already dead —
// that is the outcome we wanted, so it is success.
func (c *Client) Logout(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v4/users/logout", nil, nil)
	if IsAuth(err) {
		return nil
	}
	return err
}
```

- [ ] **Step 4: Run — PASS** (`go test -race ./internal/mm/rest/`)

- [ ] **Step 5: Commit**

```bash
git add internal/mm && git commit -m "feat(rest): Mattermost REST client core with retries and login endpoints"
```

---

### Task 6: Фейковый сервер Mattermost (+ фейковый GitLab)

**Files:**
- Create: `internal/mmfake/server.go`, `internal/mmfake/server_test.go`

**Interfaces:**
- Consumes: ничего из проекта (намеренно — фейк не должен зависеть от клиента).
- Produces: `type Options struct{ SiteName string; GitLab bool; Users []User }`; `type User struct{ ID, Username, Password string }`; `func Start(o Options) *Server`; `func (s *Server) URL() string`; `func (s *Server) Close()`; `func (s *Server) ActiveSessions() int`; `func (s *Server) RevokeAll()`.
  Поведение HTTP (контракт для e2e): `GET /oauth/gitlab/mobile_login?redirect_to=mmauth://…` → 302 на `/mmfake/gitlab/authorize?state=…`; страница содержит ссылку `#authorize`; `GET /mmfake/gitlab/complete?state=…` → страница со ссылкой `#mmauth-link` на `mmauth://callback?MMAUTHTOKEN=…&MMCSRF=…&srv=<URL>`.

- [ ] **Step 1: Падающий тест**

```go
package mmfake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestPasswordLoginMeLogout(t *testing.T) {
	s := Start(Options{})
	defer s.Close()

	resp, err := http.Post(s.URL()+"/api/v4/users/login", "application/json", strings.NewReader(`{"login_id":"alice","password":"secret"}`))
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	tok := resp.Header.Get("Token")
	require.NotEmpty(t, tok)
	assert.Equal(t, 1, s.ActiveSessions())

	req, _ := http.NewRequest(http.MethodGet, s.URL()+"/api/v4/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var me map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&me))
	assert.Equal(t, "alice", me["username"])

	req, _ = http.NewRequest(http.MethodPost, s.URL()+"/api/v4/users/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	_, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, 0, s.ActiveSessions())
}

func TestWrongPasswordIs401(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	resp, err := http.Post(s.URL()+"/api/v4/users/login", "application/json", strings.NewReader(`{"login_id":"alice","password":"nope"}`))
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestGitLabMobileFlowEndsWithMMAuthLink(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	c := noRedirect()

	resp, err := c.Get(s.URL() + "/oauth/gitlab/mobile_login?redirect_to=" + url.QueryEscape("mmauth://callback"))
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	authorize := resp.Header.Get("Location")
	require.Contains(t, authorize, "/mmfake/gitlab/authorize?state=")

	resp, err = c.Get(s.URL() + authorize)
	require.NoError(t, err)
	page, _ := io.ReadAll(resp.Body)
	m := regexp.MustCompile(`id="authorize" href="([^"]+)"`).FindSubmatch(page)
	require.NotNil(t, m)

	resp, err = c.Get(s.URL() + string(m[1]))
	require.NoError(t, err)
	page, _ = io.ReadAll(resp.Body)
	link := regexp.MustCompile(`id="mmauth-link" href="([^"]+)"`).FindSubmatch(page)
	require.NotNil(t, link)
	u, err := url.Parse(strings.ReplaceAll(string(link[1]), "&amp;", "&"))
	require.NoError(t, err)
	assert.Equal(t, "mmauth", u.Scheme)
	assert.NotEmpty(t, u.Query().Get("MMAUTHTOKEN"))
	assert.Equal(t, s.URL(), u.Query().Get("srv"))
	assert.Equal(t, 1, s.ActiveSessions())
}

func TestMobileLoginRejectsNonMMAuthRedirect(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	resp, err := noRedirect().Get(s.URL() + "/oauth/gitlab/mobile_login?redirect_to=" + url.QueryEscape("http://127.0.0.1:1/cb"))
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, string(body), "Invalid custom url scheme")
}

func TestRevokeAllKillsSessions(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	resp, _ := http.Post(s.URL()+"/api/v4/users/login", "application/json", strings.NewReader(`{"login_id":"alice","password":"secret"}`))
	tok := resp.Header.Get("Token")
	s.RevokeAll()
	req, _ := http.NewRequest(http.MethodGet, s.URL()+"/api/v4/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}
```

- [ ] **Step 2: Run — FAIL** (`go test ./internal/mmfake/`)

- [ ] **Step 3: Реализация**

```go
// Package mmfake is an in-process fake of the Mattermost server subset
// spk-mattermost talks to, plus a fake GitLab consent page for the mobile
// SSO flow. Used by Go tests and by browser mode (--mm-fake) for e2e.
package mmfake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
)

type User struct {
	ID       string
	Username string
	Password string
}

type Options struct {
	SiteName string // default "Fake MM"
	GitLab   bool   // advertise GitLab SSO; Start forces true when Users is nil and SiteName is ""
	Users    []User // default: alice/secret
}

type Server struct {
	ts       *httptest.Server
	opts     Options
	mu       sync.Mutex
	sessions map[string]string // token -> user id
	pending  map[string]string // oauth state -> redirect_to
}

func Start(o Options) *Server {
	if o.SiteName == "" && o.Users == nil {
		o.GitLab = true
	}
	if o.SiteName == "" {
		o.SiteName = "Fake MM"
	}
	if o.Users == nil {
		o.Users = []User{{ID: "u-alice", Username: "alice", Password: "secret"}}
	}
	s := &Server{opts: o, sessions: map[string]string{}, pending: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/system/ping", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "OK"})
	})
	mux.HandleFunc("GET /api/v4/config/client", s.clientConfig)
	mux.HandleFunc("POST /api/v4/users/login", s.login)
	mux.HandleFunc("GET /api/v4/users/me", s.me)
	mux.HandleFunc("POST /api/v4/users/logout", s.logout)
	mux.HandleFunc("GET /oauth/gitlab/mobile_login", s.mobileLogin)
	mux.HandleFunc("GET /mmfake/gitlab/authorize", s.gitlabAuthorize)
	mux.HandleFunc("GET /mmfake/gitlab/complete", s.gitlabComplete)
	s.ts = httptest.NewServer(mux)
	return s
}

func (s *Server) URL() string { return s.ts.URL }
func (s *Server) Close()      { s.ts.Close() }

func (s *Server) ActiveSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *Server) RevokeAll() {
	s.mu.Lock()
	s.sessions = map[string]string{}
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func appError(w http.ResponseWriter, status int, id, msg string) {
	writeJSON(w, status, map[string]any{"id": id, "message": msg, "status_code": status})
}

func newID() string {
	b := make([]byte, 13)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) userByID(id string) (User, bool) {
	for _, u := range s.opts.Users {
		if u.ID == id {
			return u, true
		}
	}
	return User{}, false
}

func userJSON(u User) map[string]string { return map[string]string{"id": u.ID, "username": u.Username} }

func (s *Server) newSession(userID string) string {
	tok := newID()
	s.mu.Lock()
	s.sessions[tok] = userID
	s.mu.Unlock()
	return tok
}

func (s *Server) authed(r *http.Request) (User, string, bool) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	uid, ok := s.sessions[tok]
	s.mu.Unlock()
	if !ok {
		return User{}, "", false
	}
	u, ok := s.userByID(uid)
	return u, tok, ok
}

func (s *Server) clientConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{
		"SiteName":               s.opts.SiteName,
		"SiteURL":                s.ts.URL,
		"Version":                "10.11.0-fake",
		"EnableSignUpWithGitLab": fmt.Sprint(s.opts.GitLab),
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LoginID  string `json:"login_id"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	for _, u := range s.opts.Users {
		if u.Username == in.LoginID && u.Password == in.Password {
			w.Header().Set("Token", s.newSession(u.ID))
			writeJSON(w, 200, userJSON(u))
			return
		}
	}
	appError(w, 401, "api.user.login.invalid_credentials_email_username", "Enter a valid email or username and/or password.")
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, _, ok := s.authed(r)
	if !ok {
		appError(w, 401, "api.context.session_expired.app_error", "Invalid or expired session, please login again.")
		return
	}
	writeJSON(w, 200, userJSON(u))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	_, tok, ok := s.authed(r)
	if !ok {
		appError(w, 401, "api.context.session_expired.app_error", "Invalid or expired session, please login again.")
		return
	}
	s.mu.Lock()
	delete(s.sessions, tok)
	s.mu.Unlock()
	writeJSON(w, 200, map[string]string{"status": "OK"})
}

// mobileLogin mirrors server/channels/web/oauth.go: only redirect_to values
// prefixed by an allowed custom scheme (default mmauth://) are accepted.
func (s *Server) mobileLogin(w http.ResponseWriter, r *http.Request) {
	redirect := r.URL.Query().Get("redirect_to")
	if !strings.HasPrefix(strings.ToLower(redirect), "mmauth://") {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("<html><body>Invalid custom url scheme has been provided</body></html>"))
		return
	}
	if !s.opts.GitLab {
		appError(w, 501, "api.user.authorize_oauth_user.unsupported.app_error", "GitLab SSO is disabled")
		return
	}
	state := newID()
	s.mu.Lock()
	s.pending[state] = redirect
	s.mu.Unlock()
	http.Redirect(w, r, "/mmfake/gitlab/authorize?state="+state, http.StatusFound)
}

func (s *Server) gitlabAuthorize(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<html><body><h1>Fake GitLab</h1><a id="authorize" href="/mmfake/gitlab/complete?state=%s">Authorize as %s</a></body></html>`,
		url.QueryEscape(state), html.EscapeString(s.opts.Users[0].Username))
}

// gitlabComplete mirrors utils.RenderMobileAuthComplete: a page that
// redirects to redirect_to with MMAUTHTOKEN, MMCSRF and srv appended.
func (s *Server) gitlabComplete(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	s.mu.Lock()
	redirect, ok := s.pending[state]
	delete(s.pending, state)
	s.mu.Unlock()
	if !ok {
		http.Error(w, "unknown state", http.StatusBadRequest)
		return
	}
	tok := s.newSession(s.opts.Users[0].ID)
	q := url.Values{"MMAUTHTOKEN": {tok}, "MMCSRF": {newID()}, "srv": {s.ts.URL}}
	sep := "?"
	if strings.Contains(redirect, "?") {
		sep = "&"
	}
	link := html.EscapeString(redirect + sep + q.Encode())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<html><head><meta http-equiv="refresh" content="2; url=%s"></head><body><h2>Authentication complete</h2><p><a id="mmauth-link" href="%s">Click here</a></p></body></html>`, link, link)
}
```

- [ ] **Step 4: Run — PASS** (`go test -race ./internal/mmfake/`)

- [ ] **Step 5: Commit**

```bash
git add internal/mmfake && git commit -m "feat(mmfake): fake Mattermost server with mobile GitLab SSO flow"
```

---

### Task 7: SSO через `mmauth://`

**Files:**
- Create: `internal/auth/sso.go`, `internal/auth/sso_test.go`

**Interfaces:**
- Consumes: `rest.NormalizeURL` (Task 5).
- Produces:
  - `const CallbackURL = "mmauth://callback"`
  - `var ErrNoPendingLogin, ErrServerMismatch, ErrMalformedCallback, ErrAlreadyCompleted error`
  - `type Result struct{ ServerID int64; Token string }`
  - `func GitLabLoginURL(serverURL string) string`
  - `func IsCallbackURL(raw string) bool`; `func FindCallbackArg(args []string) (string, bool)`
  - `func NewSSO() *SSO`; `func (s *SSO) Begin(serverID int64, urls ...string)`; `func (s *SSO) Cancel(serverID int64)`; `func (s *SSO) Complete(raw string) (Result, error)`

- [ ] **Step 1: Падающий тест**

```go
package auth

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cb(token, srv string) string {
	q := url.Values{"MMAUTHTOKEN": {token}, "MMCSRF": {"csrf"}}
	if srv != "" {
		q.Set("srv", srv)
	}
	return CallbackURL + "?" + q.Encode()
}

func TestGitLabLoginURL(t *testing.T) {
	assert.Equal(t,
		"https://mm.example.com/oauth/gitlab/mobile_login?redirect_to=mmauth%3A%2F%2Fcallback",
		GitLabLoginURL("https://mm.example.com"))
}

func TestCompleteMatchesPendingBySrv(t *testing.T) {
	s := NewSSO()
	s.Begin(1, "https://a.example.com")
	s.Begin(2, "https://b.example.com")
	res, err := s.Complete(cb("tok", "https://b.example.com/"))
	require.NoError(t, err)
	assert.Equal(t, Result{ServerID: 2, Token: "tok"}, res)
}

func TestCompleteMatchesSiteURLAlias(t *testing.T) {
	s := NewSSO()
	// user typed "mm.example.com"; server reports a different SiteURL
	s.Begin(5, "https://mm.example.com", "https://chat.example.com")
	res, err := s.Complete(cb("tok", "HTTPS://CHAT.example.com"))
	require.NoError(t, err)
	assert.Equal(t, int64(5), res.ServerID)
}

func TestCompleteWithoutSrvNeedsExactlyOnePending(t *testing.T) {
	s := NewSSO()
	_, err := s.Complete(cb("t", ""))
	assert.ErrorIs(t, err, ErrNoPendingLogin)

	s.Begin(1, "https://a")
	s.Begin(2, "https://b")
	_, err = s.Complete(cb("t", ""))
	assert.ErrorIs(t, err, ErrServerMismatch)

	s.Cancel(2)
	res, err := s.Complete(cb("t", ""))
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.ServerID)
}

func TestCompleteRejectsUnknownServer(t *testing.T) {
	s := NewSSO()
	s.Begin(1, "https://a.example.com")
	_, err := s.Complete(cb("tok", "https://evil.example.com"))
	assert.ErrorIs(t, err, ErrServerMismatch)
	// the legit pending login survives the forged callback
	_, err = s.Complete(cb("tok2", "https://a.example.com"))
	assert.NoError(t, err)
}

func TestCompleteRejectsMalformed(t *testing.T) {
	s := NewSSO()
	s.Begin(1, "https://a")
	for _, raw := range []string{"https://a/?MMAUTHTOKEN=x", "mmauth://callback", "mmauth://callback?MMAUTHTOKEN=", "%%%"} {
		_, err := s.Complete(raw)
		assert.ErrorIs(t, err, ErrMalformedCallback, raw)
	}
}

func TestCompleteTwiceIsAlreadyCompleted(t *testing.T) {
	s := NewSSO()
	s.Begin(1, "https://a")
	raw := cb("tok", "https://a")
	_, err := s.Complete(raw)
	require.NoError(t, err)
	_, err = s.Complete(raw)
	assert.ErrorIs(t, err, ErrAlreadyCompleted)
}

func TestPendingExpires(t *testing.T) {
	s := NewSSO()
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	s.Begin(1, "https://a")
	now = now.Add(pendingTTL + time.Second)
	_, err := s.Complete(cb("tok", "https://a"))
	assert.ErrorIs(t, err, ErrNoPendingLogin)
}

func TestFindCallbackArg(t *testing.T) {
	u, ok := FindCallbackArg([]string{"/usr/bin/spk-mattermost", "MMAUTH://callback?MMAUTHTOKEN=x"})
	assert.True(t, ok)
	assert.Equal(t, "MMAUTH://callback?MMAUTHTOKEN=x", u)
	_, ok = FindCallbackArg([]string{"/usr/bin/spk-mattermost", "--browser"})
	assert.False(t, ok)
}
```

- [ ] **Step 2: Run — FAIL** (`go test ./internal/auth/`)

- [ ] **Step 3: Реализация**

```go
// Package auth implements the GitLab SSO login through Mattermost's native
// app flow: /oauth/gitlab/mobile_login?redirect_to=mmauth://callback ends
// with the OS handing us mmauth://callback?MMAUTHTOKEN=…&MMCSRF=…&srv=….
// mmauth:// is the server's default allowed scheme; loopback redirects are
// rejected by the server (verified 2026-09-24), so there is no alternative.
package auth

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/rest"
)

const (
	CallbackURL = "mmauth://callback"
	scheme      = "mmauth"
	pendingTTL  = 10 * time.Minute
	recentTTL   = 2 * time.Minute
)

var (
	ErrNoPendingLogin    = errors.New("no login in progress")
	ErrServerMismatch    = errors.New("login callback does not match a server being signed in")
	ErrMalformedCallback = errors.New("malformed login callback")
	// ErrAlreadyCompleted: the same callback delivered twice (Wails launch
	// event + second-instance args, or a double click). Callers ignore it.
	ErrAlreadyCompleted = errors.New("login callback already handled")
)

type Result struct {
	ServerID int64
	Token    string
}

func GitLabLoginURL(serverURL string) string {
	return serverURL + "/oauth/gitlab/mobile_login?redirect_to=" + url.QueryEscape(CallbackURL)
}

func IsCallbackURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), scheme+"://")
}

func FindCallbackArg(args []string) (string, bool) {
	for _, a := range args {
		if IsCallbackURL(a) {
			return a, true
		}
	}
	return "", false
}

type pending struct {
	urls    []string
	started time.Time
}

type SSO struct {
	mu      sync.Mutex
	pending map[int64]pending
	recent  map[string]time.Time // completed tokens
	now     func() time.Time
}

func NewSSO() *SSO {
	return &SSO{pending: map[int64]pending{}, recent: map[string]time.Time{}, now: time.Now}
}

// Begin records that serverID is signing in; urls are every URL the server
// may report as srv (entered URL and SiteURL). Restarting replaces the entry.
func (s *SSO) Begin(serverID int64, urls ...string) {
	var norm []string
	for _, u := range urls {
		if n, err := rest.NormalizeURL(u); err == nil {
			norm = append(norm, n)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[serverID] = pending{urls: norm, started: s.now()}
}

func (s *SSO) Cancel(serverID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, serverID)
}

func (s *SSO) Complete(raw string) (Result, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, scheme) {
		return Result{}, ErrMalformedCallback
	}
	q := u.Query()
	token := q.Get("MMAUTHTOKEN")
	if token == "" {
		return Result{}, ErrMalformedCallback
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, p := range s.pending {
		if now.Sub(p.started) > pendingTTL {
			delete(s.pending, id)
		}
	}
	for tok, at := range s.recent {
		if now.Sub(at) > recentTTL {
			delete(s.recent, tok)
		}
	}
	if _, dup := s.recent[token]; dup {
		return Result{}, ErrAlreadyCompleted
	}
	if len(s.pending) == 0 {
		return Result{}, ErrNoPendingLogin
	}

	var id int64
	if srv := q.Get("srv"); srv != "" {
		n, err := rest.NormalizeURL(srv)
		if err != nil {
			return Result{}, ErrMalformedCallback
		}
		found := false
		for pid, p := range s.pending {
			for _, pu := range p.urls {
				if pu == n {
					id, found = pid, true
				}
			}
		}
		if !found {
			return Result{}, ErrServerMismatch
		}
	} else {
		// Servers older than the srv parameter: only unambiguous when a
		// single login is in flight.
		if len(s.pending) != 1 {
			return Result{}, ErrServerMismatch
		}
		for pid := range s.pending {
			id = pid
		}
	}
	delete(s.pending, id)
	s.recent[token] = now
	return Result{ServerID: id, Token: token}, nil
}
```

- [ ] **Step 4: Run — PASS** (`go test -race ./internal/auth/`)

- [ ] **Step 5: Commit**

```bash
git add internal/auth && git commit -m "feat(auth): mmauth:// GitLab SSO pending-login matching"
```

---

### Task 8: Сервис приложения (серверы и вход)

**Files:**
- Create: `internal/api/api.go`, `internal/api/service.go`, `internal/api/service_test.go`

**Interfaces:**
- Consumes: `store` (Task 3), `events` (Task 4), `rest` (Task 5), `auth` (Task 7), `mmfake` (Task 6, только тесты).
- Produces:
  - `type ServerDTO struct{ ID int64 \`json:"id"\`; Name string \`json:"name"\`; URL string \`json:"url"\`; SignedIn bool \`json:"signed_in"\`; Username string \`json:"username"\`; GitLab bool \`json:"gitlab"\` }`
  - `type API interface { ListServers(ctx) ([]ServerDTO, error); AddServer(ctx, rawURL string) (ServerDTO, error); RemoveServer(ctx, id int64) error; StartGitLabLogin(ctx, id int64) error; LoginWithPassword(ctx, id int64, login, password string) (ServerDTO, error); Logout(ctx, id int64) error }`
  - `type CodedError struct{ Code, Detail string }` (`Error()` = `"<code>: <detail>"` или `"<code>"`); коды: `CodeInvalidURL="invalid_url"`, `CodeUnreachable="unreachable"`, `CodeNotMattermost="not_mattermost"`, `CodeServerExists="server_exists"`, `CodeNotFound="not_found"`, `CodeGitLabDisabled="gitlab_disabled"`, `CodeBadCredentials="bad_credentials"`, `CodeAuthFailed="auth_failed"`, `CodeLoginMismatch="login_mismatch"`, `CodeNoPendingLogin="no_pending_login"`, `CodeInternal="internal"`
  - события: `EventServersChanged="servers_changed"`, `EventLoginFailed="login_failed"` (payload `{server_id?, code}`), `EventOpenExternal="open_external"` (payload `{url}`)
  - `type Opener func(url string) error`
  - `func NewService(st *store.Store, em *events.Emitter, open Opener, hc *http.Client) *Service` (реализует `API`) + `func (s *Service) HandleDeepLink(ctx, raw string) error`

- [ ] **Step 1: Падающие тесты**

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/auth"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/store"
)

type fixture struct {
	svc    *Service
	fake   *mmfake.Server
	opened []string
	evs    <-chan events.Event
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	fake := mmfake.Start(mmfake.Options{})
	t.Cleanup(fake.Close)
	em := events.NewEmitter()
	ch, unsub := em.Subscribe()
	t.Cleanup(unsub)
	f := &fixture{fake: fake, evs: ch}
	f.svc = NewService(st, em, func(u string) error { f.opened = append(f.opened, u); return nil }, &http.Client{Timeout: 5 * time.Second})
	return f
}

func codeOf(err error) string {
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func (f *fixture) nextEvent(t *testing.T, typ string) events.Event {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev := <-f.evs:
			if ev.Type == typ {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s event", typ)
		}
	}
}

// completeGitLab walks the fake GitLab consent flow like the user's browser
// would and returns the mmauth:// callback URL.
func completeGitLab(t *testing.T, loginURL string) string {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(loginURL)
	require.NoError(t, err)
	u, _ := url.Parse(loginURL)
	base := u.Scheme + "://" + u.Host
	state := resp.Header.Get("Location")
	pu, _ := url.Parse(state)
	resp, err = c.Get(base + "/mmfake/gitlab/complete?state=" + pu.Query().Get("state"))
	require.NoError(t, err)
	var link string
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	const marker = `id="mmauth-link" href="`
	s := string(buf[:n])
	i := len(marker) + indexOf(s, marker)
	link = s[i : i+indexOf(s[i:], `"`)]
	return replaceAmp(link)
}

func TestAddServerUsesSiteNameAndGitLabFlag(t *testing.T) {
	f := newFixture(t)
	dto, err := f.svc.AddServer(context.Background(), f.fake.URL()+"/")
	require.NoError(t, err)
	assert.Equal(t, "Fake MM", dto.Name)
	assert.Equal(t, f.fake.URL(), dto.URL)
	assert.True(t, dto.GitLab)
	assert.False(t, dto.SignedIn)
	f.nextEvent(t, EventServersChanged)

	_, err = f.svc.AddServer(context.Background(), f.fake.URL())
	assert.Equal(t, CodeServerExists, codeOf(err))
}

func TestAddServerInvalidURL(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.AddServer(context.Background(), "ftp://x")
	assert.Equal(t, CodeInvalidURL, codeOf(err))
}

func TestAddServerNotMattermost(t *testing.T) {
	f := newFixture(t)
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	_, err := f.svc.AddServer(context.Background(), other.URL)
	assert.Equal(t, CodeNotMattermost, codeOf(err))
}

func TestAddServerUnreachable(t *testing.T) {
	f := newFixture(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	u := dead.URL
	dead.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := f.svc.AddServer(ctx, u)
	assert.Equal(t, CodeUnreachable, codeOf(err))
}

func TestPasswordLoginAndLogout(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, err := f.svc.AddServer(ctx, f.fake.URL())
	require.NoError(t, err)

	_, err = f.svc.LoginWithPassword(ctx, dto.ID, "alice", "wrong")
	assert.Equal(t, CodeBadCredentials, codeOf(err))

	dto, err = f.svc.LoginWithPassword(ctx, dto.ID, "alice", "secret")
	require.NoError(t, err)
	assert.True(t, dto.SignedIn)
	assert.Equal(t, "alice", dto.Username)
	assert.Equal(t, 1, f.fake.ActiveSessions())

	require.NoError(t, f.svc.Logout(ctx, dto.ID))
	assert.Equal(t, 0, f.fake.ActiveSessions(), "logout revokes server-side")
	list, _ := f.svc.ListServers(ctx)
	assert.False(t, list[0].SignedIn)
}

func TestLogoutClearsEvenIfServerRejects(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	_, err := f.svc.LoginWithPassword(ctx, dto.ID, "alice", "secret")
	require.NoError(t, err)
	f.fake.Close() // server gone entirely
	require.NoError(t, f.svc.Logout(ctx, dto.ID))
	list, _ := f.svc.ListServers(ctx)
	assert.False(t, list[0].SignedIn)
}

func TestGitLabSSOEndToEnd(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	require.NoError(t, f.svc.StartGitLabLogin(ctx, dto.ID))
	require.Len(t, f.opened, 1)
	assert.Equal(t, auth.GitLabLoginURL(f.fake.URL()), f.opened[0])

	cb := completeGitLab(t, f.opened[0])
	require.NoError(t, f.svc.HandleDeepLink(ctx, cb))
	list, _ := f.svc.ListServers(ctx)
	assert.True(t, list[0].SignedIn)
	assert.Equal(t, "alice", list[0].Username)
}

func TestHandleDeepLinkDuplicateIsSilent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	require.NoError(t, f.svc.StartGitLabLogin(ctx, dto.ID))
	cb := completeGitLab(t, f.opened[0])
	require.NoError(t, f.svc.HandleDeepLink(ctx, cb))
	f.nextEvent(t, EventServersChanged)
	assert.NoError(t, f.svc.HandleDeepLink(ctx, cb))
	select {
	case ev := <-f.evs:
		assert.NotEqual(t, EventLoginFailed, ev.Type)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandleDeepLinkWithRevokedTokenFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	require.NoError(t, f.svc.StartGitLabLogin(ctx, dto.ID))
	cb := completeGitLab(t, f.opened[0])
	f.fake.RevokeAll()
	err := f.svc.HandleDeepLink(ctx, cb)
	assert.Equal(t, CodeAuthFailed, codeOf(err))
	ev := f.nextEvent(t, EventLoginFailed)
	assert.Equal(t, CodeAuthFailed, ev.Payload["code"])
	list, _ := f.svc.ListServers(ctx)
	assert.False(t, list[0].SignedIn)
}

func TestStartGitLabLoginDisabled(t *testing.T) {
	f := newFixture(t)
	noGitLab := mmfake.Start(mmfake.Options{SiteName: "NoGL", GitLab: false, Users: []mmfake.User{{ID: "u", Username: "u", Password: "p"}}})
	defer noGitLab.Close()
	dto, err := f.svc.AddServer(context.Background(), noGitLab.URL())
	require.NoError(t, err)
	assert.Equal(t, CodeGitLabDisabled, codeOf(f.svc.StartGitLabLogin(context.Background(), dto.ID)))
}

func TestRemoveServerRevokesSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	_, err := f.svc.LoginWithPassword(ctx, dto.ID, "alice", "secret")
	require.NoError(t, err)
	require.NoError(t, f.svc.RemoveServer(ctx, dto.ID))
	assert.Equal(t, 0, f.fake.ActiveSessions())
	list, _ := f.svc.ListServers(ctx)
	assert.Empty(t, list)
	assert.Equal(t, CodeNotFound, codeOf(f.svc.RemoveServer(ctx, dto.ID)))
}

func TestServerDTOHasNoToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	dto, _ := f.svc.AddServer(ctx, f.fake.URL())
	_, err := f.svc.LoginWithPassword(ctx, dto.ID, "alice", "secret")
	require.NoError(t, err)
	list, _ := f.svc.ListServers(ctx)
	raw, _ := json.Marshal(list)
	var generic []map[string]any
	require.NoError(t, json.Unmarshal(raw, &generic))
	assert.ElementsMatch(t, []string{"id", "name", "url", "signed_in", "username", "gitlab"}, keys(generic[0]))
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

Хелперы `indexOf`/`replaceAmp` — в конце того же файла:
```go
func indexOf(s, sub string) int { return strings.Index(s, sub) }
func replaceAmp(s string) string { return strings.ReplaceAll(s, "&amp;", "&") }
```
(добавить `"strings"` в импорты).

- [ ] **Step 2: Run — FAIL** (`go test ./internal/api/`)

- [ ] **Step 3: Реализация**

`internal/api/api.go`:
```go
// Package api is the application surface the UI talks to, independent of
// transport (Wails bindings in desktop, HTTP+SSE in browser mode).
package api

import "context"

type ServerDTO struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	SignedIn bool   `json:"signed_in"`
	Username string `json:"username"`
	GitLab   bool   `json:"gitlab"`
}

type API interface {
	ListServers(ctx context.Context) ([]ServerDTO, error)
	AddServer(ctx context.Context, rawURL string) (ServerDTO, error)
	RemoveServer(ctx context.Context, id int64) error
	StartGitLabLogin(ctx context.Context, id int64) error
	LoginWithPassword(ctx context.Context, id int64, login, password string) (ServerDTO, error)
	Logout(ctx context.Context, id int64) error
}

// Event types pushed to the UI.
const (
	EventServersChanged = "servers_changed"
	EventLoginFailed    = "login_failed"  // payload: server_id (optional), code
	EventOpenExternal   = "open_external" // payload: url — browser mode opens it in a new tab
)

// Error codes. The UI maps them to localized messages (frontend/src/errors.ts).
const (
	CodeInvalidURL     = "invalid_url"
	CodeUnreachable    = "unreachable"
	CodeNotMattermost  = "not_mattermost"
	CodeServerExists   = "server_exists"
	CodeNotFound       = "not_found"
	CodeGitLabDisabled = "gitlab_disabled"
	CodeBadCredentials = "bad_credentials"
	CodeAuthFailed     = "auth_failed"
	CodeLoginMismatch  = "login_mismatch"
	CodeNoPendingLogin = "no_pending_login"
	CodeInternal       = "internal"
)

// CodedError is what API methods return: a stable code for the UI plus a
// technical detail for logs.
type CodedError struct {
	Code   string
	Detail string
}

func (e *CodedError) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func coded(code string, err error) *CodedError {
	if err == nil {
		return &CodedError{Code: code}
	}
	return &CodedError{Code: code, Detail: err.Error()}
}
```

`internal/api/service.go`:
```go
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/spk/spk-mattermost/internal/auth"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/store"
)

type Opener func(url string) error

type Service struct {
	st   *store.Store
	em   *events.Emitter
	sso  *auth.SSO
	open Opener
	hc   *http.Client
}

var _ API = (*Service)(nil)

func NewService(st *store.Store, em *events.Emitter, open Opener, hc *http.Client) *Service {
	return &Service{st: st, em: em, sso: auth.NewSSO(), open: open, hc: hc}
}

func toDTO(s store.Server) ServerDTO {
	return ServerDTO{ID: s.ID, Name: s.Name, URL: s.URL, SignedIn: s.SignedIn(), Username: s.Username, GitLab: s.GitLab}
}

func (s *Service) emit(typ string, payload map[string]any) {
	s.em.Emit(events.Event{Type: typ, Payload: payload})
}

func (s *Service) getServer(ctx context.Context, id int64) (store.Server, error) {
	srv, err := s.st.GetServer(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Server{}, coded(CodeNotFound, nil)
	}
	if err != nil {
		return store.Server{}, coded(CodeInternal, err)
	}
	return srv, nil
}

func (s *Service) ListServers(ctx context.Context) ([]ServerDTO, error) {
	list, err := s.st.ListServers(ctx)
	if err != nil {
		return nil, coded(CodeInternal, err)
	}
	out := make([]ServerDTO, 0, len(list))
	for _, srv := range list {
		out = append(out, toDTO(srv))
	}
	return out, nil
}

func (s *Service) AddServer(ctx context.Context, rawURL string) (ServerDTO, error) {
	norm, err := rest.NormalizeURL(rawURL)
	if err != nil {
		return ServerDTO{}, coded(CodeInvalidURL, err)
	}
	c := rest.New(norm, "", s.hc)
	if err := c.Ping(ctx); err != nil {
		if rest.IsNetwork(err) {
			return ServerDTO{}, coded(CodeUnreachable, err)
		}
		return ServerDTO{}, coded(CodeNotMattermost, err)
	}
	cfg, err := c.ClientConfig(ctx)
	if err != nil {
		return ServerDTO{}, coded(CodeNotMattermost, err)
	}
	name := cfg.SiteName
	if name == "" {
		if u, perr := url.Parse(norm); perr == nil {
			name = u.Host
		}
	}
	siteURL := ""
	if cfg.SiteURL != "" {
		if n, err := rest.NormalizeURL(cfg.SiteURL); err == nil {
			siteURL = n
		}
	}
	srv, err := s.st.AddServer(ctx, store.Server{URL: norm, SiteURL: siteURL, Name: name, GitLab: cfg.GitLabEnabled()})
	if errors.Is(err, store.ErrServerExists) {
		return ServerDTO{}, coded(CodeServerExists, nil)
	}
	if err != nil {
		return ServerDTO{}, coded(CodeInternal, err)
	}
	slog.Info("server added", "srv", srv)
	s.emit(EventServersChanged, nil)
	return toDTO(srv), nil
}

// revoke logs the session out server-side, best effort: a dead server or an
// already-revoked token must never keep the user from signing out locally.
func (s *Service) revoke(ctx context.Context, srv store.Server) {
	if !srv.SignedIn() {
		return
	}
	if err := rest.New(srv.URL, srv.Token, s.hc).Logout(ctx); err != nil {
		slog.Warn("server-side logout failed; clearing locally", "srv", srv, "err", err)
	}
}

func (s *Service) RemoveServer(ctx context.Context, id int64) error {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return err
	}
	s.revoke(ctx, srv)
	s.sso.Cancel(id)
	if err := s.st.DeleteServer(ctx, id); err != nil {
		return coded(CodeInternal, err)
	}
	s.emit(EventServersChanged, nil)
	return nil
}

func (s *Service) StartGitLabLogin(ctx context.Context, id int64) error {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return err
	}
	if !srv.GitLab {
		return coded(CodeGitLabDisabled, nil)
	}
	s.sso.Begin(id, srv.URL, srv.SiteURL)
	if err := s.open(auth.GitLabLoginURL(srv.URL)); err != nil {
		s.sso.Cancel(id)
		return coded(CodeInternal, err)
	}
	return nil
}

func (s *Service) LoginWithPassword(ctx context.Context, id int64, login, password string) (ServerDTO, error) {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return ServerDTO{}, err
	}
	tok, u, err := rest.New(srv.URL, "", s.hc).Login(ctx, login, password)
	if err != nil {
		var re *rest.Error
		if errors.As(err, &re) && (re.Kind == rest.KindAuth || re.Status == http.StatusBadRequest) {
			return ServerDTO{}, coded(CodeBadCredentials, err)
		}
		if rest.IsNetwork(err) {
			return ServerDTO{}, coded(CodeUnreachable, err)
		}
		return ServerDTO{}, coded(CodeInternal, err)
	}
	if err := s.st.SetSession(ctx, id, tok, u.ID, u.Username); err != nil {
		return ServerDTO{}, coded(CodeInternal, err)
	}
	s.emit(EventServersChanged, nil)
	srv, err = s.getServer(ctx, id)
	if err != nil {
		return ServerDTO{}, err
	}
	return toDTO(srv), nil
}

func (s *Service) Logout(ctx context.Context, id int64) error {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return err
	}
	s.revoke(ctx, srv)
	if err := s.st.ClearSession(ctx, id); err != nil {
		return coded(CodeInternal, err)
	}
	s.emit(EventServersChanged, nil)
	return nil
}

// HandleDeepLink finishes a GitLab SSO login from an mmauth:// callback.
// Duplicate delivery of an already-handled callback is ignored silently.
func (s *Service) HandleDeepLink(ctx context.Context, raw string) error {
	res, err := s.sso.Complete(raw)
	switch {
	case errors.Is(err, auth.ErrAlreadyCompleted):
		return nil
	case errors.Is(err, auth.ErrNoPendingLogin):
		return s.loginFailed(0, coded(CodeNoPendingLogin, err))
	case errors.Is(err, auth.ErrServerMismatch), errors.Is(err, auth.ErrMalformedCallback):
		return s.loginFailed(0, coded(CodeLoginMismatch, err))
	case err != nil:
		return s.loginFailed(0, coded(CodeInternal, err))
	}
	srv, err := s.getServer(ctx, res.ServerID)
	if err != nil {
		return s.loginFailed(res.ServerID, err.(*CodedError))
	}
	u, err := rest.New(srv.URL, res.Token, s.hc).Me(ctx)
	if err != nil {
		return s.loginFailed(res.ServerID, coded(CodeAuthFailed, err))
	}
	if err := s.st.SetSession(ctx, srv.ID, res.Token, u.ID, u.Username); err != nil {
		return s.loginFailed(res.ServerID, coded(CodeInternal, err))
	}
	slog.Info("signed in via GitLab", "srv", srv.ID, "username", u.Username)
	s.emit(EventServersChanged, nil)
	return nil
}

func (s *Service) loginFailed(serverID int64, ce *CodedError) error {
	payload := map[string]any{"code": ce.Code}
	if serverID != 0 {
		payload["server_id"] = serverID
	}
	slog.Warn("login failed", "code", ce.Code, "detail", ce.Detail)
	s.emit(EventLoginFailed, payload)
	return ce
}
```

- [ ] **Step 4: Run — PASS** (`go test -race ./internal/api/`)

- [ ] **Step 5: Commit**

```bash
git add internal/api && git commit -m "feat(api): servers, password and GitLab SSO login service"
```

---

### Task 9: Транспорты — HTTP+SSE и Wails-биндинги

**Files:**
- Create: `internal/api/transport/guard.go` (перенос `OriginGuard`, `originMatchesHost`, `splitHostPortLoose`, `splitHostPort`, `AuthGuard`, `tokenEq`, `bearerOK` из `spk-mail/internal/api/transport/{http.go,auth.go}` — код без изменений), `internal/api/transport/http.go`, `internal/api/transport/http_test.go`, `internal/api/transport/guard_test.go` (перенос `origin_test.go` + `auth_test.go` из spk-mail, заменив имя пакета импорта на `github.com/spk/spk-mattermost/...`), `internal/api/transport/wails.go`

**Interfaces:**
- Consumes: `api.API`, `api.CodedError`, `events.Emitter`.
- Produces: `func NewHTTP(a api.API, em *events.Emitter) *HTTP` (`http.Handler`); `func (h *HTTP) AuthToken() string`; `func OriginGuard(http.Handler) http.Handler`; `func AuthGuard(token string, next http.Handler) http.Handler`; Wails: `type API struct` с методами `ListServers() ([]api.ServerDTO, error)`, `AddServer(url string) (api.ServerDTO, error)`, `RemoveServer(id int64) error`, `StartGitLabLogin(id int64) error`, `LoginWithPassword(id int64, login, password string) (api.ServerDTO, error)`, `Logout(id int64) error`; `func NewAPI(a api.API) *API`.
  HTTP-контракт: `POST /api/<Method>` с JSON-телом; ответ 200 + JSON; ошибка — 400 + `{"code": "...", "detail": "..."}`; `GET /api/events?token=` — SSE (`data: <Event JSON>\n\n`, ping-комментарий каждые 25 с).

- [ ] **Step 1: Падающий тест HTTP**

`internal/api/transport/http_test.go`:
```go
package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/events"
)

type fakeAPI struct{ added string }

func (f *fakeAPI) ListServers(context.Context) ([]api.ServerDTO, error) {
	return []api.ServerDTO{{ID: 1, Name: "A"}}, nil
}
func (f *fakeAPI) AddServer(_ context.Context, u string) (api.ServerDTO, error) {
	f.added = u
	if u == "bad" {
		return api.ServerDTO{}, &api.CodedError{Code: api.CodeInvalidURL, Detail: "nope"}
	}
	return api.ServerDTO{ID: 2, URL: u}, nil
}
func (f *fakeAPI) RemoveServer(context.Context, int64) error     { return nil }
func (f *fakeAPI) StartGitLabLogin(context.Context, int64) error { return nil }
func (f *fakeAPI) LoginWithPassword(context.Context, int64, string, string) (api.ServerDTO, error) {
	return api.ServerDTO{}, nil
}
func (f *fakeAPI) Logout(context.Context, int64) error { return nil }

func call(t *testing.T, h *HTTP, srvURL, method, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/api/"+method, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.AuthToken())
	req.Header.Set("Origin", srvURL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestPostRoutesToAPI(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "AddServer", `{"url":"https://mm"}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "https://mm", f.added)

	resp = call(t, h, ts.URL, "ListServers", `{}`)
	var list []api.ServerDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	assert.Equal(t, "A", list[0].Name)
}

func TestCodedErrorBecomes400WithCode(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp := call(t, h, ts.URL, "AddServer", `{"url":"bad"}`)
	assert.Equal(t, 400, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "invalid_url", body["code"])
}

func TestMissingTokenIs401(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/ListServers", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestSSEDeliversEvents(t *testing.T) {
	em := events.NewEmitter()
	h := NewHTTP(&fakeAPI{}, em)
	ts := httptest.NewServer(h)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events?token="+h.AuthToken(), nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	r := bufio.NewReader(resp.Body)
	line, err := r.ReadString('\n') // ": ok" preamble — subscription is live
	require.NoError(t, err)
	assert.Equal(t, ": ok\n", line)

	em.Emit(events.Event{Type: api.EventServersChanged})
	for {
		line, err = r.ReadString('\n')
		require.NoError(t, err)
		if strings.HasPrefix(line, "data: ") {
			var ev events.Event
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev))
			assert.Equal(t, api.EventServersChanged, ev.Type)
			return
		}
	}
}
```

- [ ] **Step 2: Run — FAIL** (`go test ./internal/api/transport/`)

- [ ] **Step 3: Реализация**

`internal/api/transport/guard.go` — перенести из spk-mail указанные функции; `newAuthToken` реализовать так:
```go
func newAuthToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```
(импорты `crypto/rand`, `encoding/hex`).

`internal/api/transport/http.go`:
```go
// Package transport exposes api.API to the UI: HTTP+SSE for browser mode
// (this file) and Wails bindings for desktop (wails.go).
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/events"
)

const (
	ssePing         = 25 * time.Second
	sseWriteTimeout = 10 * time.Second
)

type HTTP struct {
	api       api.API
	events    *events.Emitter
	mux       *http.ServeMux
	authToken string
}

func NewHTTP(a api.API, em *events.Emitter) *HTTP {
	h := &HTTP{api: a, events: em, mux: http.NewServeMux(), authToken: newAuthToken()}
	h.routes()
	return h
}

func (h *HTTP) AuthToken() string { return h.authToken }

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") && !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	OriginGuard(h.mux).ServeHTTP(w, r)
}

func (h *HTTP) authorized(r *http.Request) bool {
	if bearerOK(r, h.authToken) {
		return true
	}
	// EventSource cannot set headers; the query token is accepted on SSE only.
	return r.URL.Path == "/api/events" && tokenEq(r.URL.Query().Get("token"), h.authToken)
}

func handle[Req any](fn func(ctx context.Context, req *Req) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeErr(w, &api.CodedError{Code: api.CodeInternal, Detail: "bad request body: " + err.Error()})
				return
			}
		}
		out, err := fn(r.Context(), &req)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if out == nil {
			out = struct{}{}
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	var ce *api.CodedError
	if !errors.As(err, &ce) {
		ce = &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": ce.Code, "detail": ce.Detail})
}

type idReq struct {
	ID int64 `json:"id"`
}

func (h *HTTP) routes() {
	h.mux.HandleFunc("POST /api/ListServers", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.ListServers(ctx)
	}))
	h.mux.HandleFunc("POST /api/AddServer", handle(func(ctx context.Context, r *struct {
		URL string `json:"url"`
	}) (any, error) {
		return h.api.AddServer(ctx, r.URL)
	}))
	h.mux.HandleFunc("POST /api/RemoveServer", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.RemoveServer(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/StartGitLabLogin", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.StartGitLabLogin(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/LoginWithPassword", handle(func(ctx context.Context, r *struct {
		ID       int64  `json:"id"`
		Login    string `json:"login"`
		Password string `json:"password"`
	}) (any, error) {
		return h.api.LoginWithPassword(ctx, r.ID, r.Login, r.Password)
	}))
	h.mux.HandleFunc("POST /api/Logout", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.Logout(ctx, r.ID)
	}))
	h.mux.HandleFunc("GET /api/events", h.serveEvents)
}

func (h *HTTP) serveEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch, unsub := h.events.Subscribe()
	defer unsub()
	write := func(s string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
		if _, err := fmt.Fprint(w, s); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write(": ok\n\n") {
		return
	}
	ping := time.NewTicker(ssePing)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if !write(": ping\n\n") {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			if !write("data: " + string(b) + "\n\n") {
				return
			}
		}
	}
}
```

`internal/api/transport/wails.go`:
```go
//go:build wails

package transport

import (
	"context"

	"github.com/spk/spk-mattermost/internal/api"
)

// API is the Wails service. Bindings are addressed by Go FQN:
//   github.com/spk/spk-mattermost/internal/api/transport.API.<Method>
// (mirrored in frontend/src/api/client.ts).
type API struct{ a api.API }

func NewAPI(a api.API) *API { return &API{a: a} }

func (w *API) ListServers() ([]api.ServerDTO, error) { return w.a.ListServers(context.Background()) }
func (w *API) AddServer(url string) (api.ServerDTO, error) {
	return w.a.AddServer(context.Background(), url)
}
func (w *API) RemoveServer(id int64) error     { return w.a.RemoveServer(context.Background(), id) }
func (w *API) StartGitLabLogin(id int64) error { return w.a.StartGitLabLogin(context.Background(), id) }
func (w *API) LoginWithPassword(id int64, login, password string) (api.ServerDTO, error) {
	return w.a.LoginWithPassword(context.Background(), id, login, password)
}
func (w *API) Logout(id int64) error { return w.a.Logout(context.Background(), id) }
```

- [ ] **Step 4: Run — PASS** (`go test -race ./internal/api/transport/`)

- [ ] **Step 5: Commit**

```bash
git add internal/api/transport && git commit -m "feat(transport): HTTP+SSE browser transport and Wails bindings"
```

---

### Task 10: Browser-режим и test-API

**Files:**
- Modify: `cmd/spk-mattermost/browser.go` (заменить заглушку)
- Create: `cmd/spk-mattermost/static.go`, `cmd/spk-mattermost/browser_test.go`

**Interfaces:**
- Consumes: всё из Tasks 2–9.
- Produces: `func runBrowser(ctx context.Context, o browserOpts) error`; `func newBrowserHandler(svc *api.Service, em *events.Emitter, dist fs.FS, fake *mmfake.Server, testAPI bool) (http.Handler, string)` (возвращает handler и API-токен).
  Test-API (только при `--test-api`, за `AuthGuard`+`OriginGuard`): `POST /api/_test/deeplink {"url": "..."}` → `HandleDeepLink`; `GET /api/_test/fake-url` → `{"url": "<mmfake URL>"}`.
  Мета-тег токена: `<meta name="spk-mattermost-api-token" content="...">`.

- [ ] **Step 1: Падающий тест**

```go
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/store"
)

func setup(t *testing.T, testAPI bool) (*httptest.Server, string, *mmfake.Server) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	em := events.NewEmitter()
	fake := mmfake.Start(mmfake.Options{})
	t.Cleanup(fake.Close)
	svc := api.NewService(st, em, func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
	dist := fstest.MapFS{"index.html": {Data: []byte("<html><head></head><body></body></html>")}}
	h, token := newBrowserHandler(svc, em, dist, fake, testAPI)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, token, fake
}

func TestIndexCarriesTokenAndIsNotCached(t *testing.T) {
	ts, token, _ := setup(t, false)
	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), `<meta name="spk-mattermost-api-token" content="`+token+`">`)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

func TestTestAPIAbsentByDefault(t *testing.T) {
	ts, token, _ := setup(t, false)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/_test/fake-url", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestTestAPIFakeURLAndDeeplink(t *testing.T) {
	ts, token, fake := setup(t, true)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/_test/fake-url", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var out map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, fake.URL(), out["url"])

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/_test/deeplink", strings.NewReader(`{"url":"mmauth://callback?MMAUTHTOKEN=x"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", ts.URL)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var e map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	assert.Equal(t, "no_pending_login", e["code"], "no login was started")
}
```

- [ ] **Step 2: Run — FAIL** (`go test ./cmd/...`)

- [ ] **Step 3: Реализация**

`cmd/spk-mattermost/static.go`:
```go
package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

// frontendHandler serves the SPA; index.html gets the per-run API token.
// Extension-less paths fall back to index.html (SPA).
func frontendHandler(token string, dist fs.FS) http.Handler {
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || p == "index.html" || !strings.Contains(p, ".") {
			serveIndex(w, dist, token)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, dist fs.FS, token string) {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "UI not built: run `make build`", http.StatusInternalServerError)
		return
	}
	tag := fmt.Sprintf(`<meta name="spk-mattermost-api-token" content="%s">`, token)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The token changes every run; a cached index would 401 every call.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(strings.Replace(string(data), "</head>", tag+"</head>", 1)))
}
```

`cmd/spk-mattermost/browser.go`:
```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/api/transport"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/paths"
	"github.com/spk/spk-mattermost/internal/store"
)

func runBrowser(ctx context.Context, o browserOpts) error {
	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	st, err := store.Open(ctx, p.DBFile)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer st.Close()

	var fake *mmfake.Server
	if o.MMFake {
		fake = mmfake.Start(mmfake.Options{})
		defer fake.Close()
		slog.Warn("fake Mattermost server started (development only)", "url", fake.URL())
	}

	em := events.NewEmitter()
	// Browser mode has no OS browser to hand the SSO page to: the UI opens it
	// in a new tab on this event.
	open := func(u string) error {
		em.Emit(events.Event{Type: api.EventOpenExternal, Payload: map[string]any{"url": u}})
		return nil
	}
	svc := api.NewService(st, em, open, &http.Client{Timeout: 30 * time.Second})
	h, _ := newBrowserHandler(svc, em, frontendFS(), fake, o.TestAPI)

	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", o.Port),
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	slog.Info("spk-mattermost browser mode", "url", "http://"+srv.Addr, "data", p.DataDir)
	go func() { <-ctx.Done(); _ = srv.Shutdown(context.Background()) }()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func newBrowserHandler(svc *api.Service, em *events.Emitter, dist fs.FS, fake *mmfake.Server, testAPI bool) (http.Handler, string) {
	mux := http.NewServeMux()
	httpAPI := transport.NewHTTP(svc, em)
	token := httpAPI.AuthToken()
	mux.Handle("/api/", httpAPI)
	if testAPI {
		tm := http.NewServeMux()
		tm.HandleFunc("POST /api/_test/deeplink", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				URL string `json:"url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			w.Header().Set("Content-Type", "application/json")
			if err := svc.HandleDeepLink(r.Context(), in.URL); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				code := api.CodeInternal
				if ce, ok := err.(*api.CodedError); ok {
					code = ce.Code
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		})
		tm.HandleFunc("GET /api/_test/fake-url", func(w http.ResponseWriter, _ *http.Request) {
			u := ""
			if fake != nil {
				u = fake.URL()
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"url": u})
		})
		mux.Handle("/api/_test/", transport.AuthGuard(token, transport.OriginGuard(tm)))
		slog.Warn("test-api routes enabled at /api/_test/* — development only")
	}
	mux.Handle("/", frontendHandler(token, dist))
	return mux, token
}
```

Примечание: `mux.Handle("/api/", httpAPI)` и `/api/_test/` — `ServeMux` выбирает более длинный префикс, поэтому test-роуты не попадают в `httpAPI`. Без `--test-api` запрос `/api/_test/...` уходит в `httpAPI`, авторизуется и получает 404 от его mux — это и проверяет `TestTestAPIAbsentByDefault`.

- [ ] **Step 4: Run — PASS** (`go test -race ./cmd/...`)

- [ ] **Step 5: Commit**

```bash
git add cmd && git commit -m "feat(browser): browser mode with fake server and test API"
```

---

### Task 11: Фронтенд — каркас, i18n, экран серверов и входа

**Files:**
- Create: `frontend/package.json`, `frontend/pnpm-lock.yaml` (генерируется), `frontend/vite.config.ts`, `frontend/tsconfig.json`, `frontend/eslint.config.js`, `frontend/index.html`, `frontend/vitest.setup.ts`, `frontend/src/main.tsx`, `frontend/src/index.css`, `frontend/src/App.tsx`, `frontend/src/i18n.ts`, `frontend/src/errors.ts`, `frontend/src/store.ts`, `frontend/src/api/types.ts`, `frontend/src/api/client.ts`, `frontend/src/components/ServerRail.tsx`, `frontend/src/components/AddServerForm.tsx`, `frontend/src/components/ServerPanel.tsx`, тесты: `frontend/src/i18n.test.ts`, `frontend/src/errors.test.ts`, `frontend/src/api/client.test.ts`, `frontend/src/components/AddServerForm.test.tsx`, `frontend/src/components/ServerPanel.test.tsx`

**Interfaces:**
- Consumes: HTTP/Wails-контракт Task 9; события `servers_changed`, `login_failed`, `open_external`.
- Produces: `client: Client` (`listServers`, `addServer`, `removeServer`, `startGitLabLogin`, `loginWithPassword`, `logout`, `subscribeEvents`); `t(key, vars?)`; `errorMessage(err: unknown): string`; `useStore` (Zustand: `servers`, `selectedId`, `setServers`, `select`, `lastError`, `setError`).

- [ ] **Step 1: package.json и конфиги**

`frontend/package.json`:
```json
{
  "name": "spk-mattermost-frontend",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "test": "vitest run",
    "lint": "eslint ."
  },
  "dependencies": {
    "@wailsio/runtime": "3.0.0-beta.25",
    "react": "^19.2.5",
    "react-dom": "^19.2.5",
    "zustand": "^5.0.12"
  },
  "devDependencies": {
    "@eslint/js": "^10.0.1",
    "@tailwindcss/vite": "^4.2.4",
    "@testing-library/jest-dom": "^6.9.1",
    "@testing-library/react": "^16.3.2",
    "@testing-library/user-event": "^14.6.1",
    "@types/react": "^19.2.14",
    "@types/react-dom": "^19.2.3",
    "@vitejs/plugin-react": "^6.0.1",
    "eslint": "^10.2.1",
    "jsdom": "^29.1.0",
    "tailwindcss": "^4.2.4",
    "typescript": "^6.0.3",
    "typescript-eslint": "^8.59.0",
    "vite": "^8.0.10",
    "vitest": "^4.1.5"
  }
}
```

```bash
cd frontend && pnpm install
```

`frontend/vite.config.ts`:
```ts
/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: 'dist', emptyOutDir: true },
  test: { environment: 'jsdom', globals: true, setupFiles: ['./vitest.setup.ts'] },
})
```

`frontend/tsconfig.json`:
```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true,
    "types": ["vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src", "vite.config.ts", "vitest.setup.ts"]
}
```

`frontend/eslint.config.js`:
```js
import js from '@eslint/js'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
)
```

`frontend/vitest.setup.ts`:
```ts
import '@testing-library/jest-dom/vitest'
```

`frontend/index.html`:
```html
<!doctype html>
<html lang="ru">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>spk-mattermost</title>
  </head>
  <body class="bg-neutral-50 text-neutral-900">
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

`frontend/src/index.css`:
```css
@import "tailwindcss";
```

- [ ] **Step 2: Падающие тесты i18n, ошибок, клиента**

`frontend/src/i18n.test.ts`:
```ts
import { dict, setLocale, t } from './i18n'

test('ru and en have exactly the same keys and no empty values', () => {
  const ru = Object.keys(dict.ru).sort()
  const en = Object.keys(dict.en).sort()
  expect(en).toEqual(ru)
  for (const loc of ['ru', 'en'] as const) {
    for (const [k, v] of Object.entries(dict[loc])) expect(v, `${loc}.${k}`).not.toBe('')
  }
})

test('t interpolates variables and falls back to the key', () => {
  setLocale('en')
  expect(t('server.signedInAs', { name: 'alice' })).toBe('Signed in as alice')
  expect(t('no.such.key' as never)).toBe('no.such.key')
  setLocale('ru')
})
```

`frontend/src/errors.test.ts`:
```ts
import { ApiError } from './api/client'
import { errorMessage } from './errors'
import { setLocale } from './i18n'

test('maps known codes to localized text and unknown ones to the generic message', () => {
  setLocale('en')
  expect(errorMessage(new ApiError('bad_credentials', ''))).toBe('Wrong login or password')
  expect(errorMessage(new ApiError('weird_code', 'x'))).toBe('Something went wrong (weird_code)')
  expect(errorMessage(new Error('boom'))).toBe('Something went wrong (boom)')
  setLocale('ru')
})
```

`frontend/src/api/client.test.ts`:
```ts
import { afterEach, vi } from 'vitest'
import { ApiError, httpClient, parseWailsError } from './client'

afterEach(() => {
  vi.restoreAllMocks()
  document.head.innerHTML = ''
})

test('http client sends bearer token from meta tag and JSON body', async () => {
  document.head.innerHTML = '<meta name="spk-mattermost-api-token" content="tok123">'
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ id: 1, name: 'A', url: 'u', signed_in: false, username: '', gitlab: true }), {
      headers: { 'content-type': 'application/json' },
    }),
  )
  const s = await httpClient.addServer('https://mm')
  expect(s.name).toBe('A')
  const [path, init] = fetchMock.mock.calls[0]
  expect(path).toBe('/api/AddServer')
  expect((init!.headers as Record<string, string>).Authorization).toBe('Bearer tok123')
  expect(JSON.parse(init!.body as string)).toEqual({ url: 'https://mm' })
})

test('http client turns 400 {code} into ApiError', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ code: 'invalid_url', detail: 'x' }), {
      status: 400,
      headers: { 'content-type': 'application/json' },
    }),
  )
  await expect(httpClient.addServer('bad')).rejects.toEqual(new ApiError('invalid_url', 'x'))
})

test('wails error text "<code>: <detail>" parses into ApiError', () => {
  expect(parseWailsError(new Error('bad_credentials: mattermost: HTTP 401'))).toEqual(
    new ApiError('bad_credentials', 'mattermost: HTTP 401'),
  )
  expect(parseWailsError({ message: 'not_found' })).toEqual(new ApiError('not_found', ''))
})
```

- [ ] **Step 3: Run — FAIL** (`cd frontend && pnpm test`)

- [ ] **Step 4: Реализация i18n, ошибок, типов, клиента, store**

`frontend/src/i18n.ts`:
```ts
const ru = {
  'app.title': 'spk-mattermost',
  'rail.add': 'Добавить сервер',
  'add.title': 'Добавить сервер Mattermost',
  'add.urlLabel': 'Адрес сервера',
  'add.urlPlaceholder': 'mm.example.com',
  'add.submit': 'Добавить',
  'add.checking': 'Проверяю сервер…',
  'server.signedInAs': 'Вы вошли как {name}',
  'server.signedOut': 'Вы не вошли',
  'server.gitlab': 'Войти через GitLab',
  'server.gitlabWaiting': 'Завершите вход в открывшемся браузере',
  'server.orPassword': 'или по логину и паролю',
  'server.login': 'Логин или email',
  'server.password': 'Пароль',
  'server.signIn': 'Войти',
  'server.signOut': 'Выйти',
  'server.remove': 'Удалить сервер',
  'server.removeConfirm': 'Удалить сервер {name}?',
  'err.invalid_url': 'Некорректный адрес сервера',
  'err.unreachable': 'Сервер недоступен',
  'err.not_mattermost': 'По этому адресу не сервер Mattermost',
  'err.server_exists': 'Этот сервер уже добавлен',
  'err.not_found': 'Сервер не найден',
  'err.gitlab_disabled': 'На сервере выключен вход через GitLab',
  'err.bad_credentials': 'Неверный логин или пароль',
  'err.auth_failed': 'Сервер не принял вход, попробуйте ещё раз',
  'err.login_mismatch': 'Ответ входа не относится к добавленному серверу',
  'err.no_pending_login': 'Вход не был начат из приложения',
  'err.generic': 'Что-то пошло не так ({detail})',
} as const

type Key = keyof typeof ru

const en: Record<Key, string> = {
  'app.title': 'spk-mattermost',
  'rail.add': 'Add server',
  'add.title': 'Add a Mattermost server',
  'add.urlLabel': 'Server address',
  'add.urlPlaceholder': 'mm.example.com',
  'add.submit': 'Add',
  'add.checking': 'Checking server…',
  'server.signedInAs': 'Signed in as {name}',
  'server.signedOut': 'Not signed in',
  'server.gitlab': 'Sign in with GitLab',
  'server.gitlabWaiting': 'Finish signing in in the browser window',
  'server.orPassword': 'or with login and password',
  'server.login': 'Login or email',
  'server.password': 'Password',
  'server.signIn': 'Sign in',
  'server.signOut': 'Sign out',
  'server.remove': 'Remove server',
  'server.removeConfirm': 'Remove server {name}?',
  'err.invalid_url': 'Invalid server address',
  'err.unreachable': 'Server is unreachable',
  'err.not_mattermost': 'This address is not a Mattermost server',
  'err.server_exists': 'This server is already added',
  'err.not_found': 'Server not found',
  'err.gitlab_disabled': 'GitLab sign-in is disabled on this server',
  'err.bad_credentials': 'Wrong login or password',
  'err.auth_failed': 'The server rejected the sign-in, try again',
  'err.login_mismatch': 'The sign-in response does not belong to an added server',
  'err.no_pending_login': 'Sign-in was not started from the app',
  'err.generic': 'Something went wrong ({detail})',
}

export const dict = { ru, en }
export type Locale = keyof typeof dict
export type I18nKey = Key

let locale: Locale = navigator.language?.toLowerCase().startsWith('ru') ? 'ru' : 'en'

export function setLocale(l: Locale) {
  locale = l
}

export function t(key: I18nKey, vars?: Record<string, string>): string {
  let s: string = dict[locale][key] ?? key
  for (const [k, v] of Object.entries(vars ?? {})) s = s.replaceAll(`{${k}}`, v)
  return s
}
```

`frontend/src/api/types.ts`:
```ts
export interface ServerDTO {
  id: number
  name: string
  url: string
  signed_in: boolean
  username: string
  gitlab: boolean
}

export type EventType = 'servers_changed' | 'login_failed' | 'open_external'

export interface ApiEvent {
  type: EventType
  payload?: Record<string, unknown>
}
```

`frontend/src/api/client.ts`:
```ts
import { Call, Events } from '@wailsio/runtime'
import type { ApiEvent, ServerDTO } from './types'

export class ApiError extends Error {
  constructor(public code: string, public detail: string) {
    super(detail ? `${code}: ${detail}` : code)
  }
}

export interface Client {
  listServers(): Promise<ServerDTO[]>
  addServer(url: string): Promise<ServerDTO>
  removeServer(id: number): Promise<void>
  startGitLabLogin(id: number): Promise<void>
  loginWithPassword(id: number, login: string, password: string): Promise<ServerDTO>
  logout(id: number): Promise<void>
  subscribeEvents(onEvent: (e: ApiEvent) => void): () => void
}

const tokenMeta = () =>
  document.querySelector('meta[name="spk-mattermost-api-token"]')?.getAttribute('content') ?? ''

async function post<T>(method: string, body: unknown): Promise<T> {
  const headers: Record<string, string> = { 'content-type': 'application/json' }
  const token = tokenMeta()
  if (token) headers.Authorization = `Bearer ${token}`
  const r = await fetch(`/api/${method}`, { method: 'POST', headers, body: JSON.stringify(body ?? {}) })
  const isJSON = r.headers.get('content-type')?.includes('application/json')
  if (!r.ok) {
    if (isJSON) {
      const e = (await r.json()) as { code?: string; detail?: string }
      throw new ApiError(e.code ?? 'internal', e.detail ?? '')
    }
    throw new ApiError('internal', `HTTP ${r.status}`)
  }
  return (isJSON ? await r.json() : undefined) as T
}

export const httpClient: Client = {
  listServers: () => post('ListServers', {}),
  addServer: (url) => post('AddServer', { url }),
  removeServer: (id) => post('RemoveServer', { id }),
  startGitLabLogin: (id) => post('StartGitLabLogin', { id }),
  loginWithPassword: (id, login, password) => post('LoginWithPassword', { id, login, password }),
  logout: (id) => post('Logout', { id }),
  subscribeEvents(onEvent) {
    const es = new EventSource(`/api/events?token=${encodeURIComponent(tokenMeta())}`)
    es.onmessage = (m) => onEvent(JSON.parse(m.data) as ApiEvent)
    return () => es.close()
  },
}

// Go returns CodedError whose text is "<code>: <detail>" (or just "<code>").
export function parseWailsError(err: unknown): ApiError {
  const msg = err instanceof Error ? err.message : String((err as { message?: string })?.message ?? err)
  const i = msg.indexOf(': ')
  return i < 0 ? new ApiError(msg, '') : new ApiError(msg.slice(0, i), msg.slice(i + 2))
}

const FQN = 'github.com/spk/spk-mattermost/internal/api/transport.API.'

async function wcall<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    return (await Call.ByName(FQN + method, ...args)) as T
  } catch (e) {
    throw parseWailsError(e)
  }
}

const EVENT_TYPES = ['servers_changed', 'login_failed', 'open_external'] as const

export const wailsClient: Client = {
  listServers: () => wcall('ListServers'),
  addServer: (url) => wcall('AddServer', url),
  removeServer: (id) => wcall('RemoveServer', id),
  startGitLabLogin: (id) => wcall('StartGitLabLogin', id),
  loginWithPassword: (id, login, password) => wcall('LoginWithPassword', id, login, password),
  logout: (id) => wcall('Logout', id),
  subscribeEvents(onEvent) {
    const offs = EVENT_TYPES.map((type) =>
      Events.On(type, (ev: { data: unknown }) => {
        // Emit(name, payload) arrives as data=payload; tolerate a 1-element array.
        const d = Array.isArray(ev.data) && ev.data.length === 1 ? ev.data[0] : ev.data
        onEvent({ type, payload: (d ?? undefined) as Record<string, unknown> | undefined })
      }),
    )
    return () => offs.forEach((off) => off())
  },
}

export const isDesktop = () => window.location.protocol === 'wails:'
export const client: Client = isDesktop() ? wailsClient : httpClient
```

`frontend/src/errors.ts`:
```ts
import { ApiError } from './api/client'
import { t, type I18nKey } from './i18n'

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const key = `err.${err.code}` as I18nKey
    const text = t(key)
    if (text !== key) return text
    return t('err.generic', { detail: err.code })
  }
  return t('err.generic', { detail: err instanceof Error ? err.message : String(err) })
}
```

`frontend/src/store.ts`:
```ts
import { create } from 'zustand'
import type { ServerDTO } from './api/types'

interface State {
  servers: ServerDTO[]
  selectedId: number | null // null = "add server" screen
  lastError: string | null
  setServers(list: ServerDTO[]): void
  select(id: number | null): void
  setError(msg: string | null): void
}

export const useStore = create<State>((set, get) => ({
  servers: [],
  selectedId: null,
  lastError: null,
  setServers(list) {
    const sel = get().selectedId
    const stillThere = sel !== null && list.some((s) => s.id === sel)
    set({ servers: list, selectedId: stillThere ? sel : (list[0]?.id ?? null) })
  },
  select: (id) => set({ selectedId: id, lastError: null }),
  setError: (msg) => set({ lastError: msg }),
}))
```

- [ ] **Step 5: Run — PASS для i18n/errors/client** (`cd frontend && pnpm test`)

- [ ] **Step 6: Падающие тесты компонентов**

`frontend/src/components/AddServerForm.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { ApiError } from '../api/client'
import { setLocale } from '../i18n'
import { AddServerForm } from './AddServerForm'

beforeEach(() => setLocale('en'))

test('submits the URL and reports success', async () => {
  const add = vi.fn().mockResolvedValue({ id: 3 })
  const onAdded = vi.fn()
  render(<AddServerForm add={add} onAdded={onAdded} />)
  await userEvent.type(screen.getByLabelText('Server address'), 'mm.example.com')
  await userEvent.click(screen.getByRole('button', { name: 'Add' }))
  expect(add).toHaveBeenCalledWith('mm.example.com')
  expect(onAdded).toHaveBeenCalledWith(3)
})

test('shows a localized error', async () => {
  const add = vi.fn().mockRejectedValue(new ApiError('not_mattermost', ''))
  render(<AddServerForm add={add} onAdded={vi.fn()} />)
  await userEvent.type(screen.getByLabelText('Server address'), 'example.com')
  await userEvent.click(screen.getByRole('button', { name: 'Add' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('This address is not a Mattermost server')
})
```

`frontend/src/components/ServerPanel.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { Client } from '../api/client'
import { ApiError } from '../api/client'
import { setLocale } from '../i18n'
import { ServerPanel } from './ServerPanel'

const base = { id: 1, name: 'Acme', url: 'https://mm.acme', signed_in: false, username: '', gitlab: true }

function fakeClient(over: Partial<Client> = {}): Client {
  return {
    listServers: vi.fn(),
    addServer: vi.fn(),
    removeServer: vi.fn().mockResolvedValue(undefined),
    startGitLabLogin: vi.fn().mockResolvedValue(undefined),
    loginWithPassword: vi.fn().mockResolvedValue({ ...base, signed_in: true, username: 'alice' }),
    logout: vi.fn().mockResolvedValue(undefined),
    subscribeEvents: vi.fn(),
    ...over,
  } as Client
}

beforeEach(() => setLocale('en'))

test('signed-out server offers GitLab and password login', async () => {
  const c = fakeClient()
  render(<ServerPanel server={base} client={c} />)
  await userEvent.click(screen.getByRole('button', { name: 'Sign in with GitLab' }))
  expect(c.startGitLabLogin).toHaveBeenCalledWith(1)
  expect(screen.getByText('Finish signing in in the browser window')).toBeInTheDocument()

  await userEvent.type(screen.getByLabelText('Login or email'), 'alice')
  await userEvent.type(screen.getByLabelText('Password'), 'secret')
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
  expect(c.loginWithPassword).toHaveBeenCalledWith(1, 'alice', 'secret')
})

test('hides GitLab button when disabled on server', () => {
  render(<ServerPanel server={{ ...base, gitlab: false }} client={fakeClient()} />)
  expect(screen.queryByRole('button', { name: 'Sign in with GitLab' })).toBeNull()
})

test('signed-in server shows the user and signs out', async () => {
  const c = fakeClient()
  render(<ServerPanel server={{ ...base, signed_in: true, username: 'alice' }} client={c} />)
  expect(screen.getByText('Signed in as alice')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Sign out' }))
  expect(c.logout).toHaveBeenCalledWith(1)
})

test('wrong password shows localized error', async () => {
  const c = fakeClient({ loginWithPassword: vi.fn().mockRejectedValue(new ApiError('bad_credentials', '')) })
  render(<ServerPanel server={base} client={c} />)
  await userEvent.type(screen.getByLabelText('Login or email'), 'alice')
  await userEvent.type(screen.getByLabelText('Password'), 'x')
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Wrong login or password')
})
```

- [ ] **Step 7: Run — FAIL** (`pnpm test` — компонентов нет)

- [ ] **Step 8: Реализация компонентов и App**

`frontend/src/components/AddServerForm.tsx`:
```tsx
import { useState } from 'react'
import type { ServerDTO } from '../api/types'
import { errorMessage } from '../errors'
import { t } from '../i18n'

export function AddServerForm(props: { add: (url: string) => Promise<Pick<ServerDTO, 'id'>>; onAdded: (id: number) => void }) {
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const s = await props.add(url)
      props.onAdded(s.id)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="mx-auto mt-24 flex w-96 flex-col gap-3">
      <h1 className="text-lg font-semibold">{t('add.title')}</h1>
      <label className="flex flex-col gap-1 text-sm">
        {t('add.urlLabel')}
        <input
          className="rounded border border-neutral-300 px-2 py-1.5"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder={t('add.urlPlaceholder')}
          autoFocus
        />
      </label>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <button type="submit" disabled={busy || !url.trim()} className="rounded bg-blue-600 px-3 py-1.5 text-white disabled:opacity-50">
        {busy ? t('add.checking') : t('add.submit')}
      </button>
    </form>
  )
}
```

`frontend/src/components/ServerPanel.tsx`:
```tsx
import { useState } from 'react'
import type { Client } from '../api/client'
import type { ServerDTO } from '../api/types'
import { errorMessage } from '../errors'
import { t } from '../i18n'

export function ServerPanel({ server, client }: { server: ServerDTO; client: Client }) {
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [waitingGitLab, setWaitingGitLab] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const run = async (fn: () => Promise<unknown>) => {
    setError(null)
    try {
      await fn()
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  return (
    <section className="mx-auto mt-16 flex w-[28rem] flex-col gap-4">
      <header>
        <h1 className="text-xl font-semibold">{server.name}</h1>
        <p className="text-sm text-neutral-500">{server.url}</p>
      </header>
      {server.signed_in ? (
        <div className="flex items-center justify-between">
          <span>{t('server.signedInAs', { name: server.username })}</span>
          <button className="rounded border px-3 py-1" onClick={() => run(() => client.logout(server.id))}>
            {t('server.signOut')}
          </button>
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <p className="text-sm text-neutral-500">{t('server.signedOut')}</p>
          {server.gitlab && (
            <>
              <button
                className="rounded bg-orange-600 px-3 py-1.5 text-white"
                onClick={() => run(async () => { await client.startGitLabLogin(server.id); setWaitingGitLab(true) })}
              >
                {t('server.gitlab')}
              </button>
              {waitingGitLab && <p className="text-sm text-neutral-600">{t('server.gitlabWaiting')}</p>}
              <p className="text-center text-xs text-neutral-400">{t('server.orPassword')}</p>
            </>
          )}
          <form
            className="flex flex-col gap-2"
            onSubmit={(e) => { e.preventDefault(); void run(() => client.loginWithPassword(server.id, login, password)) }}
          >
            <label className="flex flex-col gap-1 text-sm">
              {t('server.login')}
              <input className="rounded border border-neutral-300 px-2 py-1.5" value={login} onChange={(e) => setLogin(e.target.value)} />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t('server.password')}
              <input type="password" className="rounded border border-neutral-300 px-2 py-1.5" value={password} onChange={(e) => setPassword(e.target.value)} />
            </label>
            <button type="submit" disabled={!login || !password} className="rounded bg-blue-600 px-3 py-1.5 text-white disabled:opacity-50">
              {t('server.signIn')}
            </button>
          </form>
        </div>
      )}
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <button
        className="self-start text-sm text-red-600 underline"
        onClick={() => { if (confirm(t('server.removeConfirm', { name: server.name }))) void run(() => client.removeServer(server.id)) }}
      >
        {t('server.remove')}
      </button>
    </section>
  )
}
```

`frontend/src/components/ServerRail.tsx`:
```tsx
import type { ServerDTO } from '../api/types'
import { t } from '../i18n'

export function ServerRail(props: { servers: ServerDTO[]; selectedId: number | null; onSelect: (id: number | null) => void }) {
  return (
    <nav className="flex w-16 flex-col items-center gap-2 bg-neutral-800 py-3">
      {props.servers.map((s) => (
        <button
          key={s.id}
          title={s.name}
          aria-label={s.name}
          aria-current={s.id === props.selectedId}
          onClick={() => props.onSelect(s.id)}
          className={`h-11 w-11 rounded-xl text-sm font-semibold text-white ${s.id === props.selectedId ? 'bg-blue-600' : 'bg-neutral-600'} ${s.signed_in ? '' : 'opacity-60'}`}
        >
          {s.name.slice(0, 2).toUpperCase()}
        </button>
      ))}
      <button
        title={t('rail.add')}
        aria-label={t('rail.add')}
        onClick={() => props.onSelect(null)}
        className="h-11 w-11 rounded-xl border border-dashed border-neutral-500 text-xl text-neutral-300"
      >
        +
      </button>
    </nav>
  )
}
```

`frontend/src/App.tsx`:
```tsx
import { useEffect } from 'react'
import { client, isDesktop } from './api/client'
import { AddServerForm } from './components/AddServerForm'
import { ServerPanel } from './components/ServerPanel'
import { ServerRail } from './components/ServerRail'
import { errorMessage } from './errors'
import { ApiError } from './api/client'
import { useStore } from './store'

export function App() {
  const { servers, selectedId, lastError, setServers, select, setError } = useStore()

  useEffect(() => {
    const refresh = () => client.listServers().then(setServers).catch((e) => setError(errorMessage(e)))
    refresh()
    return client.subscribeEvents((ev) => {
      if (ev.type === 'servers_changed') refresh()
      if (ev.type === 'login_failed') setError(errorMessage(new ApiError(String(ev.payload?.code ?? 'internal'), '')))
      if (ev.type === 'open_external' && !isDesktop()) window.open(String(ev.payload?.url), '_blank')
    })
  }, [setServers, setError])

  const selected = servers.find((s) => s.id === selectedId)
  return (
    <div className="flex h-screen">
      <ServerRail servers={servers} selectedId={selectedId} onSelect={select} />
      <main className="flex-1 overflow-auto">
        {lastError && (
          <p role="alert" className="bg-red-50 px-4 py-2 text-sm text-red-700">
            {lastError}
          </p>
        )}
        {selected ? (
          <ServerPanel key={selected.id} server={selected} client={client} />
        ) : (
          <AddServerForm add={client.addServer} onAdded={select} />
        )}
      </main>
    </div>
  )
}
```

`frontend/src/main.tsx`:
```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
```

- [ ] **Step 9: Проверить API рантайма Wails**

Run: `cd frontend && grep -n "export" node_modules/@wailsio/runtime/types/index.d.ts | head -40 && grep -rn "ByName" node_modules/@wailsio/runtime/types/calls.d.ts && grep -rn "function On\b\|export declare function On" node_modules/@wailsio/runtime/types/events.d.ts`
Expected: экспортируются `Call` (с `ByName`) и `Events` (с `On`, возвращает функцию отписки). Если сигнатуры отличаются — поправить `wailsClient` в `client.ts` под фактические и зафиксировать это в AGENTS.md («Things that bite»).

- [ ] **Step 10: Run — PASS** (`pnpm test && pnpm lint && pnpm build`)
Expected: все тесты зелёные, lint без ошибок, `frontend/dist/index.html` создан.

- [ ] **Step 11: Commit**

```bash
cd .. && git add frontend && git commit -m "feat(ui): server list, add-server and sign-in screens with ru/en i18n"
```

---

### Task 12: E2E (Playwright) против browser-режима

**Files:**
- Create: `tests/e2e/package.json`, `tests/e2e/playwright.config.ts`, `tests/e2e/login.spec.ts`

**Interfaces:**
- Consumes: бинарь `build/bin/spk-mattermost` (Task 10) с `--browser --mm-fake --test-api`; test-API; контракт mmfake (`#authorize`, `#mmauth-link`).

- [ ] **Step 1: Конфиг**

`tests/e2e/package.json`:
```json
{
  "name": "spk-mattermost-e2e",
  "private": true,
  "devDependencies": { "@playwright/test": "^1.58.0" }
}
```

`tests/e2e/playwright.config.ts`:
```ts
import { defineConfig } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const port = Number(process.env.E2E_PORT ?? 5181)
const home = mkdtempSync(join(tmpdir(), 'spk-mm-e2e-'))

export default defineConfig({
  testDir: '.',
  workers: 1, // one app instance, shared DB — tests clean up after themselves
  use: { baseURL: `http://127.0.0.1:${port}`, locale: 'en-US' },
  webServer: {
    command: `../../build/bin/spk-mattermost --browser --port ${port} --mm-fake --test-api`,
    url: `http://127.0.0.1:${port}/`,
    env: { SPK_MATTERMOST_HOME: home },
    reuseExistingServer: false,
  },
})
```

- [ ] **Step 2: Тесты**

`tests/e2e/login.spec.ts`:
```ts
import { expect, test, type Page } from '@playwright/test'

async function apiToken(page: Page) {
  return (await page.locator('meta[name="spk-mattermost-api-token"]').getAttribute('content'))!
}

async function fakeURL(page: Page) {
  const r = await page.request.get('/api/_test/fake-url', { headers: { Authorization: `Bearer ${await apiToken(page)}` } })
  return ((await r.json()) as { url: string }).url
}

async function addFakeServer(page: Page) {
  await page.goto('/')
  const url = await fakeURL(page)
  await page.getByLabel('Server address').fill(url)
  await page.getByRole('button', { name: 'Add' }).click()
  await expect(page.getByRole('heading', { name: 'Fake MM' })).toBeVisible()
}

async function removeServer(page: Page) {
  page.once('dialog', (d) => d.accept())
  await page.getByRole('button', { name: 'Remove server' }).click()
  await expect(page.getByRole('heading', { name: 'Add a Mattermost server' })).toBeVisible()
}

test('invalid address shows an error', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('Server address').fill('ftp://nope')
  await page.getByRole('button', { name: 'Add' }).click()
  await expect(page.getByRole('alert')).toHaveText('Invalid server address')
})

test('password sign-in and sign-out', async ({ page }) => {
  await addFakeServer(page)
  await page.getByLabel('Login or email').fill('alice')
  await page.getByLabel('Password').fill('wrong')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('Wrong login or password')

  await page.getByLabel('Password').fill('secret')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByText('Signed in as alice')).toBeVisible()

  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByText('Not signed in')).toBeVisible()
  await removeServer(page)
})

test('GitLab SSO through mmauth:// callback', async ({ page, context }) => {
  await addFakeServer(page)
  const popupPromise = context.waitForEvent('page')
  await page.getByRole('button', { name: 'Sign in with GitLab' }).click()
  const popup = await popupPromise
  await popup.getByText('Fake GitLab').waitFor()
  await popup.locator('#authorize').click()
  const href = await popup.locator('#mmauth-link').getAttribute('href')
  expect(href).toMatch(/^mmauth:\/\/callback\?/)
  await popup.close()

  // In the desktop app the OS hands this URL to us; here we deliver it
  // through the test API exactly as the single-instance handler would.
  const r = await page.request.post('/api/_test/deeplink', {
    headers: { Authorization: `Bearer ${await apiToken(page)}`, Origin: new URL(page.url()).origin },
    data: { url: href },
  })
  expect(r.ok()).toBeTruthy()
  await expect(page.getByText('Signed in as alice')).toBeVisible()

  // second delivery of the same callback must not surface an error
  await page.request.post('/api/_test/deeplink', {
    headers: { Authorization: `Bearer ${await apiToken(page)}`, Origin: new URL(page.url()).origin },
    data: { url: href },
  })
  await expect(page.getByRole('alert')).toHaveCount(0)
  await removeServer(page)
})
```

- [ ] **Step 3: Run**

Run: `make test-e2e`
Expected: 3 passed. Если popup не открылся — проверить обработку `open_external` в `App.tsx` и что SSE подписка активна до клика (при необходимости ждать `page.waitForResponse('**/api/events**')` в `addFakeServer`).

- [ ] **Step 4: Commit**

```bash
git add tests/e2e && git commit -m "test(e2e): add server, password login and GitLab SSO via mmauth callback"
```

---

### Task 13: Desktop — окно, трей, single-instance, deep link, уведомления (S2)

**Files:**
- Create: `internal/appfiles/embed.go`, `internal/appfiles/icons/icon.png` (генерируется), `scripts/gen-icon.go`, `internal/desktop/run.go`, `internal/desktop/tray.go`, `internal/desktop/notify.go`, `internal/desktop/devtools_dev.go`, `internal/desktop/devtools_prod.go`, `cmd/spk-mattermost/run_desktop_wails.go`
- Test: `internal/desktop/deeplink_test.go` (без тега — чистая логика), `internal/desktop/deeplink.go`

**Interfaces:**
- Consumes: `api.Service` (`HandleDeepLink`), `transport.NewAPI`, `events.Emitter`, `auth.FindCallbackArg`.
- Produces: `func Run(ctx context.Context, o Options) error` (tag `wails`); `type Options struct{ FrontendFS fs.FS; Service *api.Service; Emitter *events.Emitter; IconPNG []byte }`; `func deepLinkFromArgs(args []string) (string, bool)` (без тега); `const UniqueID = "ru.spk.spk-mattermost"`.

- [ ] **Step 1: Зависимость Wails и иконка**

```bash
go get github.com/wailsapp/wails/v3@v3.0.0-beta.25
```

`scripts/gen-icon.go`:
```go
//go:build ignore

// gen-icon draws the app icon (blue rounded square, white speech bubble) so
// the repo needs no binary design assets. Run: go run scripts/gen-icon.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	const n = 256
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	blue := color.NRGBA{0x1e, 0x5e, 0xd8, 0xff}
	white := color.NRGBA{0xff, 0xff, 0xff, 0xff}
	inRounded := func(x, y, x0, y0, x1, y1, r int) bool {
		if x < x0 || x >= x1 || y < y0 || y >= y1 {
			return false
		}
		cx, cy := x, y
		if x < x0+r {
			cx = x0 + r
		} else if x >= x1-r {
			cx = x1 - r - 1
		}
		if y < y0+r {
			cy = y0 + r
		} else if y >= y1-r {
			cy = y1 - r - 1
		}
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= r*r
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			switch {
			case inRounded(x, y, 60, 64, 196, 164, 28):
				img.Set(x, y, white)
			case x >= 90 && x < 130 && y >= 160 && y < 200 && (x-90) <= (200-y):
				img.Set(x, y, white) // bubble tail
			case inRounded(x, y, 8, 8, n-8, n-8, 48):
				img.Set(x, y, blue)
			}
		}
	}
	f, err := os.Create("internal/appfiles/icons/icon.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
```

```bash
mkdir -p internal/appfiles/icons && go run scripts/gen-icon.go
```

`internal/appfiles/embed.go`:
```go
// Package appfiles embeds static app assets (icons).
package appfiles

import _ "embed"

//go:embed icons/icon.png
var IconPNG []byte
```

- [ ] **Step 2: Падающий тест чистой логики deep link**

`internal/desktop/deeplink_test.go`:
```go
package desktop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeepLinkFromArgs(t *testing.T) {
	u, ok := deepLinkFromArgs([]string{"mmauth://callback?MMAUTHTOKEN=x"})
	assert.True(t, ok)
	assert.Equal(t, "mmauth://callback?MMAUTHTOKEN=x", u)

	// Windows passes the URL quoted in some launchers
	u, ok = deepLinkFromArgs([]string{`"mmauth://callback?MMAUTHTOKEN=y"`})
	assert.True(t, ok)
	assert.Equal(t, "mmauth://callback?MMAUTHTOKEN=y", u)

	_, ok = deepLinkFromArgs([]string{"--browser"})
	assert.False(t, ok)
}
```

Run: `go test ./internal/desktop/` → FAIL (`undefined: deepLinkFromArgs`).

- [ ] **Step 3: Реализация чистой логики**

`internal/desktop/deeplink.go` (без build-тега):
```go
// Package desktop runs the Wails window, tray and OS integration. Only this
// file builds without the `wails` tag, so its logic is testable everywhere.
package desktop

import (
	"strings"

	"github.com/spk/spk-mattermost/internal/auth"
)

// UniqueID identifies the app for Wails single-instance locking.
const UniqueID = "ru.spk.spk-mattermost"

// deepLinkFromArgs finds an mmauth:// callback among process args, tolerating
// surrounding quotes some Windows launchers keep.
func deepLinkFromArgs(args []string) (string, bool) {
	clean := make([]string, 0, len(args))
	for _, a := range args {
		clean = append(clean, strings.Trim(a, `"'`))
	}
	return auth.FindCallbackArg(clean)
}
```

Run: `go test ./internal/desktop/` → PASS.

- [ ] **Step 4: Desktop runner (tag `wails`)**

`internal/desktop/devtools_dev.go`:
```go
//go:build wails && !production

package desktop

// Dev builds keep DevTools and the "test notification" tray item.
const (
	devToolsEnabled = true
	devMenu         = true
)
```

`internal/desktop/devtools_prod.go`:
```go
//go:build wails && production

package desktop

const (
	devToolsEnabled = false
	devMenu         = false
)
```

`internal/desktop/notify.go`:
```go
//go:build wails

package desktop

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// notifier wraps the Wails notification service (spike S2). onClick receives
// the Data map of the clicked notification.
type notifier struct {
	svc *notifications.NotificationService
}

func newNotifier(onClick func(data map[string]any)) *notifier {
	svc := notifications.New()
	svc.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			slog.Warn("notification response error", "err", r.Error)
			return
		}
		onClick(r.Response.UserInfo)
	})
	return &notifier{svc: svc}
}

func (n *notifier) test() {
	id := fmt.Sprintf("test-%d", time.Now().UnixNano())
	err := n.svc.SendNotification(notifications.NotificationOptions{
		ID:    id,
		Title: "spk-mattermost",
		Body:  "Тестовое уведомление — кликните, чтобы открыть окно",
		Data:  map[string]any{"target": "test", "id": id},
	})
	if err != nil {
		slog.Warn("test notification failed", "err", err)
	}
}
```

`internal/desktop/tray.go`:
```go
//go:build wails

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

func setupTray(app *application.App, icon []byte, show func(), toggle func(), n *notifier) {
	menu := app.NewMenu()
	menu.Add("Открыть").OnClick(func(*application.Context) { show() })
	if devMenu {
		menu.Add("Тестовое уведомление").OnClick(func(*application.Context) { n.test() })
	}
	menu.AddSeparator()
	menu.Add("Выход").OnClick(func(*application.Context) { app.Quit() })

	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("spk-mattermost")
	tray.SetMenu(menu)
	tray.OnClick(toggle)
}
```

`internal/desktop/run.go`:
```go
//go:build wails

package desktop

import (
	"context"
	"io/fs"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/api/transport"
	mmevents "github.com/spk/spk-mattermost/internal/events"
)

type Options struct {
	FrontendFS fs.FS
	Service    *api.Service
	Emitter    *mmevents.Emitter
	IconPNG    []byte
}

// Run starts the Wails loop. Closing the window hides it (tray brings it
// back); quitting is via the tray or ctx cancellation.
func Run(ctx context.Context, o Options) error {
	var win *application.WebviewWindow
	show := func() {
		if win == nil {
			return
		}
		win.Show()
		win.Focus()
	}
	deliver := func(raw string) {
		// HandleDeepLink reports failures to the UI via login_failed.
		go func() { _ = o.Service.HandleDeepLink(context.Background(), raw) }()
		show()
	}

	var n *notifier
	n = newNotifier(func(data map[string]any) {
		slog.Info("notification clicked", "data", data)
		show()
	})

	app := application.New(application.Options{
		Name:        "spk-mattermost",
		Description: "Lightweight Mattermost client",
		Icon:        o.IconPNG,
		Services: []application.Service{
			application.NewService(transport.NewAPI(o.Service)),
			application.NewService(n.svc),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(o.FrontendFS)},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: UniqueID,
			// Linux/Windows: the OS starts a second process with the
			// mmauth:// URL; Wails forwards its args here and exits it.
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				if u, ok := deepLinkFromArgs(d.Args); ok {
					deliver(u)
					return
				}
				show()
			},
		},
	})

	// Cold start with the URL (Linux/Windows argv) and macOS open-URL events.
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		if u := e.Context().URL(); u != "" {
			deliver(u)
		}
	})

	go func() {
		ch, unsub := o.Emitter.Subscribe()
		defer unsub()
		for ev := range ch {
			app.Event.Emit(ev.Type, ev.Payload)
		}
	}()

	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "spk-mattermost",
		Width:            1200,
		Height:           800,
		BackgroundColour: application.NewRGBA(250, 250, 250, 255),
		URL:              "/",
		DevToolsEnabled:  devToolsEnabled,
	})
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		win.Hide()
	})

	toggle := func() {
		if win.IsVisible() {
			win.Hide()
		} else {
			show()
		}
	}
	setupTray(app, o.IconPNG, show, toggle, n)

	go func() {
		<-ctx.Done()
		app.Quit()
	}()
	return app.Run()
}
```

`cmd/spk-mattermost/run_desktop_wails.go`:
```go
//go:build wails

package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/appfiles"
	"github.com/spk/spk-mattermost/internal/desktop"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/paths"
	"github.com/spk/spk-mattermost/internal/store"
)

func runDesktop(ctx context.Context) error {
	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	st, err := store.Open(ctx, p.DBFile)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer st.Close()

	em := events.NewEmitter()
	open := func(u string) error { return application.Get().Browser.OpenURL(u) }
	svc := api.NewService(st, em, open, &http.Client{Timeout: 30 * time.Second})
	return desktop.Run(ctx, desktop.Options{
		FrontendFS: frontendFS(),
		Service:    svc,
		Emitter:    em,
		IconPNG:    appfiles.IconPNG,
	})
}
```

- [ ] **Step 5: Сборка и проверка сигнатур Wails**

Run: `make build-desktop && make cross-check && go build ./... && go test ./...`
Expected: всё собирается. Если компилятор укажет на расхождение API беты (например, `e.Context().URL()`, `application.Get().Browser`, `menu.Add(...).OnClick` сигнатура) — свериться с исходниками `$(go env GOMODCACHE)/github.com/wailsapp/wails/v3@v3.0.0-beta.25/pkg/application/` и поправить; расхождение записать в AGENTS.md («Things that bite»).

- [ ] **Step 6: Ручная проверка desktop на Linux**

Run: `SPK_MATTERMOST_HOME=$(mktemp -d) build/bin/spk-mattermost-desktop &`
Проверить и снять скриншот окна (`import -window root /tmp/spk-mm-desktop.png` или аналог):
- окно открылось, экран «Добавить сервер Mattermost»;
- закрытие окна прячет его, клик по трею возвращает;
- второй запуск `build/bin/spk-mattermost-desktop` не открывает второе окно, а поднимает первое;
- пункт трея «Тестовое уведомление» показывает уведомление; клик по нему поднимает окно (в логе `notification clicked`). Это спайк **S2** на Linux.

- [ ] **Step 7: Commit**

```bash
git add internal/appfiles internal/desktop cmd scripts/gen-icon.go go.mod go.sum
git commit -m "feat(desktop): window, tray, single-instance deep links and notification spike"
```

---

### Task 14: Спайки на реальной системе, замер памяти, документация

**Files:**
- Create: `scripts/install-dev-linux.sh`, `scripts/pss.sh`, `docs/spikes/2026-09-24-stage1-spikes.md`, `README.md`
- Modify: `AGENTS.md`, `docs/specs/2026-09-24-spk-mattermost-design.md` (статус, результаты спайков)

- [ ] **Step 1: Скрипт регистрации `mmauth://` для dev на Linux**

`scripts/install-dev-linux.sh`:
```bash
#!/usr/bin/env bash
# Registers a dev build as the mmauth:// handler for the current user.
# Usage: scripts/install-dev-linux.sh /abs/path/to/spk-mattermost-desktop
set -euo pipefail
bin="${1:?path to desktop binary}"
apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$apps"
cat > "$apps/spk-mattermost.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=spk-mattermost
Exec=$bin %u
Terminal=false
Categories=Network;Chat;
MimeType=x-scheme-handler/mmauth;
EOF
update-desktop-database "$apps" 2>/dev/null || true
xdg-mime default spk-mattermost.desktop x-scheme-handler/mmauth
echo "mmauth:// -> $(xdg-mime query default x-scheme-handler/mmauth)"
```

- [ ] **Step 2: Скрипт замера памяти**

`scripts/pss.sh`:
```bash
#!/usr/bin/env bash
# Sums PSS (MB) of a process and all its descendants (Linux).
# Usage: scripts/pss.sh <pid>
set -euo pipefail
root="${1:?pid}"
pids="$root"
frontier="$root"
while [ -n "$frontier" ]; do
  next=""
  for p in $frontier; do
    kids=$(cat /proc/"$p"/task/*/children 2>/dev/null || true)
    next="$next $kids"
  done
  frontier=$(echo $next)
  pids="$pids $frontier"
done
total=0
for p in $pids; do
  kb=$(awk '/^Pss:/{print $2}' /proc/"$p"/smaps_rollup 2>/dev/null || echo 0)
  printf "%8.1f MB  %s %s\n" "$(echo "$kb/1024" | bc -l)" "$p" "$(tr '\0' ' ' < /proc/"$p"/cmdline | cut -c1-80)"
  total=$((total + kb))
done
printf "TOTAL PSS: %.1f MB\n" "$(echo "$total/1024" | bc -l)"
```

```bash
chmod +x scripts/*.sh
```

- [ ] **Step 3: Спайк S1 — реальный вход через GitLab**

1. `make install-dev-linux` — проверить вывод `mmauth:// -> spk-mattermost.desktop`.
2. Запустить `build/bin/spk-mattermost-desktop` (с реальным `~/.spk/spk-mattermost`).
3. Добавить сервер пользователя (URL из `~/.config/Mattermost/config.json` официального клиента), нажать «Войти через GitLab».
4. **Вход в GitLab выполняет пользователь** в открывшемся браузере (единственный ручной шаг — агент не вводит чужие учётные данные).
5. Проверить: браузер передаёт `mmauth://` приложению, окно показывает «Вы вошли как …»; `sqlite3 ~/.spk/spk-mattermost/db.sqlite "select id,name,username,length(token) from servers"` — токен сохранён.
6. Перезапустить приложение — сессия сохранена. «Выйти» — сессия отозвана (в Mattermost → Профиль → Безопасность → Активные сессии её нет).

- [ ] **Step 4: Спайк S4 — память**

Run: `make pss PID=$(pgrep -n -f 'build/bin/[s]pk-mattermost-desktop')`
Записать TOTAL PSS (оболочка без чата) — ориентир для бюджета 150 МБ.

- [ ] **Step 5: Результаты спайков**

`docs/spikes/2026-09-24-stage1-spikes.md` — заполнить фактическими результатами:
```markdown
# Спайки этапа 1 — результаты

Дата прогона: <дата>. Машина: Linux Mint (Cinnamon), webkit2gtk-4.1.

## S1. Вход через mmauth:// (Linux)
- Регистрация схемы: <работает / нет, детали>.
- Доставка URL во второй экземпляр: <результат>.
- Вход на реальный сервер (Mattermost 10.11.22): <результат, скриншот>.
- Windows/macOS: не проверено (нет машин) — проверяется в этапе 4 (CI + ручная проверка).

## S2. Уведомления Wails v3 (Linux)
- Показ: <результат>. Клик → окно: <результат>. Решение: <используем сервис Wails / свой D-Bus>.

## S3. Особенности Wails beta.25, найденные при сборке
- <список расхождений API/поведения, или «нет»>

## S4. Память оболочки
- TOTAL PSS: <N> МБ (окно + трей, без чата).
```

- [ ] **Step 6: README и AGENTS.md**

`README.md`:
```markdown
# spk-mattermost

Лёгкий десктопный клиент Mattermost (Linux, Windows, macOS) на Wails v3 + React.
Спецификация — `docs/specs/2026-09-24-spk-mattermost-design.md`.

## Разработка

    make build          # фронт + бинарь (browser-режим)
    make run-browser    # http://127.0.0.1:5180 с фейковым сервером
    make run            # desktop
    make test           # go + фронт + e2e
    make install-dev-linux  # зарегистрировать dev-сборку для mmauth:// (Linux)

Данные: `~/.spk/spk-mattermost/` (переопределяется `SPK_MATTERMOST_HOME`).
```

В `AGENTS.md` добавить раздел «Things that bite» с найденным в Task 11 Step 9, Task 13 Step 5 и в спайках (если ничего — не добавлять пустой раздел).

- [ ] **Step 7: Обновить спецификацию**

В `docs/specs/2026-09-24-spk-mattermost-design.md`: статус → «Этап 1 выполнен»; в «Риски и спайки» у S1/S2/S4 — ссылка на `docs/spikes/2026-09-24-stage1-spikes.md` и итог одной строкой; зафиксировать выбор Wails `v3.0.0-beta.25` (вместо альфы из соседних проектов).

- [ ] **Step 8: Финальная проверка**

Run: `make lint && make test && make cross-check`
Expected: всё зелёное.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "docs: stage 1 spike results, dev scripts and README"
```
