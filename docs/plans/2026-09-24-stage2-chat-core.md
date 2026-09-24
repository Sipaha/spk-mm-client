# spk-mattermost — Этап 2: ядро чата. План реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Клиентом можно пользоваться каждый день вместо официального: он синхронизирует серверы в фоне, показывает команды и каналы с непрочитанными и упоминаниями, открывает канал мгновенно из памяти, отправляет, правит и удаляет сообщения, отмечает прочитанное, показывает уведомления и сам переживает обрывы сети и сон.

**Architecture:** На каждый сервер с выполненным входом работает воркер (`internal/mmsync`). Он проверяет токен через REST, держит один WebSocket с resume по `connection_id`/`sequence_number`, при потере потока делает ресинк метаданных и догоняет посты через `since`. Все данные лежат в горячем слое в памяти Go (`internal/state`): метаданные и окно из последних 60 постов на канал. UI читает готовые представления (сайдбар, канал) через `api.API` и получает события `*_changed` со схлопыванием. Изменения каждые 3 с одной транзакцией сбрасываются в SQLite как снимок для холодного старта. Решение «уведомлять или нет» — чистая функция по правилам сервера (`internal/notify`).

**Tech Stack:** Go 1.26, Wails v3.0.0-beta.25, `github.com/coder/websocket` v1.8.14, `golang.org/x/time/rate`, modernc.org/sqlite, testify; React 19, TypeScript 6, Vite 8, Tailwind 4, Zustand 5, `@tanstack/react-virtual` 3, `react-markdown` 10 + `remark-gfm` 4, Vitest, Playwright.

**Spec:** `docs/specs/2026-09-24-spk-mattermost-design.md`. Проверенные факты API Mattermost 10.11 (поля, события, ловушки `since`, resume, правила непрочитанного и уведомлений): `docs/research/2026-09-24-mattermost-api-facts.md`. **Исполнитель каждой задачи читает оба документа.**

## Объём этапа

Приоритет пользователя — рабочий клиент, которым можно заменить официальный (2026-09-24). Поэтому в этап 2 из этапа 3 перенесены **уведомления** и **бейдж трея**: без них с официального клиента не уйти.

Входит: синхронизация и горячий слой, снимок для холодного старта, сайдбар (команды, категории сервера, DM/GM по правилам видимости, непрочитанные и упоминания, бейджи), лента (виртуализация, группировка, разделители дней, линия «новые», markdown GFM с подсветкой @упоминаний, текст вложений ботов, карточки файлов и чипы реакций — **только показ**, подгрузка истории вверх, индикатор дыры), редактор (отправка с pending/повтором, Enter / Shift+Enter, ↑ — правка последнего своего, черновики), действия с постом (правка, удаление, «отметить непрочитанным», ссылка), прочтение при фокусе, уведомления с кликом, бейдж и подсказка трея, состояния подключения и «Войти снова».

**Не входит (этап 3):** треды — панель, ответы; под CRT лента показывает только корневые посты со счётчиком ответов. Также не входят: ставить и снимать реакции, эмодзи-пикер, `:shortcode:`, загрузка и скачивание файлов, превью картинок, аватары (пока инициалы), поиск, автодополнение `@ ~ :`, подсветка кода, статусы присутствия и «печатает…», Ctrl+K. Язык UI — в `docs/backlog.md`.

## Global Constraints

- Go-модуль `github.com/spk/spk-mattermost`, Go `1.26`; Wails Go `v3.0.0-beta.25` = npm `@wailsio/runtime` `3.0.0-beta.25`.
- `go build ./...` и `go test ./...` без тега `wails` обязаны проходить; desktop-код — за тегом `wails` (Linux: `wails gtk3`).
- UI никогда не ходит к серверу Mattermost напрямую; токены не попадают в DTO, логи и события (`store.Server.LogValue` маскирует).
- Горячий слой: окно **60** последних постов на канал в памяти Go; вложения в памяти не держим; переключение канала читает только память.
- Снимок в SQLite — отложенная запись: не чаще раза в 3 с одной транзакцией и при остановке; никакой записи на каждое сообщение. Снимок — таблица `cache_entries` отдельно от `servers` («сбросить кэш» не разлогинивает).
- REST: ≤ 10 запросов/с на сервер (token bucket); POST не повторяются при сетевых ошибках; 429 — с `Retry-After`.
- WebSocket: авторизация заголовком `Authorization: Bearer`, **без** `authentication_challenge`; `sequence_number` = следующий ожидаемый seq.
- Машина состояний сервера: `off → connecting → live → reconnecting (1 с … 60 с, ×2) → needs_reauth`; пробуждение и «сеть появилась» — немедленная попытка.
- Никаких блокирующих вызовов системных сервисов на пути старта; у сетевых операций — таймауты.
- Память: Private_Dirty всех процессов ≤ 150 МБ с 2–3 серверами и ~100 каналами (`scripts/pss.sh <pid>`).
- UI: без UI-библиотеки и роутера; все строки — через i18n (`ru` и `en`, паритет ключей проверяется тестом).
- Коммиты — `Pavel Simonov <sipahabk@gmail.com>` (локальный git config репозитория), без `Co-Authored-By`, без amend; пуш в `main` разрешён.

## Review Focus

1. **Сообщение пришло во время стартовой синхронизации или ресинка.** Не должно быть дубля в ленте и двойного счётчика непрочитанного; ничего не теряется. Тесты: Task 7 `TestPostedDuringBootstrapNotDoubleCounted`, `TestPostedTwiceIsIdempotent`.
2. **Долгий офлайн: в канале больше 1000 изменений.** Окно заменяется последней страницей, индикатор дыры исчезает, строки истории правок (`original_id`) и удалённые посты не попадают в ленту. Тесты: Task 7 `TestMergeSinceDropsDeletedAndHistory`, Task 10 `TestCatchUpOverflowReloadsLatestPage`.
3. **Сессию отозвали, пока клиент работает** (401 на REST при переподключении). Сервер переходит в `needs_reauth`, кэш остаётся читаемым, нет цикла повторов. Тесты: Task 10 `TestRevokedSessionGoesNeedsReauth`; Task 12 `App.test` «needs_reauth keeps the chat and offers the sign-in form»; e2e «expired session: cached chat stays readable…».
4. **Эхо своего поста по WS пришло раньше ответа REST (или наоборот).** Ровно один пост, pending исчезает; при ошибке — «не отправлено» с повтором, а повтор с тем же `pending_post_id` не создаёт дубль. Тесты: Task 7 `TestPendingReplacedByEchoEitherOrder`, Task 10 `TestSendFailureThenRetry`.
5. **Переподключение с потерей и без потери буфера сервера.** Без потери — досылка без ресинка; с потерей (новый `connection_id` в hello) — ресинк и догонка, пропущенный пост появляется. Тесты: Task 5 `TestResumeReplaysMissedEvents`, `TestResumeAfterLossGetsResetHello`; Task 10 `TestReconnectWithLossCatchesUp`; e2e `reconnect.spec.ts`.

---

## File Structure

```
internal/
├── mm/model/model.go          # проводные типы Mattermost (Team, Channel, ChannelMember, User, Post, …)
├── mm/model/props.go          # терпимый разбор post.props (from_bot, override_username, attachments)
├── mm/rest/chat.go            # чтение: команды, каналы, члены, категории, preferences, статус, пользователи, посты
├── mm/rest/write.go           # запись: create/patch/delete post, view, set_unread, save preferences
├── mm/rest/client.go          # + лимитер запросов (WithLimiter)
├── mm/ws/conn.go              # WebSocket: Dial (Bearer), resume, проверка seq, keepalive
├── mm/ws/decode.go            # разбор data событий
├── mmfake/chat.go             # фейк: данные чата, REST-эндпоинты, управление из тестов
├── mmfake/seed.go             # начальные данные (команда, каналы, пользователи, посты)
├── mmfake/ws.go               # фейк WebSocket: hello, seq, dead queue, resume, рассылка
├── state/server.go            # горячий слой одного сервера: метаданные, непрочитанное
├── state/sidebar.go           # представление сайдбара (категории, видимость DM, сортировка)
├── state/posts.go             # окна постов, pending, черновики, история при просмотре
├── state/events.go            # применение WS-событий → Effects
├── state/view.go              # представление канала (ChannelView, PostView)
├── state/snapshot.go          # снимок ↔ записи cache_entries, dirty-трекинг
├── store/cache.go             # cache_entries: Load/Save/Clear
├── store/migrations/0002_cache.sql
├── notify/decide.go           # правила «уведомлять ли» (как веб-клиент)
├── mmsync/worker.go           # воркер сервера: FSM, сессия WS, bootstrap, применение событий
├── mmsync/fetch.go            # очередь догонки/предзагрузки с приоритетами
├── mmsync/actions.go          # действия пользователя: открыть канал, отправить, править, …
├── mmsync/wake.go             # детект сна по расхождению wall/monotonic
├── mmsync/manager.go          # воркеры всех серверов
├── events/coalesce.go         # схлопывание частых событий по ключу
├── api/api.go, api/service.go # + методы чата, DTO, коды ошибок, события; вход/выход запускают воркеры
├── api/sync.go                # запуск воркеров, хуки → события, фокус активного сервера, бейджи
├── api/chat.go                # методы чата сервиса
├── api/notifications.go       # решение + склейка уведомлений, Notifier
├── api/locale.go              # локаль форматов дат (LC_TIME)
├── api/transport/{http,wails}.go
├── appfiles/icons/icon-{unread,mention}.png  # иконки трея с точкой (scripts/gen-icon.go)
└── desktop/{run,notify,notifyclick,tray,traylabels}.go  # клик по уведомлению → канал; бейдж трея
cmd/spk-mattermost/{main.go,browser.go,run_desktop_wails.go}  # запуск/остановка воркеров, test-API фейка, dev-десктоп с фейком
frontend/src/
├── api/{client,types}.ts      # + методы и события чата
├── store.ts                   # сервер, сайдбар, канал, правка, повторный вход
├── chat.ts                    # контроллер: загрузки с защитой от устаревших ответов, действия
├── format.ts                  # время/дата (локаль форматов из Go), размеры
├── components/ServerRail.tsx  # + бейджи/состояние
├── components/Sidebar.tsx, glyph.ts  # команды, категории, строка состояния, меню сервера
├── components/ChannelPane.tsx # шапка + лента + редактор + баннеры
├── components/Feed.tsx, feedRows.ts   # виртуализированная лента и построение строк
├── components/PostItem.tsx, Markdown.tsx, remarkMentions.ts, emoji.ts
└── components/Composer.tsx
tests/e2e/{helpers.ts,chat.spec.ts,reconnect.spec.ts}
```

---

### Task 1: Модель Mattermost и REST-чтение с лимитером

**Files:**
- Create: `internal/mm/model/model.go`, `internal/mm/model/props.go`, `internal/mm/model/model_test.go`
- Create: `internal/mm/rest/chat.go`, `internal/mm/rest/chat_test.go`
- Modify: `internal/mm/rest/client.go` (лимитер), `internal/mm/rest/endpoints.go` (`User` → `model.User`, `ClientConfig` + поля), `go.mod` (`golang.org/x/time`)

**Interfaces:**
- Produces (пакет `model`): `Team`, `Channel` (+ `IsDM()`, `IsGroup()`, `DMPartner(me string) string`), `ChannelMember` (+ `Muted()`), `User` (+ `FullName()`), `Preference`, `SidebarCategory`, `OrderedCategories`, `Post`, `PostProps`, `Attachment`, `AttachmentField`, `PostMetadata`, `FileInfo`, `Reaction`, `PostList` (+ `Ascending() []Post`), `ChannelUnreadAt`, `Status`, `Flag`, `FlexString`; константы `ChannelOpen/Private/Direct/Group`.
- Produces (пакет `rest`): `type User = model.User`; `ClientConfig` + `CollapsedThreads`, `TeammateNameDisplay`, `LockTeammateNameDisplay`; `(*Client).WithLimiter(*rate.Limiter) *Client`; `NewLimiter() *rate.Limiter` (10/с, burst 20); `MyTeams`, `MyChannels`, `MyChannelMembers`, `Categories(ctx, teamID)`, `MyPreferences`, `MyStatus`, `UsersByIDs(ctx, ids)`, `ChannelPosts(ctx, channelID, PostsQuery)`; `type PostsQuery struct{ PerPage int; Before string; Since int64; CollapsedThreads bool }`; `const SinceLimit = 1000`.

- [ ] **Step 1: Тест модели (терпимый разбор props, порядок PostList, DM-партнёр)**

`internal/mm/model/model_test.go`:
```go
package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostPropsTolerateOddTypes(t *testing.T) {
	raw := `{"id":"p1","message":"hi","props":{
		"from_bot":"true","from_webhook":true,"override_username":42,
		"attachments":[{"title":"T","text":"body","fields":[{"title":"n","value":7,"short":"true"}]}],
		"unknown":{"x":[1,2]}}}`
	var p Post
	require.NoError(t, json.Unmarshal([]byte(raw), &p))
	assert.True(t, bool(p.Props.FromBot))
	assert.True(t, bool(p.Props.FromWebhook))
	assert.Equal(t, "42", string(p.Props.OverrideUsername))
	require.Len(t, p.Props.Attachments, 1)
	assert.Equal(t, "7", string(p.Props.Attachments[0].Fields[0].Value))
	assert.True(t, bool(p.Props.Attachments[0].Fields[0].Short))
}

func TestBrokenAttachmentsDoNotDropThePost(t *testing.T) {
	var p Post
	require.NoError(t, json.Unmarshal([]byte(`{"id":"p1","props":{"attachments":"garbage"}}`), &p))
	assert.Equal(t, "p1", p.ID)
	assert.Nil(t, p.Props.Attachments)
}

func TestPropsRoundTrip(t *testing.T) {
	p := Post{ID: "p1", Props: PostProps{FromBot: true, OverrideUsername: "bot"}}
	b, err := json.Marshal(p)
	require.NoError(t, err)
	var q Post
	require.NoError(t, json.Unmarshal(b, &q))
	assert.Equal(t, p.Props, q.Props)
}

func TestPostListAscending(t *testing.T) {
	l := PostList{
		Order: []string{"c", "b", "a", "missing"},
		Posts: map[string]Post{"a": {ID: "a", CreateAt: 1}, "b": {ID: "b", CreateAt: 2}, "c": {ID: "c", CreateAt: 3}},
	}
	got := l.Ascending()
	require.Len(t, got, 3)
	assert.Equal(t, []string{"a", "b", "c"}, []string{got[0].ID, got[1].ID, got[2].ID})
}

func TestPostListAscendingSortsByCreateAtNotOrder(t *testing.T) {
	// since-responses list order by CreateAt DESC but posts may be absent from order (roots of replies)
	l := PostList{Order: []string{"a", "b"}, Posts: map[string]Post{"a": {ID: "a", CreateAt: 5}, "b": {ID: "b", CreateAt: 9}}}
	got := l.Ascending()
	assert.Equal(t, "a", got[0].ID)
	assert.Equal(t, "b", got[1].ID)
}

func TestDMPartnerAndMuted(t *testing.T) {
	c := Channel{Type: ChannelDirect, Name: "u1__u2"}
	assert.Equal(t, "u2", c.DMPartner("u1"))
	assert.Equal(t, "u1", c.DMPartner("u2"))
	assert.Equal(t, "u1", Channel{Type: ChannelDirect, Name: "u1__u1"}.DMPartner("u1"))
	assert.Equal(t, "", Channel{Type: ChannelOpen, Name: "town-square"}.DMPartner("u1"))
	assert.True(t, ChannelMember{NotifyProps: map[string]string{"mark_unread": "mention"}}.Muted())
	assert.False(t, ChannelMember{}.Muted())
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/mm/model/`
Expected: FAIL (пакета нет).

- [ ] **Step 3: Реализация модели**

`internal/mm/model/model.go`:
```go
// Package model holds the Mattermost wire types spk-mattermost uses, trimmed
// to the fields the client reads (see docs/research/2026-09-24-mattermost-api-facts.md).
// The same types serialize the local cache snapshot, so json tags matter.
package model

import (
	"sort"
	"strings"
)

const (
	ChannelOpen    = "O"
	ChannelPrivate = "P"
	ChannelDirect  = "D"
	ChannelGroup   = "G"
)

type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	DeleteAt    int64  `json:"delete_at,omitempty"`
}

type Channel struct {
	ID                string `json:"id"`
	TeamID            string `json:"team_id"`
	Type              string `json:"type"`
	DisplayName       string `json:"display_name"`
	Name              string `json:"name"`
	Header            string `json:"header,omitempty"`
	Purpose           string `json:"purpose,omitempty"`
	CreateAt          int64  `json:"create_at"`
	UpdateAt          int64  `json:"update_at,omitempty"`
	DeleteAt          int64  `json:"delete_at,omitempty"`
	LastPostAt        int64  `json:"last_post_at"`
	LastRootPostAt    int64  `json:"last_root_post_at"`
	TotalMsgCount     int64  `json:"total_msg_count"`
	TotalMsgCountRoot int64  `json:"total_msg_count_root"`
}

func (c Channel) IsDM() bool    { return c.Type == ChannelDirect }
func (c Channel) IsGroup() bool { return c.Type == ChannelGroup }

// DMPartner returns the other user of a DM ("idA__idB"); a self-DM returns
// me; "" for non-DM channels.
func (c Channel) DMPartner(me string) string {
	if c.Type != ChannelDirect {
		return ""
	}
	a, b, ok := strings.Cut(c.Name, "__")
	if !ok {
		return ""
	}
	if a == me {
		return b
	}
	return a
}

type ChannelMember struct {
	ChannelID          string            `json:"channel_id"`
	UserID             string            `json:"user_id"`
	LastViewedAt       int64             `json:"last_viewed_at"`
	MsgCount           int64             `json:"msg_count"`
	MsgCountRoot       int64             `json:"msg_count_root"`
	MentionCount       int64             `json:"mention_count"`
	MentionCountRoot   int64             `json:"mention_count_root"`
	UrgentMentionCount int64             `json:"urgent_mention_count"`
	LastUpdateAt       int64             `json:"last_update_at,omitempty"`
	NotifyProps        map[string]string `json:"notify_props,omitempty"`
}

// Muted mirrors the webapp's isChannelMuted: mark_unread == "mention".
func (m ChannelMember) Muted() bool { return m.NotifyProps["mark_unread"] == "mention" }

type User struct {
	ID          string            `json:"id"`
	Username    string            `json:"username"`
	FirstName   string            `json:"first_name,omitempty"`
	LastName    string            `json:"last_name,omitempty"`
	Nickname    string            `json:"nickname,omitempty"`
	Locale      string            `json:"locale,omitempty"`
	IsBot       bool              `json:"is_bot,omitempty"`
	DeleteAt    int64             `json:"delete_at,omitempty"`
	UpdateAt    int64             `json:"update_at,omitempty"`
	NotifyProps map[string]string `json:"notify_props,omitempty"`
}

func (u User) FullName() string { return strings.TrimSpace(u.FirstName + " " + u.LastName) }

type Preference struct {
	UserID   string `json:"user_id"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Value    string `json:"value"`
}

type SidebarCategory struct {
	ID          string   `json:"id"`
	TeamID      string   `json:"team_id"`
	Type        string   `json:"type"`    // favorites | channels | direct_messages | custom
	DisplayName string   `json:"display_name"`
	Sorting     string   `json:"sorting"` // "" | manual | recent | alpha
	SortOrder   int64    `json:"sort_order"`
	Muted       bool     `json:"muted"`
	Collapsed   bool     `json:"collapsed"`
	ChannelIDs  []string `json:"channel_ids"`
}

type OrderedCategories struct {
	Categories []SidebarCategory `json:"categories"`
	Order      []string          `json:"order"`
}

type FileInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Extension       string `json:"extension,omitempty"`
	Size            int64  `json:"size"`
	MimeType        string `json:"mime_type,omitempty"`
	HasPreviewImage bool   `json:"has_preview_image,omitempty"`
}

type Reaction struct {
	UserID    string `json:"user_id"`
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
	CreateAt  int64  `json:"create_at,omitempty"`
}

// PostMetadata keeps only files and reactions; embeds/images/emojis are
// dropped at decode time (memory: the hot layer holds ~60 posts × channels).
type PostMetadata struct {
	Files     []FileInfo `json:"files,omitempty"`
	Reactions []Reaction `json:"reactions,omitempty"`
}

type Post struct {
	ID            string        `json:"id"`
	ChannelID     string        `json:"channel_id"`
	UserID        string        `json:"user_id"`
	RootID        string        `json:"root_id,omitempty"`
	OriginalID    string        `json:"original_id,omitempty"`
	Message       string        `json:"message"`
	Type          string        `json:"type,omitempty"`
	PendingPostID string        `json:"pending_post_id,omitempty"`
	CreateAt      int64         `json:"create_at"`
	UpdateAt      int64         `json:"update_at"`
	EditAt        int64         `json:"edit_at,omitempty"`
	DeleteAt      int64         `json:"delete_at,omitempty"`
	IsPinned      bool          `json:"is_pinned,omitempty"`
	ReplyCount    int64         `json:"reply_count,omitempty"`
	LastReplyAt   int64         `json:"last_reply_at,omitempty"`
	FileIDs       []string      `json:"file_ids,omitempty"`
	Props         PostProps     `json:"props"`
	Metadata      *PostMetadata `json:"metadata,omitempty"`
}

// IsSystem: join/leave/header-change etc. are posted as type system_*.
func (p Post) IsSystem() bool { return strings.HasPrefix(p.Type, "system_") }

type PostList struct {
	Order      []string        `json:"order"`
	Posts      map[string]Post `json:"posts"`
	NextPostID string          `json:"next_post_id"`
	PrevPostID string          `json:"prev_post_id"`
}

// Ascending returns the posts listed in Order, oldest first. Posts only in
// the map (roots fetched alongside replies in since-responses) are skipped.
func (l PostList) Ascending() []Post {
	out := make([]Post, 0, len(l.Order))
	for _, id := range l.Order {
		if p, ok := l.Posts[id]; ok {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreateAt < out[j].CreateAt })
	return out
}

type ChannelUnreadAt struct {
	TeamID             string `json:"team_id"`
	ChannelID          string `json:"channel_id"`
	MsgCount           int64  `json:"msg_count"`
	MsgCountRoot       int64  `json:"msg_count_root"`
	MentionCount       int64  `json:"mention_count"`
	MentionCountRoot   int64  `json:"mention_count_root"`
	UrgentMentionCount int64  `json:"urgent_mention_count"`
	LastViewedAt       int64  `json:"last_viewed_at"`
}

// Status: dnd_end_time is in SECONDS (unlike every other Mattermost timestamp).
type Status struct {
	UserID     string `json:"user_id"`
	Status     string `json:"status"` // online | away | dnd | offline | ooo
	DNDEndTime int64  `json:"dnd_end_time,omitempty"`
}
```

`internal/mm/model/props.go`:
```go
package model

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Flag is a bool that also accepts the strings "true"/"false" (Mattermost
// props mix both).
type Flag bool

func (f *Flag) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	*f = Flag(strings.EqualFold(s, "true"))
	return nil
}

// FlexString accepts a JSON string, number or bool and keeps its text.
type FlexString string

func (s *FlexString) UnmarshalJSON(b []byte) error {
	var str string
	if json.Unmarshal(b, &str) == nil {
		*s = FlexString(str)
		return nil
	}
	t := strings.TrimSpace(string(b))
	if t == "null" {
		*s = ""
		return nil
	}
	if _, err := strconv.ParseFloat(t, 64); err == nil || t == "true" || t == "false" {
		*s = FlexString(t)
		return nil
	}
	*s = ""
	return nil
}

type AttachmentField struct {
	Title string     `json:"title,omitempty"`
	Value FlexString `json:"value,omitempty"`
	Short Flag       `json:"short,omitempty"`
}

type Attachment struct {
	Fallback   string            `json:"fallback,omitempty"`
	Color      string            `json:"color,omitempty"`
	Pretext    string            `json:"pretext,omitempty"`
	AuthorName string            `json:"author_name,omitempty"`
	Title      string            `json:"title,omitempty"`
	TitleLink  string            `json:"title_link,omitempty"`
	Text       string            `json:"text,omitempty"`
	Footer     string            `json:"footer,omitempty"`
	Fields     []AttachmentField `json:"fields,omitempty"`
}

// PostProps keeps the props the client renders. Decoding never fails: a
// field of an unexpected shape is dropped, not the whole post (bots and
// plugins put arbitrary JSON in props).
type PostProps struct {
	FromBot           Flag         `json:"from_bot,omitempty"`
	FromWebhook       Flag         `json:"from_webhook,omitempty"`
	OverrideUsername  FlexString   `json:"override_username,omitempty"`
	ForceNotification Flag         `json:"force_notification,omitempty"`
	Attachments       []Attachment `json:"attachments,omitempty"`
}

func (p *PostProps) UnmarshalJSON(b []byte) error {
	*p = PostProps{}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	_ = json.Unmarshal(m["from_bot"], &p.FromBot)
	_ = json.Unmarshal(m["from_webhook"], &p.FromWebhook)
	_ = json.Unmarshal(m["override_username"], &p.OverrideUsername)
	_ = json.Unmarshal(m["force_notification"], &p.ForceNotification)
	if raw, ok := m["attachments"]; ok {
		var atts []Attachment
		if json.Unmarshal(raw, &atts) == nil {
			p.Attachments = atts
		}
	}
	return nil
}
```
(`json.Unmarshal(nil, …)` для отсутствующего ключа возвращает ошибку, и поле остаётся нулевым — это ожидаемо.)

- [ ] **Step 4: Тест модели проходит**

Run: `go test ./internal/mm/model/`
Expected: PASS.

- [ ] **Step 5: Падающие тесты REST-чтения и лимитера**

`internal/mm/rest/chat_test.go` (использует `newTestClient` из `client_test.go`):
```go
package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestMyChannelsAndTeams(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/me/teams":
			_, _ = w.Write([]byte(`[{"id":"t1","name":"fake","display_name":"Fake"}]`))
		case "/api/v4/users/me/channels":
			_, _ = w.Write([]byte(`[{"id":"c1","team_id":"t1","type":"O","display_name":"Town","total_msg_count":5}]`))
		default:
			http.NotFound(w, r)
		}
	})
	teams, err := c.MyTeams(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Fake", teams[0].DisplayName)
	chans, err := c.MyChannels(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(5), chans[0].TotalMsgCount)
}

func TestMyChannelMembersPagesUntilShortPage(t *testing.T) {
	var pages []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/users/me/channel_members", r.URL.Path)
		assert.Equal(t, "200", r.URL.Query().Get("per_page"))
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		n := 200
		if page == "1" {
			n = 5
		}
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf(`{"channel_id":"c%s-%d","msg_count":1}`, page, i)
		}
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	})
	got, err := c.MyChannelMembers(context.Background())
	require.NoError(t, err)
	assert.Len(t, got, 205)
	assert.Equal(t, []string{"0", "1"}, pages)
}

func TestChannelPostsQueryParams(t *testing.T) {
	var q []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/channels/c1/posts", r.URL.Path)
		q = append(q, r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode(map[string]any{"order": []string{"p1"}, "posts": map[string]any{"p1": map[string]any{"id": "p1"}}, "prev_post_id": ""})
	})
	ctx := context.Background()
	_, err := c.ChannelPosts(ctx, "c1", PostsQuery{PerPage: 60, CollapsedThreads: true})
	require.NoError(t, err)
	_, err = c.ChannelPosts(ctx, "c1", PostsQuery{PerPage: 60, Before: "p9"})
	require.NoError(t, err)
	l, err := c.ChannelPosts(ctx, "c1", PostsQuery{Since: 1234, CollapsedThreads: true})
	require.NoError(t, err)
	assert.Equal(t, "p1", l.Order[0])
	assert.Equal(t, []string{
		"collapsedThreads=true&collapsedThreadsExtended=false&page=0&per_page=60",
		"before=p9&collapsedThreads=false&collapsedThreadsExtended=false&page=0&per_page=60",
		"collapsedThreads=true&collapsedThreadsExtended=false&since=1234&skipFetchThreads=true",
	}, q)
}

func TestUsersByIDsChunksBy100(t *testing.T) {
	var sizes []int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/users/ids", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var ids []string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&ids))
		sizes = append(sizes, len(ids))
		out := make([]map[string]string, len(ids))
		for i, id := range ids {
			out[i] = map[string]string{"id": id, "username": "u" + id}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	ids := make([]string, 150)
	for i := range ids {
		ids[i] = fmt.Sprint(i)
	}
	users, err := c.UsersByIDs(context.Background(), ids)
	require.NoError(t, err)
	assert.Len(t, users, 150)
	assert.Equal(t, []int{100, 50}, sizes)
	none, err := c.UsersByIDs(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestCategoriesPrefsStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/me/teams/t1/channels/categories":
			_, _ = w.Write([]byte(`{"categories":[{"id":"k1","type":"channels","channel_ids":["c1"]}],"order":["k1"]}`))
		case "/api/v4/users/me/preferences":
			_, _ = w.Write([]byte(`[{"category":"display_settings","name":"name_format","value":"full_name"}]`))
		case "/api/v4/users/me/status":
			_, _ = w.Write([]byte(`{"user_id":"u1","status":"dnd","dnd_end_time":0}`))
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	cats, err := c.Categories(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, []string{"c1"}, cats.Categories[0].ChannelIDs)
	prefs, err := c.MyPreferences(ctx)
	require.NoError(t, err)
	assert.Equal(t, "full_name", prefs[0].Value)
	st, err := c.MyStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dnd", st.Status)
}

func TestLimiterSpacesRequests(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"OK"}`)) })
	c = c.WithLimiter(rate.NewLimiter(rate.Every(40*time.Millisecond), 1))
	start := time.Now()
	for i := 0; i < 4; i++ {
		require.NoError(t, c.Ping(context.Background()))
	}
	assert.GreaterOrEqual(t, time.Since(start), 110*time.Millisecond)
}

func TestLimiterWaitHonoursContext(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"OK"}`)) })
	c = c.WithLimiter(rate.NewLimiter(rate.Every(time.Hour), 1))
	require.NoError(t, c.Ping(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Ping(ctx)
	require.Error(t, err)
	assert.True(t, IsNetwork(err))
}
```

- [ ] **Step 6: Запустить — падает**

Run: `go get golang.org/x/time@v0.16.0 && go test ./internal/mm/rest/`
Expected: FAIL (нет `MyTeams`, `WithLimiter` и т.д.).

- [ ] **Step 7: Реализация**

В `client.go`: поле `lim *rate.Limiter` в `Client`; метод
```go
// WithLimiter returns a copy whose every attempt (retries included) first
// takes a token from l. One limiter is shared by all clients of a server.
func (c *Client) WithLimiter(l *rate.Limiter) *Client {
	cp := *c
	cp.lim = l
	return &cp
}

// NewLimiter is the per-server budget: 10 req/s sustained, bursts of 20
// (Mattermost's own default limit is 10/s with burst 100).
func NewLimiter() *rate.Limiter { return rate.NewLimiter(10, 20) }
```
и в `do` первой строкой тела цикла `for attempt := 1; ; attempt++ {`:
```go
		if c.lim != nil {
			if err := c.lim.Wait(ctx); err != nil {
				return nil, &Error{Kind: KindNetwork, Err: err}
			}
		}
```
В `endpoints.go`: удалить `type User struct{…}`, добавить `type User = model.User`; в `ClientConfig` добавить
```go
	CollapsedThreads        string `json:"CollapsedThreads"`
	TeammateNameDisplay     string `json:"TeammateNameDisplay"`
	LockTeammateNameDisplay string `json:"LockTeammateNameDisplay"`
```

`internal/mm/rest/chat.go`:
```go
package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

// SinceLimit is the server's hard cap on a posts?since= response; a result
// this large may have skipped posts and must be replaced by a fresh page.
const SinceLimit = 1000

const (
	membersPageSize = 200
	usersChunk      = 100
)

func (c *Client) get(ctx context.Context, path string, out any) error {
	_, err := c.do(ctx, http.MethodGet, path, nil, out)
	return err
}

func (c *Client) MyTeams(ctx context.Context) ([]model.Team, error) {
	var out []model.Team
	return out, c.get(ctx, "/api/v4/users/me/teams", &out)
}

// MyChannels returns channels of all teams plus DMs/GMs in one array.
func (c *Client) MyChannels(ctx context.Context) ([]model.Channel, error) {
	var out []model.Channel
	return out, c.get(ctx, "/api/v4/users/me/channels", &out)
}

// MyChannelMembers pages through all of the user's memberships.
func (c *Client) MyChannelMembers(ctx context.Context) ([]model.ChannelMember, error) {
	var all []model.ChannelMember
	for page := 0; ; page++ {
		var out []model.ChannelMember
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(membersPageSize)}}
		if err := c.get(ctx, "/api/v4/users/me/channel_members?"+q.Encode(), &out); err != nil {
			return nil, err
		}
		all = append(all, out...)
		if len(out) < membersPageSize {
			return all, nil
		}
	}
}

func (c *Client) Categories(ctx context.Context, teamID string) (model.OrderedCategories, error) {
	var out model.OrderedCategories
	return out, c.get(ctx, "/api/v4/users/me/teams/"+url.PathEscape(teamID)+"/channels/categories", &out)
}

func (c *Client) MyPreferences(ctx context.Context) ([]model.Preference, error) {
	var out []model.Preference
	return out, c.get(ctx, "/api/v4/users/me/preferences", &out)
}

func (c *Client) MyStatus(ctx context.Context) (model.Status, error) {
	var out model.Status
	return out, c.get(ctx, "/api/v4/users/me/status", &out)
}

// UsersByIDs fetches profiles in chunks (the request body is an id array).
func (c *Client) UsersByIDs(ctx context.Context, ids []string) ([]model.User, error) {
	var all []model.User
	for start := 0; start < len(ids); start += usersChunk {
		end := min(start+usersChunk, len(ids))
		var out []model.User
		if _, err := c.do(ctx, http.MethodPost, "/api/v4/users/ids", ids[start:end], &out); err != nil {
			return nil, err
		}
		all = append(all, out...)
	}
	return all, nil
}

type PostsQuery struct {
	PerPage          int    // page mode; 0 → 60
	Before           string // page mode: posts older than this id
	Since            int64  // ms; >0 switches to since mode (edits/deletes included, ≤ SinceLimit)
	CollapsedThreads bool   // CRT: root posts only
}

func (c *Client) ChannelPosts(ctx context.Context, channelID string, q PostsQuery) (model.PostList, error) {
	v := url.Values{
		"collapsedThreads":         {strconv.FormatBool(q.CollapsedThreads)},
		"collapsedThreadsExtended": {"false"},
	}
	if q.Since > 0 {
		v.Set("since", strconv.FormatInt(q.Since, 10))
		v.Set("skipFetchThreads", "true")
	} else {
		per := q.PerPage
		if per <= 0 {
			per = 60
		}
		v.Set("page", "0")
		v.Set("per_page", strconv.Itoa(per))
		if q.Before != "" {
			v.Set("before", q.Before)
		}
	}
	var out model.PostList
	err := c.get(ctx, "/api/v4/channels/"+url.PathEscape(channelID)+"/posts?"+v.Encode(), &out)
	return out, err
}
```
Поправить места, где использовался `rest.User` с полями (`internal/auth`, `internal/api`) — поля `ID`/`Username` сохранились, правки не нужны; `go build ./...` это подтвердит.

- [ ] **Step 8: Тесты проходят, сборка чистая**

Run: `go test ./internal/mm/... && go build ./... && go vet ./...`
Expected: PASS, без ошибок.

- [ ] **Step 9: Коммит**

```bash
git add go.mod go.sum internal/mm
git commit -m "mm: wire model, REST read endpoints for chat, per-server rate limiter"
```

---

### Task 2: REST-запись

**Files:**
- Create: `internal/mm/rest/write.go`, `internal/mm/rest/write_test.go`

**Interfaces:**
- Consumes: Task 1 (`model.*`, `Client.do`).
- Produces: `CreatePost(ctx, model.Post) (model.Post, error)`; `PatchPost(ctx, postID, message string) (model.Post, error)`; `DeletePost(ctx, postID string) error`; `ViewChannel(ctx, channelID string) error`; `SetUnread(ctx, postID string) (model.ChannelUnreadAt, error)`; `SavePreferences(ctx, []model.Preference) error`.

- [ ] **Step 1: Падающие тесты**

`internal/mm/rest/write_test.go`:
```go
package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

func TestCreatePostSendsPendingIDAndAccepts201(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/posts", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var in map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		assert.Equal(t, "c1", in["channel_id"])
		assert.Equal(t, "hello", in["message"])
		assert.Equal(t, "u1:1", in["pending_post_id"])
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"p1","channel_id":"c1","message":"hello","pending_post_id":"u1:1","create_at":5}`))
	})
	p, err := c.CreatePost(context.Background(), model.Post{ChannelID: "c1", Message: "hello", PendingPostID: "u1:1", UserID: "u1"})
	require.NoError(t, err)
	assert.Equal(t, "p1", p.ID)
}

func TestCreatePostIsNotRetriedOnServerError(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.CreatePost(context.Background(), model.Post{ChannelID: "c1", Message: "x"})
	require.Error(t, err)
	assert.Equal(t, int32(1), n.Load())
}

func TestPatchDeleteViewUnreadPrefs(t *testing.T) {
	var seen []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/v4/posts/p1/patch":
			assert.Equal(t, "edited", body["message"])
			_, _ = w.Write([]byte(`{"id":"p1","message":"edited","edit_at":9}`))
		case "/api/v4/posts/p1":
			_, _ = w.Write([]byte(`{"status":"OK"}`))
		case "/api/v4/channels/members/me/view":
			assert.Equal(t, "c1", body["channel_id"])
			assert.Equal(t, true, body["collapsed_threads_supported"])
			_, _ = w.Write([]byte(`{"status":"OK"}`))
		case "/api/v4/users/me/posts/p1/set_unread":
			assert.Equal(t, true, body["collapsed_threads_supported"])
			_, _ = w.Write([]byte(`{"channel_id":"c1","msg_count":3,"last_viewed_at":7}`))
		case "/api/v4/users/me/preferences":
			_, _ = w.Write([]byte(`{"status":"OK"}`))
		}
	})
	ctx := context.Background()
	p, err := c.PatchPost(ctx, "p1", "edited")
	require.NoError(t, err)
	assert.Equal(t, int64(9), p.EditAt)
	require.NoError(t, c.DeletePost(ctx, "p1"))
	require.NoError(t, c.ViewChannel(ctx, "c1"))
	u, err := c.SetUnread(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, int64(3), u.MsgCount)
	require.NoError(t, c.SavePreferences(ctx, []model.Preference{{UserID: "u1", Category: "direct_channel_show", Name: "u2", Value: "true"}}))
	assert.Equal(t, []string{
		"PUT /api/v4/posts/p1/patch", "DELETE /api/v4/posts/p1", "POST /api/v4/channels/members/me/view",
		"POST /api/v4/users/me/posts/p1/set_unread", "PUT /api/v4/users/me/preferences",
	}, seen)
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/mm/rest/ -run 'Create|Patch'`
Expected: FAIL (нет методов).

- [ ] **Step 3: Реализация**

`internal/mm/rest/write.go`:
```go
package rest

import (
	"context"
	"net/http"
	"net/url"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

// createPostReq is the subset of a Post the server needs; sending the whole
// model.Post would post zero-valued fields (props, create_at) we never set.
type createPostReq struct {
	ChannelID     string `json:"channel_id"`
	Message       string `json:"message"`
	RootID        string `json:"root_id,omitempty"`
	PendingPostID string `json:"pending_post_id,omitempty"`
	UserID        string `json:"user_id,omitempty"`
}

// CreatePost is never retried on transport errors (do() only retries GET);
// resending with the same pending_post_id is safe — the server returns the
// already-created post for 30 s.
func (c *Client) CreatePost(ctx context.Context, p model.Post) (model.Post, error) {
	var out model.Post
	_, err := c.do(ctx, http.MethodPost, "/api/v4/posts",
		createPostReq{ChannelID: p.ChannelID, Message: p.Message, RootID: p.RootID, PendingPostID: p.PendingPostID, UserID: p.UserID}, &out)
	return out, err
}

func (c *Client) PatchPost(ctx context.Context, postID, message string) (model.Post, error) {
	var out model.Post
	_, err := c.do(ctx, http.MethodPut, "/api/v4/posts/"+url.PathEscape(postID)+"/patch", map[string]string{"message": message}, &out)
	return out, err
}

func (c *Client) DeletePost(ctx context.Context, postID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/v4/posts/"+url.PathEscape(postID), nil, nil)
	return err
}

func (c *Client) ViewChannel(ctx context.Context, channelID string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v4/channels/members/me/view",
		map[string]any{"channel_id": channelID, "prev_channel_id": "", "collapsed_threads_supported": true}, nil)
	return err
}

func (c *Client) SetUnread(ctx context.Context, postID string) (model.ChannelUnreadAt, error) {
	var out model.ChannelUnreadAt
	_, err := c.do(ctx, http.MethodPost, "/api/v4/users/me/posts/"+url.PathEscape(postID)+"/set_unread",
		map[string]bool{"collapsed_threads_supported": true}, &out)
	return out, err
}

func (c *Client) SavePreferences(ctx context.Context, prefs []model.Preference) error {
	_, err := c.do(ctx, http.MethodPut, "/api/v4/users/me/preferences", prefs, nil)
	return err
}
```

- [ ] **Step 4: Тесты проходят**

Run: `go test -race ./internal/mm/rest/`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/mm/rest
git commit -m "mm/rest: create/patch/delete post, view channel, set unread, save preferences"
```

---
### Task 3: mmfake — данные чата и REST

Фейк обслуживает Go-тесты воркера и браузерный режим (e2e). Он реализует
семантику из `docs/research/…` только в объёме, который читает клиент:
счётчики, `since` с удалёнными постами и строками истории, CRT-фильтр,
идемпотентный `pending_post_id`, `set_unread`, упоминания.

**Files:**
- Create: `internal/mmfake/chat.go`, `internal/mmfake/seed.go`, `internal/mmfake/chat_test.go`
- Modify: `internal/mmfake/server.go` (Options, пользователи по умолчанию, маршруты, `userJSON` → `model.User`, `clientConfig` + CRT)

**Interfaces:**
- Consumes: Task 1 `model.*`.
- Produces:
  - `Options` + `CRT bool` (конфиг `CollapsedThreads`: `always_on`, иначе `disabled`), `SeedPosts int` (0 → 150 постов в Town Square; <0 → нет), `ExtraChannels int` (открытые каналы `load-NNN` с 20 постами каждый), `SinceLimit int` (0 → 1000).
  - Пользователи по умолчанию: `alice` (`u-alice`), `bob` (`u-bob`), `carol` (`u-carol`), пароль `secret`.
  - Начальные данные: команда `t-fake` («Fake Team», name `fake`); каналы `c-town` (O «Town Square», name `town-square`: alice, bob, carol), `c-offtopic` (O «Off-Topic», `off-topic`: alice, bob), `c-secret` (P «Secret», `secret`: alice), `c-dm-bob` (D, name `u-alice__u-bob`), `c-gm` (G «alice, bob, carol»); всё прочитано; preferences alice: `direct_channel_show/u-bob=true`, `group_channel_show/c-gm=true`.
  - Управление из тестов (потокобезопасно): `(*Server).PostAs(channelID, username, message string) model.Post`, `ReplyAs(channelID, rootID, username, message string) model.Post`, `EditAs(postID, message string)`, `DeleteAs(postID string)`, `AddChannel(id, display string, usernames ...string)`, `Member(channelID, username string) model.ChannelMember`, `Channel(channelID string) model.Channel`, `VisiblePosts(channelID string) []model.Post`, `SetStatus(username, status string)`, `Events() []RecordedEvent` (`RecordedEvent{Name string; To []string}`).
  - Внутренний крючок публикации для Task 4: `(*Server).publishLocked(name string, data map[string]any, b wsBroadcast, to []string, mentions []string)`; тип `wsBroadcast{UserID, ChannelID, TeamID string}` с json-тегами `user_id/channel_id/team_id`.

- [ ] **Step 1: Падающие тесты**

`internal/mmfake/chat_test.go`:
```go
package mmfake

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

type authed struct {
	t   *testing.T
	s   *Server
	tok string
}

func loginAs(t *testing.T, s *Server, user string) authed {
	t.Helper()
	resp, err := http.Post(s.URL()+"/api/v4/users/login", "application/json", strings.NewReader(`{"login_id":"`+user+`","password":"secret"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	return authed{t: t, s: s, tok: resp.Header.Get("Token")}
}

func (a authed) call(method, path string, body any, out any) int {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.s.URL()+path, rd)
	req.Header.Set("Authorization", "Bearer "+a.tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		require.NoError(a.t, json.NewDecoder(resp.Body).Decode(out))
	}
	return resp.StatusCode
}

func TestSeedVisibleToAlice(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var teams []model.Team
	require.Equal(t, 200, a.call("GET", "/api/v4/users/me/teams", nil, &teams))
	assert.Equal(t, "Fake Team", teams[0].DisplayName)
	var chans []model.Channel
	a.call("GET", "/api/v4/users/me/channels", nil, &chans)
	assert.Len(t, chans, 5)
	var members []model.ChannelMember
	a.call("GET", "/api/v4/users/me/channel_members?page=0&per_page=200", nil, &members)
	assert.Len(t, members, 5)
	town := s.Channel("c-town")
	assert.Equal(t, int64(150), town.TotalMsgCount)
	assert.Equal(t, town.TotalMsgCount, s.Member("c-town", "alice").MsgCount, "seed is read")
	var cats model.OrderedCategories
	a.call("GET", "/api/v4/users/me/teams/t-fake/channels/categories", nil, &cats)
	byType := map[string][]string{}
	for _, c := range cats.Categories {
		byType[c.Type] = c.ChannelIDs
	}
	assert.ElementsMatch(t, []string{"c-town", "c-offtopic", "c-secret"}, byType["channels"])
	assert.ElementsMatch(t, []string{"c-dm-bob", "c-gm"}, byType["direct_messages"])
	assert.Len(t, cats.Order, 3)
}

func TestPostsPagingAndBefore(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var page model.PostList
	a.call("GET", "/api/v4/channels/c-town/posts?page=0&per_page=60", nil, &page)
	require.Len(t, page.Order, 60)
	asc := page.Ascending()
	assert.Equal(t, "Message #150", asc[59].Message)
	assert.NotEmpty(t, page.PrevPostID)
	var older model.PostList
	a.call("GET", "/api/v4/channels/c-town/posts?page=0&per_page=100&before="+asc[0].ID, nil, &older)
	assert.Len(t, older.Order, 90)
	assert.Empty(t, older.PrevPostID, "reached the beginning")
	assert.Equal(t, 403, a.call("GET", "/api/v4/channels/c-nope/posts", nil, nil))
}

func TestCreateIsIdempotentByPendingIDAndCountsMentions(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	b := loginAs(t, s, "bob")
	var p1, p2 model.Post
	body := map[string]string{"channel_id": "c-offtopic", "message": "hi @alice", "pending_post_id": "u-bob:1"}
	require.Equal(t, 201, b.call("POST", "/api/v4/posts", body, &p1))
	require.Equal(t, 201, b.call("POST", "/api/v4/posts", body, &p2))
	assert.Equal(t, p1.ID, p2.ID)
	assert.Equal(t, "u-bob:1", p1.PendingPostID)
	m := s.Member("c-offtopic", "alice")
	assert.Equal(t, int64(1), m.MentionCount)
	assert.Equal(t, s.Channel("c-offtopic").TotalMsgCount-1, m.MsgCount)
	assert.Equal(t, s.Channel("c-offtopic").TotalMsgCount, s.Member("c-offtopic", "bob").MsgCount, "own post is read")
}

func TestDMCountsAsMention(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	s.PostAs("c-dm-bob", "bob", "no at-sign here")
	assert.Equal(t, int64(1), s.Member("c-dm-bob", "alice").MentionCount)
}

func TestSinceReturnsEditsDeletesAndHistoryAndHonoursLimit(t *testing.T) {
	s := Start(Options{SinceLimit: 3})
	defer s.Close()
	a := loginAs(t, s, "alice")
	mark := s.Channel("c-offtopic").LastPostAt
	p := s.PostAs("c-offtopic", "bob", "one")
	s.EditAs(p.ID, "one!")
	q := s.PostAs("c-offtopic", "bob", "two")
	s.DeleteAs(q.ID)
	var l model.PostList
	a.call("GET", "/api/v4/channels/c-offtopic/posts?since="+itoa(mark), nil, &l)
	require.Len(t, l.Order, 3, "limit applied: edited p, deleted q, history row of p")
	var sawHistory, sawDeleted bool
	for _, id := range l.Order {
		post := l.Posts[id]
		if post.OriginalID == p.ID {
			sawHistory = true
		}
		if post.ID == q.ID && post.DeleteAt > 0 {
			sawDeleted = true
		}
	}
	assert.True(t, sawHistory)
	assert.True(t, sawDeleted)
	vis := s.VisiblePosts("c-offtopic")
	assert.Equal(t, "one!", vis[len(vis)-1].Message)
}

func TestCRTFiltersReplies(t *testing.T) {
	s := Start(Options{CRT: true, SeedPosts: -1})
	defer s.Close()
	a := loginAs(t, s, "alice")
	root := s.PostAs("c-town", "bob", "root")
	s.ReplyAs("c-town", root.ID, "carol", "reply")
	var l model.PostList
	a.call("GET", "/api/v4/channels/c-town/posts?page=0&per_page=60&collapsedThreads=true", nil, &l)
	require.Len(t, l.Order, 1)
	assert.Equal(t, int64(1), l.Posts[root.ID].ReplyCount)
	var cfg map[string]string
	a.call("GET", "/api/v4/config/client?format=old", nil, &cfg)
	assert.Equal(t, "always_on", cfg["CollapsedThreads"])
}

func TestViewAndSetUnread(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	p1 := s.PostAs("c-offtopic", "bob", "a @alice")
	s.PostAs("c-offtopic", "bob", "b")
	require.Equal(t, 200, a.call("POST", "/api/v4/channels/members/me/view", map[string]any{"channel_id": "c-offtopic"}, nil))
	m := s.Member("c-offtopic", "alice")
	assert.Equal(t, s.Channel("c-offtopic").TotalMsgCount, m.MsgCount)
	assert.Zero(t, m.MentionCount)
	var u model.ChannelUnreadAt
	require.Equal(t, 200, a.call("POST", "/api/v4/users/me/posts/"+p1.ID+"/set_unread", map[string]any{"collapsed_threads_supported": true}, &u))
	assert.Equal(t, s.Channel("c-offtopic").TotalMsgCount-2, u.MsgCount)
	assert.Equal(t, int64(1), u.MentionCount)
	names := []string{}
	for _, e := range s.Events() {
		names = append(names, e.Name)
	}
	assert.Contains(t, names, "multiple_channels_viewed")
	assert.Contains(t, names, "post_unread")
}

func TestPatchAndDeleteOnlyOwnPosts(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	bobs := s.PostAs("c-town", "bob", "bob's")
	assert.Equal(t, 403, a.call("PUT", "/api/v4/posts/"+bobs.ID+"/patch", map[string]string{"message": "x"}, nil))
	var mine model.Post
	a.call("POST", "/api/v4/posts", map[string]string{"channel_id": "c-town", "message": "mine"}, &mine)
	var edited model.Post
	require.Equal(t, 200, a.call("PUT", "/api/v4/posts/"+mine.ID+"/patch", map[string]string{"message": "mine2"}, &edited))
	assert.NotZero(t, edited.EditAt)
	require.Equal(t, 200, a.call("DELETE", "/api/v4/posts/"+mine.ID, nil, nil))
	for _, p := range s.VisiblePosts("c-town") {
		assert.NotEqual(t, mine.ID, p.ID)
	}
}

func TestUsersPrefsStatus(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var users []model.User
	a.call("POST", "/api/v4/users/ids", []string{"u-bob", "u-carol", "nope"}, &users)
	assert.Len(t, users, 2)
	var prefs []model.Preference
	a.call("GET", "/api/v4/users/me/preferences", nil, &prefs)
	assert.Contains(t, prefs, model.Preference{UserID: "u-alice", Category: "direct_channel_show", Name: "u-bob", Value: "true"})
	require.Equal(t, 200, a.call("PUT", "/api/v4/users/me/preferences", []model.Preference{{UserID: "u-alice", Category: "x", Name: "y", Value: "z"}}, nil))
	a.call("GET", "/api/v4/users/me/preferences", nil, &prefs)
	assert.Contains(t, prefs, model.Preference{UserID: "u-alice", Category: "x", Name: "y", Value: "z"})
	s.SetStatus("alice", "dnd")
	var st model.Status
	a.call("GET", "/api/v4/users/me/status", nil, &st)
	assert.Equal(t, "dnd", st.Status)
	var me model.User
	a.call("GET", "/api/v4/users/me", nil, &me)
	assert.Equal(t, "mention", me.NotifyProps["desktop"])
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/mmfake/`
Expected: FAIL (нет `Channel`, `PostAs`, маршрутов).

- [ ] **Step 3: Реализация**

В `server.go`:
- `User` дополнить `FirstName, LastName string`; пользователи по умолчанию — alice/bob/carol (`u-alice`/`u-bob`/`u-carol`, пароль `secret`).
- `Options` дополнить полями из «Interfaces»; в `Start` после создания `s` вызвать `s.seed()` (из `seed.go`) **до** `httptest.NewServer`, зарегистрировать маршруты чата вызовом `s.chatRoutes(mux)` (из `chat.go`).
- `userJSON(u User) model.User` — `{ID, Username, FirstName, LastName}`; для `/users/me` добавить `NotifyProps: map[string]string{"desktop": "mention", "channel": "true", "desktop_threads": "all", "mention_keys": "", "first_name": "false"}`.
- `clientConfig` дополнить `"CollapsedThreads"` (`always_on` при `CRT`, иначе `disabled`) и `"TeammateNameDisplay": "username"`.
- Хелпер `func (s *Server) handleAuthed(h func(w http.ResponseWriter, r *http.Request, u User)) http.HandlerFunc` — 401 `api.context.session_expired.app_error`, если токен неизвестен.

`internal/mmfake/chat.go`:
```go
package mmfake

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

type fpost struct {
	model.Post
	mentions []string // user ids the server counted as mentioned
}

type chatData struct {
	teams      []model.Team
	channels   map[string]*model.Channel
	members    map[string]map[string]*model.ChannelMember // channel → user → member
	posts      map[string][]*fpost                         // channel → by CreateAt; includes deleted + history rows
	byID       map[string]*fpost
	pending    map[string]string // pending_post_id → post id
	prefs      map[string][]model.Preference
	status     map[string]string
	events     []RecordedEvent
	lastMs     int64
	sinceLimit int
}

type RecordedEvent struct {
	Name string
	To   []string
}

type wsBroadcast struct {
	UserID    string `json:"user_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	TeamID    string `json:"team_id,omitempty"`
}

// nowLocked returns strictly increasing ms so ordering by time is total.
func (s *Server) nowLocked() int64 {
	ms := time.Now().UnixMilli()
	if ms <= s.chat.lastMs {
		ms = s.chat.lastMs + 1
	}
	s.chat.lastMs = ms
	return ms
}

func (s *Server) chatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v4/users/me/teams", s.handleAuthed(s.myTeams))
	mux.HandleFunc("GET /api/v4/users/me/channels", s.handleAuthed(s.myChannels))
	mux.HandleFunc("GET /api/v4/users/me/channel_members", s.handleAuthed(s.myMembers))
	mux.HandleFunc("GET /api/v4/users/me/teams/{tid}/channels/categories", s.handleAuthed(s.categories))
	mux.HandleFunc("GET /api/v4/users/me/preferences", s.handleAuthed(s.getPrefs))
	mux.HandleFunc("PUT /api/v4/users/me/preferences", s.handleAuthed(s.putPrefs))
	mux.HandleFunc("GET /api/v4/users/me/status", s.handleAuthed(s.getStatus))
	mux.HandleFunc("POST /api/v4/users/ids", s.handleAuthed(s.usersByIDs))
	mux.HandleFunc("GET /api/v4/channels/{cid}/posts", s.handleAuthed(s.channelPosts))
	mux.HandleFunc("POST /api/v4/posts", s.handleAuthed(s.createPost))
	mux.HandleFunc("PUT /api/v4/posts/{pid}/patch", s.handleAuthed(s.patchPost))
	mux.HandleFunc("DELETE /api/v4/posts/{pid}", s.handleAuthed(s.deletePost))
	mux.HandleFunc("POST /api/v4/channels/members/me/view", s.handleAuthed(s.viewChannel))
	mux.HandleFunc("POST /api/v4/users/me/posts/{pid}/set_unread", s.handleAuthed(s.setUnread))
}

func (s *Server) isMemberLocked(channelID, userID string) bool {
	return s.chat.members[channelID][userID] != nil
}

func (s *Server) myTeams(w http.ResponseWriter, _ *http.Request, _ User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, 200, s.chat.teams)
}

func (s *Server) myChannels(w http.ResponseWriter, _ *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.Channel{}
	for _, id := range s.sortedChannelIDsLocked() {
		if s.isMemberLocked(id, u.ID) && s.chat.channels[id].DeleteAt == 0 {
			out = append(out, *s.chat.channels[id])
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) sortedChannelIDsLocked() []string {
	ids := make([]string, 0, len(s.chat.channels))
	for id := range s.chat.channels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Server) myMembers(w http.ResponseWriter, r *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := []model.ChannelMember{}
	for _, id := range s.sortedChannelIDsLocked() {
		if m := s.chat.members[id][u.ID]; m != nil {
			all = append(all, *m)
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per <= 0 {
		per = 60
	}
	start := min(page*per, len(all))
	writeJSON(w, 200, all[start:min(start+per, len(all))])
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request, u User) {
	tid := r.PathValue("tid")
	s.mu.Lock()
	defer s.mu.Unlock()
	var chans, dms []string
	for _, id := range s.sortedChannelIDsLocked() {
		c := s.chat.channels[id]
		if !s.isMemberLocked(id, u.ID) || c.DeleteAt != 0 {
			continue
		}
		switch {
		case c.Type == model.ChannelDirect || c.Type == model.ChannelGroup:
			dms = append(dms, id)
		case c.TeamID == tid:
			chans = append(chans, id)
		}
	}
	mk := func(typ, name, sorting string, ids []string) model.SidebarCategory {
		if ids == nil {
			ids = []string{}
		}
		return model.SidebarCategory{ID: typ + "_" + u.ID + "_" + tid, TeamID: tid, Type: typ, DisplayName: name, Sorting: sorting, ChannelIDs: ids}
	}
	cats := []model.SidebarCategory{
		mk("favorites", "Favorites", "", nil),
		mk("channels", "Channels", "alpha", chans),
		mk("direct_messages", "Direct Messages", "recent", dms),
	}
	out := model.OrderedCategories{Categories: cats}
	for _, c := range cats {
		out.Order = append(out.Order, c.ID)
	}
	writeJSON(w, 200, out)
}

func (s *Server) getPrefs(w http.ResponseWriter, _ *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]model.Preference{}, s.chat.prefs[u.ID]...)
	writeJSON(w, 200, out)
}

func (s *Server) putPrefs(w http.ResponseWriter, r *http.Request, u User) {
	var in []model.Preference
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		appError(w, 400, "api.preference.bad_body", err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range in {
		p.UserID = u.ID
		s.upsertPrefLocked(p)
	}
	b, _ := json.Marshal(in)
	s.publishLocked("preferences_changed", map[string]any{"preferences": string(b)}, wsBroadcast{UserID: u.ID}, []string{u.ID}, nil)
	writeJSON(w, 200, map[string]string{"status": "OK"})
}

func (s *Server) upsertPrefLocked(p model.Preference) {
	list := s.chat.prefs[p.UserID]
	for i := range list {
		if list[i].Category == p.Category && list[i].Name == p.Name {
			list[i] = p
			return
		}
	}
	s.chat.prefs[p.UserID] = append(list, p)
}

func (s *Server) getStatus(w http.ResponseWriter, _ *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.chat.status[u.ID]
	if st == "" {
		st = "online"
	}
	writeJSON(w, 200, model.Status{UserID: u.ID, Status: st})
}

func (s *Server) usersByIDs(w http.ResponseWriter, r *http.Request, _ User) {
	var ids []string
	_ = json.NewDecoder(r.Body).Decode(&ids)
	out := []model.User{}
	for _, id := range ids {
		if u, ok := s.userByID(id); ok {
			out = append(out, userJSON(u))
		}
	}
	writeJSON(w, 200, out)
}

func visible(p *fpost, crt bool) bool {
	return p.DeleteAt == 0 && p.OriginalID == "" && (!crt || p.RootID == "")
}

func (s *Server) channelPosts(w http.ResponseWriter, r *http.Request, u User) {
	cid := r.PathValue("cid")
	q := r.URL.Query()
	crt := q.Get("collapsedThreads") == "true"
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isMemberLocked(cid, u.ID) {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	list := model.PostList{Order: []string{}, Posts: map[string]model.Post{}}
	all := s.chat.posts[cid]
	if since, _ := strconv.ParseInt(q.Get("since"), 10, 64); since > 0 {
		for i := len(all) - 1; i >= 0 && len(list.Order) < s.chat.sinceLimit; i-- {
			p := all[i]
			if p.UpdateAt <= since || (crt && p.RootID != "") {
				continue
			}
			list.Order = append(list.Order, p.ID)
			list.Posts[p.ID] = p.Post
		}
		writeJSON(w, 200, list)
		return
	}
	var vis []*fpost
	for _, p := range all {
		if visible(p, crt) {
			vis = append(vis, p)
		}
	}
	end := len(vis)
	if before := q.Get("before"); before != "" {
		end = slices.IndexFunc(vis, func(p *fpost) bool { return p.ID == before })
		if end < 0 {
			end = 0
		}
	}
	per, _ := strconv.Atoi(q.Get("per_page"))
	if per <= 0 {
		per = 60
	}
	per = min(per, 200)
	start := max(0, end-per)
	for i := end - 1; i >= start; i-- {
		list.Order = append(list.Order, vis[i].ID)
		list.Posts[vis[i].ID] = vis[i].Post
	}
	if start > 0 {
		list.PrevPostID = vis[start-1].ID
	}
	writeJSON(w, 200, list)
}

var mentionRe = regexp.MustCompile(`@([a-zA-Z0-9][a-zA-Z0-9._-]*)`)

// mentionsLocked mirrors the server: @username, @all/@channel/@here, and
// every DM message mentions the other member. The author is never mentioned.
func (s *Server) mentionsLocked(c *model.Channel, authorID, msg string) []string {
	set := map[string]bool{}
	for uid := range s.chat.members[c.ID] {
		if uid != authorID && c.Type == model.ChannelDirect {
			set[uid] = true
		}
	}
	for _, m := range mentionRe.FindAllStringSubmatch(msg, -1) {
		name := strings.ToLower(strings.TrimRight(m[1], "."))
		switch name {
		case "all", "channel", "here":
			for uid := range s.chat.members[c.ID] {
				set[uid] = true
			}
		default:
			for _, u := range s.opts.Users {
				if strings.ToLower(u.Username) == name && s.chat.members[c.ID][u.ID] != nil {
					set[u.ID] = true
				}
			}
		}
	}
	delete(set, authorID)
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (s *Server) memberIDsLocked(channelID string) []string {
	ids := make([]string, 0, len(s.chat.members[channelID]))
	for id := range s.chat.members[channelID] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Server) insertPostLocked(p *fpost) {
	list := append(s.chat.posts[p.ChannelID], p)
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreateAt < list[j].CreateAt })
	s.chat.posts[p.ChannelID] = list
	s.chat.byID[p.ID] = p
}

type apiErr struct {
	status int
	id     string
}

func (s *Server) createPostLocked(userID string, in model.Post) (model.Post, *apiErr) {
	c := s.chat.channels[in.ChannelID]
	if c == nil || !s.isMemberLocked(c.ID, userID) {
		return model.Post{}, &apiErr{403, "api.context.permissions.app_error"}
	}
	if in.PendingPostID != "" {
		if id, ok := s.chat.pending[in.PendingPostID]; ok {
			return s.chat.byID[id].Post, nil
		}
	}
	now := s.nowLocked()
	p := &fpost{Post: model.Post{ID: newID(), ChannelID: c.ID, UserID: userID, RootID: in.RootID,
		Message: in.Message, PendingPostID: in.PendingPostID, CreateAt: now, UpdateAt: now}}
	root := in.RootID == ""
	c.LastPostAt = now
	c.TotalMsgCount++
	if root {
		c.TotalMsgCountRoot++
		c.LastRootPostAt = now
	} else if r := s.chat.byID[in.RootID]; r != nil {
		r.ReplyCount++
		r.LastReplyAt = now
		r.UpdateAt = now
	}
	p.mentions = s.mentionsLocked(c, userID, in.Message)
	for uid, m := range s.chat.members[c.ID] {
		if uid == userID {
			m.MsgCount, m.MsgCountRoot, m.LastViewedAt = c.TotalMsgCount, c.TotalMsgCountRoot, now
			continue
		}
		if slices.Contains(p.mentions, uid) {
			m.MentionCount++
			if root {
				m.MentionCountRoot++
			}
		}
	}
	s.insertPostLocked(p)
	if in.PendingPostID != "" {
		s.chat.pending[in.PendingPostID] = p.ID
	}
	b, _ := json.Marshal(p.Post)
	sender, _ := s.userByID(userID)
	s.publishLocked("posted", map[string]any{
		"post": string(b), "channel_type": c.Type, "channel_display_name": c.DisplayName,
		"channel_name": c.Name, "sender_name": "@" + sender.Username, "team_id": c.TeamID, "set_online": true,
	}, wsBroadcast{ChannelID: c.ID}, s.memberIDsLocked(c.ID), p.mentions)
	return p.Post, nil
}

func (s *Server) editPostLocked(userID, postID, msg string) (model.Post, *apiErr) {
	p := s.chat.byID[postID]
	if p == nil || p.DeleteAt != 0 || p.OriginalID != "" {
		return model.Post{}, &apiErr{404, "app.post.get.app_error"}
	}
	if p.UserID != userID {
		return model.Post{}, &apiErr{403, "api.context.permissions.app_error"}
	}
	now := s.nowLocked()
	hist := &fpost{Post: p.Post}
	hist.ID, hist.OriginalID, hist.DeleteAt, hist.UpdateAt = newID(), p.ID, now, now
	s.insertPostLocked(hist)
	p.Message, p.EditAt, p.UpdateAt = msg, now, now
	s.chat.channels[p.ChannelID].LastPostAt = now
	b, _ := json.Marshal(p.Post)
	s.publishLocked("post_edited", map[string]any{"post": string(b)}, wsBroadcast{ChannelID: p.ChannelID}, s.memberIDsLocked(p.ChannelID), nil)
	return p.Post, nil
}

func (s *Server) deletePostLocked(userID, postID string) *apiErr {
	p := s.chat.byID[postID]
	if p == nil || p.DeleteAt != 0 || p.OriginalID != "" {
		return &apiErr{404, "app.post.get.app_error"}
	}
	if userID != "" && p.UserID != userID {
		return &apiErr{403, "api.context.permissions.app_error"}
	}
	now := s.nowLocked()
	for _, q := range s.chat.posts[p.ChannelID] {
		if q.ID == p.ID || q.RootID == p.ID {
			q.DeleteAt, q.UpdateAt = now, now
		}
	}
	b, _ := json.Marshal(p.Post)
	s.publishLocked("post_deleted", map[string]any{"post": string(b)}, wsBroadcast{ChannelID: p.ChannelID}, s.memberIDsLocked(p.ChannelID), nil)
	return nil
}

func writeAPIErr(w http.ResponseWriter, e *apiErr) { appError(w, e.status, e.id, e.id) }

func (s *Server) createPost(w http.ResponseWriter, r *http.Request, u User) {
	var in model.Post
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		appError(w, 400, "api.post.bad_body", err.Error())
		return
	}
	s.mu.Lock()
	p, e := s.createPostLocked(u.ID, in)
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) patchPost(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	s.mu.Lock()
	p, e := s.editPostLocked(u.ID, r.PathValue("pid"), in.Message)
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) deletePost(w http.ResponseWriter, r *http.Request, u User) {
	s.mu.Lock()
	e := s.deletePostLocked(u.ID, r.PathValue("pid"))
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "OK"})
}

func (s *Server) viewChannel(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		ChannelID string `json:"channel_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.chat.members[in.ChannelID][u.ID]
	if m == nil {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	c := s.chat.channels[in.ChannelID]
	now := s.nowLocked()
	hadUnread := m.MsgCount < c.TotalMsgCount || m.MentionCount > 0
	m.MsgCount, m.MsgCountRoot, m.MentionCount, m.MentionCountRoot, m.UrgentMentionCount, m.LastViewedAt =
		c.TotalMsgCount, c.TotalMsgCountRoot, 0, 0, 0, now
	if hadUnread {
		s.publishLocked("multiple_channels_viewed", map[string]any{"channel_times": map[string]int64{c.ID: now}},
			wsBroadcast{UserID: u.ID}, []string{u.ID}, nil)
	}
	writeJSON(w, 200, map[string]any{"status": "OK", "last_viewed_at_times": map[string]int64{c.ID: now}})
}

func (s *Server) setUnread(w http.ResponseWriter, r *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.chat.byID[r.PathValue("pid")]
	if p == nil {
		appError(w, 404, "app.post.get.app_error", "not found")
		return
	}
	m := s.chat.members[p.ChannelID][u.ID]
	if m == nil {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	var msgs, roots, ment, mentRoot int64
	for _, q := range s.chat.posts[p.ChannelID] {
		if q.OriginalID != "" {
			continue
		}
		if q.CreateAt < p.CreateAt {
			msgs++
			if q.RootID == "" {
				roots++
			}
		} else if slices.Contains(q.mentions, u.ID) {
			ment++
			if q.RootID == "" {
				mentRoot++
			}
		}
	}
	m.MsgCount, m.MsgCountRoot, m.MentionCount, m.MentionCountRoot, m.LastViewedAt = msgs, roots, ment, mentRoot, p.CreateAt-1
	c := s.chat.channels[p.ChannelID]
	out := model.ChannelUnreadAt{TeamID: c.TeamID, ChannelID: c.ID, MsgCount: msgs, MsgCountRoot: roots,
		MentionCount: ment, MentionCountRoot: mentRoot, LastViewedAt: m.LastViewedAt}
	s.publishLocked("post_unread", map[string]any{
		"msg_count": msgs, "msg_count_root": roots, "mention_count": ment, "mention_count_root": mentRoot,
		"urgent_mention_count": 0, "last_viewed_at": m.LastViewedAt, "post_id": p.ID,
	}, wsBroadcast{UserID: u.ID, ChannelID: c.ID, TeamID: c.TeamID}, []string{u.ID}, nil)
	writeJSON(w, 200, out)
}

// publishLocked records the event; Task 4 also delivers it over WebSocket.
func (s *Server) publishLocked(name string, data map[string]any, b wsBroadcast, to []string, mentions []string) {
	s.chat.events = append(s.chat.events, RecordedEvent{Name: name, To: to})
	s.deliverLocked(name, data, b, to, mentions)
}

// ---- test controls ----

func (s *Server) userIDByName(username string) string {
	for _, u := range s.opts.Users {
		if u.Username == username {
			return u.ID
		}
	}
	panic("mmfake: unknown user " + username)
}

func (s *Server) PostAs(channelID, username, message string) model.Post {
	return s.ReplyAs(channelID, "", username, message)
}

func (s *Server) ReplyAs(channelID, rootID, username, message string) model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.createPostLocked(s.userIDByName(username), model.Post{ChannelID: channelID, RootID: rootID, Message: message})
	if e != nil {
		panic("mmfake: PostAs: " + e.id)
	}
	return p
}

func (s *Server) EditAs(postID, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.chat.byID[postID]
	if p == nil {
		panic("mmfake: EditAs: no post " + postID)
	}
	if _, e := s.editPostLocked(p.UserID, postID, message); e != nil {
		panic("mmfake: EditAs: " + e.id)
	}
}

func (s *Server) DeleteAs(postID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.deletePostLocked("", postID); e != nil {
		panic("mmfake: DeleteAs: " + e.id)
	}
}

func (s *Server) Member(channelID, username string) model.ChannelMember {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.chat.members[channelID][s.userIDByName(username)]; m != nil {
		return *m
	}
	return model.ChannelMember{}
}

func (s *Server) Channel(channelID string) model.Channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.chat.channels[channelID]; c != nil {
		return *c
	}
	return model.Channel{}
}

func (s *Server) VisiblePosts(channelID string) []model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Post
	for _, p := range s.chat.posts[channelID] {
		if visible(p, false) {
			out = append(out, p.Post)
		}
	}
	return out
}

func (s *Server) SetStatus(username, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.userIDByName(username)
	s.chat.status[id] = status
	s.publishLocked("status_change", map[string]any{"status": status, "user_id": id}, wsBroadcast{UserID: id}, []string{id}, nil)
}

// AddChannel creates an open team channel with the given members and tells
// each of them (user_added), like a server-side "add to channel".
func (s *Server) AddChannel(id, display string, usernames ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	s.chat.channels[id] = &model.Channel{ID: id, TeamID: s.chat.teams[0].ID, Type: model.ChannelOpen,
		DisplayName: display, Name: strings.ToLower(strings.ReplaceAll(display, " ", "-")), CreateAt: now}
	s.chat.members[id] = map[string]*model.ChannelMember{}
	for _, name := range usernames {
		uid := s.userIDByName(name)
		s.chat.members[id][uid] = &model.ChannelMember{ChannelID: id, UserID: uid, LastViewedAt: now,
			NotifyProps: map[string]string{"desktop": "default", "mark_unread": "all"}}
		s.publishLocked("user_added", map[string]any{"user_id": uid, "team_id": s.chat.teams[0].ID},
			wsBroadcast{UserID: uid, ChannelID: id}, []string{uid}, nil)
	}
}

func (s *Server) Events() []RecordedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RecordedEvent(nil), s.chat.events...)
}
```
До Task 4 в `chat.go` держать заглушку `func (s *Server) deliverLocked(string, map[string]any, wsBroadcast, []string, []string) {}` — Task 4 заменяет её реальной доставкой (и удаляет заглушку).

`internal/mmfake/seed.go`:
```go
package mmfake

import (
	"fmt"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

func defaultNotify() map[string]string {
	return map[string]string{"desktop": "default", "mark_unread": "all", "ignore_channel_mentions": "default"}
}

func (s *Server) seed() {
	o := s.opts
	s.chat = chatData{
		teams:      []model.Team{{ID: "t-fake", Name: "fake", DisplayName: "Fake Team"}},
		channels:   map[string]*model.Channel{},
		members:    map[string]map[string]*model.ChannelMember{},
		posts:      map[string][]*fpost{},
		byID:       map[string]*fpost{},
		pending:    map[string]string{},
		prefs:      map[string][]model.Preference{},
		status:     map[string]string{},
		sinceLimit: o.SinceLimit,
	}
	if s.chat.sinceLimit <= 0 {
		s.chat.sinceLimit = 1000
	}
	seedPosts := o.SeedPosts
	if seedPosts == 0 {
		seedPosts = 150
	}
	seedPosts = max(seedPosts, 0)
	base := time.Now().Add(-time.Duration(seedPosts+o.ExtraChannels*20+10) * time.Minute).UnixMilli()
	s.chat.lastMs = base
	add := func(id, typ, display, name string, users ...string) {
		s.chat.channels[id] = &model.Channel{ID: id, Type: typ, DisplayName: display, Name: name, CreateAt: base}
		if typ == model.ChannelOpen || typ == model.ChannelPrivate {
			s.chat.channels[id].TeamID = "t-fake"
		}
		s.chat.members[id] = map[string]*model.ChannelMember{}
		for _, u := range users {
			s.chat.members[id][u] = &model.ChannelMember{ChannelID: id, UserID: u, NotifyProps: defaultNotify()}
		}
	}
	add("c-town", model.ChannelOpen, "Town Square", "town-square", "u-alice", "u-bob", "u-carol")
	add("c-offtopic", model.ChannelOpen, "Off-Topic", "off-topic", "u-alice", "u-bob")
	add("c-secret", model.ChannelPrivate, "Secret", "secret", "u-alice")
	add("c-dm-bob", model.ChannelDirect, "", "u-alice__u-bob", "u-alice", "u-bob")
	add("c-gm", model.ChannelGroup, "alice, bob, carol", "gm-alice-bob-carol", "u-alice", "u-bob", "u-carol")
	for i := 1; i <= o.ExtraChannels; i++ {
		add(fmt.Sprintf("c-load-%03d", i), model.ChannelOpen, fmt.Sprintf("Load %03d", i), fmt.Sprintf("load-%03d", i), "u-alice", "u-bob")
	}
	authors := []string{"u-bob", "u-carol"}
	for i := 1; i <= seedPosts; i++ {
		s.seedPostLocked("c-town", authors[i%2], fmt.Sprintf("Message #%d", i))
	}
	s.seedPostLocked("c-offtopic", "u-bob", "Welcome to off-topic")
	s.seedPostLocked("c-dm-bob", "u-bob", "Hi Alice, this is Bob")
	for i := 1; i <= o.ExtraChannels; i++ {
		for j := 1; j <= 20; j++ {
			s.seedPostLocked(fmt.Sprintf("c-load-%03d", i), "u-bob", fmt.Sprintf("Load message %d with **some** markdown and a [link](https://example.com)", j))
		}
	}
	for _, m := range s.allMembersLocked() {
		c := s.chat.channels[m.ChannelID]
		m.MsgCount, m.MsgCountRoot, m.LastViewedAt = c.TotalMsgCount, c.TotalMsgCountRoot, s.chat.lastMs
	}
	s.chat.prefs["u-alice"] = []model.Preference{
		{UserID: "u-alice", Category: "direct_channel_show", Name: "u-bob", Value: "true"},
		{UserID: "u-alice", Category: "group_channel_show", Name: "c-gm", Value: "true"},
	}
}

// seedPostLocked appends a post one minute after the previous seed post,
// without mentions or events (the seed is history the client finds on login).
func (s *Server) seedPostLocked(channelID, userID, msg string) {
	s.chat.lastMs += int64(time.Minute / time.Millisecond)
	c := s.chat.channels[channelID]
	p := &fpost{Post: model.Post{ID: newID(), ChannelID: channelID, UserID: userID, Message: msg, CreateAt: s.chat.lastMs, UpdateAt: s.chat.lastMs}}
	c.LastPostAt, c.LastRootPostAt = s.chat.lastMs, s.chat.lastMs
	c.TotalMsgCount++
	c.TotalMsgCountRoot++
	s.insertPostLocked(p)
}

func (s *Server) allMembersLocked() []*model.ChannelMember {
	var out []*model.ChannelMember
	for _, byUser := range s.chat.members {
		for _, m := range byUser {
			out = append(out, m)
		}
	}
	return out
}
```
(Поле `chat chatData` добавить в `Server`.)

- [ ] **Step 4: Тесты проходят (включая старые тесты входа)**

Run: `go test -race ./internal/mmfake/ ./cmd/... ./internal/api/...`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/mmfake
git commit -m "mmfake: chat data, REST for channels/posts/members/categories, test controls"
```

---

### Task 4: mmfake — WebSocket с seq и resume

**Files:**
- Create: `internal/mmfake/ws.go`, `internal/mmfake/ws_test.go`
- Modify: `internal/mmfake/chat.go` (удалить заглушку `deliverLocked`), `internal/mmfake/server.go` (маршрут `GET /api/v4/websocket`, поле `hub`), `go.mod` (`github.com/coder/websocket` из indirect в direct)

**Interfaces:**
- Consumes: Task 3 (`publishLocked`, `wsBroadcast`, `authed`).
- Produces: `GET /api/v4/websocket` — авторизация `Authorization: Bearer` (иначе 401 до upgrade); `?connection_id=&sequence_number=` — resume по семантике сервера (досылка из dead queue на 128 событий / без потерь / новый `connection_id` + `hello` seq 0); `(*Server).DropConnections(lose bool)` — закрыть все сокеты; `lose=true` забывает сессии, и следующий resume получает новый `hello`. Событие: `{"event","data","broadcast","seq"}`; у `posted` в `data.mentions` — JSON-строка `["<id получателя>"]`, только если получатель упомянут.

- [ ] **Step 1: Падающие тесты**

`internal/mmfake/ws_test.go`:
```go
package mmfake

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type frame struct {
	Event     string          `json:"event"`
	Data      map[string]any  `json:"data"`
	Broadcast json.RawMessage `json:"broadcast"`
	Seq       int64           `json:"seq"`
}

func dialWS(t *testing.T, s *Server, tok, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := "ws" + strings.TrimPrefix(s.URL(), "http") + "/api/v4/websocket" + query
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	require.NoError(t, err)
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func read(t *testing.T, c *websocket.Conn) frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	require.NoError(t, err)
	var f frame
	require.NoError(t, json.Unmarshal(b, &f))
	return f
}

func noFrame(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := c.Read(ctx)
	require.Error(t, err, "expected no frame")
}

func TestWSHelloThenPostedWithRecipientMentions(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	hello := read(t, c)
	assert.Equal(t, "hello", hello.Event)
	assert.Equal(t, int64(0), hello.Seq)
	assert.NotEmpty(t, hello.Data["connection_id"])

	s.PostAs("c-offtopic", "bob", "hey @alice")
	f := read(t, c)
	assert.Equal(t, "posted", f.Event)
	assert.Equal(t, int64(1), f.Seq)
	assert.Equal(t, `["u-alice"]`, f.Data["mentions"])
	assert.IsType(t, "", f.Data["post"], "post is a JSON string")

	s.PostAs("c-offtopic", "bob", "no mention")
	f = read(t, c)
	assert.Equal(t, int64(2), f.Seq)
	_, has := f.Data["mentions"]
	assert.False(t, has)
}

func TestWSUnauthorizedRejected(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	u := "ws" + strings.TrimPrefix(s.URL(), "http") + "/api/v4/websocket"
	_, resp, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer nope"}}})
	require.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestWSResumeReplaysMissed(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	id := read(t, c).Data["connection_id"].(string)
	s.PostAs("c-offtopic", "bob", "one")
	assert.Equal(t, int64(1), read(t, c).Seq)

	s.DropConnections(false)
	s.PostAs("c-offtopic", "bob", "two") // seq 2 while disconnected
	c2 := dialWS(t, s, a.tok, "?connection_id="+id+"&sequence_number=2")
	f := read(t, c2)
	assert.Equal(t, "posted", f.Event, "replay, no hello")
	assert.Equal(t, int64(2), f.Seq)
}

func TestWSResumeLosslessSendsNothing(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	id := read(t, c).Data["connection_id"].(string)
	s.DropConnections(false)
	c2 := dialWS(t, s, a.tok, "?connection_id="+id+"&sequence_number=1")
	noFrame(t, c2)
	s.PostAs("c-offtopic", "bob", "live")
	assert.Equal(t, int64(1), read(t, c2).Seq)
}

func TestWSResumeAfterLossGetsNewHello(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	id := read(t, c).Data["connection_id"].(string)
	s.DropConnections(true)
	s.PostAs("c-offtopic", "bob", "lost")
	c2 := dialWS(t, s, a.tok, "?connection_id="+id+"&sequence_number=1")
	f := read(t, c2)
	assert.Equal(t, "hello", f.Event)
	assert.Equal(t, int64(0), f.Seq)
	assert.NotEqual(t, id, f.Data["connection_id"])
}

func TestWSOnlyMembersGetChannelEvents(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	carol := loginAs(t, s, "carol")
	c := dialWS(t, s, carol.tok, "")
	read(t, c) // hello
	s.PostAs("c-offtopic", "bob", "carol is not a member")
	noFrame(t, c)
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/mmfake/ -run WS`
Expected: FAIL (нет маршрута, `DropConnections`).

- [ ] **Step 3: Реализация**

`internal/mmfake/ws.go`:
```go
package mmfake

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/coder/websocket"
)

const deadQueueSize = 128

type wsEvent struct {
	Event     string         `json:"event"`
	Data      map[string]any `json:"data"`
	Broadcast wsBroadcast    `json:"broadcast"`
	Seq       int64          `json:"seq"`
}

// wsSession is one server-side connection identity (connection_id). It
// outlives the socket: while parked (conn == nil) events keep landing in the
// dead queue, so a resume can replay them — like the real hub.
type wsSession struct {
	id      string
	userID  string
	nextSeq int64
	dead    []wsEvent
	conn    *websocket.Conn
	out     chan wsEvent
	cancel  context.CancelFunc
}

type wsHub struct{ sessions map[string]*wsSession }

func (h *wsHub) stamp(sess *wsSession, ev wsEvent) wsEvent {
	ev.Seq = sess.nextSeq
	sess.nextSeq++
	sess.dead = append(sess.dead, ev)
	if len(sess.dead) > deadQueueSize {
		sess.dead = sess.dead[len(sess.dead)-deadQueueSize:]
	}
	return ev
}

func (s *Server) websocketHandler(w http.ResponseWriter, r *http.Request) {
	u, _, ok := s.authed(r)
	if !ok {
		appError(w, 401, "api.context.session_expired.app_error", "Invalid or expired session, please login again.")
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	q := r.URL.Query()
	connID, seqStr := q.Get("connection_id"), q.Get("sequence_number")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	s.mu.Lock()
	var sess *wsSession
	var replay []wsEvent
	hello := false
	if connID != "" {
		seq, perr := strconv.ParseInt(seqStr, 10, 64)
		if perr != nil {
			s.mu.Unlock()
			c.Close(websocket.StatusPolicyViolation, "sequence number not present")
			return
		}
		if old := s.hub.sessions[connID]; old != nil && old.userID == u.ID && old.conn == nil {
			sess = old
			idx := slices.IndexFunc(old.dead, func(e wsEvent) bool { return e.Seq == seq })
			switch {
			case idx >= 0:
				replay = append(replay, old.dead[idx:]...)
			case len(old.dead) == 0 || old.dead[len(old.dead)-1].Seq == seq-1:
				// lossless: nothing missed
			default:
				delete(s.hub.sessions, old.id)
				sess.id, sess.nextSeq, sess.dead = newID(), 0, nil
				hello = true
			}
		}
	}
	if sess == nil {
		sess = &wsSession{id: newID(), userID: u.ID}
		hello = true
	}
	s.hub.sessions[sess.id] = sess
	sess.conn, sess.out, sess.cancel = c, make(chan wsEvent, 1024), cancel
	out := sess.out
	if hello {
		out <- s.hub.stamp(sess, wsEvent{Event: "hello",
			Data:      map[string]any{"connection_id": sess.id, "server_version": "10.11.0-fake"},
			Broadcast: wsBroadcast{UserID: u.ID}})
	}
	for _, ev := range replay {
		out <- ev
	}
	s.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-out:
				b, _ := json.Marshal(ev)
				if c.Write(ctx, websocket.MessageText, b) != nil {
					cancel()
					return
				}
			}
		}
	}()
	for {
		// Reading also answers the client's pings.
		if _, _, err := c.Read(ctx); err != nil {
			break
		}
	}
	cancel()
	s.mu.Lock()
	if sess.conn == c {
		sess.conn, sess.out = nil, nil
	}
	s.mu.Unlock()
	c.CloseNow()
}

// deliverLocked stamps the event into every session of every recipient
// (parked ones included) and pushes it to live sockets. A full socket queue
// drops that session entirely — the next resume then gets a new hello, the
// same as the real server's send-queue overflow.
func (s *Server) deliverLocked(name string, data map[string]any, b wsBroadcast, to []string, mentions []string) {
	for _, sess := range s.hub.sessions {
		if !slices.Contains(to, sess.userID) {
			continue
		}
		d := make(map[string]any, len(data)+1)
		for k, v := range data {
			d[k] = v
		}
		if slices.Contains(mentions, sess.userID) {
			d["mentions"] = `["` + sess.userID + `"]`
		}
		ev := s.hub.stamp(sess, wsEvent{Event: name, Data: d, Broadcast: b})
		if sess.out != nil {
			select {
			case sess.out <- ev:
			default:
				sess.cancel()
				sess.conn, sess.out = nil, nil
				delete(s.hub.sessions, sess.id)
			}
		}
	}
}

// DropConnections closes every socket. lose=true also forgets the sessions,
// so a resume gets a fresh connection_id (server restart / reaped conn).
func (s *Server) DropConnections(lose bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.hub.sessions {
		if sess.cancel != nil {
			sess.cancel()
		}
		sess.conn, sess.out = nil, nil
		if lose {
			delete(s.hub.sessions, id)
		}
	}
}
```
В `server.go`: поле `hub wsHub` (инициализировать `sessions: map[string]*wsSession{}` в `Start`), маршрут `mux.HandleFunc("GET /api/v4/websocket", s.websocketHandler)`; в `Close()` перед `s.ts.Close()` вызвать `s.DropConnections(true)`, иначе `httptest.Server.Close` ждёт висящие сокеты.

- [ ] **Step 4: Тесты проходят**

Run: `go mod tidy && go test -race ./internal/mmfake/`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add go.mod go.sum internal/mmfake
git commit -m "mmfake: websocket with hello, seq, dead-queue resume and drop control"
```

---
### Task 5: WebSocket-клиент

**Files:**
- Create: `internal/mm/ws/conn.go`, `internal/mm/ws/decode.go`, `internal/mm/ws/conn_test.go`, `internal/mm/ws/decode_test.go`

**Interfaces:**
- Consumes: Task 1 (`model`, `rest.Error`/`KindNetwork`/`KindAuth`), Task 4 (фейк для тестов).
- Produces:
  - `type Options struct{ BaseURL, Token string; HTTPClient *http.Client; PingInterval, PingTimeout time.Duration }` (0 → 30 с / 20 с).
  - `type Resume struct{ ConnectionID string; NextSeq int64 }`.
  - `type Broadcast struct{ UserID, ChannelID, TeamID string }` (json `user_id/channel_id/team_id`).
  - `type Event struct{ Type string; Seq int64; Data json.RawMessage; Broadcast Broadcast; Reset bool }`: `Reset` — hello с новым `connection_id` после уже бывшего потока (события потеряны).
  - `var ErrSeqGap`.
  - `func Dial(ctx, Options, Resume) (*Conn, error)`: ошибка — `*rest.Error` (401/403 на upgrade → `KindAuth`, иначе `KindNetwork`).
  - `(*Conn).Events() <-chan Event` (закрывается при обрыве), `Err() error` (после закрытия, `*rest.Error` `KindNetwork`, оборачивает `ErrSeqGap`), `Resume() Resume`, `Close()`.
  - `func URL(base string, r Resume) (string, error)`.
  - Декодеры: `DecodePosted(Event) (Posted, error)` (`Posted{Post model.Post; ChannelType, ChannelName, TeamID, SenderName string; Mentions, Followers []string}`), `DecodePost`, `DecodeReaction`, `DecodeChannelTimes`, `DecodePostUnread` (`model.ChannelUnreadAt`, channel/team из broadcast), `DecodeChannel`, `DecodeMember`, `DecodePreferences`, `DecodeUser`, `(Event).Str(key string) string`.

- [ ] **Step 1: Падающие тесты**

`internal/mm/ws/conn_test.go`:
```go
package ws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/mmfake"
)

func login(t *testing.T, f *mmfake.Server, user string) string {
	t.Helper()
	tok, _, err := rest.New(f.URL(), "", nil).Login(context.Background(), user, "secret")
	require.NoError(t, err)
	return tok
}

func next(t *testing.T, c *Conn) Event {
	t.Helper()
	select {
	case ev, ok := <-c.Events():
		require.True(t, ok, "stream closed: %v", c.Err())
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

func waitClosed(t *testing.T, c *Conn) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-c.Events():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream did not close")
		}
	}
}

func TestURL(t *testing.T) {
	u, err := URL("https://mm.example.com/sub", Resume{})
	require.NoError(t, err)
	assert.Equal(t, "wss://mm.example.com/sub/api/v4/websocket", u)
	u, _ = URL("http://127.0.0.1:8065", Resume{ConnectionID: "abc", NextSeq: 7})
	assert.Equal(t, "ws://127.0.0.1:8065/api/v4/websocket?connection_id=abc&sequence_number=7", u)
}

func TestDialHelloAndPosted(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	c, err := Dial(context.Background(), Options{BaseURL: f.URL(), Token: login(t, f, "alice")}, Resume{})
	require.NoError(t, err)
	defer c.Close()
	hello := next(t, c)
	assert.Equal(t, "hello", hello.Type)
	assert.False(t, hello.Reset)
	assert.NotEmpty(t, c.Resume().ConnectionID)
	assert.Equal(t, int64(1), c.Resume().NextSeq)

	f.PostAs("c-offtopic", "bob", "hi @alice")
	ev := next(t, c)
	require.Equal(t, "posted", ev.Type)
	p, err := DecodePosted(ev)
	require.NoError(t, err)
	assert.Equal(t, "hi @alice", p.Post.Message)
	assert.Equal(t, []string{"u-alice"}, p.Mentions)
	assert.Equal(t, "c-offtopic", ev.Broadcast.ChannelID)
	assert.Equal(t, int64(2), c.Resume().NextSeq)
}

func TestDialUnauthorizedIsAuthError(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	_, err := Dial(context.Background(), Options{BaseURL: f.URL(), Token: "bad"}, Resume{})
	require.Error(t, err)
	assert.True(t, rest.IsAuth(err))
}

func TestResumeReplaysMissedEvents(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	o := Options{BaseURL: f.URL(), Token: login(t, f, "alice")}
	c, err := Dial(context.Background(), o, Resume{})
	require.NoError(t, err)
	next(t, c) // hello
	f.DropConnections(false)
	waitClosed(t, c)
	assert.True(t, rest.IsNetwork(c.Err()))
	f.PostAs("c-offtopic", "bob", "while away")

	c2, err := Dial(context.Background(), o, c.Resume())
	require.NoError(t, err)
	defer c2.Close()
	ev := next(t, c2)
	require.Equal(t, "posted", ev.Type, "replayed, no hello")
	p, _ := DecodePosted(ev)
	assert.Equal(t, "while away", p.Post.Message)
}

func TestResumeAfterLossGetsResetHello(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	o := Options{BaseURL: f.URL(), Token: login(t, f, "alice")}
	c, err := Dial(context.Background(), o, Resume{})
	require.NoError(t, err)
	next(t, c)
	old := c.Resume().ConnectionID
	f.DropConnections(true)
	waitClosed(t, c)
	c2, err := Dial(context.Background(), o, c.Resume())
	require.NoError(t, err)
	defer c2.Close()
	hello := next(t, c2)
	assert.Equal(t, "hello", hello.Type)
	assert.True(t, hello.Reset)
	assert.NotEqual(t, old, c2.Resume().ConnectionID)
}

// rawServer runs a hand-written server side for protocol edge cases.
func rawServer(t *testing.T, h func(ctx context.Context, c *websocket.Conn)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		h(r.Context(), c)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestSeqGapClosesAndKeepsResumePoint(t *testing.T) {
	u := rawServer(t, func(ctx context.Context, c *websocket.Conn) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"hello","data":{"connection_id":"x"},"seq":0}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"posted","data":{},"seq":5}`))
		_, _, _ = c.Read(ctx)
	})
	c, err := Dial(context.Background(), Options{BaseURL: u, Token: "t"}, Resume{})
	require.NoError(t, err)
	defer c.Close()
	assert.Equal(t, "hello", next(t, c).Type)
	waitClosed(t, c)
	assert.True(t, errors.Is(c.Err(), ErrSeqGap))
	assert.Equal(t, Resume{ConnectionID: "x", NextSeq: 1}, c.Resume())
}

func TestRepliesWithSeqReplyAreIgnored(t *testing.T) {
	u := rawServer(t, func(ctx context.Context, c *websocket.Conn) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"hello","data":{"connection_id":"x"},"seq":0}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"status":"OK","seq_reply":1}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"typing","data":{},"seq":1}`))
		_, _, _ = c.Read(ctx)
	})
	c, err := Dial(context.Background(), Options{BaseURL: u, Token: "t"}, Resume{})
	require.NoError(t, err)
	defer c.Close()
	next(t, c)
	assert.Equal(t, "typing", next(t, c).Type)
}

func TestKeepaliveDetectsSilentPeer(t *testing.T) {
	u := rawServer(t, func(ctx context.Context, _ *websocket.Conn) {
		<-ctx.Done() // never reads → never answers pings
	})
	c, err := Dial(context.Background(), Options{BaseURL: u, Token: "t", PingInterval: 50 * time.Millisecond, PingTimeout: 100 * time.Millisecond}, Resume{})
	require.NoError(t, err)
	defer c.Close()
	waitClosed(t, c)
	assert.True(t, rest.IsNetwork(c.Err()))
}

func TestCloseIsIdempotentAndClosesEvents(t *testing.T) {
	f := mmfake.Start(mmfake.Options{})
	defer f.Close()
	c, err := Dial(context.Background(), Options{BaseURL: f.URL(), Token: login(t, f, "alice")}, Resume{})
	require.NoError(t, err)
	c.Close()
	c.Close()
	waitClosed(t, c)
}
```

`internal/mm/ws/decode_test.go`:
```go
package ws

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ev(typ, data string, b Broadcast) Event {
	return Event{Type: typ, Data: json.RawMessage(data), Broadcast: b}
}

func TestDecoders(t *testing.T) {
	p, err := DecodePosted(ev("posted", `{"post":"{\"id\":\"p1\",\"message\":\"m\",\"channel_id\":\"c1\"}","channel_type":"D","sender_name":"@bob","team_id":"","mentions":"[\"u1\"]","followers":"[\"u1\"]"}`, Broadcast{ChannelID: "c1"}))
	require.NoError(t, err)
	assert.Equal(t, "p1", p.Post.ID)
	assert.Equal(t, "D", p.ChannelType)
	assert.Equal(t, []string{"u1"}, p.Mentions)
	assert.Equal(t, []string{"u1"}, p.Followers)

	post, err := DecodePost(ev("post_edited", `{"post":"{\"id\":\"p2\",\"edit_at\":5}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, int64(5), post.EditAt)

	r, err := DecodeReaction(ev("reaction_added", `{"reaction":"{\"user_id\":\"u2\",\"post_id\":\"p1\",\"emoji_name\":\"+1\"}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, "+1", r.EmojiName)

	times, err := DecodeChannelTimes(ev("multiple_channels_viewed", `{"channel_times":{"c1":10,"c2":20}}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, int64(20), times["c2"])

	u, err := DecodePostUnread(ev("post_unread", `{"msg_count":3,"mention_count":1,"last_viewed_at":7,"post_id":"p1"}`, Broadcast{ChannelID: "c1", TeamID: "t1"}))
	require.NoError(t, err)
	assert.Equal(t, "c1", u.ChannelID)
	assert.Equal(t, int64(3), u.MsgCount)

	ch, err := DecodeChannel(ev("channel_updated", `{"channel":"{\"id\":\"c1\",\"display_name\":\"New\"}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, "New", ch.DisplayName)

	m, err := DecodeMember(ev("channel_member_updated", `{"channelMember":"{\"channel_id\":\"c1\",\"notify_props\":{\"mark_unread\":\"mention\"}}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.True(t, m.Muted())

	prefs, err := DecodePreferences(ev("preferences_changed", `{"preferences":"[{\"category\":\"a\",\"name\":\"b\",\"value\":\"c\"}]"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, "c", prefs[0].Value)

	usr, err := DecodeUser(ev("user_updated", `{"user":{"id":"u2","username":"bob"}}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, "bob", usr.Username)

	assert.Equal(t, "c9", ev("user_removed", `{"channel_id":"c9"}`, Broadcast{}).Str("channel_id"))
	assert.Equal(t, "", ev("x", `{"n":1}`, Broadcast{}).Str("n"))
}

func TestDecodePostedRejectsGarbage(t *testing.T) {
	_, err := DecodePosted(ev("posted", `{"post":"not json"}`, Broadcast{}))
	assert.Error(t, err)
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/mm/ws/`
Expected: FAIL (пакета нет).

- [ ] **Step 3: Реализация**

`internal/mm/ws/conn.go`:
```go
// Package ws is the Mattermost WebSocket client. It authenticates with the
// Authorization header on the upgrade request — the only way the server can
// resume a connection (the authentication_challenge path always gets a new
// connection_id) — tracks the event sequence, resumes with
// connection_id+sequence_number and detects dead peers with pings. See
// docs/research/2026-09-24-mattermost-api-facts.md §1.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/spk/spk-mattermost/internal/mm/rest"
)

const (
	defaultPingInterval = 30 * time.Second
	defaultPingTimeout  = 20 * time.Second
	readLimit           = 16 << 20
	eventBuffer         = 4096
)

var ErrSeqGap = errors.New("ws: event sequence gap")

type Options struct {
	BaseURL      string // normalized http(s) server URL
	Token        string
	HTTPClient   *http.Client
	PingInterval time.Duration
	PingTimeout  time.Duration
}

// Resume identifies the server-side stream to continue: NextSeq is the next
// sequence number we expect (Mattermost's sequence_number semantics).
type Resume struct {
	ConnectionID string
	NextSeq      int64
}

type Broadcast struct {
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id"`
	TeamID    string `json:"team_id"`
}

type Event struct {
	Type      string
	Seq       int64
	Data      json.RawMessage
	Broadcast Broadcast
	// Reset: a hello that starts a new server-side stream after we already
	// had one — events were lost and the caller must resync.
	Reset bool
}

type Conn struct {
	ws     *websocket.Conn
	events chan Event
	cancel context.CancelFunc
	once   sync.Once

	mu     sync.Mutex
	resume Resume
	err    error
}

func URL(base string, r Resume) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("ws: unsupported scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v4/websocket"
	u.RawQuery = ""
	if r.ConnectionID != "" {
		u.RawQuery = "connection_id=" + url.QueryEscape(r.ConnectionID) + "&sequence_number=" + strconv.FormatInt(r.NextSeq, 10)
	}
	return u.String(), nil
}

func Dial(ctx context.Context, o Options, r Resume) (*Conn, error) {
	u, err := URL(o.BaseURL, r)
	if err != nil {
		return nil, &rest.Error{Kind: rest.KindAPI, Err: err}
	}
	wc, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPClient: o.HTTPClient,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + o.Token}},
	})
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return nil, &rest.Error{Kind: rest.KindAuth, Status: resp.StatusCode, Err: err}
		}
		return nil, &rest.Error{Kind: rest.KindNetwork, Err: err}
	}
	wc.SetReadLimit(readLimit)
	interval, timeout := o.PingInterval, o.PingTimeout
	if interval <= 0 {
		interval = defaultPingInterval
	}
	if timeout <= 0 {
		timeout = defaultPingTimeout
	}
	rctx, cancel := context.WithCancel(context.Background())
	c := &Conn{ws: wc, events: make(chan Event, eventBuffer), cancel: cancel, resume: r}
	go c.readLoop(rctx)
	go c.keepalive(rctx, interval, timeout)
	return c, nil
}

func (c *Conn) Events() <-chan Event { return c.events }

func (c *Conn) Resume() Resume {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resume
}

// Err is why the stream ended; meaningful once Events() is closed.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) Close() {
	c.fail(errors.New("closed"))
}

// fail records the first error and tears the socket down; the read loop
// then closes Events().
func (c *Conn) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = &rest.Error{Kind: rest.KindNetwork, Err: err}
	}
	c.mu.Unlock()
	c.once.Do(func() {
		c.cancel()
		c.ws.CloseNow()
	})
}

type envelope struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data"`
	Broadcast Broadcast       `json:"broadcast"`
	Seq       int64           `json:"seq"`
}

func (c *Conn) readLoop(ctx context.Context) {
	defer close(c.events)
	for {
		_, b, err := c.ws.Read(ctx)
		if err != nil {
			c.fail(fmt.Errorf("ws read: %w", err))
			return
		}
		var env envelope
		if json.Unmarshal(b, &env) != nil || env.Event == "" {
			continue // replies to our requests (seq_reply), junk
		}
		ev := Event{Type: env.Event, Seq: env.Seq, Data: env.Data, Broadcast: env.Broadcast}
		c.mu.Lock()
		if env.Event == "hello" {
			var d struct {
				ConnectionID string `json:"connection_id"`
			}
			_ = json.Unmarshal(env.Data, &d)
			ev.Reset = c.resume.ConnectionID != "" && d.ConnectionID != c.resume.ConnectionID
			c.resume = Resume{ConnectionID: d.ConnectionID, NextSeq: env.Seq + 1}
		} else if env.Seq != c.resume.NextSeq {
			want := c.resume.NextSeq
			c.mu.Unlock()
			// Keep NextSeq at the gap: the resume asks the server for it.
			c.fail(fmt.Errorf("%w: got %d, want %d", ErrSeqGap, env.Seq, want))
			return
		} else {
			c.resume.NextSeq = env.Seq + 1
		}
		c.mu.Unlock()
		select {
		case c.events <- ev:
		case <-ctx.Done():
			return
		}
	}
}

// keepalive pings the server; a socket that stays open while the peer stops
// answering (dropped NAT mapping, hung proxy, laptop sleep) would otherwise
// look live forever.
func (c *Conn) keepalive(ctx context.Context, interval, timeout time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, timeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					c.fail(fmt.Errorf("ws keepalive: %w", err))
				}
				return
			}
		}
	}
}
```

`internal/mm/ws/decode.go`:
```go
package ws

import (
	"encoding/json"
	"errors"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

// Str returns a string field of the event data ("" if absent or not a string).
func (e Event) Str(key string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(e.Data, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[key], &s) != nil {
		return ""
	}
	return s
}

// embedded decodes data[key], a JSON document encoded as a string (Mattermost
// double-encodes posts, reactions, channels, members and preferences).
func embedded(e Event, key string, out any) error {
	s := e.Str(key)
	if s == "" {
		return errors.New("ws: missing " + key)
	}
	return json.Unmarshal([]byte(s), out)
}

type Posted struct {
	Post        model.Post
	ChannelType string
	ChannelName string
	TeamID      string
	SenderName  string
	Mentions    []string // contains our id only if we are mentioned
	Followers   []string // contains our id only if we follow the thread
}

func DecodePosted(e Event) (Posted, error) {
	var out Posted
	if err := embedded(e, "post", &out.Post); err != nil {
		return Posted{}, err
	}
	out.ChannelType, out.ChannelName = e.Str("channel_type"), e.Str("channel_display_name")
	out.TeamID, out.SenderName = e.Str("team_id"), e.Str("sender_name")
	if m := e.Str("mentions"); m != "" {
		_ = json.Unmarshal([]byte(m), &out.Mentions)
	}
	if f := e.Str("followers"); f != "" {
		_ = json.Unmarshal([]byte(f), &out.Followers)
	}
	return out, nil
}

func DecodePost(e Event) (model.Post, error) {
	var p model.Post
	return p, embedded(e, "post", &p)
}

func DecodeReaction(e Event) (model.Reaction, error) {
	var r model.Reaction
	return r, embedded(e, "reaction", &r)
}

func DecodeChannelTimes(e Event) (map[string]int64, error) {
	var d struct {
		ChannelTimes map[string]int64 `json:"channel_times"`
	}
	err := json.Unmarshal(e.Data, &d)
	return d.ChannelTimes, err
}

func DecodePostUnread(e Event) (model.ChannelUnreadAt, error) {
	var u model.ChannelUnreadAt
	err := json.Unmarshal(e.Data, &u)
	u.ChannelID, u.TeamID = e.Broadcast.ChannelID, e.Broadcast.TeamID
	return u, err
}

func DecodeChannel(e Event) (model.Channel, error) {
	var c model.Channel
	return c, embedded(e, "channel", &c)
}

func DecodeMember(e Event) (model.ChannelMember, error) {
	var m model.ChannelMember
	return m, embedded(e, "channelMember", &m)
}

func DecodePreferences(e Event) ([]model.Preference, error) {
	var p []model.Preference
	return p, embedded(e, "preferences", &p)
}

// DecodeUser: user_updated carries the user as an object, not a string.
func DecodeUser(e Event) (model.User, error) {
	var d struct {
		User model.User `json:"user"`
	}
	err := json.Unmarshal(e.Data, &d)
	return d.User, err
}
```

- [ ] **Step 4: Тесты проходят**

Run: `go test -race ./internal/mm/ws/`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/mm/ws go.mod go.sum
git commit -m "mm/ws: websocket client with bearer auth, seq tracking, resume and keepalive"
```

---

### Task 6: Горячий слой — метаданные, непрочитанное, сайдбар

**Files:**
- Create: `internal/state/server.go`, `internal/state/sidebar.go`, `internal/state/server_test.go`, `internal/state/sidebar_test.go`, `internal/state/fixture_test.go`

**Interfaces:**
- Consumes: Task 1 `model.*`.
- Produces (пакет `state`):
  - `const WindowSize = 60`.
  - `type Config struct{ CollapsedThreads, TeammateNameDisplay string; LockTeammateNameDisplay bool }` (json snake_case).
  - `type Bootstrap struct{ Me model.User; Status model.Status; Config Config; Prefs []model.Preference; Teams []model.Team; Channels []model.Channel; Members []model.ChannelMember; Categories map[string]model.OrderedCategories }`.
  - `type Chan struct{ Info model.Channel; Member model.ChannelMember; Win Window }`; `type Window struct{ Posts []model.Post; Loaded, Complete bool; SyncedAt int64; Stale bool; GapAfter string }` (json snake_case).
  - `type Server`; `func New(now func() time.Time) *Server`.
  - Методы: `Bootstrap(Bootstrap)`, `Me() model.User`, `CRT() bool`, `TeamIDs() []string`, `SetUsers([]model.User)`, `MissingUserIDs() []string`, `Badge() Badge`, `Sidebar(teamID string) SidebarView`, `SetStatus(model.Status)`.
  - `type Badge struct{ Unread bool; Mentions int }`; `type SidebarView struct{ TeamID string; SelectedChannelID string; Teams []TeamItem; Categories []CategoryView }`; `type TeamItem struct{ ID, Name, DisplayName string; Unread bool; Mentions int }`; `type CategoryView struct{ ID, Type, Name string; Collapsed bool; Channels []ChannelItem }`; `type ChannelItem struct{ ID, Name, Type string; Unread bool; Mentions int; Muted bool }` — все с json-тегами snake_case, это DTO для UI.
  - Внутренние (для Task 7/8): `mu`, `chans map[string]*Chan`, `prefs map[prefKey]string`, `users`, `nav Nav`, `active`, `focused`, `dirty dirtySet`, `unreadLocked(*Chan) (bool, int)`, `crtLocked()`, `displayNameLocked(userID)`, `channelNameLocked(*Chan)`, `prefLocked(cat, name, def)`.

- [ ] **Step 1: Фикстура и падающие тесты**

`internal/state/fixture_test.go`:
```go
package state

import (
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

var t0 = time.UnixMilli(1_000_000)

func fixedNow() time.Time { return t0 }

func notify(extra ...string) map[string]string {
	m := map[string]string{"desktop": "default", "mark_unread": "all"}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	return m
}

// fixture: me=u1 in team t1.
//   town   O read              off   O 2 unread (favorite)
//   muted  O 3 unread, muted   arch  O archived
//   dm2    D u2, 1 unread (mention)   dm3 D u3, read, direct_channel_show=false
//   gm     G read, group_channel_show=true
func fixture() Bootstrap {
	ch := func(id, typ, name string, total int64, lastPost int64) model.Channel {
		team := "t1"
		if typ == model.ChannelDirect || typ == model.ChannelGroup {
			team = ""
		}
		slug := id
		if id == "town" {
			slug = "town-square"
		}
		return model.Channel{ID: id, TeamID: team, Type: typ, DisplayName: name, Name: slug,
			TotalMsgCount: total, TotalMsgCountRoot: total, LastPostAt: lastPost, LastRootPostAt: lastPost, CreateAt: 1}
	}
	mem := func(id string, read int64, mentions int64, np map[string]string) model.ChannelMember {
		return model.ChannelMember{ChannelID: id, UserID: "u1", MsgCount: read, MsgCountRoot: read,
			MentionCount: mentions, MentionCountRoot: mentions, NotifyProps: np, LastViewedAt: 10}
	}
	dm2 := ch("dm2", model.ChannelDirect, "", 5, 500)
	dm2.Name = "u1__u2"
	dm3 := ch("dm3", model.ChannelDirect, "", 1, 100)
	dm3.Name = "u3__u1"
	arch := ch("arch", model.ChannelOpen, "Archived", 1, 50)
	arch.DeleteAt = 99
	return Bootstrap{
		Me:     model.User{ID: "u1", Username: "alice"},
		Config: Config{CollapsedThreads: "disabled", TeammateNameDisplay: "username"},
		Prefs: []model.Preference{
			{Category: "direct_channel_show", Name: "u3", Value: "false"},
			{Category: "group_channel_show", Name: "gm", Value: "true"},
		},
		Teams: []model.Team{{ID: "t1", Name: "team", DisplayName: "Team"}},
		Channels: []model.Channel{
			ch("town", model.ChannelOpen, "Town Square", 10, 400), ch("off", model.ChannelOpen, "Off-Topic", 7, 300),
			ch("muted", model.ChannelOpen, "Muted", 3, 200), arch, dm2, dm3, ch("gm", model.ChannelGroup, "alice, bob, carol", 2, 150),
			ch("notmember", model.ChannelOpen, "Not a member", 1, 1),
		},
		Members: []model.ChannelMember{
			mem("town", 10, 0, notify()), mem("off", 5, 0, notify()), mem("muted", 0, 0, notify("mark_unread", "mention")),
			mem("arch", 1, 0, notify()), mem("dm2", 4, 1, notify()), mem("dm3", 1, 0, notify()), mem("gm", 2, 0, notify()),
		},
		Categories: map[string]model.OrderedCategories{"t1": {
			Categories: []model.SidebarCategory{
				{ID: "fav", Type: "favorites", DisplayName: "Favorites", ChannelIDs: []string{"off"}},
				{ID: "chs", Type: "channels", DisplayName: "Channels", Sorting: "alpha", ChannelIDs: []string{"town", "muted", "arch"}},
				{ID: "dms", Type: "direct_messages", DisplayName: "Direct Messages", Sorting: "recent", ChannelIDs: []string{"dm2", "dm3", "gm"}},
			},
			Order: []string{"fav", "chs", "dms"},
		}},
	}
}

func newFixture() *Server {
	s := New(fixedNow)
	s.Bootstrap(fixture())
	s.SetUsers([]model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob", FirstName: "Bob", LastName: "Brown"}, {ID: "u3", Username: "carol"}})
	return s
}

func ids(items []ChannelItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}
```

`internal/state/server_test.go`:
```go
package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

func TestUnreadNonCRTAndMuted(t *testing.T) {
	s := newFixture()
	s.mu.Lock()
	defer s.mu.Unlock()
	u, m := s.unreadLocked(s.chans["off"])
	assert.True(t, u)
	assert.Equal(t, 0, m)
	u, _ = s.unreadLocked(s.chans["muted"])
	assert.False(t, u, "muted: unread only on mentions")
	u, m = s.unreadLocked(s.chans["dm2"])
	assert.True(t, u)
	assert.Equal(t, 1, m)
	u, _ = s.unreadLocked(s.chans["town"])
	assert.False(t, u)
}

func TestUnreadCRTUsesRootCounters(t *testing.T) {
	b := fixture()
	b.Config.CollapsedThreads = "always_on"
	for i := range b.Channels {
		if b.Channels[i].ID == "town" {
			b.Channels[i].TotalMsgCount = 15 // 5 replies, roots unchanged
		}
	}
	s := New(fixedNow)
	s.Bootstrap(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	u, _ := s.unreadLocked(s.chans["town"])
	assert.False(t, u, "replies do not make a channel unread under CRT")
}

func TestCRTFromConfigAndPreference(t *testing.T) {
	cases := []struct {
		cfg, pref string
		want      bool
	}{
		{"disabled", "on", false}, {"", "on", false}, {"always_on", "off", true},
		{"default_on", "", true}, {"default_on", "off", false}, {"default_off", "", false}, {"default_off", "on", true},
	}
	for _, c := range cases {
		b := fixture()
		b.Config.CollapsedThreads = c.cfg
		if c.pref != "" {
			b.Prefs = append(b.Prefs, model.Preference{Category: "display_settings", Name: "collapsed_reply_threads", Value: c.pref})
		}
		s := New(fixedNow)
		s.Bootstrap(b)
		assert.Equal(t, c.want, s.CRT(), "%+v", c)
	}
}

func TestBadgeSkipsMutedAndArchived(t *testing.T) {
	s := newFixture()
	b := s.Badge()
	assert.True(t, b.Unread)
	assert.Equal(t, 1, b.Mentions)
}

func TestNameFormat(t *testing.T) {
	s := newFixture()
	sb := s.Sidebar("t1")
	assert.Equal(t, "bob", findItem(sb, "dm2").Name)
	b := fixture()
	b.Prefs = append(b.Prefs, model.Preference{Category: "display_settings", Name: "name_format", Value: "full_name"})
	s.Bootstrap(b)
	assert.Equal(t, "Bob Brown", findItem(s.Sidebar("t1"), "dm2").Name)
}

func TestMissingUsers(t *testing.T) {
	s := New(fixedNow)
	s.Bootstrap(fixture())
	assert.ElementsMatch(t, []string{"u2", "u3"}, s.MissingUserIDs())
	s.SetUsers([]model.User{{ID: "u2"}, {ID: "u3"}})
	assert.Empty(t, s.MissingUserIDs())
}

func TestBootstrapDropsChannelsWeLeft(t *testing.T) {
	s := newFixture()
	b := fixture()
	b.Members = b.Members[1:] // left "town"
	s.Bootstrap(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.chans["town"]
	require.False(t, ok)
	_, ok = s.chans["notmember"]
	assert.False(t, ok, "channels without membership are ignored")
}

func findItem(sb SidebarView, id string) ChannelItem {
	for _, c := range sb.Categories {
		for _, it := range c.Channels {
			if it.ID == id {
				return it
			}
		}
	}
	return ChannelItem{}
}
```

`internal/state/sidebar_test.go`:
```go
package state

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

func TestSidebarCategoriesVisibilityAndOrder(t *testing.T) {
	sb := newFixture().Sidebar("t1")
	assert.Equal(t, "t1", sb.TeamID)
	require.Len(t, sb.Categories, 3)
	assert.Equal(t, []string{"off"}, ids(sb.Categories[0].Channels))
	assert.Equal(t, []string{"town", "muted"}, ids(sb.Categories[1].Channels), "alpha, muted last, archived hidden")
	assert.Equal(t, []string{"dm2", "gm"}, ids(sb.Categories[2].Channels), "recent; dm3 closed by preference")
	off := sb.Categories[0].Channels[0]
	assert.True(t, off.Unread)
	muted := sb.Categories[1].Channels[1]
	assert.True(t, muted.Muted)
	assert.False(t, muted.Unread)
}

func TestSidebarTeamsAggregateWithoutDMs(t *testing.T) {
	sb := newFixture().Sidebar("")
	require.Len(t, sb.Teams, 1)
	assert.True(t, sb.Teams[0].Unread, "off is unread")
	assert.Equal(t, 0, sb.Teams[0].Mentions, "dm2 mention is not a team mention")
}

func TestUncategorizedChannelsAreAppended(t *testing.T) {
	b := fixture()
	b.Channels = append(b.Channels, model.Channel{ID: "new", TeamID: "t1", Type: model.ChannelOpen, DisplayName: "Brand New"})
	b.Members = append(b.Members, model.ChannelMember{ChannelID: "new", UserID: "u1"})
	s := New(fixedNow)
	s.Bootstrap(b)
	assert.Contains(t, ids(s.Sidebar("t1").Categories[1].Channels), "new")
}

func TestMissingCategoriesAreSynthesized(t *testing.T) {
	b := fixture()
	b.Categories = nil
	s := New(fixedNow)
	s.Bootstrap(b)
	sb := s.Sidebar("t1")
	require.Len(t, sb.Categories, 2)
	assert.Equal(t, "channels", sb.Categories[0].Type)
	assert.Equal(t, "direct_messages", sb.Categories[1].Type)
	assert.ElementsMatch(t, []string{"town", "off", "muted"}, ids(sb.Categories[0].Channels))
}

func TestNaturalAlphaSort(t *testing.T) {
	b := fixture()
	b.Categories["t1"].Categories[1].ChannelIDs = []string{"c10", "c2", "c1"}
	for _, n := range []string{"c10", "c2", "c1"} {
		b.Channels = append(b.Channels, model.Channel{ID: n, TeamID: "t1", Type: model.ChannelOpen, DisplayName: "Ch " + n[1:]})
		b.Members = append(b.Members, model.ChannelMember{ChannelID: n, UserID: "u1"})
	}
	s := New(fixedNow)
	s.Bootstrap(b)
	// town/muted are not in the category any more → appended as uncategorized, sorted after
	assert.Equal(t, []string{"c1", "c2", "c10"}, ids(s.Sidebar("t1").Categories[1].Channels)[:3])
}

func TestManualSortKeepsServerOrder(t *testing.T) {
	b := fixture()
	b.Categories["t1"].Categories[1].Sorting = "manual"
	b.Categories["t1"].Categories[1].ChannelIDs = []string{"muted", "town"}
	s := New(fixedNow)
	s.Bootstrap(b)
	assert.Equal(t, []string{"muted", "town"}, ids(s.Sidebar("t1").Categories[1].Channels))
}

func TestDMLimitKeepsUnreadAndMostRecentlyViewed(t *testing.T) {
	b := fixture()
	b.Prefs = append(b.Prefs, model.Preference{Category: "sidebar_settings", Name: "limit_visible_dms_gms", Value: "2"})
	var dmIDs []string
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("dx%d", i)
		dmIDs = append(dmIDs, id)
		other := fmt.Sprintf("ux%d", i)
		b.Channels = append(b.Channels, model.Channel{ID: id, Type: model.ChannelDirect, Name: "u1__" + other, TotalMsgCount: 1, LastPostAt: int64(10 + i)})
		b.Members = append(b.Members, model.ChannelMember{ChannelID: id, UserID: "u1", MsgCount: 1, LastViewedAt: int64(100 + i)})
		b.Prefs = append(b.Prefs, model.Preference{Category: "direct_channel_show", Name: other, Value: "true"})
	}
	b.Categories["t1"].Categories[2].ChannelIDs = append([]string{"dm2", "dm3", "gm"}, dmIDs...)
	s := New(fixedNow)
	s.Bootstrap(b)
	got := ids(s.Sidebar("t1").Categories[2].Channels)
	// dm2 (unread) + the most recently viewed read one (dx3); limit = max(2, unread count 1) = 2
	assert.ElementsMatch(t, []string{"dm2", "dx3"}, got)
}

func TestSelectedChannelDefaultsAndSticks(t *testing.T) {
	s := newFixture()
	assert.Equal(t, "town", s.Sidebar("t1").SelectedChannelID, "no history: the team's town-square-like first channel")
	s.mu.Lock()
	s.nav.Channel = map[string]string{"t1": "off"}
	s.mu.Unlock()
	assert.Equal(t, "off", s.Sidebar("t1").SelectedChannelID)
}
```
(Правило выбора по умолчанию: последний открытый канал команды, иначе канал команды с `Name == "town-square"`, иначе первый канал первой непустой категории.)

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/state/`
Expected: FAIL (пакета нет).

- [ ] **Step 3: Реализация**

`internal/state/server.go`:
```go
// Package state is the in-memory hot layer of one Mattermost server:
// metadata (teams, channels, memberships, categories, preferences, users)
// and a window of the latest posts per channel. Every view the UI shows is
// built from here, so switching channels never waits on the network. The
// sync worker (internal/mmsync) is the only writer. All methods are safe for
// concurrent use; *Locked helpers expect s.mu held.
package state

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

const WindowSize = 60

type Config struct {
	CollapsedThreads        string `json:"collapsed_threads"`
	TeammateNameDisplay     string `json:"teammate_name_display"`
	LockTeammateNameDisplay bool   `json:"lock_teammate_name_display"`
}

type Bootstrap struct {
	Me         model.User
	Status     model.Status
	Config     Config
	Prefs      []model.Preference
	Teams      []model.Team
	Channels   []model.Channel
	Members    []model.ChannelMember
	Categories map[string]model.OrderedCategories // by team id
}

type Window struct {
	Posts    []model.Post `json:"posts"` // oldest → newest, ≤ WindowSize
	Loaded   bool         `json:"loaded"`
	Complete bool         `json:"complete"`  // Posts start at the channel's first post
	SyncedAt int64        `json:"synced_at"` // local ms: history complete up to here
	Stale    bool         `json:"stale"`     // posts after GapAfter may be missing
	GapAfter string       `json:"gap_after"`
}

type Chan struct {
	Info   model.Channel
	Member model.ChannelMember
	Win    Window
}

type Nav struct {
	TeamID  string            `json:"team_id"`
	Channel map[string]string `json:"channel"` // team id → last opened channel
}

type Badge struct {
	Unread   bool `json:"unread"`
	Mentions int  `json:"mentions"`
}

type prefKey struct{ cat, name string }

type Server struct {
	mu  sync.Mutex
	now func() time.Time

	me     model.User
	status model.Status
	cfg    Config
	prefs  map[prefKey]string
	teams  []model.Team
	chans  map[string]*Chan
	cats   map[string]model.OrderedCategories
	users  map[string]model.User
	nav    Nav

	// Task 7: posts, pending, active channel.
	pending       map[string][]Pending
	drafts        map[string]string
	active        string
	focused       bool
	newSince      int64
	older         []model.Post
	olderComplete bool
	suppressView  string
	guard         map[string]int64
	seen          seenSet

	dirty dirtySet // Task 8
}

func New(now func() time.Time) *Server {
	if now == nil {
		now = time.Now
	}
	return &Server{
		now: now, prefs: map[prefKey]string{}, chans: map[string]*Chan{}, cats: map[string]model.OrderedCategories{},
		users: map[string]model.User{}, pending: map[string][]Pending{}, drafts: map[string]string{},
		nav: Nav{Channel: map[string]string{}}, guard: map[string]int64{}, seen: newSeenSet(2000), dirty: newDirtySet(),
	}
}

// Bootstrap replaces all metadata with a fresh server read. Post windows of
// channels we are still in survive; channels we left disappear.
func (s *Server) Bootstrap(b Bootstrap) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.me, s.status, s.cfg = b.Me, b.Status, b.Config
	s.users[b.Me.ID] = b.Me
	s.prefs = map[prefKey]string{}
	for _, p := range b.Prefs {
		s.prefs[prefKey{p.Category, p.Name}] = p.Value
	}
	s.teams = s.teams[:0]
	for _, t := range b.Teams {
		if t.DeleteAt == 0 {
			s.teams = append(s.teams, t)
		}
	}
	sort.SliceStable(s.teams, func(i, j int) bool {
		return strings.ToLower(s.teams[i].DisplayName) < strings.ToLower(s.teams[j].DisplayName)
	})
	members := make(map[string]model.ChannelMember, len(b.Members))
	for _, m := range b.Members {
		members[m.ChannelID] = m
	}
	next := make(map[string]*Chan, len(b.Channels))
	s.guard = map[string]int64{}
	for _, c := range b.Channels {
		m, ok := members[c.ID]
		if !ok {
			continue
		}
		ch := s.chans[c.ID]
		if ch == nil {
			ch = &Chan{}
		}
		ch.Info, ch.Member = c, m
		next[c.ID] = ch
		s.guard[c.ID] = c.LastPostAt
		s.dirty.chans[c.ID] = true
	}
	for id := range s.chans {
		if next[id] == nil {
			s.forgetChannelLocked(id)
		}
	}
	s.chans = next
	s.cats = map[string]model.OrderedCategories{}
	for k, v := range b.Categories {
		s.cats[k] = v
	}
	if !s.hasTeamLocked(s.nav.TeamID) && len(s.teams) > 0 {
		s.nav.TeamID = s.teams[0].ID
	}
	s.dirty.meta = true
}

// forgetChannelLocked drops everything local about a channel (left, kicked,
// deleted server-side). The caller removes it from s.chans.
func (s *Server) forgetChannelLocked(id string) {
	delete(s.drafts, id)
	delete(s.pending, id)
	s.dirty.dropChan(id)
}

func (s *Server) hasTeamLocked(id string) bool {
	for _, t := range s.teams {
		if t.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) Me() model.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.me
}

func (s *Server) SetStatus(st model.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = st
}

func (s *Server) CRT() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.crtLocked()
}

func (s *Server) TeamIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.teams))
	for i, t := range s.teams {
		out[i] = t.ID
	}
	return out
}

func (s *Server) prefLocked(cat, name, def string) string {
	if v, ok := s.prefs[prefKey{cat, name}]; ok {
		return v
	}
	return def
}

// crtLocked mirrors the webapp's isCollapsedThreadsEnabled.
func (s *Server) crtLocked() bool {
	switch s.cfg.CollapsedThreads {
	case "", "disabled":
		return false
	case "always_on":
		return true
	}
	def := "off"
	if s.cfg.CollapsedThreads == "default_on" {
		def = "on"
	}
	return s.prefLocked("display_settings", "collapsed_reply_threads", def) == "on"
}

func (s *Server) nameFormatLocked() string {
	if s.cfg.LockTeammateNameDisplay && s.cfg.TeammateNameDisplay != "" {
		return s.cfg.TeammateNameDisplay
	}
	if v := s.prefLocked("display_settings", "name_format", ""); v != "" {
		return v
	}
	if s.cfg.TeammateNameDisplay != "" {
		return s.cfg.TeammateNameDisplay
	}
	return "username"
}

// displayNameLocked: "" for users not loaded yet (the UI shows a placeholder).
func (s *Server) displayNameLocked(userID string) string {
	u, ok := s.users[userID]
	if !ok {
		return ""
	}
	switch s.nameFormatLocked() {
	case "full_name":
		if n := u.FullName(); n != "" {
			return n
		}
	case "nickname_full_name":
		if u.Nickname != "" {
			return u.Nickname
		}
		if n := u.FullName(); n != "" {
			return n
		}
	}
	return u.Username
}

func (s *Server) channelNameLocked(c *Chan) string {
	if c.Info.IsDM() {
		if n := s.displayNameLocked(c.Info.DMPartner(s.me.ID)); n != "" {
			return n
		}
		return c.Info.DMPartner(s.me.ID)
	}
	if c.Info.DisplayName != "" {
		return c.Info.DisplayName
	}
	return c.Info.Name
}

// unreadLocked mirrors the webapp's calculateUnreadCount.
func (s *Server) unreadLocked(c *Chan) (unread bool, mentions int) {
	var msgs, ment int64
	if s.crtLocked() {
		msgs, ment = c.Info.TotalMsgCountRoot-c.Member.MsgCountRoot, c.Member.MentionCountRoot
	} else {
		msgs, ment = c.Info.TotalMsgCount-c.Member.MsgCount, c.Member.MentionCount
	}
	return ment > 0 || (!c.Member.Muted() && msgs > 0), int(ment)
}

// Badge sums mentions across the server, skipping muted and archived
// channels (the webapp's getUnreadStatus).
func (s *Server) Badge() Badge {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b Badge
	for _, c := range s.chans {
		if c.Info.DeleteAt != 0 || c.Member.Muted() {
			continue
		}
		u, m := s.unreadLocked(c)
		b.Unread = b.Unread || u
		b.Mentions += m
	}
	return b
}

func (s *Server) SetUsers(us []model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range us {
		s.users[u.ID] = u
		s.dirty.users[u.ID] = true
	}
}

// MissingUserIDs lists users the views need (DM partners, post authors)
// that are not loaded yet.
func (s *Server) MissingUserIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	need := map[string]bool{}
	for _, c := range s.chans {
		if p := c.Info.DMPartner(s.me.ID); p != "" {
			need[p] = true
		}
		for _, p := range c.Win.Posts {
			need[p.UserID] = true
		}
	}
	for _, p := range s.older {
		need[p.UserID] = true
	}
	var out []string
	for id := range need {
		if _, ok := s.users[id]; !ok && id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func atoiDefault(s string, def int64) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}
```

`internal/state/sidebar.go`:
```go
package state

import (
	"sort"
	"strings"
	"unicode"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

type TeamItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Unread      bool   `json:"unread"`
	Mentions    int    `json:"mentions"`
}

type ChannelItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Unread   bool   `json:"unread"`
	Mentions int    `json:"mentions"`
	Muted    bool   `json:"muted"`
}

type CategoryView struct {
	ID        string        `json:"id"`
	Type      string        `json:"type"`
	Name      string        `json:"name"`
	Collapsed bool          `json:"collapsed"`
	Channels  []ChannelItem `json:"channels"`
}

type SidebarView struct {
	TeamID            string         `json:"team_id"`
	SelectedChannelID string         `json:"selected_channel_id"`
	Teams             []TeamItem     `json:"teams"`
	Categories        []CategoryView `json:"categories"`
}

const defaultDMLimit = 40

// Sidebar builds the sidebar of teamID ("" = the last used team) and makes
// it the current team.
func (s *Server) Sidebar(teamID string) SidebarView {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasTeamLocked(teamID) {
		teamID = s.nav.TeamID
	}
	if teamID != s.nav.TeamID {
		s.nav.TeamID = teamID
		s.dirty.meta = true
	}
	v := SidebarView{TeamID: teamID, Teams: s.teamItemsLocked(), Categories: s.categoriesLocked(teamID)}
	v.SelectedChannelID = s.selectedLocked(teamID, v.Categories)
	return v
}

func (s *Server) teamItemsLocked() []TeamItem {
	out := make([]TeamItem, 0, len(s.teams))
	for _, t := range s.teams {
		it := TeamItem{ID: t.ID, Name: t.Name, DisplayName: t.DisplayName}
		for _, c := range s.chans {
			if c.Info.TeamID != t.ID || c.Info.DeleteAt != 0 || c.Member.Muted() {
				continue
			}
			u, m := s.unreadLocked(c)
			it.Unread = it.Unread || u
			it.Mentions += m
		}
		out = append(out, it)
	}
	return out
}

func (s *Server) inTeamLocked(c *Chan, teamID string) bool {
	return c.Info.DeleteAt == 0 && (c.Info.TeamID == teamID || c.Info.TeamID == "")
}

func isDirect(c *Chan) bool { return c.Info.IsDM() || c.Info.IsGroup() }

func (s *Server) categoriesLocked(teamID string) []CategoryView {
	oc, ok := s.cats[teamID]
	if !ok || len(oc.Categories) == 0 {
		oc = model.OrderedCategories{
			Categories: []model.SidebarCategory{
				{ID: "channels", Type: "channels", DisplayName: "Channels", Sorting: "alpha"},
				{ID: "direct_messages", Type: "direct_messages", DisplayName: "Direct Messages", Sorting: "recent"},
			},
			Order: []string{"channels", "direct_messages"},
		}
	}
	byID := map[string]model.SidebarCategory{}
	for _, c := range oc.Categories {
		byID[c.ID] = c
	}
	order := oc.Order
	if len(order) == 0 {
		for _, c := range oc.Categories {
			order = append(order, c.ID)
		}
	}
	placed := map[string]bool{}
	var cats []model.SidebarCategory
	for _, id := range order {
		c, ok := byID[id]
		if !ok {
			continue
		}
		var keep []string
		for _, chID := range c.ChannelIDs {
			if ch := s.chans[chID]; ch != nil && s.inTeamLocked(ch, teamID) && !placed[chID] {
				keep = append(keep, chID)
				placed[chID] = true
			}
		}
		c.ChannelIDs = keep
		cats = append(cats, c)
	}
	// Channels the categories don't know yet (joined a moment ago) go to
	// the default category of their kind.
	var extraCh, extraDM []string
	for id, ch := range s.chans {
		if placed[id] || !s.inTeamLocked(ch, teamID) {
			continue
		}
		if isDirect(ch) {
			extraDM = append(extraDM, id)
		} else {
			extraCh = append(extraCh, id)
		}
	}
	sort.Strings(extraCh)
	sort.Strings(extraDM)
	for i := range cats {
		switch cats[i].Type {
		case "channels":
			cats[i].ChannelIDs = append(cats[i].ChannelIDs, extraCh...)
			extraCh = nil
		case "direct_messages":
			cats[i].ChannelIDs = append(cats[i].ChannelIDs, extraDM...)
			extraDM = nil
		}
	}
	out := make([]CategoryView, 0, len(cats))
	for _, c := range cats {
		ids := c.ChannelIDs
		if c.Type == "direct_messages" {
			ids = s.visibleDMsLocked(ids)
		} else {
			var vis []string
			for _, id := range ids {
				ch := s.chans[id]
				u, _ := s.unreadLocked(ch)
				if !isDirect(ch) || s.dmShownLocked(ch, u) {
					vis = append(vis, id)
				}
			}
			ids = vis
		}
		ids = s.sortLocked(ids, c.Sorting)
		cv := CategoryView{ID: c.ID, Type: c.Type, Name: c.DisplayName, Collapsed: c.Collapsed, Channels: []ChannelItem{}}
		for _, id := range ids {
			ch := s.chans[id]
			u, m := s.unreadLocked(ch)
			cv.Channels = append(cv.Channels, ChannelItem{ID: id, Name: s.channelNameLocked(ch), Type: ch.Info.Type,
				Unread: u, Mentions: m, Muted: ch.Member.Muted()})
		}
		out = append(out, cv)
	}
	return out
}

// dmShownLocked: the webapp's manual-close filter.
func (s *Server) dmShownLocked(c *Chan, unread bool) bool {
	if unread || c.Info.ID == s.active {
		return true
	}
	var v string
	var ok bool
	if c.Info.IsDM() {
		v, ok = s.prefs[prefKey{"direct_channel_show", c.Info.DMPartner(s.me.ID)}]
	} else {
		v, ok = s.prefs[prefKey{"group_channel_show", c.Info.ID}]
	}
	return ok && v != "false"
}

func (s *Server) dmLastSeenLocked(c *Chan) int64 {
	t := c.Member.LastViewedAt
	t = max(t, atoiDefault(s.prefLocked("channel_approximate_view_time", c.Info.ID, ""), 0))
	t = max(t, atoiDefault(s.prefLocked("channel_open_time", c.Info.ID, ""), 0))
	return t
}

// visibleDMsLocked: manual-close filter, then the autoclose limit
// (sidebar_settings/limit_visible_dms_gms, default 40) keeping the active,
// unread and most recently seen conversations.
func (s *Server) visibleDMsLocked(ids []string) []string {
	type cand struct {
		id     string
		active bool
		unread bool
		seen   int64
	}
	var cs []cand
	unreadN := 0
	for _, id := range ids {
		ch := s.chans[id]
		u, _ := s.unreadLocked(ch)
		if !s.dmShownLocked(ch, u) {
			continue
		}
		if u {
			unreadN++
		}
		cs = append(cs, cand{id, id == s.active, u, s.dmLastSeenLocked(ch)})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.active != b.active {
			return a.active
		}
		if a.unread != b.unread {
			return a.unread
		}
		return a.seen > b.seen
	})
	limit := int(atoiDefault(s.prefLocked("sidebar_settings", "limit_visible_dms_gms", ""), defaultDMLimit))
	limit = max(limit, unreadN)
	if limit > 0 && len(cs) > limit {
		cs = cs[:limit]
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.id
	}
	return out
}

func (s *Server) sortLocked(ids []string, sorting string) []string {
	out := append([]string(nil), ids...)
	switch sorting {
	case "manual":
		return out
	case "recent":
		crt := s.crtLocked()
		key := func(id string) int64 {
			c := s.chans[id].Info
			last := c.LastPostAt
			if crt && c.LastRootPostAt != 0 {
				last = c.LastRootPostAt
			}
			return max(last, c.CreateAt)
		}
		sort.SliceStable(out, func(i, j int) bool { return key(out[i]) > key(out[j]) })
	default: // "", "alpha"
		sort.SliceStable(out, func(i, j int) bool {
			a, b := s.chans[out[i]], s.chans[out[j]]
			if a.Member.Muted() != b.Member.Muted() {
				return b.Member.Muted()
			}
			return naturalLess(s.channelNameLocked(a), s.channelNameLocked(b))
		})
	}
	return out
}

// naturalLess compares case-insensitively with digit runs as numbers
// ("ch2" < "ch10"), like localeCompare(…, {numeric: true}).
func naturalLess(a, b string) bool {
	ar, br := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if unicode.IsDigit(ar[i]) && unicode.IsDigit(br[j]) {
			si := i
			for i < len(ar) && unicode.IsDigit(ar[i]) {
				i++
			}
			sj := j
			for j < len(br) && unicode.IsDigit(br[j]) {
				j++
			}
			na := strings.TrimLeft(string(ar[si:i]), "0")
			nb := strings.TrimLeft(string(br[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ar[i] != br[j] {
			return ar[i] < br[j]
		}
		i++
		j++
	}
	return len(ar)-i < len(br)-j
}

func (s *Server) selectedLocked(teamID string, cats []CategoryView) string {
	if id := s.nav.Channel[teamID]; id != "" {
		if c := s.chans[id]; c != nil && s.inTeamLocked(c, teamID) {
			return id
		}
	}
	for _, c := range s.chans {
		if c.Info.TeamID == teamID && c.Info.Name == "town-square" && c.Info.DeleteAt == 0 {
			return c.Info.ID
		}
	}
	for _, cat := range cats {
		if len(cat.Channels) > 0 {
			return cat.Channels[0].ID
		}
	}
	return ""
}
```
(Типы `Pending`, `seenSet`, `dirtySet` — из Task 7/8. Чтобы Task 6 компилировался сам по себе, добавить в этой задаче минимальные определения в `internal/state/posts.go` и `internal/state/snapshot.go`:
```go
// posts.go (Task 7 extends)
type Pending struct{ ID, ChannelID, RootID, Message string; CreateAt int64; Failed bool }
type seenSet struct{ ids map[string]struct{}; ring []string; next int }
func newSeenSet(n int) seenSet { return seenSet{ids: map[string]struct{}{}, ring: make([]string, n)} }
// snapshot.go (Task 8 extends)
type dirtySet struct{ meta, live bool; chans, posts, users, delChans map[string]bool }
func newDirtySet() dirtySet { return dirtySet{chans: map[string]bool{}, posts: map[string]bool{}, users: map[string]bool{}, delChans: map[string]bool{}} }
func (d *dirtySet) dropChan(id string) { delete(d.chans, id); delete(d.posts, id); d.delChans[id] = true }
```
Task 7 и 8 заменяют их полными версиями. Поля `Server`, которые использует только Task 7 (`newSince`, `older`, `olderComplete`, `suppressView`, `focused`), можно добавить в Task 7, если `golangci-lint` (`unused`) ругается на них здесь.)

- [ ] **Step 4: Тесты проходят**

Run: `go test -race ./internal/state/`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/state
git commit -m "state: hot metadata layer — unread (CRT-aware), badges, sidebar categories and DM visibility"
```

---
### Task 7: Горячий слой — окна постов, события, представление канала

**Files:**
- Create/replace: `internal/state/posts.go` (полная версия вместо заглушки из Task 6), `internal/state/events.go`, `internal/state/view.go`
- Create: `internal/state/posts_test.go`, `internal/state/events_test.go`, `internal/state/view_test.go`

**Interfaces:**
- Consumes: Task 5 (`ws.Event`, `ws.Decode*`), Task 6 (`Server`, `unreadLocked`, `channelNameLocked`, `displayNameLocked`, `dirty`).
- Produces:
  - Окна: `SetWindow(channelID string, page []model.Post, complete bool, syncedAt int64)`; `MergeSince(channelID string, posts []model.Post, syncedAt int64)`; `AppendOlder(channelID string, posts []model.Post, complete bool)`; `OldestPostID(channelID string) string`; `MarkStale(liveUntil int64)`; `type SyncItem struct{ ChannelID string; Loaded bool; SyncedAt int64; Priority int }`; `SyncItems() []SyncItem`; `SyncItemFor(channelID string) (SyncItem, bool)`.
  - Pending: `type Pending struct{ ID, ChannelID, RootID, Message string; CreateAt int64; Failed bool }`; `AddPending(channelID, rootID, message string) Pending`; `FailPending(channelID, id string) Change`; `RetryPending(channelID, id string) (Pending, bool)`; `DropPending(channelID, id string) Change`.
  - Посты: `PostCreated(model.Post) Change`; `ApplyPostUpdate(model.Post) Change`; `RemovePost(postID string) Change`; `FindPost(postID string) (model.Post, bool)`.
  - Черновики и активный канал: `SetDraft(channelID, text string)`; `SetActive(channelID string) (newSince int64)`; `Active() string`; `SetFocused(bool)`; `Focused() bool`; `NeedsView(channelID string) bool`; `ViewedLocally(channelID string, at int64) Change`; `SetUnread(model.ChannelUnreadAt) Change`; `ClearGuard()`.
  - События: `ApplyEvent(ws.Event) Effects`; `type Change struct{ Sidebar, Badge bool; Channels []string }` (+ `Merge(Change)`, `Empty() bool`); `type Effects struct{ Change; NeedMeta bool; NeedUsers []string; ShowDM *model.Preference; View string; Notify *NotifyCandidate; Resync bool }`; `type NotifyCandidate struct{ Post model.Post; Channel model.Channel; ChannelName string; Member model.ChannelMember; Me model.User; Status model.Status; CRT, Focused bool; Active string; Mentions, Followers []string; SenderName string }`.
  - Представление: `ChannelView(channelID string) (ChannelView, bool)`; `ChannelView{ID, Name, Type, Header, Purpose, TeamName string; Posts []PostView; NewSince int64; HasMore, Loaded, Syncing bool; GapAfter, Draft, MeID string; CRT, Muted bool}`; `PostView{ID, UserID, Author, RootID, Message string; CreateAt, EditAt, ReplyCount int64; System, Bot, Pending, Failed bool; Attachments []model.Attachment; Files []FileView; Reactions []ReactionView}`; `FileView{Name string; Size int64; Mime string}`; `ReactionView{Emoji string; Count int; Mine bool}` — json snake_case.

Правила (из `docs/research/…` §2, §4, §5):
- В ленту попадает пост, если `original_id == ""`, `delete_at == 0` и (без CRT или `root_id == ""`).
- Счётчики на `posted` меняются **один раз на id**: `seenSet` помнит 2000 последних id; пост, уже бывший в окне, — не новый. Пока действует guard после Bootstrap (до `ClearGuard`), пост с `create_at <= channel.last_post_at` из Bootstrap счётчики не меняет: REST уже его учёл.
- Новый пост: `total_msg_count++` (`_root++` для корня), `last_post_at = max`. Свой пост — член канала «прочитал всё». Чужой с нашим id в `mentions` — `mention_count++` (`_root++` для корня). CRT-ответ в ленту не идёт, но у корня в окне растёт `reply_count`.
- Окно ≤ 60 постов; при обрезке `Complete = false`. Страница `SetWindow` сливается с постами окна, которые **новее** страницы (пришли по WS во время запроса).
- `MergeSince`: `original_id != ""` пропустить; `delete_at > 0` — удалить из окна (и ответы под ним); новые — вставить, если не старше самого старого поста окна (или окно `Complete`/пустое); известные — заменить, если `update_at` не меньше.

- [ ] **Step 1: Падающие тесты**

`internal/state/posts_test.go`:
```go
package state

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/ws"
)

func mkPost(id, ch, user string, at int64) model.Post {
	return model.Post{ID: id, ChannelID: ch, UserID: user, Message: "msg " + id, CreateAt: at, UpdateAt: at}
}

func postedEv(p model.Post, mentions ...string) ws.Event {
	pb, _ := json.Marshal(p)
	d := map[string]any{"post": string(pb), "channel_type": "O", "sender_name": "@" + p.UserID}
	if len(mentions) > 0 {
		mb, _ := json.Marshal(mentions)
		d["mentions"] = string(mb)
	}
	db, _ := json.Marshal(d)
	return ws.Event{Type: "posted", Data: db, Broadcast: ws.Broadcast{ChannelID: p.ChannelID}}
}

func postEv(typ string, p model.Post) ws.Event {
	pb, _ := json.Marshal(p)
	db, _ := json.Marshal(map[string]any{"post": string(pb)})
	return ws.Event{Type: typ, Data: db, Broadcast: ws.Broadcast{ChannelID: p.ChannelID}}
}

func windowIDs(s *Server, ch string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, p := range s.chans[ch].Win.Posts {
		out = append(out, p.ID)
	}
	return out
}

func TestSetWindowTrimsAndKeepsNewerWSPosts(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000)}, true, 5)
	s.ApplyEvent(postedEv(mkPost("ws", "off", "u2", 3000)))
	// a slower page fetch finishing after the WS post must not drop it
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000), mkPost("b", "off", "u2", 2000)}, true, 6)
	assert.Equal(t, []string{"a", "b", "ws"}, windowIDs(s, "off"))

	var page []model.Post
	for i := 0; i < WindowSize+5; i++ {
		page = append(page, mkPost(fmt.Sprint("p", i), "town", "u2", int64(10+i)))
	}
	s.SetWindow("town", page, true, 7)
	s.mu.Lock()
	w := s.chans["town"].Win
	s.mu.Unlock()
	assert.Len(t, w.Posts, WindowSize)
	assert.False(t, w.Complete, "trimmed window no longer reaches the start")
	assert.Equal(t, fmt.Sprint("p", WindowSize+4), w.Posts[WindowSize-1].ID)
}

func TestMergeSinceDropsDeletedAndHistory(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000), mkPost("b", "off", "u2", 2000)}, false, 5)
	s.MarkStale(10)
	edited := mkPost("a", "off", "u2", 1000)
	edited.Message, edited.EditAt, edited.UpdateAt = "edited", 4000, 4000
	hist := mkPost("h", "off", "u2", 1000)
	hist.OriginalID, hist.DeleteAt, hist.UpdateAt = "a", 4000, 4000
	del := mkPost("b", "off", "u2", 2000)
	del.DeleteAt, del.UpdateAt = 4100, 4100
	older := mkPost("old", "off", "u2", 500) // before the window start: not ours to insert
	newer := mkPost("c", "off", "u2", 4200)
	s.MergeSince("off", []model.Post{older, hist, edited, del, newer}, 99)
	assert.Equal(t, []string{"a", "c"}, windowIDs(s, "off"))
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.chans["off"].Win
	assert.Equal(t, "edited", w.Posts[0].Message)
	assert.False(t, w.Stale)
	assert.Empty(t, w.GapAfter)
	assert.Equal(t, int64(99), w.SyncedAt)
}

func TestMarkStaleAndSyncItemsPriority(t *testing.T) {
	s := newFixture()
	s.SetWindow("town", []model.Post{mkPost("t1", "town", "u2", 1)}, false, 5)
	s.MarkStale(50)
	items := s.SyncItems()
	byID := map[string]SyncItem{}
	for _, it := range items {
		byID[it.ChannelID] = it
	}
	require.Contains(t, byID, "town")
	assert.True(t, byID["town"].Loaded)
	assert.Equal(t, int64(50), byID["town"].SyncedAt, "stale window resumes from the last live moment")
	assert.False(t, byID["off"].Loaded)
	assert.Equal(t, 1, byID["dm2"].Priority, "DM with unread/mention first")
	assert.Equal(t, 2, byID["off"].Priority)
	assert.Equal(t, "dm2", items[0].ChannelID)
	assert.NotContains(t, byID, "arch", "archived channels are not fetched")
	s.SetActive("gm")
	it, ok := s.SyncItemFor("gm")
	require.True(t, ok)
	assert.Equal(t, 0, it.Priority, "the open channel jumps the queue")
	s.mu.Lock()
	assert.Equal(t, "t1", s.chans["town"].Win.GapAfter)
	s.mu.Unlock()
}

func TestAppendOlderOnlyForActiveChannel(t *testing.T) {
	s := newFixture()
	s.SetWindow("town", []model.Post{mkPost("w1", "town", "u2", 100)}, false, 5)
	s.AppendOlder("town", []model.Post{mkPost("o1", "town", "u2", 50)}, true)
	assert.Equal(t, "w1", s.OldestPostID("town"), "not active: ignored")
	s.SetActive("town")
	s.AppendOlder("town", []model.Post{mkPost("o1", "town", "u2", 50)}, true)
	assert.Equal(t, "o1", s.OldestPostID("town"))
	v, _ := s.ChannelView("town")
	assert.Equal(t, []string{"o1", "w1"}, []string{v.Posts[0].ID, v.Posts[1].ID})
	assert.False(t, v.HasMore)
	s.SetActive("off")
	v, _ = s.ChannelView("town")
	assert.Len(t, v.Posts, 1, "history is dropped when leaving the channel")
}
```

`internal/state/events_test.go`:
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

func counts(s *Server, id string) (model.Channel, model.ChannelMember) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chans[id].Info, s.chans[id].Member
}

func TestPostedBumpsCountsAndInsertsIntoLoadedWindow(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("town", nil, true, 5)
	eff := s.ApplyEvent(postedEv(mkPost("p1", "town", "u2", 5000), "u1"))
	info, m := counts(s, "town")
	assert.Equal(t, int64(11), info.TotalMsgCount)
	assert.Equal(t, int64(1), m.MentionCount)
	assert.Equal(t, int64(5000), info.LastPostAt)
	assert.Equal(t, []string{"p1"}, windowIDs(s, "town"))
	assert.True(t, eff.Sidebar)
	assert.True(t, eff.Badge)
	assert.Equal(t, []string{"town"}, eff.Channels)
	require.NotNil(t, eff.Notify)
	assert.Equal(t, "Town Square", eff.Notify.ChannelName)
}

func TestPostedTwiceIsIdempotent(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	p := mkPost("p1", "off", "u2", 5000)
	s.ApplyEvent(postedEv(p))
	eff := s.ApplyEvent(postedEv(p))
	info, _ := counts(s, "off")
	assert.Equal(t, int64(8), info.TotalMsgCount)
	assert.Nil(t, eff.Notify, "a replay does not notify twice")
}

func TestPostedDuringBootstrapNotDoubleCounted(t *testing.T) {
	s := newFixture() // guard active: off.last_post_at = 300 from REST
	s.ApplyEvent(postedEv(mkPost("early", "off", "u2", 300)))
	info, _ := counts(s, "off")
	assert.Equal(t, int64(7), info.TotalMsgCount, "REST already counted it")
	s.ApplyEvent(postedEv(mkPost("late", "off", "u2", 301)))
	info, _ = counts(s, "off")
	assert.Equal(t, int64(8), info.TotalMsgCount)
	s.ClearGuard()
	s.ApplyEvent(postedEv(mkPost("x", "off", "u2", 200))) // clock skew after the guard: still new
	info, _ = counts(s, "off")
	assert.Equal(t, int64(9), info.TotalMsgCount)
}

func TestOwnPostMarksChannelRead(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	eff := s.ApplyEvent(postedEv(mkPost("mine", "off", "u1", 5000)))
	info, m := counts(s, "off")
	assert.Equal(t, info.TotalMsgCount, m.MsgCount)
	assert.Nil(t, eff.Notify)
}

func TestCRTReplyUpdatesRootNotFeed(t *testing.T) {
	b := fixture()
	b.Config.CollapsedThreads = "always_on"
	s := New(fixedNow)
	s.Bootstrap(b)
	s.ClearGuard()
	s.SetWindow("town", []model.Post{mkPost("root", "town", "u2", 1000)}, true, 5)
	reply := mkPost("r1", "town", "u3", 2000)
	reply.RootID = "root"
	s.ApplyEvent(postedEv(reply, "u1"))
	assert.Equal(t, []string{"root"}, windowIDs(s, "town"))
	s.mu.Lock()
	assert.Equal(t, int64(1), s.chans["town"].Win.Posts[0].ReplyCount)
	s.mu.Unlock()
	info, m := counts(s, "town")
	assert.Equal(t, int64(10), info.TotalMsgCountRoot)
	assert.Equal(t, int64(0), m.MentionCountRoot, "reply mentions are thread mentions, not channel ones")
	assert.Equal(t, int64(1), m.MentionCount)
}

func TestPendingReplacedByEchoEitherOrder(t *testing.T) {
	for _, echoFirst := range []bool{false, true} {
		s := newFixture()
		s.ClearGuard()
		s.SetWindow("off", nil, true, 5)
		pd := s.AddPending("off", "", "hello")
		assert.Contains(t, pd.ID, "u1:")
		v, _ := s.ChannelView("off")
		require.Len(t, v.Posts, 1)
		assert.True(t, v.Posts[0].Pending)

		p := mkPost("real", "off", "u1", 6000)
		p.PendingPostID = pd.ID
		if echoFirst {
			s.ApplyEvent(postedEv(p))
			s.PostCreated(p)
		} else {
			s.PostCreated(p)
			s.ApplyEvent(postedEv(p))
		}
		v, _ = s.ChannelView("off")
		require.Len(t, v.Posts, 1, "echoFirst=%v", echoFirst)
		assert.Equal(t, "real", v.Posts[0].ID)
		assert.False(t, v.Posts[0].Pending)
		info, _ := counts(s, "off")
		assert.Equal(t, int64(8), info.TotalMsgCount, "counted once")
	}
}

func TestFailRetryDropPending(t *testing.T) {
	s := newFixture()
	pd := s.AddPending("off", "", "hello")
	s.FailPending("off", pd.ID)
	v, _ := s.ChannelView("off")
	assert.True(t, v.Posts[0].Failed)
	again, ok := s.RetryPending("off", pd.ID)
	require.True(t, ok)
	assert.Equal(t, pd.ID, again.ID, "same pending id → the server dedupes")
	v, _ = s.ChannelView("off")
	assert.False(t, v.Posts[0].Failed)
	s.DropPending("off", pd.ID)
	v, _ = s.ChannelView("off")
	assert.Empty(t, v.Posts)
}

func TestEditDeleteReactionEvents(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000), mkPost("b", "off", "u2", 2000)}, true, 5)
	e := mkPost("a", "off", "u2", 1000)
	e.Message, e.EditAt, e.UpdateAt = "changed", 3000, 3000
	eff := s.ApplyEvent(postEv("post_edited", e))
	assert.Equal(t, []string{"off"}, eff.Channels)
	s.ApplyEvent(postEv("post_deleted", mkPost("b", "off", "u2", 2000)))
	assert.Equal(t, []string{"a"}, windowIDs(s, "off"))

	rb, _ := json.Marshal(model.Reaction{UserID: "u1", PostID: "a", EmojiName: "+1"})
	db, _ := json.Marshal(map[string]string{"reaction": string(rb)})
	s.ApplyEvent(ws.Event{Type: "reaction_added", Data: db, Broadcast: ws.Broadcast{ChannelID: "off"}})
	s.ApplyEvent(ws.Event{Type: "reaction_added", Data: db, Broadcast: ws.Broadcast{ChannelID: "off"}}) // duplicate
	v, _ := s.ChannelView("off")
	assert.Equal(t, "changed", v.Posts[0].Message)
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, v.Posts[0].Reactions)
	s.ApplyEvent(ws.Event{Type: "reaction_removed", Data: db, Broadcast: ws.Broadcast{ChannelID: "off"}})
	v, _ = s.ChannelView("off")
	assert.Empty(t, v.Posts[0].Reactions)
}

func TestViewedEventsAndPostUnread(t *testing.T) {
	s := newFixture()
	db, _ := json.Marshal(map[string]any{"channel_times": map[string]int64{"off": 777, "dm2": 778}})
	s.ApplyEvent(ws.Event{Type: "multiple_channels_viewed", Data: db})
	b := s.Badge()
	assert.False(t, b.Unread)
	assert.Zero(t, b.Mentions)
	_, m := counts(s, "off")
	assert.Equal(t, int64(777), m.LastViewedAt)

	ub, _ := json.Marshal(map[string]any{"msg_count": 3, "msg_count_root": 3, "mention_count": 1, "mention_count_root": 1, "last_viewed_at": 5})
	s.ApplyEvent(ws.Event{Type: "post_unread", Data: ub, Broadcast: ws.Broadcast{ChannelID: "off", TeamID: "t1"}})
	_, m = counts(s, "off")
	assert.Equal(t, int64(3), m.MsgCount)
	assert.Equal(t, 1, s.Badge().Mentions)
}

func TestActiveFocusedChannelRequestsViewUnlessMarkedUnread(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetActive("off")
	s.SetFocused(true)
	eff := s.ApplyEvent(postedEv(mkPost("p1", "off", "u2", 5000)))
	assert.Equal(t, "off", eff.View)
	assert.True(t, s.NeedsView("off"))
	s.ViewedLocally("off", 5001)
	assert.False(t, s.NeedsView("off"))

	s.SetUnread(model.ChannelUnreadAt{ChannelID: "off", MsgCount: 1, MsgCountRoot: 1, LastViewedAt: 1})
	eff = s.ApplyEvent(postedEv(mkPost("p2", "off", "u2", 6000)))
	assert.Empty(t, eff.View, "marked unread by the user: stay unread until they leave")
	assert.False(t, s.NeedsView("off"))
	s.SetActive("town")
	assert.True(t, s.NeedsView("off"), "leaving clears the suppression")

	s.SetFocused(false)
	s.SetActive("off")
	eff = s.ApplyEvent(postedEv(mkPost("p3", "off", "u2", 7000)))
	assert.Empty(t, eff.View, "window not focused")
}

func TestMembershipAndPreferenceEvents(t *testing.T) {
	s := newFixture()
	s.SetActive("off")
	db, _ := json.Marshal(map[string]string{"channel_id": "off", "remover_id": "u2"})
	eff := s.ApplyEvent(ws.Event{Type: "user_removed", Data: db, Broadcast: ws.Broadcast{UserID: "u1"}})
	assert.True(t, eff.Sidebar)
	assert.Equal(t, []string{"off"}, eff.Channels)
	_, ok := s.ChannelView("off")
	assert.False(t, ok)

	db, _ = json.Marshal(map[string]string{"user_id": "u1", "team_id": "t1"})
	assert.True(t, s.ApplyEvent(ws.Event{Type: "user_added", Data: db, Broadcast: ws.Broadcast{ChannelID: "new", UserID: "u1"}}).NeedMeta)
	db, _ = json.Marshal(map[string]string{"user_id": "u9", "team_id": "t1"})
	assert.False(t, s.ApplyEvent(ws.Event{Type: "user_added", Data: db, Broadcast: ws.Broadcast{ChannelID: "town"}}).NeedMeta)
	assert.True(t, s.ApplyEvent(ws.Event{Type: "direct_added", Data: []byte(`{}`)}).NeedMeta)

	pb, _ := json.Marshal([]model.Preference{{Category: "display_settings", Name: "collapsed_reply_threads", Value: "on"}})
	db, _ = json.Marshal(map[string]string{"preferences": string(pb)})
	b := fixture()
	b.Config.CollapsedThreads = "default_off"
	s.Bootstrap(b)
	eff = s.ApplyEvent(ws.Event{Type: "preferences_changed", Data: db})
	assert.True(t, eff.Resync, "CRT toggled: windows hold the wrong kind of posts")
	assert.True(t, s.CRT())
}

func TestPostInHiddenDMAsksToShowIt(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	eff := s.ApplyEvent(postedEv(mkPost("d1", "dm3", "u3", 5000)))
	require.NotNil(t, eff.ShowDM)
	assert.Equal(t, model.Preference{UserID: "u1", Category: "direct_channel_show", Name: "u3", Value: "true"}, *eff.ShowDM)
	s.ViewedLocally("dm3", 5001)
	assert.Contains(t, ids(s.Sidebar("t1").Categories[2].Channels), "dm3", "shown optimistically")
}

func TestUnknownChannelPostAsksForMeta(t *testing.T) {
	s := newFixture()
	eff := s.ApplyEvent(postedEv(mkPost("x", "brand-new", "u2", 5000)))
	assert.True(t, eff.NeedMeta)
}
```

`internal/state/view_test.go`:
```go
package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

func TestChannelViewComposition(t *testing.T) {
	s := newFixture()
	bot := mkPost("bot", "off", "u2", 1000)
	bot.Props = model.PostProps{FromWebhook: true, OverrideUsername: "gitlab", Attachments: []model.Attachment{{Title: "MR !1"}}}
	sys := mkPost("sys", "off", "u3", 1500)
	sys.Type = "system_join_channel"
	withMeta := mkPost("m", "off", "u2", 2000)
	withMeta.EditAt = 2100
	withMeta.Metadata = &model.PostMetadata{
		Files:     []model.FileInfo{{ID: "f1", Name: "a.pdf", Size: 10, MimeType: "application/pdf"}},
		Reactions: []model.Reaction{{UserID: "u2", EmojiName: "+1"}, {UserID: "u1", EmojiName: "+1"}, {UserID: "u3", EmojiName: "tada"}},
	}
	s.SetWindow("off", []model.Post{bot, sys, withMeta}, false, 5)
	s.MarkStale(6)
	s.SetDraft("off", "draft text")
	newSince := s.SetActive("off")
	assert.Equal(t, int64(10), newSince, "member.last_viewed_at before opening")
	s.AddPending("off", "", "sending…")

	v, ok := s.ChannelView("off")
	require.True(t, ok)
	assert.Equal(t, "Off-Topic", v.Name)
	assert.Equal(t, "team", v.TeamName)
	assert.Equal(t, "draft text", v.Draft)
	assert.Equal(t, "u1", v.MeID)
	assert.True(t, v.HasMore)
	assert.True(t, v.Syncing)
	assert.Equal(t, "m", v.GapAfter)
	require.Len(t, v.Posts, 4)
	assert.Equal(t, "gitlab", v.Posts[0].Author)
	assert.True(t, v.Posts[0].Bot)
	assert.Equal(t, "MR !1", v.Posts[0].Attachments[0].Title)
	assert.True(t, v.Posts[1].System)
	assert.Equal(t, "bob", v.Posts[2].Author)
	assert.Equal(t, int64(2100), v.Posts[2].EditAt)
	assert.Equal(t, []FileView{{Name: "a.pdf", Size: 10, Mime: "application/pdf"}}, v.Posts[2].Files)
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 2, Mine: true}, {Emoji: "tada", Count: 1}}, v.Posts[2].Reactions)
	assert.True(t, v.Posts[3].Pending)
	assert.Equal(t, "alice", v.Posts[3].Author)
}

func TestChannelViewDMNameAndUnknownChannel(t *testing.T) {
	s := newFixture()
	v, ok := s.ChannelView("dm2")
	require.True(t, ok)
	assert.Equal(t, "bob", v.Name)
	assert.Equal(t, "D", v.Type)
	assert.False(t, v.Loaded)
	assert.True(t, v.Syncing)
	_, ok = s.ChannelView("nope")
	assert.False(t, ok)
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/state/`
Expected: FAIL (нет `SetWindow`, `ApplyEvent`, `ChannelView`, …).

- [ ] **Step 3: Реализация**

`internal/state/posts.go` (полностью заменяет заглушку Task 6):
```go
package state

import (
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

type Pending struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	RootID    string `json:"root_id,omitempty"`
	Message   string `json:"message"`
	CreateAt  int64  `json:"create_at"`
	Failed    bool   `json:"failed,omitempty"`
}

// seenSet remembers recent post ids so a post delivered twice (REST
// response + WS echo, a replayed event) changes counters only once.
type seenSet struct {
	ids  map[string]struct{}
	ring []string
	next int
}

func newSeenSet(n int) seenSet { return seenSet{ids: map[string]struct{}{}, ring: make([]string, n)} }

func (s *seenSet) has(id string) bool { _, ok := s.ids[id]; return ok }

func (s *seenSet) add(id string) {
	if s.has(id) {
		return
	}
	if old := s.ring[s.next]; old != "" {
		delete(s.ids, old)
	}
	s.ring[s.next] = id
	s.ids[id] = struct{}{}
	s.next = (s.next + 1) % len(s.ring)
}

// keep: does p belong in a channel feed?
func keep(p model.Post, crt bool) bool {
	return p.OriginalID == "" && p.DeleteAt == 0 && (!crt || p.RootID == "")
}

func indexOf(posts []model.Post, id string) int {
	return slices.IndexFunc(posts, func(p model.Post) bool { return p.ID == id })
}

func sortPosts(ps []model.Post) {
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].CreateAt < ps[j].CreateAt })
}

func trimWindow(w *Window) {
	if len(w.Posts) > WindowSize {
		w.Posts = append([]model.Post(nil), w.Posts[len(w.Posts)-WindowSize:]...)
		w.Complete = false
	}
}

func (s *Server) upsertLocked(ch *Chan, p model.Post) {
	if i := indexOf(ch.Win.Posts, p.ID); i >= 0 {
		if p.UpdateAt >= ch.Win.Posts[i].UpdateAt {
			if p.ReplyCount == 0 {
				p.ReplyCount, p.LastReplyAt = ch.Win.Posts[i].ReplyCount, ch.Win.Posts[i].LastReplyAt
			}
			ch.Win.Posts[i] = p
		}
	} else {
		ch.Win.Posts = append(ch.Win.Posts, p)
		sortPosts(ch.Win.Posts)
		trimWindow(&ch.Win)
	}
	s.dirty.posts[ch.Info.ID] = true
}

// removeLocked deletes a post and its replies (they die with the root).
func (s *Server) removeLocked(ch *Chan, id string) bool {
	gone := func(p model.Post) bool { return p.ID == id || p.RootID == id }
	n := len(ch.Win.Posts)
	ch.Win.Posts = slices.DeleteFunc(ch.Win.Posts, gone)
	changed := len(ch.Win.Posts) != n
	if ch.Info.ID == s.active {
		m := len(s.older)
		s.older = slices.DeleteFunc(s.older, gone)
		changed = changed || len(s.older) != m
	}
	if changed {
		s.dirty.posts[ch.Info.ID] = true
	}
	return changed
}

// SetWindow installs the latest page of a channel. Posts already in the
// window that are newer than the page arrived over WS while the request was
// in flight — they are kept.
func (s *Server) SetWindow(channelID string, page []model.Post, complete bool, syncedAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil {
		return
	}
	crt := s.crtLocked()
	var merged []model.Post
	var newest int64
	for _, p := range page {
		if keep(p, crt) {
			merged = append(merged, p)
			s.seen.add(p.ID)
		}
		newest = max(newest, p.CreateAt)
	}
	for _, p := range ch.Win.Posts {
		if p.CreateAt > newest && indexOf(merged, p.ID) < 0 {
			merged = append(merged, p)
		}
	}
	sortPosts(merged)
	ch.Win = Window{Posts: merged, Loaded: true, Complete: complete, SyncedAt: syncedAt}
	trimWindow(&ch.Win)
	s.dirty.posts[channelID] = true
}

// MergeSince applies a posts?since= response (edits, deletions, new posts)
// and marks the window caught up.
func (s *Server) MergeSince(channelID string, posts []model.Post, syncedAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil {
		return
	}
	crt := s.crtLocked()
	var oldest int64
	if len(ch.Win.Posts) > 0 && !ch.Win.Complete {
		oldest = ch.Win.Posts[0].CreateAt
	}
	for _, p := range posts {
		switch {
		case p.OriginalID != "":
			continue // edit-history row
		case p.DeleteAt > 0:
			s.removeLocked(ch, p.ID)
		case !keep(p, crt):
			continue
		case indexOf(ch.Win.Posts, p.ID) >= 0 || p.CreateAt >= oldest:
			s.upsertLocked(ch, p)
			s.seen.add(p.ID)
		}
	}
	ch.Win.Loaded, ch.Win.Stale, ch.Win.GapAfter = true, false, ""
	ch.Win.SyncedAt = max(ch.Win.SyncedAt, syncedAt)
	s.dirty.posts[channelID] = true
}

// AppendOlder adds a page of history above the window while the channel is
// open. It lives only in memory and is dropped when the user leaves.
func (s *Server) AppendOlder(channelID string, posts []model.Post, complete bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil || channelID != s.active {
		return
	}
	crt := s.crtLocked()
	var add []model.Post
	for _, p := range posts {
		if keep(p, crt) && indexOf(s.older, p.ID) < 0 && indexOf(ch.Win.Posts, p.ID) < 0 {
			add = append(add, p)
		}
	}
	s.older = append(add, s.older...)
	sortPosts(s.older)
	s.olderComplete = complete
}

func (s *Server) OldestPostID(channelID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if channelID == s.active && len(s.older) > 0 {
		return s.older[0].ID
	}
	if ch := s.chans[channelID]; ch != nil && len(ch.Win.Posts) > 0 {
		return ch.Win.Posts[0].ID
	}
	return ""
}

// MarkStale: the event stream was lost. Every loaded window may miss posts
// after its last one; it stays readable and is caught up by the worker.
func (s *Server) MarkStale(liveUntil int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.chans {
		if !ch.Win.Loaded || ch.Win.Stale {
			continue
		}
		ch.Win.SyncedAt = max(ch.Win.SyncedAt, liveUntil)
		ch.Win.Stale = true
		ch.Win.GapAfter = ""
		if n := len(ch.Win.Posts); n > 0 {
			ch.Win.GapAfter = ch.Win.Posts[n-1].ID
		}
		s.dirty.posts[id] = true
	}
}

type SyncItem struct {
	ChannelID string
	Loaded    bool
	SyncedAt  int64
	Priority  int // lower first
}

const recentWindow = 7 * 24 * time.Hour

// priorityLocked: open channel → DMs and mentions → unread → recently
// active → the rest (spec «Предзагрузка постов»).
func (s *Server) priorityLocked(c *Chan) int {
	if c.Info.ID == s.active {
		return 0
	}
	u, m := s.unreadLocked(c)
	switch {
	case m > 0 || (u && isDirect(c)):
		return 1
	case u:
		return 2
	case s.now().UnixMilli()-c.Info.LastPostAt < recentWindow.Milliseconds():
		return 3
	}
	return 4
}

func (s *Server) syncItemLocked(c *Chan) (SyncItem, bool) {
	if c.Info.DeleteAt != 0 || (c.Win.Loaded && !c.Win.Stale) {
		return SyncItem{}, false
	}
	return SyncItem{ChannelID: c.Info.ID, Loaded: c.Win.Loaded, SyncedAt: c.Win.SyncedAt, Priority: s.priorityLocked(c)}, true
}

func (s *Server) SyncItems() []SyncItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []SyncItem
	for _, c := range s.chans {
		if it, ok := s.syncItemLocked(c); ok {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return s.chans[out[i].ChannelID].Info.LastPostAt > s.chans[out[j].ChannelID].Info.LastPostAt
	})
	return out
}

func (s *Server) SyncItemFor(channelID string) (SyncItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chans[channelID]
	if c == nil {
		return SyncItem{}, false
	}
	return s.syncItemLocked(c)
}

// ---- pending (optimistic) posts ----

func (s *Server) AddPending(channelID, rootID, message string) Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	id := s.me.ID + ":" + strconv.FormatInt(now, 10)
	for n := 1; s.pendingIndexLocked(channelID, id) >= 0; n++ {
		id = s.me.ID + ":" + strconv.FormatInt(now, 10) + "-" + strconv.Itoa(n)
	}
	p := Pending{ID: id, ChannelID: channelID, RootID: rootID, Message: message, CreateAt: now}
	s.pending[channelID] = append(s.pending[channelID], p)
	return p
}

func (s *Server) pendingIndexLocked(channelID, id string) int {
	return slices.IndexFunc(s.pending[channelID], func(p Pending) bool { return p.ID == id })
}

func (s *Server) FailPending(channelID, id string) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.pendingIndexLocked(channelID, id); i >= 0 {
		s.pending[channelID][i].Failed = true
	}
	return Change{Channels: []string{channelID}}
}

func (s *Server) RetryPending(channelID, id string) (Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.pendingIndexLocked(channelID, id)
	if i < 0 {
		return Pending{}, false
	}
	s.pending[channelID][i].Failed = false
	return s.pending[channelID][i], true
}

func (s *Server) DropPending(channelID, id string) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropPendingLocked(channelID, id)
	return Change{Channels: []string{channelID}}
}

func (s *Server) dropPendingLocked(channelID, id string) {
	if id == "" {
		return
	}
	s.pending[channelID] = slices.DeleteFunc(s.pending[channelID], func(p Pending) bool { return p.ID == id })
}

// ---- posts from the user's own actions ----

// PostCreated applies the REST response of CreatePost (the WS echo may come
// before or after it; both paths go through applyNewPostLocked).
func (s *Server) PostCreated(p model.Post) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[p.ChannelID]
	if ch == nil {
		return Change{}
	}
	s.applyNewPostLocked(ch, p, nil)
	return Change{Sidebar: true, Badge: true, Channels: []string{p.ChannelID}}
}

// applyNewPostLocked is the single path for a created post (WS posted or
// REST create). It updates the window and, once per post id, the counters.
func (s *Server) applyNewPostLocked(ch *Chan, p model.Post, mentions []string) (bumped bool) {
	crt := s.crtLocked()
	isNew := !s.seen.has(p.ID) && indexOf(ch.Win.Posts, p.ID) < 0
	s.seen.add(p.ID)
	s.dropPendingLocked(ch.Info.ID, p.PendingPostID)
	root := p.RootID == ""
	switch {
	case keep(p, crt):
		if ch.Win.Loaded {
			s.upsertLocked(ch, p)
		}
	case crt && !root && isNew:
		if i := indexOf(ch.Win.Posts, p.RootID); i >= 0 {
			ch.Win.Posts[i].ReplyCount++
			ch.Win.Posts[i].LastReplyAt = max(ch.Win.Posts[i].LastReplyAt, p.CreateAt)
			s.dirty.posts[ch.Info.ID] = true
		}
	}
	if !isNew {
		return false
	}
	if g, ok := s.guard[ch.Info.ID]; ok && p.CreateAt <= g {
		return false
	}
	ch.Info.TotalMsgCount++
	ch.Info.LastPostAt = max(ch.Info.LastPostAt, p.CreateAt)
	if root {
		ch.Info.TotalMsgCountRoot++
		ch.Info.LastRootPostAt = max(ch.Info.LastRootPostAt, p.CreateAt)
	}
	if p.UserID == s.me.ID {
		ch.Member.MsgCount, ch.Member.MsgCountRoot = ch.Info.TotalMsgCount, ch.Info.TotalMsgCountRoot
		ch.Member.LastViewedAt = max(ch.Member.LastViewedAt, p.CreateAt)
	} else if slices.Contains(mentions, s.me.ID) {
		ch.Member.MentionCount++
		if root {
			ch.Member.MentionCountRoot++
		}
	}
	s.dirty.chans[ch.Info.ID] = true
	return true
}

// ApplyPostUpdate applies an edited post (REST patch response or event).
func (s *Server) ApplyPostUpdate(p model.Post) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updatePostLocked(p) {
		return Change{Channels: []string{p.ChannelID}}
	}
	return Change{}
}

func (s *Server) updatePostLocked(p model.Post) bool {
	ch := s.chans[p.ChannelID]
	if ch == nil {
		return false
	}
	changed := false
	if indexOf(ch.Win.Posts, p.ID) >= 0 {
		s.upsertLocked(ch, p)
		changed = true
	}
	if ch.Info.ID == s.active {
		if i := indexOf(s.older, p.ID); i >= 0 && p.UpdateAt >= s.older[i].UpdateAt {
			s.older[i] = p
			changed = true
		}
	}
	return changed
}

func (s *Server) RemovePost(postID string) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.chans {
		if s.removeLocked(ch, postID) {
			return Change{Channels: []string{id}}
		}
	}
	return Change{}
}

func (s *Server) FindPost(postID string) (model.Post, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.chans {
		if i := indexOf(ch.Win.Posts, postID); i >= 0 {
			return ch.Win.Posts[i], true
		}
	}
	if i := indexOf(s.older, postID); i >= 0 {
		return s.older[i], true
	}
	return model.Post{}, false
}

func (s *Server) reactLocked(channelID string, r model.Reaction, add bool) bool {
	ch := s.chans[channelID]
	if ch == nil {
		return false
	}
	apply := func(p *model.Post) bool {
		if p.ID != r.PostID {
			return false
		}
		var reacts []model.Reaction
		files := []model.FileInfo(nil)
		if p.Metadata != nil {
			reacts = append(reacts, p.Metadata.Reactions...)
			files = p.Metadata.Files
		}
		same := func(x model.Reaction) bool { return x.UserID == r.UserID && x.EmojiName == r.EmojiName }
		if add {
			if slices.IndexFunc(reacts, same) >= 0 {
				return false
			}
			reacts = append(reacts, r)
		} else {
			n := len(reacts)
			if reacts = slices.DeleteFunc(reacts, same); len(reacts) == n {
				return false
			}
		}
		p.Metadata = &model.PostMetadata{Files: files, Reactions: reacts}
		return true
	}
	for i := range ch.Win.Posts {
		if apply(&ch.Win.Posts[i]) {
			s.dirty.posts[channelID] = true
			return true
		}
	}
	if channelID == s.active {
		for i := range s.older {
			if apply(&s.older[i]) {
				return true
			}
		}
	}
	return false
}

// ---- drafts, active channel, read state ----

func (s *Server) SetDraft(channelID, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafts[channelID] == text {
		return
	}
	if text == "" {
		delete(s.drafts, channelID)
	} else {
		s.drafts[channelID] = text
	}
	if s.chans[channelID] != nil {
		s.dirty.chans[channelID] = true
	}
}

// SetActive makes channelID the open channel and returns its "new messages"
// boundary: last_viewed_at at the moment it was opened. Re-opening the same
// channel keeps the boundary.
func (s *Server) SetActive(channelID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if channelID == s.active {
		return s.newSince
	}
	s.active, s.older, s.olderComplete, s.suppressView, s.newSince = channelID, nil, false, "", 0
	if ch := s.chans[channelID]; ch != nil {
		s.newSince = ch.Member.LastViewedAt
		team := ch.Info.TeamID
		if team == "" {
			team = s.nav.TeamID
		}
		if s.nav.Channel == nil {
			s.nav.Channel = map[string]string{}
		}
		s.nav.Channel[team] = channelID
		s.dirty.meta = true
	}
	return s.newSince
}

func (s *Server) Active() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *Server) SetFocused(f bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.focused = f
}

func (s *Server) Focused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.focused
}

// NeedsView: the channel has something to mark read and the user did not
// just mark it unread on purpose.
func (s *Server) NeedsView(channelID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil || s.suppressView == channelID {
		return false
	}
	return ch.Info.TotalMsgCount > ch.Member.MsgCount || ch.Info.TotalMsgCountRoot > ch.Member.MsgCountRoot ||
		ch.Member.MentionCount > 0 || ch.Member.MentionCountRoot > 0
}

func (s *Server) markReadLocked(ch *Chan, at int64) {
	ch.Member.MsgCount, ch.Member.MsgCountRoot = ch.Info.TotalMsgCount, ch.Info.TotalMsgCountRoot
	ch.Member.MentionCount, ch.Member.MentionCountRoot, ch.Member.UrgentMentionCount = 0, 0, 0
	ch.Member.LastViewedAt = max(ch.Member.LastViewedAt, at)
	s.dirty.chans[ch.Info.ID] = true
}

func (s *Server) ViewedLocally(channelID string, at int64) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch := s.chans[channelID]; ch != nil {
		s.markReadLocked(ch, at)
	}
	return Change{Sidebar: true, Badge: true}
}

func (s *Server) setUnreadLocked(u model.ChannelUnreadAt) {
	ch := s.chans[u.ChannelID]
	if ch == nil {
		return
	}
	ch.Member.MsgCount, ch.Member.MsgCountRoot = u.MsgCount, u.MsgCountRoot
	ch.Member.MentionCount, ch.Member.MentionCountRoot = u.MentionCount, u.MentionCountRoot
	ch.Member.UrgentMentionCount, ch.Member.LastViewedAt = u.UrgentMentionCount, u.LastViewedAt
	s.dirty.chans[u.ChannelID] = true
}

// SetUnread applies a "mark as unread" result. If it is the open channel,
// auto-marking it read is suppressed until the user leaves it.
func (s *Server) SetUnread(u model.ChannelUnreadAt) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setUnreadLocked(u)
	if u.ChannelID == s.active {
		s.suppressView = u.ChannelID
	}
	return Change{Sidebar: true, Badge: true, Channels: []string{u.ChannelID}}
}

// ClearGuard ends the post-bootstrap window during which posted events
// already reflected in the REST counters must not bump them again.
func (s *Server) ClearGuard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guard = map[string]int64{}
}
```

`internal/state/events.go`:
```go
package state

import (
	"slices"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/ws"
)

// Change tells the API layer which UI views to refresh.
type Change struct {
	Sidebar  bool
	Badge    bool
	Channels []string
}

func (c *Change) Merge(o Change) {
	c.Sidebar = c.Sidebar || o.Sidebar
	c.Badge = c.Badge || o.Badge
	for _, id := range o.Channels {
		if !slices.Contains(c.Channels, id) {
			c.Channels = append(c.Channels, id)
		}
	}
}

func (c Change) Empty() bool { return !c.Sidebar && !c.Badge && len(c.Channels) == 0 }

// NotifyCandidate carries everything notify.Decide needs, copied under lock.
type NotifyCandidate struct {
	Post        model.Post
	Channel     model.Channel
	ChannelName string
	Member      model.ChannelMember
	Me          model.User
	Status      model.Status
	CRT         bool
	Focused     bool
	Active      string
	Mentions    []string
	Followers   []string
	SenderName  string
}

// Effects is what the worker must do after an event, besides refreshing
// the views in Change.
type Effects struct {
	Change
	NeedMeta  bool              // channels/memberships/categories changed server-side
	NeedUsers []string          // profiles to fetch
	ShowDM    *model.Preference // DM/GM got a message while closed: save the "show" preference
	View      string            // open + focused channel got a post: mark it viewed
	Notify    *NotifyCandidate
	Resync    bool // CRT toggled: every window holds the wrong kind of posts
}

func (s *Server) ApplyEvent(ev ws.Event) Effects {
	s.mu.Lock()
	defer s.mu.Unlock()
	var eff Effects
	switch ev.Type {
	case "posted":
		s.onPostedLocked(ev, &eff)
	case "post_edited":
		if p, err := ws.DecodePost(ev); err == nil && s.updatePostLocked(p) {
			eff.Channels = []string{p.ChannelID}
		}
	case "post_deleted":
		if p, err := ws.DecodePost(ev); err == nil {
			if ch := s.chans[p.ChannelID]; ch != nil && s.removeLocked(ch, p.ID) {
				eff.Channels = []string{p.ChannelID}
			}
		}
	case "reaction_added", "reaction_removed":
		if r, err := ws.DecodeReaction(ev); err == nil && s.reactLocked(ev.Broadcast.ChannelID, r, ev.Type == "reaction_added") {
			eff.Channels = []string{ev.Broadcast.ChannelID}
		}
	case "multiple_channels_viewed":
		times, _ := ws.DecodeChannelTimes(ev)
		for id, at := range times {
			if ch := s.chans[id]; ch != nil {
				s.markReadLocked(ch, at)
			}
		}
		eff.Sidebar, eff.Badge = true, true
	case "post_unread":
		if u, err := ws.DecodePostUnread(ev); err == nil {
			s.setUnreadLocked(u)
			eff.Sidebar, eff.Badge = true, true
		}
	case "channel_updated":
		if c, err := ws.DecodeChannel(ev); err == nil {
			if ch := s.chans[c.ID]; ch != nil {
				ch.Info = c
				s.dirty.chans[c.ID] = true
				eff.Sidebar, eff.Badge, eff.Channels = true, true, []string{c.ID}
			}
		}
	case "channel_deleted":
		id := ev.Str("channel_id")
		if ch := s.chans[id]; ch != nil {
			ch.Info.DeleteAt = s.now().UnixMilli()
			s.dirty.chans[id] = true
			eff.Sidebar, eff.Badge, eff.Channels = true, true, []string{id}
		}
	case "user_removed":
		// The copy addressed to the removed user carries channel_id in data.
		if ev.Broadcast.UserID == s.me.ID {
			if id := ev.Str("channel_id"); s.chans[id] != nil {
				delete(s.chans, id)
				s.forgetChannelLocked(id)
				eff.Sidebar, eff.Badge, eff.Channels = true, true, []string{id}
			}
		}
	case "user_added":
		eff.NeedMeta = ev.Str("user_id") == s.me.ID
	case "direct_added", "group_added", "channel_created", "channel_restored", "channel_converted",
		"sidebar_category_created", "sidebar_category_updated", "sidebar_category_deleted", "sidebar_category_order_updated":
		eff.NeedMeta = true
	case "channel_member_updated":
		if m, err := ws.DecodeMember(ev); err == nil && m.UserID == s.me.ID {
			if ch := s.chans[m.ChannelID]; ch != nil {
				ch.Member = m
				s.dirty.chans[m.ChannelID] = true
				eff.Sidebar, eff.Badge = true, true
			}
		}
	case "preferences_changed", "preferences_deleted":
		if prefs, err := ws.DecodePreferences(ev); err == nil {
			wasCRT := s.crtLocked()
			for _, p := range prefs {
				if ev.Type == "preferences_changed" {
					s.prefs[prefKey{p.Category, p.Name}] = p.Value
				} else {
					delete(s.prefs, prefKey{p.Category, p.Name})
				}
			}
			s.dirty.meta = true
			eff.Sidebar, eff.Badge = true, true
			eff.Resync = wasCRT != s.crtLocked()
		}
	case "user_updated":
		if u, err := ws.DecodeUser(ev); err == nil && u.ID != "" {
			if u.ID == s.me.ID {
				if u.NotifyProps == nil { // sanitized broadcast copy
					u.NotifyProps = s.me.NotifyProps
				}
				s.me = u
				s.dirty.meta = true
			}
			s.users[u.ID] = u
			s.dirty.users[u.ID] = true
			eff.Sidebar = true
		}
	case "status_change":
		if ev.Str("user_id") == s.me.ID {
			s.status.Status = ev.Str("status")
		}
	}
	return eff
}

func (s *Server) onPostedLocked(ev ws.Event, eff *Effects) {
	d, err := ws.DecodePosted(ev)
	if err != nil {
		return
	}
	p := d.Post
	ch := s.chans[p.ChannelID]
	if ch == nil {
		eff.NeedMeta = true // a DM/GM or channel we were just added to
		return
	}
	bumped := s.applyNewPostLocked(ch, p, d.Mentions)
	eff.Sidebar, eff.Badge, eff.Channels = true, true, []string{ch.Info.ID}
	if _, ok := s.users[p.UserID]; !ok && p.UserID != "" {
		eff.NeedUsers = []string{p.UserID}
	}
	if p.UserID == s.me.ID {
		return
	}
	if isDirect(ch) {
		pref := model.Preference{UserID: s.me.ID, Category: "group_channel_show", Name: ch.Info.ID, Value: "true"}
		if ch.Info.IsDM() {
			pref.Category, pref.Name = "direct_channel_show", ch.Info.DMPartner(s.me.ID)
		}
		if s.prefs[prefKey{pref.Category, pref.Name}] != "true" {
			s.prefs[prefKey{pref.Category, pref.Name}] = "true"
			s.dirty.meta = true
			eff.ShowDM = &pref
		}
	}
	if !bumped {
		return
	}
	crt := s.crtLocked()
	if ch.Info.ID == s.active && s.focused && s.suppressView != ch.Info.ID && (!crt || p.RootID == "") {
		eff.View = ch.Info.ID
	}
	eff.Notify = &NotifyCandidate{
		Post: p, Channel: ch.Info, ChannelName: s.channelNameLocked(ch), Member: ch.Member, Me: s.me,
		Status: s.status, CRT: crt, Focused: s.focused, Active: s.active,
		Mentions: d.Mentions, Followers: d.Followers, SenderName: s.displayNameLocked(p.UserID),
	}
	if eff.Notify.SenderName == "" {
		eff.Notify.SenderName = trimAt(d.SenderName)
	}
}

func trimAt(s string) string {
	if len(s) > 0 && s[0] == '@' {
		return s[1:]
	}
	return s
}
```

`internal/state/view.go`:
```go
package state

import (
	"sort"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

type FileView struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
}

type ReactionView struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

type PostView struct {
	ID          string             `json:"id"`
	UserID      string             `json:"user_id"`
	Author      string             `json:"author"`
	RootID      string             `json:"root_id,omitempty"`
	Message     string             `json:"message"`
	CreateAt    int64              `json:"create_at"`
	EditAt      int64              `json:"edit_at,omitempty"`
	ReplyCount  int64              `json:"reply_count,omitempty"`
	System      bool               `json:"system,omitempty"`
	Bot         bool               `json:"bot,omitempty"`
	Pending     bool               `json:"pending,omitempty"`
	Failed      bool               `json:"failed,omitempty"`
	Attachments []model.Attachment `json:"attachments,omitempty"`
	Files       []FileView         `json:"files,omitempty"`
	Reactions   []ReactionView     `json:"reactions,omitempty"`
}

type ChannelView struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Header   string     `json:"header"`
	Purpose  string     `json:"purpose"`
	TeamName string     `json:"team_name"`
	Posts    []PostView `json:"posts"`
	NewSince int64      `json:"new_since"`
	HasMore  bool       `json:"has_more"`
	Loaded   bool       `json:"loaded"`
	Syncing  bool       `json:"syncing"`
	GapAfter string     `json:"gap_after"`
	Draft    string     `json:"draft"`
	MeID     string     `json:"me_id"`
	CRT      bool       `json:"crt"`
	Muted    bool       `json:"muted"`
}

// ChannelView renders a channel for the UI: browsed history, the window
// and our pending posts, oldest first.
func (s *Server) ChannelView(channelID string) (ChannelView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil {
		return ChannelView{}, false
	}
	v := ChannelView{
		ID: ch.Info.ID, Name: s.channelNameLocked(ch), Type: ch.Info.Type, Header: ch.Info.Header, Purpose: ch.Info.Purpose,
		TeamName: s.teamNameLocked(ch), Loaded: ch.Win.Loaded, Syncing: !ch.Win.Loaded || ch.Win.Stale,
		GapAfter: ch.Win.GapAfter, Draft: s.drafts[channelID], MeID: s.me.ID, CRT: s.crtLocked(), Muted: ch.Member.Muted(),
		Posts: []PostView{},
	}
	var posts []model.Post
	if channelID == s.active {
		v.NewSince = s.newSince
		posts = append(posts, s.older...)
	}
	posts = append(posts, ch.Win.Posts...)
	if channelID == s.active && len(s.older) > 0 {
		v.HasMore = !s.olderComplete
	} else {
		v.HasMore = ch.Win.Loaded && !ch.Win.Complete
	}
	for _, p := range posts {
		v.Posts = append(v.Posts, s.postViewLocked(p))
	}
	pend := append([]Pending(nil), s.pending[channelID]...)
	sort.SliceStable(pend, func(i, j int) bool { return pend[i].CreateAt < pend[j].CreateAt })
	for _, p := range pend {
		v.Posts = append(v.Posts, PostView{ID: p.ID, UserID: s.me.ID, Author: s.displayNameLocked(s.me.ID),
			RootID: p.RootID, Message: p.Message, CreateAt: p.CreateAt, Pending: !p.Failed, Failed: p.Failed})
	}
	return v, true
}

func (s *Server) teamNameLocked(ch *Chan) string {
	id := ch.Info.TeamID
	if id == "" {
		id = s.nav.TeamID
	}
	for _, t := range s.teams {
		if t.ID == id {
			return t.Name
		}
	}
	return ""
}

func (s *Server) postViewLocked(p model.Post) PostView {
	v := PostView{ID: p.ID, UserID: p.UserID, RootID: p.RootID, Message: p.Message, CreateAt: p.CreateAt,
		EditAt: p.EditAt, ReplyCount: p.ReplyCount, System: p.IsSystem(), Attachments: p.Props.Attachments,
		Bot: bool(p.Props.FromBot) || bool(p.Props.FromWebhook)}
	v.Author = s.displayNameLocked(p.UserID)
	if bool(p.Props.FromWebhook) && p.Props.OverrideUsername != "" {
		v.Author = string(p.Props.OverrideUsername)
	}
	if u, ok := s.users[p.UserID]; ok && u.IsBot {
		v.Bot = true
	}
	if p.Metadata != nil {
		for _, f := range p.Metadata.Files {
			v.Files = append(v.Files, FileView{Name: f.Name, Size: f.Size, Mime: f.MimeType})
		}
		idx := map[string]int{}
		for _, r := range p.Metadata.Reactions {
			i, ok := idx[r.EmojiName]
			if !ok {
				i = len(v.Reactions)
				idx[r.EmojiName] = i
				v.Reactions = append(v.Reactions, ReactionView{Emoji: r.EmojiName})
			}
			v.Reactions[i].Count++
			v.Reactions[i].Mine = v.Reactions[i].Mine || r.UserID == s.me.ID
		}
	}
	return v
}
```

- [ ] **Step 4: Тесты проходят**

Run: `go test -race ./internal/state/`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/state
git commit -m "state: post windows, pending posts, WS event application, channel view"
```

---
### Task 8: Снимок горячего слоя в SQLite

**Files:**
- Create: `internal/store/migrations/0002_cache.sql`, `internal/store/cache.go`, `internal/store/cache_test.go`
- Create/replace: `internal/state/snapshot.go` (полная версия вместо заглушки Task 6), `internal/state/snapshot_test.go`

**Interfaces:**
- Consumes: Task 6/7 (`Server`, `dirty`).
- Produces:
  - `store.CacheKey{Kind, Key string}`, `store.CacheEntry{Kind, Key string; Data []byte}`; `(*Store).LoadCache(ctx, serverID int64) ([]CacheEntry, error)`; `(*Store).SaveCache(ctx, serverID int64, put []CacheEntry, del []CacheKey) error` — одна транзакция; `(*Store).ClearCache(ctx, serverID int64) error`. Строки кэша удаляются каскадно вместе с сервером.
  - `state.SnapshotVersion = 1`; `state.ErrNoSnapshot`, `state.ErrSnapshotVersion`; `(*Server).TakeSnapshot() ([]store.CacheEntry, []store.CacheKey)` — только изменённое с прошлого вызова; `(*Server).Restore([]store.CacheEntry) error`; `(*Server).SetLiveAt(ms int64)`; `(*Server).HasData() bool`.
  - Виды записей: `meta/server` (me, status, config, prefs, teams, categories, nav, version), `live/at` (мс последнего живого WS — отдельной крошечной записью, чтобы не переписывать meta каждые 3 с), `chan/<id>` (канал, членство, черновик), `posts/<id>` (окно), `user/<id>`.
  - После `Restore` каждое окно помечено `Stale`, а `SyncedAt` окна, которое было свежим в момент снимка, поднят до `live/at`. Воркер догоняет такие окна через `since`.

- [ ] **Step 1: Падающие тесты**

`internal/store/cache_test.go`:
```go
package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCacheSaveLoadDeleteClear(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	srv, err := s.AddServer(ctx, Server{URL: "https://a", Name: "A"})
	require.NoError(t, err)
	require.NoError(t, s.SaveCache(ctx, srv.ID, []CacheEntry{
		{Kind: "meta", Key: "server", Data: []byte(`{"v":1}`)},
		{Kind: "chan", Key: "c1", Data: []byte(`1`)},
		{Kind: "chan", Key: "c2", Data: []byte(`2`)},
	}, nil))
	require.NoError(t, s.SaveCache(ctx, srv.ID,
		[]CacheEntry{{Kind: "chan", Key: "c1", Data: []byte(`11`)}},
		[]CacheKey{{Kind: "chan", Key: "c2"}}))
	got, err := s.LoadCache(ctx, srv.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []CacheEntry{
		{Kind: "meta", Key: "server", Data: []byte(`{"v":1}`)},
		{Kind: "chan", Key: "c1", Data: []byte(`11`)},
	}, got)
	require.NoError(t, s.ClearCache(ctx, srv.ID))
	got, err = s.LoadCache(ctx, srv.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
	_, err = s.GetServer(ctx, srv.ID)
	assert.NoError(t, err, "clearing the cache keeps the server and its session")
}

func TestCacheGoesWithTheServer(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	srv, _ := s.AddServer(ctx, Server{URL: "https://a", Name: "A"})
	require.NoError(t, s.SaveCache(ctx, srv.ID, []CacheEntry{{Kind: "chan", Key: "c1", Data: []byte(`1`)}}, nil))
	require.NoError(t, s.DeleteServer(ctx, srv.ID))
	var n int
	require.NoError(t, s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cache_entries`).Scan(&n))
	assert.Zero(t, n)
}
```
(Если в пакете уже есть хелпер открытия тестовой базы — использовать его вместо `openTest`.)

`internal/state/snapshot_test.go`:
```go
package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/store"
)

func keysOf(put []store.CacheEntry) []string {
	var out []string
	for _, e := range put {
		out = append(out, e.Kind+"/"+e.Key)
	}
	return out
}

func TestSnapshotRoundTrip(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 100), mkPost("b", "off", "u2", 200)}, false, 5)
	s.SetDraft("off", "half-written")
	s.SetActive("off")
	s.SetLiveAt(900)
	put, _ := s.TakeSnapshot()

	r := New(fixedNow)
	require.NoError(t, r.Restore(put))
	assert.True(t, r.HasData())
	assert.Equal(t, s.Sidebar("t1"), r.Sidebar("t1"))
	v, ok := r.ChannelView("off")
	require.True(t, ok)
	assert.Len(t, v.Posts, 2)
	assert.Equal(t, "half-written", v.Draft)
	assert.True(t, v.Syncing, "restored windows must be caught up")
	assert.Equal(t, "b", v.GapAfter)
	it, ok := r.SyncItemFor("off")
	require.True(t, ok)
	assert.Equal(t, int64(900), it.SyncedAt, "fresh at snapshot time → complete up to the last live moment")
	assert.Equal(t, "off", r.Sidebar("t1").SelectedChannelID)
	assert.Equal(t, "bob", findItem(r.Sidebar("t1"), "dm2").Name, "users restored")
}

func TestTakeSnapshotOnlyWritesWhatChanged(t *testing.T) {
	s := newFixture()
	s.TakeSnapshot()
	put, del := s.TakeSnapshot()
	assert.Empty(t, put)
	assert.Empty(t, del)
	s.ClearGuard()
	s.SetWindow("off", nil, true, 5)
	s.TakeSnapshot()
	s.ApplyEvent(postedEv(mkPost("p", "off", "u2", 5000)))
	put, _ = s.TakeSnapshot()
	assert.ElementsMatch(t, []string{"chan/off", "posts/off"}, keysOf(put))
	s.SetLiveAt(1)
	put, _ = s.TakeSnapshot()
	assert.Equal(t, []string{"live/at"}, keysOf(put))
}

func TestLeftChannelIsDeletedFromSnapshot(t *testing.T) {
	s := newFixture()
	s.TakeSnapshot()
	b := fixture()
	b.Members = b.Members[1:] // left town
	s.Bootstrap(b)
	_, del := s.TakeSnapshot()
	assert.ElementsMatch(t, []store.CacheKey{{Kind: "chan", Key: "town"}, {Kind: "posts", Key: "town"}}, del)
}

func TestRestoreRejectsMissingOrForeignVersion(t *testing.T) {
	assert.ErrorIs(t, New(fixedNow).Restore(nil), ErrNoSnapshot)
	meta, _ := json.Marshal(map[string]any{"version": 99})
	err := New(fixedNow).Restore([]store.CacheEntry{{Kind: "meta", Key: "server", Data: meta}})
	assert.ErrorIs(t, err, ErrSnapshotVersion)
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/store/ ./internal/state/`
Expected: FAIL (нет `SaveCache`, `TakeSnapshot`, …).

- [ ] **Step 3: Реализация**

`internal/store/migrations/0002_cache.sql`:
```sql
-- cache_entries is the write-behind snapshot of each server's hot state
-- (internal/state): metadata, post windows, users. Separate from servers so
-- "reset cache" and a rebuilt snapshot never sign the user out; rows go away
-- with their server.
CREATE TABLE cache_entries (
    server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    kind      TEXT    NOT NULL,
    key       TEXT    NOT NULL,
    data      BLOB    NOT NULL,
    PRIMARY KEY (server_id, kind, key)
) WITHOUT ROWID;
```

`internal/store/cache.go`:
```go
package store

import (
	"context"
	"database/sql"
)

type CacheKey struct {
	Kind string
	Key  string
}

type CacheEntry struct {
	Kind string
	Key  string
	Data []byte
}

func (s *Store) LoadCache(ctx context.Context, serverID int64) ([]CacheEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, key, data FROM cache_entries WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CacheEntry
	for rows.Next() {
		var e CacheEntry
		if err := rows.Scan(&e.Kind, &e.Key, &e.Data); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveCache writes one flush of the write-behind snapshot atomically.
func (s *Store) SaveCache(ctx context.Context, serverID int64, put []CacheEntry, del []CacheKey) error {
	if len(put) == 0 && len(del) == 0 {
		return nil
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, k := range del {
			if _, err := tx.ExecContext(ctx, `DELETE FROM cache_entries WHERE server_id = ? AND kind = ? AND key = ?`, serverID, k.Kind, k.Key); err != nil {
				return err
			}
		}
		for _, e := range put {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO cache_entries(server_id, kind, key, data) VALUES (?, ?, ?, ?)
				 ON CONFLICT(server_id, kind, key) DO UPDATE SET data = excluded.data`,
				serverID, e.Kind, e.Key, e.Data); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ClearCache(ctx context.Context, serverID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cache_entries WHERE server_id = ?`, serverID)
	return err
}
```

`internal/state/snapshot.go` (полная версия):
```go
package state

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/store"
)

// SnapshotVersion changes whenever the snapshot format does; an old
// snapshot is then discarded and the server is synced from scratch.
const SnapshotVersion = 1

var (
	ErrNoSnapshot      = errors.New("state: no snapshot")
	ErrSnapshotVersion = errors.New("state: snapshot version mismatch")
)

const (
	kindMeta  = "meta"
	kindLive  = "live"
	kindChan  = "chan"
	kindPosts = "posts"
	kindUser  = "user"
)

type dirtySet struct {
	meta, live                     bool
	chans, posts, users, delChans  map[string]bool
}

func newDirtySet() dirtySet {
	return dirtySet{chans: map[string]bool{}, posts: map[string]bool{}, users: map[string]bool{}, delChans: map[string]bool{}}
}

func (d *dirtySet) dropChan(id string) {
	delete(d.chans, id)
	delete(d.posts, id)
	d.delChans[id] = true
}

type metaSnap struct {
	Version    int                                `json:"version"`
	Me         model.User                         `json:"me"`
	Status     model.Status                       `json:"status"`
	Config     Config                             `json:"config"`
	Prefs      []model.Preference                 `json:"prefs"`
	Teams      []model.Team                       `json:"teams"`
	Categories map[string]model.OrderedCategories `json:"categories"`
	Nav        Nav                                `json:"nav"`
}

type chanSnap struct {
	Info   model.Channel       `json:"info"`
	Member model.ChannelMember `json:"member"`
	Draft  string              `json:"draft,omitempty"`
}

func (s *Server) SetLiveAt(ms int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.liveAt = ms
	s.dirty.live = true
}

func (s *Server) HasData() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.me.ID != ""
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("snapshot encode", "err", err) // model types always encode
	}
	return b
}

// TakeSnapshot returns what changed since the previous call and resets the
// dirty set. Encoding happens under the lock — it is cheap next to the disk
// write, which the caller does after releasing it.
func (s *Server) TakeSnapshot() (put []store.CacheEntry, del []store.CacheKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.dirty
	s.dirty = newDirtySet()
	if d.meta {
		prefs := make([]model.Preference, 0, len(s.prefs))
		for k, v := range s.prefs {
			prefs = append(prefs, model.Preference{UserID: s.me.ID, Category: k.cat, Name: k.name, Value: v})
		}
		put = append(put, store.CacheEntry{Kind: kindMeta, Key: "server", Data: mustJSON(metaSnap{
			Version: SnapshotVersion, Me: s.me, Status: s.status, Config: s.cfg, Prefs: prefs,
			Teams: s.teams, Categories: s.cats, Nav: s.nav,
		})})
	}
	if d.live {
		put = append(put, store.CacheEntry{Kind: kindLive, Key: "at", Data: mustJSON(s.liveAt)})
	}
	for id := range d.chans {
		if ch := s.chans[id]; ch != nil {
			put = append(put, store.CacheEntry{Kind: kindChan, Key: id, Data: mustJSON(chanSnap{Info: ch.Info, Member: ch.Member, Draft: s.drafts[id]})})
		}
	}
	for id := range d.posts {
		if ch := s.chans[id]; ch != nil && ch.Win.Loaded {
			put = append(put, store.CacheEntry{Kind: kindPosts, Key: id, Data: mustJSON(ch.Win)})
		}
	}
	for id := range d.users {
		if u, ok := s.users[id]; ok {
			put = append(put, store.CacheEntry{Kind: kindUser, Key: id, Data: mustJSON(u)})
		}
	}
	for id := range d.delChans {
		if s.chans[id] == nil {
			del = append(del, store.CacheKey{Kind: kindChan, Key: id}, store.CacheKey{Kind: kindPosts, Key: id})
		}
	}
	return put, del
}

// Restore loads a snapshot into an empty Server (cold start). Every window
// comes back stale: the worker catches it up with posts?since=.
func (s *Server) Restore(entries []store.CacheEntry) error {
	var meta *metaSnap
	for _, e := range entries {
		if e.Kind == kindMeta && e.Key == "server" {
			var m metaSnap
			if err := json.Unmarshal(e.Data, &m); err != nil {
				return errors.Join(ErrSnapshotVersion, err)
			}
			meta = &m
		}
	}
	if meta == nil {
		return ErrNoSnapshot
	}
	if meta.Version != SnapshotVersion {
		return ErrSnapshotVersion
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.me, s.status, s.cfg, s.teams, s.cats = meta.Me, meta.Status, meta.Config, meta.Teams, meta.Categories
	if s.cats == nil {
		s.cats = map[string]model.OrderedCategories{}
	}
	s.nav = meta.Nav
	if s.nav.Channel == nil {
		s.nav.Channel = map[string]string{}
	}
	for _, p := range meta.Prefs {
		s.prefs[prefKey{p.Category, p.Name}] = p.Value
	}
	s.users[s.me.ID] = s.me
	var liveAt int64
	wins := map[string]Window{}
	for _, e := range entries {
		switch e.Kind {
		case kindLive:
			_ = json.Unmarshal(e.Data, &liveAt)
		case kindChan:
			var c chanSnap
			if json.Unmarshal(e.Data, &c) == nil {
				s.chans[e.Key] = &Chan{Info: c.Info, Member: c.Member}
				if c.Draft != "" {
					s.drafts[e.Key] = c.Draft
				}
			}
		case kindPosts:
			var w Window
			if json.Unmarshal(e.Data, &w) == nil {
				wins[e.Key] = w
			}
		case kindUser:
			var u model.User
			if json.Unmarshal(e.Data, &u) == nil {
				s.users[u.ID] = u
			}
		}
	}
	s.liveAt = liveAt
	for id, w := range wins {
		ch := s.chans[id]
		if ch == nil {
			continue
		}
		if !w.Stale {
			w.SyncedAt = max(w.SyncedAt, liveAt)
		}
		w.Loaded, w.Stale, w.GapAfter = true, true, ""
		if n := len(w.Posts); n > 0 {
			w.GapAfter = w.Posts[n-1].ID
		}
		ch.Win = w
		for _, p := range w.Posts {
			s.seen.add(p.ID)
		}
	}
	return nil
}
```
В `Server` (server.go) добавить поле `liveAt int64`.

- [ ] **Step 4: Тесты проходят**

Run: `go test -race ./internal/store/ ./internal/state/`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/store internal/state
git commit -m "store+state: write-behind cache snapshot (cache_entries), restore marks windows stale"
```

---

### Task 9: Правила уведомлений

**Files:**
- Create: `internal/notify/decide.go`, `internal/notify/decide_test.go`

**Interfaces:**
- Consumes: Task 1 `model.Post`, `model.Attachment`.
- Produces: `type Input struct{ MeID, MeUsername, MeFirstName string; UserNotify, MemberNotify map[string]string; Status string; DNDEndSec, NowMs int64; Post model.Post; ChannelType string; Mentions, Followers []string; CRT, Focused bool; ActiveChannelID string }`; `func Decide(Input) (bool, string)` — второе значение — причина отказа (для логов/тестов), `""` при «да».

Порядок правил — `docs/research/…` §6 (веб-клиент `notification_actions.tsx`); исключение «вас добавили в канал» для системных постов не реализуем.

- [ ] **Step 1: Падающий тест**

`internal/notify/decide_test.go`:
```go
package notify

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

func base() Input {
	return Input{
		MeID: "me", MeUsername: "alice", MeFirstName: "Alice",
		UserNotify:   map[string]string{"desktop": "mention", "channel": "true"},
		MemberNotify: map[string]string{"desktop": "default", "mark_unread": "all"},
		Status:       "online", NowMs: 1_000_000,
		Post:         model.Post{ID: "p", ChannelID: "c", UserID: "bob", Message: "hello"},
		ChannelType:  "O",
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name   string
		mut    func(*Input)
		want   bool
		reason string
	}{
		{"plain message, level mention", func(*Input) {}, false, "not_mentioned"},
		{"mentioned", func(in *Input) { in.Mentions = []string{"me"} }, true, ""},
		{"DM always", func(in *Input) { in.ChannelType = "D" }, true, ""},
		{"own post", func(in *Input) { in.Post.UserID = "me"; in.Mentions = []string{"me"} }, false, "own_post"},
		{"own webhook post", func(in *Input) { in.Post.UserID = "me"; in.Post.Props.FromWebhook = true; in.ChannelType = "D" }, true, ""},
		{"system post", func(in *Input) { in.Post.Type = "system_join_channel"; in.ChannelType = "D" }, false, "system_message"},
		{"force", func(in *Input) { in.Post.Props.ForceNotification = true; in.MemberNotify["mark_unread"] = "mention" }, true, ""},
		{"muted", func(in *Input) { in.ChannelType = "D"; in.MemberNotify["mark_unread"] = "mention" }, false, "channel_muted"},
		{"dnd", func(in *Input) { in.ChannelType = "D"; in.Status = "dnd" }, false, "user_status"},
		{"dnd expired", func(in *Input) { in.ChannelType = "D"; in.Status = "dnd"; in.DNDEndSec = 999 }, true, ""},
		{"ooo", func(in *Input) { in.ChannelType = "D"; in.Status = "ooo" }, false, "user_status"},
		{"user level all", func(in *Input) { in.UserNotify["desktop"] = "all" }, true, ""},
		{"channel override none", func(in *Input) { in.MemberNotify["desktop"] = "none"; in.Mentions = []string{"me"} }, false, "notify_level_none"},
		{"channel override all", func(in *Input) { in.MemberNotify["desktop"] = "all" }, true, ""},
		{"follower counts as mention", func(in *Input) { in.Followers = []string{"me"} }, true, ""},
		{"GM default+mention becomes all", func(in *Input) { in.ChannelType = "G" }, true, ""},
		{"GM explicit mention level, not mentioned", func(in *Input) { in.ChannelType = "G"; in.MemberNotify["desktop"] = "mention" }, false, "not_explicitly_mentioned"},
		{"GM explicit mention level, @alice", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.Post.Message = "hey @Alice, look"
		}, true, ""},
		{"GM @here ignored by member", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.MemberNotify["ignore_channel_mentions"] = "on"
			in.Post.Message = "@here standup"
		}, false, "not_explicitly_mentioned"},
		{"GM @here ignored by user setting", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.UserNotify["channel"] = "false"
			in.Post.Message = "@channel standup"
		}, false, "not_explicitly_mentioned"},
		{"GM first name when enabled", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.UserNotify["first_name"] = "true"
			in.Post.Message = "Alice?"
		}, true, ""},
		{"GM mention key in attachment", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.UserNotify["mention_keys"] = "deploy,oncall"
			in.Post.Props.Attachments = []model.Attachment{{Text: "ping ONCALL now"}}
		}, true, ""},
		{"GM username inside a word is not a mention", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.Post.Message = "email@alice.example"
		}, false, "not_explicitly_mentioned"},
		{"CRT reply, level all, not following", func(in *Input) {
			in.CRT = true
			in.Post.RootID = "r"
			in.UserNotify["desktop"] = "all"
		}, false, "not_following_thread"},
		{"CRT reply, following", func(in *Input) {
			in.CRT = true
			in.Post.RootID = "r"
			in.UserNotify["desktop"] = "all"
			in.Followers = []string{"me"}
		}, true, ""},
		{"focused on this channel", func(in *Input) { in.ChannelType = "D"; in.Focused = true; in.ActiveChannelID = "c" }, false, "channel_is_open"},
		{"not focused on this channel", func(in *Input) { in.ChannelType = "D"; in.ActiveChannelID = "c" }, true, ""},
		{"focused elsewhere", func(in *Input) { in.ChannelType = "D"; in.Focused = true; in.ActiveChannelID = "other" }, true, ""},
		{"no user props → level all", func(in *Input) { in.UserNotify = nil }, true, ""},
	}
	for _, c := range cases {
		in := base()
		in.UserNotify = map[string]string{"desktop": "mention", "channel": "true"}
		in.MemberNotify = map[string]string{"desktop": "default", "mark_unread": "all"}
		c.mut(&in)
		got, reason := Decide(in)
		assert.Equal(t, c.want, got, c.name)
		assert.Equal(t, c.reason, reason, c.name)
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/notify/`
Expected: FAIL (пакета нет).

- [ ] **Step 3: Реализация**

`internal/notify/decide.go`:
```go
// Package notify decides whether a new post deserves a desktop
// notification, following the server's notification settings the way the
// official webapp does (docs/research/2026-09-24-mattermost-api-facts.md §6).
// Mentions come from the server (the posted event's mentions/followers), not
// from parsing the text — except for group messages at "mention" level,
// which the webapp scans itself.
package notify

import (
	"slices"
	"strings"
	"unicode"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

type Input struct {
	MeID, MeUsername, MeFirstName string
	UserNotify                    map[string]string // user.notify_props
	MemberNotify                  map[string]string // channel member notify_props
	Status                        string
	DNDEndSec                     int64 // seconds; 0 = no end
	NowMs                         int64
	Post                          model.Post
	ChannelType                   string
	Mentions, Followers           []string
	CRT                           bool
	Focused                       bool
	ActiveChannelID               string
}

func Decide(in Input) (bool, string) {
	p := in.Post
	if p.UserID == in.MeID && !bool(p.Props.FromWebhook) {
		return false, "own_post"
	}
	if p.IsSystem() {
		return false, "system_message"
	}
	if bool(p.Props.ForceNotification) {
		return true, ""
	}
	if in.MemberNotify["mark_unread"] == "mention" {
		return false, "channel_muted"
	}
	dnd := in.Status == "dnd" && (in.DNDEndSec == 0 || in.NowMs/1000 < in.DNDEndSec)
	if dnd || in.Status == "ooo" {
		return false, "user_status"
	}
	mentioned := slices.Contains(in.Mentions, in.MeID) || slices.Contains(in.Followers, in.MeID)
	chProp := in.MemberNotify["desktop"]
	if chProp == "" {
		chProp = "default"
	}
	level := chProp
	if chProp == "default" {
		level = in.UserNotify["desktop"]
		if level == "" {
			level = "all"
		}
	}
	if in.ChannelType == model.ChannelGroup && chProp == "default" && in.UserNotify["desktop"] == "mention" {
		level = "all"
	}
	crtReply := in.CRT && p.RootID != ""
	switch {
	case level == "none":
		return false, "notify_level_none"
	case in.ChannelType == model.ChannelGroup && level == "mention":
		if !explicitlyMentioned(in) {
			return false, "not_explicitly_mentioned"
		}
	case level == "mention" && !mentioned && in.ChannelType != model.ChannelDirect:
		return false, "not_mentioned"
	case crtReply && level == "all" && !slices.Contains(in.Followers, in.MeID):
		return false, "not_following_thread"
	}
	// Thread panels arrive in stage 3; until then a CRT reply is never
	// "open", a root post is when its channel is on screen.
	if in.Focused && !crtReply && in.ActiveChannelID == p.ChannelID {
		return false, "channel_is_open"
	}
	return true, ""
}

func mentionKeys(in Input) []string {
	keys := []string{"@" + in.MeUsername}
	if in.UserNotify["first_name"] == "true" && in.MeFirstName != "" {
		keys = append(keys, in.MeFirstName)
	}
	for _, k := range strings.Split(in.UserNotify["mention_keys"], ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	ignore := in.MemberNotify["ignore_channel_mentions"]
	channelOff := ignore == "on" || ((ignore == "" || ignore == "default") && in.UserNotify["channel"] == "false")
	if !channelOff {
		keys = append(keys, "@channel", "@all", "@here")
	}
	return keys
}

func explicitlyMentioned(in Input) bool {
	texts := []string{in.Post.Message}
	for _, a := range in.Post.Props.Attachments {
		texts = append(texts, a.Pretext, a.Title, a.Text, a.Fallback)
	}
	text := strings.ToLower(strings.Join(texts, "\n"))
	for _, k := range mentionKeys(in) {
		if containsWord(text, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '-' || r == '@' }

// containsWord finds key in text not glued to other word characters
// ("@alice," matches; "email@alice.example" does not). A trailing '.' is
// sentence punctuation, not part of the word.
func containsWord(text, key string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], key)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(key)
		before := start == 0 || !isWordRune(lastRune(text[:start]))
		after := end == len(text) || !isWordRune(firstRune(text[end:])) ||
			(text[end] == '.' && (end+1 == len(text) || !isWordRune(firstRune(text[end+1:]))))
		if before && after {
			return true
		}
		i = start + 1
	}
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	r := []rune(s)
	return r[len(r)-1]
}
```

- [ ] **Step 4: Тест проходит**

Run: `go test -race ./internal/notify/`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/notify
git commit -m "notify: desktop notification rules mirroring the Mattermost webapp"
```

---
### Task 10: Воркер синхронизации сервера и менеджер

**Files:**
- Create: `internal/mmsync/worker.go`, `internal/mmsync/fetch.go`, `internal/mmsync/actions.go`, `internal/mmsync/wake.go`, `internal/mmsync/manager.go`
- Create: `internal/mmsync/harness_test.go`, `internal/mmsync/worker_test.go`, `internal/mmsync/fetch_test.go`, `internal/mmsync/manager_test.go`
- Modify: `internal/state/posts.go` (+ `ResetWindows()`), `internal/mmfake/chat.go` (+ `FailPosts(n int)`: следующие n `POST /posts` отвечают 500)

**Interfaces:**
- Consumes: Tasks 1–8.
- Produces (пакет `mmsync`):
  - `type Status string`: `StatusOff`, `StatusConnecting`, `StatusLive`, `StatusReconnecting`, `StatusNeedsReauth` (значения `off|connecting|live|reconnecting|needs_reauth`).
  - `type Hooks struct{ Changed func(serverID int64, ch state.Change); Status func(serverID int64, st Status); Notify func(serverID int64, c state.NotifyCandidate) }` — вызываются из горутин воркера и не должны блокировать.
  - `type Config struct{ Store *store.Store; HTTPClient *http.Client; Hooks Hooks; Now func() time.Time; FlushEvery, MinBackoff, MaxBackoff, WakeCheck time.Duration; Fetchers int; WS func(*ws.Options) }`. Нули → 3 с, 1 с, 60 с, 5 с, 3.
  - `NewWorker(Config, store.Server) *Worker`; `(*Worker).Run(ctx)` (блокирует до отмены ctx; перед выходом сбрасывает снимок); `State() *state.Server`; `Status() Status`; `Nudge()`.
  - Действия: `OpenChannel(channelID string) (state.ChannelView, bool)`; `SetFocused(bool)`; `LoadOlder(ctx, channelID string) error`; `Send(channelID, message string) error`; `Retry(channelID, pendingID string)`; `Discard(channelID, pendingID string)`; `Edit(ctx, postID, message string) error`; `Delete(ctx, postID string) error`; `MarkUnread(ctx, postID string) error`; `SaveDraft(channelID, text string)`.
  - `NewManager(root context.Context, Config) *Manager`; `StartAll(ctx) error` (все серверы с токеном); `Start(store.Server)` (перезапуск, если уже есть); `Stop(id int64)` (ждёт финальный сброс); `Worker(id int64) *Worker` (nil, если нет); `Each(fn func(id int64, w *Worker))` (снимок списка под мьютексом, `fn` вызывается без него); `NudgeAll()`; `Close()`. Фокус менеджер не раздаёт: сервис (Task 11) применяет его только к воркеру активного сервера — иначе «активный» канал фонового сервера отмечался бы прочитанным.
  - `state.(*Server).ResetWindows()` — сбросить все окна (переключился CRT).
  - `mmfake.(*Server).FailPosts(n int)`.

Поведение (спецификация «Данные и синхронизация» + `docs/research/…` §1, §4):
- Каждая попытка подключения: `GET users/me` (401 → `needs_reauth`, других попыток нет) → `ws.Dial` с сохранённым `Resume` → если потока ещё не было (`!booted` или `connection_id` пуст) — bootstrap. Bootstrap = `config/client`, `users/me`, preferences, команды, каналы, членства, статус, категории каждой команды → `state.Bootstrap`; при потере потока ещё и `MarkStale(lastLive)`. Затем догрузка пользователей и постановка в очередь всех окон, которые нужно загрузить или догнать.
- Живой цикл: события → `state.ApplyEvent` → хуки/эффекты. `hello` с `Reset` → bootstrap с `MarkStale`. Когда буфер событий опустел — `ClearGuard`.
- Обрыв → `reconnecting`, пауза 1 с, 2 с, 4 с … 60 с. `Nudge` (сон/сеть) прерывает паузу, а в живом состоянии переподключает сразу (resume делает это дешёвым).
- Сессия считается мёртвой **только по HTTP 401** (403 — это отказ в правах на конкретный канал, а не протухший токен).
- Очередь догонки: 3 параллельных загрузчика, приоритет из `SyncItem`. Незагруженное окно — последняя страница (60). Загруженное — `since = SyncedAt − 2 мин`; если в ответе ≥ `rest.SinceLimit` постов в `order` — перезагрузить последней страницей. Сетевая ошибка — повтор того же элемента через 10 с; ошибка API (4xx) — без повтора.
- Снимок: каждые `FlushEvery` в состоянии `live` — `SetLiveAt(now)`, затем `TakeSnapshot` → `SaveCache`; финальный сброс при остановке. Холодный старт: `LoadCache` → `Restore`; `ErrSnapshotVersion` → `ClearCache`.
- Прочтение: `OpenChannel` и `SetFocused(true)` зовут `view`, если окно в фокусе и канал непрочитан. Одновременно — не больше одного запроса на канал; после ответа проверка повторяется (пришло новое, пока шёл запрос).

- [ ] **Step 1: Падающие тесты**

`internal/mmsync/harness_test.go`:
```go
package mmsync

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/mm/ws"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/state"
	"github.com/spk/spk-mattermost/internal/store"
)

type harness struct {
	t      *testing.T
	fake   *mmfake.Server
	store  *store.Store
	srv    store.Server
	w      *Worker
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	statuses []Status
	notes    []state.NotifyCandidate
}

func newHarness(t *testing.T, o mmfake.Options) *harness {
	t.Helper()
	fake := mmfake.Start(o)
	t.Cleanup(fake.Close)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	srv, err := st.AddServer(context.Background(), store.Server{URL: fake.URL(), Name: "Fake"})
	require.NoError(t, err)
	tok, u, err := rest.New(fake.URL(), "", nil).Login(context.Background(), "alice", "secret")
	require.NoError(t, err)
	require.NoError(t, st.SetSession(context.Background(), srv.ID, tok, u.ID, u.Username))
	srv, _ = st.GetServer(context.Background(), srv.ID)
	return &harness{t: t, fake: fake, store: st, srv: srv}
}

func (h *harness) config() Config {
	return Config{
		Store: h.store,
		Hooks: Hooks{
			Changed: func(int64, state.Change) {},
			Status: func(_ int64, s Status) {
				h.mu.Lock()
				h.statuses = append(h.statuses, s)
				h.mu.Unlock()
			},
			Notify: func(_ int64, c state.NotifyCandidate) {
				h.mu.Lock()
				h.notes = append(h.notes, c)
				h.mu.Unlock()
			},
		},
		FlushEvery: 50 * time.Millisecond, MinBackoff: 20 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
		WakeCheck: time.Hour,
		WS:        func(o *ws.Options) { o.PingInterval, o.PingTimeout = 200*time.Millisecond, time.Second },
	}
}

func (h *harness) start() {
	h.t.Helper()
	h.w = NewWorker(h.config(), h.srv)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan struct{})
	go func() { h.w.Run(ctx); close(h.done) }()
	h.t.Cleanup(h.stop)
}

func (h *harness) stop() {
	if h.cancel != nil {
		h.cancel()
		<-h.done
		h.cancel = nil
	}
}

func (h *harness) eventually(cond func() bool, msg string) {
	h.t.Helper()
	require.Eventually(h.t, cond, 10*time.Second, 20*time.Millisecond, msg)
}

func (h *harness) live() {
	h.t.Helper()
	h.eventually(func() bool { return h.w.Status() == StatusLive }, "worker never went live")
}

func (h *harness) view(ch string) state.ChannelView {
	v, _ := h.w.State().ChannelView(ch)
	return v
}

func (h *harness) hasMessage(ch, msg string) bool {
	for _, p := range h.view(ch).Posts {
		if p.Message == msg && !p.Pending {
			return true
		}
	}
	return false
}

func (h *harness) allLoaded() bool { return len(h.w.State().SyncItems()) == 0 }
```

`internal/mmsync/worker_test.go`:
```go
package mmsync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mmfake"
)

func TestBootstrapLoadsEverything(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch did not finish")
	sb := h.w.State().Sidebar("")
	assert.Equal(t, "t-fake", sb.TeamID)
	assert.Equal(t, "c-town", sb.SelectedChannelID)
	town := h.view("c-town")
	assert.Len(t, town.Posts, 60)
	assert.True(t, town.HasMore)
	assert.Equal(t, "Message #150", town.Posts[59].Message)
	h.eventually(func() bool { return h.view("c-town").Posts[58].Author == "carol" }, "authors are resolved after the page lands")
}

func TestLivePostArrivesCountsAndNotifies(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.PostAs("c-offtopic", "bob", "hi @alice")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "hi @alice") }, "post did not arrive")
	assert.Equal(t, 1, h.w.State().Badge().Mentions)
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Len(t, h.notes, 1)
	assert.Equal(t, "Off-Topic", h.notes[0].ChannelName)
}

func TestReconnectWithoutLossResumes(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.DropConnections(false)
	h.fake.PostAs("c-offtopic", "bob", "sent while away")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "sent while away") }, "missed post not replayed")
	h.live()
	assert.False(t, h.view("c-offtopic").Syncing, "lossless resume needs no catch-up")
}

func TestReconnectWithLossCatchesUp(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.DropConnections(true)
	h.fake.PostAs("c-offtopic", "bob", "lost event")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "lost event") }, "since catch-up did not bring the post")
	h.eventually(func() bool { return !h.view("c-offtopic").Syncing }, "window stayed stale")
}

func TestColdStartFromSnapshotAndOfflineReadable(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.stop()
	h.fake.Close() // server gone
	h.start()
	h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "should keep retrying")
	assert.Len(t, h.view("c-town").Posts, 60, "cache is readable offline")
	assert.True(t, h.view("c-town").Syncing)
}

func TestCatchUpOverflowReloadsLatestPage(t *testing.T) {
	h := newHarness(t, mmfake.Options{SinceLimit: 5})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.stop()
	for i := 0; i < 10; i++ {
		h.fake.PostAs("c-town", "bob", fmt.Sprintf("offline %d", i))
	}
	h.start()
	h.live()
	h.eventually(func() bool { return h.hasMessage("c-town", "offline 9") && !h.view("c-town").Syncing }, "overflow must reload the page")
	v := h.view("c-town")
	assert.Len(t, v.Posts, 60)
	assert.Empty(t, v.GapAfter)
	assert.True(t, h.hasMessage("c-town", "offline 0"), "the reloaded page holds everything recent")
}

func TestRevokedSessionGoesNeedsReauth(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.RevokeAll()
	h.fake.DropConnections(true)
	h.eventually(func() bool { return h.w.Status() == StatusNeedsReauth }, "should need re-auth")
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, StatusNeedsReauth, h.w.Status(), "no retry loop")
	assert.NotEmpty(t, h.view("c-town").Posts, "cache stays readable")
}

func TestSendPostOnce(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	require.NoError(t, h.w.Send("c-offtopic", "hello from test"))
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "hello from test") }, "sent post not shown")
	time.Sleep(200 * time.Millisecond) // let the WS echo land too
	n := 0
	for _, p := range h.view("c-offtopic").Posts {
		if p.Message == "hello from test" {
			n++
		}
	}
	assert.Equal(t, 1, n)
}

func TestSendFailureThenRetry(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.FailPosts(1)
	require.NoError(t, h.w.Send("c-offtopic", "flaky"))
	var failedID string
	h.eventually(func() bool {
		for _, p := range h.view("c-offtopic").Posts {
			if p.Failed {
				failedID = p.ID
				return true
			}
		}
		return false
	}, "post should fail")
	h.w.Retry("c-offtopic", failedID)
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "flaky") }, "retry did not send")
	n := 0
	for _, p := range h.fake.VisiblePosts("c-offtopic") {
		if p.Message == "flaky" {
			n++
		}
	}
	assert.Equal(t, 1, n)
}

func TestOpenChannelMarksReadWhenFocused(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.PostAs("c-offtopic", "bob", "unread one")
	h.eventually(func() bool { return h.w.State().Badge().Unread }, "should be unread")
	h.w.OpenChannel("c-offtopic") // not focused: stays unread
	time.Sleep(100 * time.Millisecond)
	assert.True(t, h.w.State().Badge().Unread)
	h.w.SetFocused(true)
	h.eventually(func() bool { return !h.w.State().Badge().Unread }, "focus should mark read")
	h.eventually(func() bool {
		return h.fake.Member("c-offtopic", "alice").MsgCount == h.fake.Channel("c-offtopic").TotalMsgCount
	}, "server not told")
	h.fake.PostAs("c-offtopic", "bob", "while open")
	h.eventually(func() bool {
		return h.fake.Member("c-offtopic", "alice").MsgCount == h.fake.Channel("c-offtopic").TotalMsgCount && h.hasMessage("c-offtopic", "while open")
	}, "open focused channel keeps being read")
}

func TestMarkUnreadStaysUnreadWhileOpen(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.SetFocused(true)
	v, _ := h.w.OpenChannel("c-town")
	require.NoError(t, h.w.MarkUnread(context.Background(), v.Posts[50].ID))
	assert.True(t, h.w.State().Badge().Unread)
	time.Sleep(200 * time.Millisecond)
	assert.True(t, h.w.State().Badge().Unread, "no auto-read after an explicit mark-unread")
}

func TestLoadOlderUntilTheStart(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.OpenChannel("c-town")
	require.NoError(t, h.w.LoadOlder(context.Background(), "c-town"))
	assert.Len(t, h.view("c-town").Posts, 120)
	require.NoError(t, h.w.LoadOlder(context.Background(), "c-town"))
	v := h.view("c-town")
	assert.Len(t, v.Posts, 150)
	assert.False(t, v.HasMore)
	assert.Equal(t, "Message #1", v.Posts[0].Message)
}

func TestEditAndDelete(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	require.NoError(t, h.w.Send("c-offtopic", "v1"))
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "v1") }, "send")
	var id string
	for _, p := range h.view("c-offtopic").Posts {
		if p.Message == "v1" {
			id = p.ID
		}
	}
	require.NoError(t, h.w.Edit(context.Background(), id, "v2"))
	assert.True(t, h.hasMessage("c-offtopic", "v2"))
	require.NoError(t, h.w.Delete(context.Background(), id))
	assert.False(t, h.hasMessage("c-offtopic", "v2"))
}

func TestAddedToChannelRefreshesSidebar(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.fake.AddChannel("c-new", "Brand New", "alice", "bob")
	h.eventually(func() bool {
		for _, c := range h.w.State().Sidebar("").Categories {
			for _, it := range c.Channels {
				if it.ID == "c-new" {
					return true
				}
			}
		}
		return false
	}, "new channel not in sidebar")
	h.eventually(func() bool { return h.view("c-new").Loaded }, "new channel not prefetched")
}

func TestSleptDetection(t *testing.T) {
	assert.False(t, slept(5*time.Second, 5*time.Second, 15*time.Second))
	assert.True(t, slept(10*time.Minute, 5*time.Second, 15*time.Second))
}
```

`internal/mmsync/fetch_test.go`:
```go
package mmsync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/state"
)

func TestFetchQueuePriorityDedupAndCancel(t *testing.T) {
	q := newFetchQueue()
	q.push(state.SyncItem{ChannelID: "a", Priority: 3})
	q.push(state.SyncItem{ChannelID: "b", Priority: 1})
	q.push(state.SyncItem{ChannelID: "c", Priority: 3})
	q.push(state.SyncItem{ChannelID: "a", Priority: 0}) // boost
	q.push(state.SyncItem{ChannelID: "b", Priority: 4}) // never demote
	ctx := context.Background()
	var got []string
	for i := 0; i < 3; i++ {
		it, ok := q.pop(ctx)
		require.True(t, ok)
		got = append(got, it.ChannelID)
	}
	assert.Equal(t, []string{"a", "b", "c"}, got)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	_, ok := q.pop(cctx)
	assert.False(t, ok, "empty queue blocks until ctx is done")
}
```

`internal/mmsync/manager_test.go`:
```go
package mmsync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/store"
)

func TestManagerStartsSignedInServersOnly(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	other, err := h.store.AddServer(context.Background(), store.Server{URL: "https://signed-out.example", Name: "Out"})
	require.NoError(t, err)
	m := NewManager(context.Background(), h.config())
	defer m.Close()
	require.NoError(t, m.StartAll(context.Background()))
	require.NotNil(t, m.Worker(h.srv.ID))
	assert.Nil(t, m.Worker(other.ID))
	h.w = m.Worker(h.srv.ID)
	h.live()
	m.Stop(h.srv.ID)
	assert.Nil(t, m.Worker(h.srv.ID))
	entries, err := h.store.LoadCache(context.Background(), h.srv.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "stop flushes the snapshot")
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/mmsync/`
Expected: FAIL (пакета нет).

- [ ] **Step 3: Дополнения в state и mmfake**

`internal/state/posts.go`:
```go
// ResetWindows forgets every post window (CRT was toggled: the windows hold
// the wrong kind of posts). Channels are refetched by the worker.
func (s *Server) ResetWindows() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.chans {
		ch.Win = Window{}
		s.dirty.posts[id] = true
	}
	s.older, s.olderComplete = nil, false
}
```
В `TakeSnapshot` (Task 8) незагруженное окно, помеченное в `d.posts`, удаляется из снимка, иначе после рестарта вернулось бы окно не того вида (CRT/не-CRT). Цикл по `d.posts` заменить на:
```go
	for id := range d.posts {
		if ch := s.chans[id]; ch != nil {
			if ch.Win.Loaded {
				put = append(put, store.CacheEntry{Kind: kindPosts, Key: id, Data: mustJSON(ch.Win)})
			} else {
				del = append(del, store.CacheKey{Kind: kindPosts, Key: id})
			}
		}
	}
```

`internal/mmfake/chat.go`: поле `failPosts int` в `chatData`, метод
```go
// FailPosts makes the next n POST /posts fail with 500 (send-failure tests).
func (s *Server) FailPosts(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chat.failPosts = n
}
```
и в начале `createPost` (HTTP-обработчика, не `createPostLocked`) после разбора тела:
```go
	s.mu.Lock()
	if s.chat.failPosts > 0 {
		s.chat.failPosts--
		s.mu.Unlock()
		appError(w, 500, "app.post.save.app_error", "injected failure")
		return
	}
	p, e := s.createPostLocked(u.ID, in)
	s.mu.Unlock()
```

- [ ] **Step 4: Реализация воркера**

`internal/mmsync/worker.go`:
```go
// Package mmsync keeps one Mattermost server in sync: a worker per signed-in
// server validates the session, holds the WebSocket (with resume), resyncs
// metadata when the event stream is lost, catches post windows up in the
// background, writes the cache snapshot behind and performs user actions.
package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/mm/ws"
	"github.com/spk/spk-mattermost/internal/state"
	"github.com/spk/spk-mattermost/internal/store"
)

type Status string

const (
	StatusOff          Status = "off"
	StatusConnecting   Status = "connecting"
	StatusLive         Status = "live"
	StatusReconnecting Status = "reconnecting"
	StatusNeedsReauth  Status = "needs_reauth"
)

type Hooks struct {
	Changed func(serverID int64, ch state.Change)
	Status  func(serverID int64, st Status)
	Notify  func(serverID int64, c state.NotifyCandidate)
}

type Config struct {
	Store      *store.Store
	HTTPClient *http.Client
	Hooks      Hooks
	Now        func() time.Time
	FlushEvery time.Duration
	MinBackoff time.Duration
	MaxBackoff time.Duration
	WakeCheck  time.Duration
	Fetchers   int
	WS         func(*ws.Options) // test seam (ping intervals)
}

func (c *Config) defaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.FlushEvery <= 0 {
		c.FlushEvery = 3 * time.Second
	}
	if c.MinBackoff <= 0 {
		c.MinBackoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 60 * time.Second
	}
	if c.WakeCheck <= 0 {
		c.WakeCheck = 5 * time.Second
	}
	if c.Fetchers <= 0 {
		c.Fetchers = 3
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
}

const (
	sinceMargin    = 2 * time.Minute
	retryFetchIn   = 10 * time.Second
	createTimeout  = 30 * time.Second
	metaDebounce   = 300 * time.Millisecond
	finalFlushTime = 5 * time.Second
)

var errNudged = errors.New("mmsync: reconnect requested")

// sessionExpired: only 401 means the token is dead; 403 is a per-resource
// permission refusal (rest classifies both as KindAuth).
func sessionExpired(err error) bool {
	var e *rest.Error
	return errors.As(err, &e) && e.Status == http.StatusUnauthorized
}

type Worker struct {
	cfg Config
	srv store.Server
	rc  *rest.Client
	st  *state.Server

	runCtx   atomic.Value // context.Context of Run; background work derives from it
	status   atomic.Value // Status
	nudge    chan struct{}
	authFail chan struct{}
	metaReq  chan struct{}
	queue    *fetchQueue
	viewing  sync.Map // channel id → in-flight view
	usersMu  sync.Mutex
	bg       sync.WaitGroup
	lastLive atomic.Int64

	// only touched by the Run goroutine
	resume ws.Resume
	booted bool
}

func NewWorker(cfg Config, srv store.Server) *Worker {
	cfg.defaults()
	w := &Worker{
		cfg: cfg, srv: srv,
		rc:       rest.New(srv.URL, srv.Token, cfg.HTTPClient).WithLimiter(rest.NewLimiter()),
		st:       state.New(cfg.Now),
		nudge:    make(chan struct{}, 1),
		authFail: make(chan struct{}, 1),
		metaReq:  make(chan struct{}, 1),
		queue:    newFetchQueue(),
	}
	w.status.Store(StatusOff)
	return w
}

func (w *Worker) State() *state.Server { return w.st }
func (w *Worker) Status() Status       { return w.status.Load().(Status) }

func (w *Worker) setStatus(s Status) {
	if w.status.Swap(s) != s && w.cfg.Hooks.Status != nil {
		w.cfg.Hooks.Status(w.srv.ID, s)
	}
}

func (w *Worker) changed(c state.Change) {
	if !c.Empty() && w.cfg.Hooks.Changed != nil {
		w.cfg.Hooks.Changed(w.srv.ID, c)
	}
}

// Nudge asks for an immediate reconnect (wake from sleep, network back).
func (w *Worker) Nudge() {
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}

func (w *Worker) signalAuth() {
	select {
	case w.authFail <- struct{}{}:
	default:
	}
}

// goBG runs fn in the background under Run's context (actions may arrive
// from UI goroutines before Run has started; they then get Background).
func (w *Worker) goBG(fn func(ctx context.Context)) {
	ctx, _ := w.runCtx.Load().(context.Context)
	if ctx == nil {
		ctx = context.Background()
	}
	w.bg.Add(1)
	go func() {
		defer w.bg.Done()
		fn(ctx)
	}()
}

func (w *Worker) Run(ctx context.Context) {
	w.runCtx.Store(ctx)
	w.restore(ctx)
	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){w.flushLoop, w.fetchLoop, w.metaLoop, w.wakeLoop} {
		wg.Add(1)
		go func() { defer wg.Done(); loop(ctx) }()
	}
	attempt := 0
	for ctx.Err() == nil {
		if attempt == 0 {
			w.setStatus(StatusConnecting)
		} else {
			w.setStatus(StatusReconnecting)
		}
		err := w.session(ctx, func() { attempt = 0 })
		if ctx.Err() != nil {
			break
		}
		if sessionExpired(err) {
			slog.Warn("session expired, sign-in needed", "srv", w.srv.ID, "err", err)
			w.setStatus(StatusNeedsReauth)
			<-ctx.Done()
			break
		}
		if errors.Is(err, errNudged) {
			attempt = 0
			continue
		}
		attempt++
		d := min(w.cfg.MinBackoff<<(attempt-1), w.cfg.MaxBackoff)
		if d <= 0 {
			d = w.cfg.MaxBackoff
		}
		slog.Info("server connection lost, retrying", "srv", w.srv.ID, "in", d, "err", err)
		w.setStatus(StatusReconnecting)
		select {
		case <-ctx.Done():
		case <-w.nudge:
		case <-time.After(d):
		}
	}
	wg.Wait()
	w.bg.Wait()
	w.setStatus(StatusOff)
}

func (w *Worker) wsOptions() ws.Options {
	o := ws.Options{BaseURL: w.srv.URL, Token: w.srv.Token, HTTPClient: w.cfg.HTTPClient}
	if w.cfg.WS != nil {
		w.cfg.WS(&o)
	}
	return o
}

// session is one connection lifetime: validate the token, dial (resuming
// the previous stream if any), bootstrap when there is no stream to
// continue, then apply events until the stream ends.
func (w *Worker) session(ctx context.Context, onLive func()) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	select { // drop stale signals from the previous session
	case <-w.authFail:
	default:
	}
	if _, err := w.rc.Me(sctx); err != nil {
		return err
	}
	conn, err := ws.Dial(sctx, w.wsOptions(), w.resume)
	if err != nil {
		return err
	}
	defer func() {
		w.resume = conn.Resume()
		conn.Close()
		if w.Status() == StatusLive {
			w.lastLive.Store(w.cfg.Now().UnixMilli())
		}
	}()
	if !w.booted || w.resume.ConnectionID == "" {
		if err := w.bootstrap(sctx, w.booted); err != nil {
			return err
		}
	}
	w.setStatus(StatusLive)
	w.lastLive.Store(w.cfg.Now().UnixMilli())
	onLive()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.nudge:
			return errNudged
		case <-w.authFail:
			return &rest.Error{Kind: rest.KindAuth, Status: http.StatusUnauthorized, Err: errors.New("request rejected with 401")}
		case ev, ok := <-conn.Events():
			if !ok {
				return conn.Err()
			}
			if ev.Type == "hello" {
				if ev.Reset {
					slog.Info("event stream lost, resyncing", "srv", w.srv.ID)
					if err := w.bootstrap(sctx, true); err != nil {
						return err
					}
				}
				continue
			}
			w.apply(ev)
			if len(conn.Events()) == 0 {
				w.st.ClearGuard()
			}
		}
	}
}

func (w *Worker) bootstrap(ctx context.Context, lost bool) error {
	b, err := w.fetchMeta(ctx)
	if err != nil {
		return err
	}
	w.st.Bootstrap(b)
	if lost {
		w.st.MarkStale(w.lastLive.Load())
	}
	w.booted = true
	w.loadUsers(ctx)
	w.enqueueAll()
	w.changed(state.Change{Sidebar: true, Badge: true, Channels: []string{w.st.Active()}})
	return nil
}

func (w *Worker) fetchMeta(ctx context.Context) (state.Bootstrap, error) {
	var b state.Bootstrap
	cfg, err := w.rc.ClientConfig(ctx)
	if err != nil {
		return b, err
	}
	b.Config = state.Config{CollapsedThreads: cfg.CollapsedThreads, TeammateNameDisplay: cfg.TeammateNameDisplay,
		LockTeammateNameDisplay: cfg.LockTeammateNameDisplay == "true"}
	if b.Me, err = w.rc.Me(ctx); err != nil {
		return b, err
	}
	if b.Prefs, err = w.rc.MyPreferences(ctx); err != nil {
		return b, err
	}
	if b.Teams, err = w.rc.MyTeams(ctx); err != nil {
		return b, err
	}
	if b.Channels, err = w.rc.MyChannels(ctx); err != nil {
		return b, err
	}
	if b.Members, err = w.rc.MyChannelMembers(ctx); err != nil {
		return b, err
	}
	if b.Status, err = w.rc.MyStatus(ctx); err != nil {
		slog.Debug("status unavailable", "srv", w.srv.ID, "err", err)
		b.Status.Status = "online"
	}
	b.Categories = map[string]model.OrderedCategories{}
	for _, t := range b.Teams {
		cats, err := w.rc.Categories(ctx, t.ID)
		if err != nil {
			if sessionExpired(err) {
				return b, err
			}
			slog.Warn("sidebar categories unavailable", "srv", w.srv.ID, "team", t.ID, "err", err)
			continue
		}
		b.Categories[t.ID] = cats
	}
	return b, nil
}

func (w *Worker) apply(ev ws.Event) {
	eff := w.st.ApplyEvent(ev)
	w.changed(eff.Change)
	if eff.NeedMeta {
		w.requestMeta()
	}
	if len(eff.NeedUsers) > 0 {
		w.goBG(w.loadUsers)
	}
	if eff.ShowDM != nil {
		pref := *eff.ShowDM
		w.goBG(func(ctx context.Context) {
			if err := w.rc.SavePreferences(ctx, []model.Preference{pref}); err != nil {
				slog.Warn("could not save DM visibility", "srv", w.srv.ID, "err", err)
			}
		})
	}
	if eff.View != "" {
		w.view(eff.View)
	}
	if eff.Notify != nil && w.cfg.Hooks.Notify != nil {
		w.cfg.Hooks.Notify(w.srv.ID, *eff.Notify)
	}
	if eff.Resync {
		w.st.ResetWindows()
		w.enqueueAll()
		w.changed(state.Change{Sidebar: true, Channels: []string{w.st.Active()}})
	}
}

func (w *Worker) requestMeta() {
	select {
	case w.metaReq <- struct{}{}:
	default:
	}
}

// metaLoop refreshes channels/memberships/categories after events that
// change them (joined a channel, new DM, categories edited elsewhere),
// debounced so a burst costs one refresh.
func (w *Worker) metaLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.metaReq:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(metaDebounce):
		}
		select {
		case <-w.metaReq:
		default:
		}
		b, err := w.fetchMeta(ctx)
		if err != nil {
			if sessionExpired(err) {
				w.signalAuth()
			}
			slog.Warn("metadata refresh failed", "srv", w.srv.ID, "err", err)
			continue
		}
		w.st.Bootstrap(b)
		w.loadUsers(ctx)
		w.enqueueAll()
		w.changed(state.Change{Sidebar: true, Badge: true})
	}
}

func (w *Worker) loadUsers(ctx context.Context) {
	w.usersMu.Lock()
	defer w.usersMu.Unlock()
	ids := w.st.MissingUserIDs()
	if len(ids) == 0 {
		return
	}
	users, err := w.rc.UsersByIDs(ctx, ids)
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Warn("user profiles unavailable", "srv", w.srv.ID, "err", err)
		return
	}
	w.st.SetUsers(users)
	w.changed(state.Change{Sidebar: true, Channels: []string{w.st.Active()}})
}

func (w *Worker) restore(ctx context.Context) {
	entries, err := w.cfg.Store.LoadCache(ctx, w.srv.ID)
	if err != nil {
		slog.Warn("cache unreadable", "srv", w.srv.ID, "err", err)
		return
	}
	switch err := w.st.Restore(entries); {
	case err == nil:
		w.changed(state.Change{Sidebar: true, Badge: true})
	case errors.Is(err, state.ErrNoSnapshot):
	default:
		slog.Warn("discarding incompatible cache snapshot", "srv", w.srv.ID, "err", err)
		if err := w.cfg.Store.ClearCache(ctx, w.srv.ID); err != nil {
			slog.Warn("cache clear failed", "srv", w.srv.ID, "err", err)
		}
	}
}

func (w *Worker) flushLoop(ctx context.Context) {
	t := time.NewTicker(w.cfg.FlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), finalFlushTime)
			w.flush(fctx)
			cancel()
			return
		case <-t.C:
			if w.Status() == StatusLive {
				now := w.cfg.Now().UnixMilli()
				w.lastLive.Store(now)
				w.st.SetLiveAt(now)
			}
			w.flush(ctx)
		}
	}
}

func (w *Worker) flush(ctx context.Context) {
	put, del := w.st.TakeSnapshot()
	if err := w.cfg.Store.SaveCache(ctx, w.srv.ID, put, del); err != nil {
		slog.Warn("cache flush failed", "srv", w.srv.ID, "err", err)
	}
}

func (w *Worker) wakeLoop(ctx context.Context) {
	watchWake(ctx, w.cfg.WakeCheck, func() {
		slog.Info("system woke up, reconnecting", "srv", w.srv.ID)
		w.Nudge()
	})
}
```
(Импорт `github.com/spk/spk-mattermost/internal/mm/model` нужен для `model.OrderedCategories`/`model.Preference`.)

`internal/mmsync/fetch.go`:
```go
package mmsync

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/state"
)

// fetchQueue holds channels whose window must be loaded or caught up,
// popped by priority (then FIFO); pushing a queued channel again can only
// raise its priority.
type fetchQueue struct {
	mu    sync.Mutex
	items map[string]state.SyncItem
	order map[string]int64
	n     int64
	wake  chan struct{}
}

func newFetchQueue() *fetchQueue {
	return &fetchQueue{items: map[string]state.SyncItem{}, order: map[string]int64{}, wake: make(chan struct{}, 1)}
}

func (q *fetchQueue) push(it state.SyncItem) {
	q.mu.Lock()
	if cur, ok := q.items[it.ChannelID]; !ok || it.Priority < cur.Priority {
		if !ok {
			q.n++
			q.order[it.ChannelID] = q.n
		}
		q.items[it.ChannelID] = it
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *fetchQueue) pop(ctx context.Context) (state.SyncItem, bool) {
	for {
		q.mu.Lock()
		var best state.SyncItem
		found := false
		for id, it := range q.items {
			if !found || it.Priority < best.Priority || (it.Priority == best.Priority && q.order[id] < q.order[best.ChannelID]) {
				best, found = it, true
			}
		}
		if found {
			delete(q.items, best.ChannelID)
			delete(q.order, best.ChannelID)
		}
		more := len(q.items) > 0
		q.mu.Unlock()
		if found {
			if more { // let the next fetcher go
				select {
				case q.wake <- struct{}{}:
				default:
				}
			}
			return best, true
		}
		select {
		case <-ctx.Done():
			return state.SyncItem{}, false
		case <-q.wake:
		}
	}
}

func (w *Worker) enqueueAll() {
	for _, it := range w.st.SyncItems() {
		w.queue.push(it)
	}
}

func (w *Worker) fetchLoop(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < w.cfg.Fetchers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				it, ok := w.queue.pop(ctx)
				if !ok {
					return
				}
				w.fetch(ctx, it)
			}
		}()
	}
	wg.Wait()
}

func (w *Worker) fetch(ctx context.Context, queued state.SyncItem) {
	it, need := w.st.SyncItemFor(queued.ChannelID) // the queued copy may be outdated
	if !need {
		return
	}
	crt := w.st.CRT()
	started := w.cfg.Now().UnixMilli()
	var err error
	if it.Loaded {
		var l model.PostList
		l, err = w.rc.ChannelPosts(ctx, it.ChannelID, rest.PostsQuery{Since: max(1, it.SyncedAt-sinceMargin.Milliseconds()), CollapsedThreads: crt})
		switch {
		case err != nil:
		case len(l.Order) >= rest.SinceLimit:
			err = w.loadLatest(ctx, it.ChannelID, crt, started)
		default:
			w.st.MergeSince(it.ChannelID, l.Ascending(), started)
		}
	} else {
		err = w.loadLatest(ctx, it.ChannelID, crt, started)
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if sessionExpired(err) {
			w.signalAuth()
			return
		}
		if rest.IsNetwork(err) {
			time.AfterFunc(retryFetchIn, func() {
				if ctx.Err() == nil {
					w.queue.push(it)
				}
			})
		}
		slog.Debug("channel fetch failed", "srv", w.srv.ID, "channel", it.ChannelID, "err", err)
		return
	}
	w.loadUsers(ctx)
	w.changed(state.Change{Sidebar: true, Channels: []string{it.ChannelID}})
}

func (w *Worker) loadLatest(ctx context.Context, channelID string, crt bool, started int64) error {
	l, err := w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: state.WindowSize, CollapsedThreads: crt})
	if err != nil {
		return err
	}
	w.st.SetWindow(channelID, l.Ascending(), l.PrevPostID == "", started)
	return nil
}
```
(Импорт `model` для `model.PostList`.)

`internal/mmsync/actions.go`:
```go
package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/state"
)

var ErrEmptyMessage = errors.New("mmsync: empty message")

// OpenChannel makes the channel current: moves it to the front of the fetch
// queue and marks it read if the window is focused.
func (w *Worker) OpenChannel(channelID string) (state.ChannelView, bool) {
	w.st.SetActive(channelID)
	if it, ok := w.st.SyncItemFor(channelID); ok {
		w.queue.push(it)
	}
	if w.st.Focused() {
		w.view(channelID)
	}
	w.changed(state.Change{Sidebar: true})
	return w.st.ChannelView(channelID)
}

func (w *Worker) SetFocused(f bool) {
	w.st.SetFocused(f)
	if a := w.st.Active(); f && a != "" {
		w.view(a)
	}
}

// view marks a channel read on the server, at most one request per channel
// at a time; it re-checks afterwards in case posts arrived meanwhile.
func (w *Worker) view(channelID string) {
	if !w.st.NeedsView(channelID) {
		return
	}
	if _, busy := w.viewing.LoadOrStore(channelID, true); busy {
		return
	}
	w.goBG(func(ctx context.Context) {
		defer w.viewing.Delete(channelID)
		for i := 0; i < 3 && w.st.NeedsView(channelID) && w.st.Active() == channelID && w.st.Focused(); i++ {
			if err := w.rc.ViewChannel(ctx, channelID); err != nil {
				if sessionExpired(err) {
					w.signalAuth()
				}
				slog.Warn("mark read failed", "srv", w.srv.ID, "channel", channelID, "err", err)
				return
			}
			w.changed(w.st.ViewedLocally(channelID, w.cfg.Now().UnixMilli()))
		}
	})
}

func (w *Worker) LoadOlder(ctx context.Context, channelID string) error {
	before := w.st.OldestPostID(channelID)
	if before == "" {
		return nil
	}
	l, err := w.rc.ChannelPosts(ctx, channelID, rest.PostsQuery{PerPage: state.WindowSize, Before: before, CollapsedThreads: w.st.CRT()})
	if err != nil {
		return w.actionErr(err)
	}
	w.st.AppendOlder(channelID, l.Ascending(), l.PrevPostID == "")
	w.loadUsers(ctx)
	w.changed(state.Change{Channels: []string{channelID}})
	return nil
}

func (w *Worker) Send(channelID, message string) error {
	if strings.TrimSpace(message) == "" {
		return ErrEmptyMessage
	}
	p := w.st.AddPending(channelID, "", message)
	w.changed(state.Change{Channels: []string{channelID}})
	w.goBG(func(ctx context.Context) { w.create(ctx, p) })
	return nil
}

func (w *Worker) create(ctx context.Context, p state.Pending) {
	cctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	post, err := w.rc.CreatePost(cctx, model.Post{ChannelID: p.ChannelID, RootID: p.RootID, Message: p.Message,
		PendingPostID: p.ID, UserID: w.st.Me().ID})
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Warn("send failed", "srv", w.srv.ID, "channel", p.ChannelID, "err", err)
		w.changed(w.st.FailPending(p.ChannelID, p.ID))
		return
	}
	w.changed(w.st.PostCreated(post))
}

// Retry resends a failed post with the same pending id: if the first
// attempt did reach the server, it returns the existing post (no duplicate).
func (w *Worker) Retry(channelID, pendingID string) {
	p, ok := w.st.RetryPending(channelID, pendingID)
	if !ok {
		return
	}
	w.changed(state.Change{Channels: []string{channelID}})
	w.goBG(func(ctx context.Context) { w.create(ctx, p) })
}

func (w *Worker) Discard(channelID, pendingID string) {
	w.changed(w.st.DropPending(channelID, pendingID))
}

func (w *Worker) Edit(ctx context.Context, postID, message string) error {
	if strings.TrimSpace(message) == "" {
		return ErrEmptyMessage
	}
	p, err := w.rc.PatchPost(ctx, postID, message)
	if err != nil {
		return w.actionErr(err)
	}
	w.changed(w.st.ApplyPostUpdate(p))
	return nil
}

func (w *Worker) Delete(ctx context.Context, postID string) error {
	if err := w.rc.DeletePost(ctx, postID); err != nil {
		return w.actionErr(err)
	}
	w.changed(w.st.RemovePost(postID))
	return nil
}

func (w *Worker) MarkUnread(ctx context.Context, postID string) error {
	u, err := w.rc.SetUnread(ctx, postID)
	if err != nil {
		return w.actionErr(err)
	}
	w.changed(w.st.SetUnread(u))
	return nil
}

func (w *Worker) SaveDraft(channelID, text string) { w.st.SetDraft(channelID, text) }

func (w *Worker) actionErr(err error) error {
	if sessionExpired(err) {
		w.signalAuth()
	}
	return err
}
```

`internal/mmsync/wake.go`:
```go
package mmsync

import (
	"context"
	"time"
)

// slept reports a suspend between two ticks: the wall clock kept running
// while the monotonic clock (which stops during suspend on Linux and
// Windows) did not.
func slept(wallDelta, monoDelta, threshold time.Duration) bool {
	return wallDelta-monoDelta > threshold
}

func watchWake(ctx context.Context, every time.Duration, fire func()) {
	t := time.NewTicker(every)
	defer t.Stop()
	prev := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			if slept(now.Round(0).Sub(prev.Round(0)), now.Sub(prev), 2*every+5*time.Second) {
				fire()
			}
			prev = now
		}
	}
}
```

`internal/mmsync/manager.go`:
```go
package mmsync

import (
	"context"
	"sync"
)

type Manager struct {
	cfg  Config
	root context.Context
	mu   sync.Mutex
	ws   map[int64]*entry
}

type entry struct {
	w      *Worker
	cancel context.CancelFunc
	done   chan struct{}
}

func NewManager(root context.Context, cfg Config) *Manager {
	cfg.defaults()
	return &Manager{cfg: cfg, root: root, ws: map[int64]*entry{}}
}

func (m *Manager) StartAll(ctx context.Context) error {
	list, err := m.cfg.Store.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, srv := range list {
		if srv.SignedIn() {
			m.Start(srv)
		}
	}
	return nil
}

// Start (re)starts the worker of srv — after sign-in, with the new token.
func (m *Manager) Start(srv store.Server) {
	m.Stop(srv.ID)
	w := NewWorker(m.cfg, srv)
	ctx, cancel := context.WithCancel(m.root)
	e := &entry{w: w, cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.ws[srv.ID] = e
	m.mu.Unlock()
	go func() {
		defer close(e.done)
		w.Run(ctx)
	}()
}

// Stop cancels the worker and waits for its final snapshot flush.
func (m *Manager) Stop(id int64) {
	m.mu.Lock()
	e := m.ws[id]
	delete(m.ws, id)
	m.mu.Unlock()
	if e != nil {
		e.cancel()
		<-e.done
	}
}

func (m *Manager) Worker(id int64) *Worker {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.ws[id]; e != nil {
		return e.w
	}
	return nil
}

// Each calls fn for every running worker; fn runs without the manager lock
// (it may call Start/Stop).
func (m *Manager) Each(fn func(id int64, w *Worker)) {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.ws))
	ws := make([]*Worker, 0, len(m.ws))
	for id, e := range m.ws {
		ids, ws = append(ids, id), append(ws, e.w)
	}
	m.mu.Unlock()
	for i, w := range ws {
		fn(ids[i], w)
	}
}

func (m *Manager) NudgeAll() { m.Each(func(_ int64, w *Worker) { w.Nudge() }) }

func (m *Manager) Close() {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.ws))
	for id := range m.ws {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}
```
(Импорт `store` в manager.go.)

- [ ] **Step 5: Тесты проходят (и под -race стабильно)**

Run: `go test -race -count=3 ./internal/mmsync/ ./internal/state/ ./internal/mmfake/`
Expected: PASS все три прогона.

- [ ] **Step 6: Коммит**

```bash
git add internal/mmsync internal/state internal/mmfake
git commit -m "mmsync: per-server sync worker — session FSM, WS resume/resync, catch-up queue, write-behind, actions"
```

---

### Task 11: Сервис API чата, схлопывание событий, уведомления, транспорты

**Files:**
- Create: `internal/events/coalesce.go`, `internal/events/coalesce_test.go`
- Create: `internal/api/sync.go` (запуск/остановка воркеров, хуки, фокус, бейджи), `internal/api/chat.go` (методы чата), `internal/api/notifications.go` (решение, склейка, `Notifier`), `internal/api/locale.go`
- Create: `internal/api/chat_test.go`, `internal/api/notifications_test.go`, `internal/api/locale_test.go`
- Modify: `internal/api/api.go` (DTO, интерфейс, события, коды), `internal/api/service.go` (поля, `dto`, вход/выход/удаление запускают и останавливают воркеры)
- Modify: `internal/state/view.go` (+ `ChannelView.TeamID`)
- Modify: `internal/api/transport/http.go`, `internal/api/transport/wails.go`, `internal/api/transport/http_test.go`
- Modify: `cmd/spk-mattermost/main.go` (`--mm-fake-channels`), `cmd/spk-mattermost/browser.go` (Start/Close, записывающий notifier, test-API фейка), `cmd/spk-mattermost/browser_test.go`, `cmd/spk-mattermost/run_desktop_wails.go` (Start/Close)

**Interfaces:**
- Consumes: Task 3 (`mmfake.Options.ExtraChannels`, `PostAs`, `DropConnections`, `RevokeAll`), Task 6/7 (`state.SidebarView`, `state.ChannelView`, `state.Badge`, `state.Change`, `state.NotifyCandidate`), Task 9 (`notify.Input`, `notify.Decide`), Task 10 (`mmsync.Manager`, `Worker`, `Hooks`, `Status*`, `ErrEmptyMessage`).
- Produces (пакет `events`): `NewCoalescer(delay time.Duration) *Coalescer`; `(*Coalescer).Schedule(key string, fn func())` — первый вызов по ключу запускает таймер `delay`, повторные до срабатывания только подменяют `fn` (выполнится последняя); все `fn` выполняются по одной, не параллельно; `(*Coalescer).Close()` — отменяет ожидающие и ждёт выполняющуюся.
- Produces (пакет `api`):
  - `ServerDTO` + `State string` (`off|connecting|live|reconnecting|needs_reauth`), `Unread bool`, `Mentions int` (json `state`, `unread`, `mentions`).
  - `type SidebarDTO = state.SidebarView`; `type ChannelDTO = state.ChannelView`; `type Badge = state.Badge`; `type AppInfo struct{ FormatLocale string }` (json `format_locale`).
  - Методы `API` (кроме этапа 1): `AppInfo(ctx) (AppInfo, error)`; `SelectServer(ctx, id int64) error` (0 — ни один); `SetFocused(ctx, focused bool) error`; `NetworkChanged(ctx) error`; `OpenURL(ctx, url string) error`; `Sidebar(ctx, id int64, teamID string) (SidebarDTO, error)`; `OpenChannel(ctx, id int64, channelID string) (ChannelDTO, error)`; `GetChannel(ctx, id int64, channelID string) (ChannelDTO, error)`; `LoadOlder(ctx, id int64, channelID string) error`; `SendPost(ctx, id int64, channelID, message string) error`; `RetryPost(ctx, id int64, channelID, pendingID string) error`; `DiscardPost(ctx, id int64, channelID, pendingID string) error`; `EditPost(ctx, id int64, postID, message string) error`; `DeletePost(ctx, id int64, postID string) error`; `MarkUnread(ctx, id int64, postID string) error`; `SaveDraft(ctx, id int64, channelID, text string) error`.
  - События: `EventSidebarChanged = "sidebar_changed"` (`server_id`), `EventChannelChanged = "channel_changed"` (`server_id`, `channel_id`), `EventOpenChannel = "open_channel"` (`server_id`, `channel_id`); `servers_changed` теперь приходит и при смене состояния подключения или бейджа сервера.
  - Коды: `CodeNotSignedIn = "not_signed_in"`, `CodeSessionExpired = "session_expired"`, `CodeNoChannel = "no_channel"`, `CodeEmptyMessage = "empty_message"`, `CodeForbidden = "forbidden"`.
  - Жизненный цикл: `(*Service).Start(ctx) error` (читает только БД), `Close()`, `OnBadge(fn func(Badge))`, `SetNotifier(Notifier)`, `NotificationClicked(serverID int64, channelID string)`.
  - Уведомления: `type Notification struct{ ID, Title, Body string; ServerID int64; ChannelID string }` (json snake_case); `type Notifier interface{ Notify(Notification) }`; `type RecordingNotifier` (`Notify`, `List() []Notification`) — для браузерного режима и тестов.
  - `state.ChannelView.TeamID string` (json `team_id`, пусто у DM/GM).
- Produces (транспорт): HTTP `POST /api/<Метод>` для каждого метода, тела — `{id, team_id, channel_id, post_id, pending_id, message, text, focused, url}` по надобности; Wails — методы `transport.API` с теми же именами и позиционными аргументами в том же порядке.
- Produces (browser test-API, только с `--test-api`): `GET /api/_test/notifications` → `[]Notification`; `POST /api/_test/notification-click {server_id, channel_id}`; `POST /api/_test/fake/post {channel_id, username, message}` → `{id}`; `POST /api/_test/fake/drop {lose}`; `POST /api/_test/fake/revoke` (отзыв всех сессий + обрыв с потерей). Флаг `--mm-fake-channels N` — `mmfake.Options.ExtraChannels`.

Поведение:
- **Фокус только у активного сервера.** Сервис помнит активный сервер (`SelectServer`, `OpenChannel`) и фокус окна; воркеру активного сервера — `SetFocused(focused)`, всем остальным — `SetFocused(false)`. Иначе канал, открытый когда-то на фоновом сервере, отмечался бы прочитанным и глушил бы уведомления.
- **Схлопывание.** Хук `Changed` планирует `sidebar_changed` по ключу `sidebar/<id>`, `channel_changed` — `channel/<id>/<канал>`, пересчёт бейджей — `badge`; задержка 100 мс. Пересчёт бейджей сравнивает (состояние, непрочитанное, упоминания) каждого сервера с прошлым и шлёт `servers_changed` только при отличии; сумму по серверам отдаёт в `OnBadge` только при её изменении.
- **Уведомления.** `notify.Decide` по `NotifyCandidate`; «да» → очередь. Первое сообщение канала показывается сразу; следующие в течение 10 с копятся и по истечении окна показываются одним уведомлением «последнее (+N)», где N — сколько ещё сообщений не показано. ID уведомления `mm-<server>-<channel>` (на Linux Wails заменяет уведомление с тем же ID — `replaces_id`). Заголовок — имя канала (у DM это собеседник), тело — `отправитель: текст` (у DM — только текст), текст без лишних пробелов, ≤ 200 рун; пустой текст — из вложения (fallback/pretext/title/text) или `📎 имя файла`. Отправка в `Notifier` — в отдельной горутине: хуки воркера не блокируются.
- **Вход.** Успешный вход (пароль или GitLab) останавливает воркер сервера, чистит кэш, если сменился пользователь, и запускает воркер с новым токеном. «Выйти» — остановить воркер (финальный сброс), отозвать сессию, стереть токен и кэш. «Удалить сервер» — остановить воркер до удаления (кэш удалится каскадно).
- **Ошибки действий:** `ErrEmptyMessage` → `empty_message`; HTTP 401 → `session_expired`; 403 → `forbidden`; сеть → `unreachable`; прочее → `internal`. Сервер без входа → `not_signed_in`; неизвестный канал → `no_channel`; действия записи при `needs_reauth` → `session_expired` сразу, без запроса. Чтение (`Sidebar`, `GetChannel`, `OpenChannel`) при `needs_reauth` работает из кэша.
- `OpenURL` пропускает только `http`/`https` с хостом и `mailto`; остальное — `invalid_url`.
- `AppInfo.FormatLocale` — из `LC_ALL`, `LC_TIME`, `LANG` (первое непустое): `ru_RU.UTF-8` → `ru-RU`; `C`/`POSIX`/пусто → `""` (UI берёт `navigator.language`).

- [ ] **Step 1: Падающие тесты схлопывания**

`internal/events/coalesce_test.go`:
```go
package events

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	mu  sync.Mutex
	got []string
}

func (r *recorder) add(s string) func() {
	return func() {
		r.mu.Lock()
		r.got = append(r.got, s)
		r.mu.Unlock()
	}
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func TestCoalescerBurstRunsOnceWithLatest(t *testing.T) {
	c := NewCoalescer(30 * time.Millisecond)
	defer c.Close()
	var r recorder
	for _, s := range []string{"a", "b", "c"} {
		c.Schedule("k", r.add(s))
	}
	require.Eventually(t, func() bool { return len(r.list()) == 1 }, time.Second, 5*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, []string{"c"}, r.list())
}

func TestCoalescerKeysAreIndependentAndRearm(t *testing.T) {
	c := NewCoalescer(20 * time.Millisecond)
	defer c.Close()
	var r recorder
	c.Schedule("x", r.add("x1"))
	c.Schedule("y", r.add("y1"))
	require.Eventually(t, func() bool { return len(r.list()) == 2 }, time.Second, 5*time.Millisecond)
	c.Schedule("x", r.add("x2"))
	require.Eventually(t, func() bool { return len(r.list()) == 3 }, time.Second, 5*time.Millisecond)
	assert.ElementsMatch(t, []string{"x1", "y1", "x2"}, r.list())
}

func TestCoalescerRunsSerially(t *testing.T) {
	c := NewCoalescer(time.Millisecond)
	defer c.Close()
	var mu sync.Mutex
	running, maxRunning, done := 0, 0, 0
	for i := 0; i < 20; i++ {
		c.Schedule(string(rune('a'+i)), func() {
			mu.Lock()
			running++
			maxRunning = max(maxRunning, running)
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			running--
			done++
			mu.Unlock()
		})
	}
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return done == 20 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, maxRunning)
}

func TestCoalescerCloseDropsPending(t *testing.T) {
	c := NewCoalescer(30 * time.Millisecond)
	var r recorder
	c.Schedule("k", r.add("late"))
	c.Close()
	c.Schedule("k", r.add("after close"))
	time.Sleep(80 * time.Millisecond)
	assert.Empty(t, r.list())
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/events/`
Expected: FAIL (`undefined: NewCoalescer`).

- [ ] **Step 3: Реализация схлопывания**

`internal/events/coalesce.go`:
```go
package events

import (
	"sync"
	"time"
)

// Coalescer turns bursts of "something changed" into one call per key: the
// first Schedule for a key arms a timer, later ones before it fires only
// replace the function (the latest wins). Latency is bounded by the delay —
// a steady stream of changes still produces a call every delay. Functions
// run one at a time.
type Coalescer struct {
	delay  time.Duration
	mu     sync.Mutex
	run    sync.Mutex
	jobs   map[string]*job
	closed bool
}

type job struct {
	fn func()
	t  *time.Timer
}

func NewCoalescer(delay time.Duration) *Coalescer {
	return &Coalescer{delay: delay, jobs: map[string]*job{}}
}

func (c *Coalescer) Schedule(key string, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if j, ok := c.jobs[key]; ok {
		j.fn = fn
		return
	}
	j := &job{fn: fn}
	c.jobs[key] = j
	j.t = time.AfterFunc(c.delay, func() { c.fire(key, j) })
}

func (c *Coalescer) fire(key string, j *job) {
	c.run.Lock()
	defer c.run.Unlock()
	c.mu.Lock()
	if c.closed || c.jobs[key] != j {
		c.mu.Unlock()
		return
	}
	delete(c.jobs, key) // a Schedule during fn arms a new job: no update is lost
	fn := j.fn
	c.mu.Unlock()
	fn()
}

// Close drops pending calls and waits for a running one to finish.
func (c *Coalescer) Close() {
	c.mu.Lock()
	c.closed = true
	for _, j := range c.jobs {
		j.t.Stop()
	}
	c.jobs = map[string]*job{}
	c.mu.Unlock()
	c.run.Lock() // waits for a running fn; fires after this see closed and return
	defer c.run.Unlock()
}
```

Run: `go test -race ./internal/events/`
Expected: PASS.

- [ ] **Step 4: Падающие тесты сервиса**

`internal/api/locale_test.go`:
```go
package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatLocale(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	assert.Equal(t, "ru-RU", formatLocale(env(map[string]string{"LANG": "en_US.UTF-8", "LC_TIME": "ru_RU.UTF-8"})))
	assert.Equal(t, "de-DE", formatLocale(env(map[string]string{"LC_ALL": "de_DE.UTF-8@euro", "LC_TIME": "ru_RU.UTF-8"})))
	assert.Equal(t, "en-US", formatLocale(env(map[string]string{"LANG": "en_US"})))
	assert.Equal(t, "", formatLocale(env(map[string]string{"LANG": "C.UTF-8"})))
	assert.Equal(t, "", formatLocale(env(map[string]string{"LC_ALL": "POSIX"})))
	assert.Equal(t, "", formatLocale(env(nil)))
}
```

`internal/api/notifications_test.go`:
```go
package api

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/state"
)

func candidate(chType, chName, sender, msg string) state.NotifyCandidate {
	return state.NotifyCandidate{
		Post:        model.Post{ID: "p1", ChannelID: "c1", UserID: "u-bob", Message: msg},
		Channel:     model.Channel{ID: "c1", Type: chType},
		ChannelName: chName, SenderName: sender,
	}
}

func TestNotificationText(t *testing.T) {
	n := notificationFor(7, candidate(model.ChannelOpen, "Off-Topic", "bob", "hi  @alice\n\nsecond line"))
	assert.Equal(t, Notification{ID: "mm-7-c1", Title: "Off-Topic", Body: "bob: hi @alice second line", ServerID: 7, ChannelID: "c1"}, n)

	dm := notificationFor(7, candidate(model.ChannelDirect, "bob", "bob", "ping"))
	assert.Equal(t, "bob", dm.Title)
	assert.Equal(t, "ping", dm.Body, "a DM body has no sender prefix")

	long := notificationFor(1, candidate(model.ChannelOpen, "C", "bob", strings.Repeat("я", 300)))
	assert.Equal(t, 200, len([]rune(long.Body)))
	assert.True(t, strings.HasSuffix(long.Body, "…"))

	att := candidate(model.ChannelOpen, "C", "ci", "")
	att.Post.Props.Attachments = []model.Attachment{{Fallback: "Build #42 failed"}}
	assert.Equal(t, "ci: Build #42 failed", notificationFor(1, att).Body)

	file := candidate(model.ChannelOpen, "C", "bob", "")
	file.Post.Metadata = &model.PostMetadata{Files: []model.FileInfo{{Name: "report.pdf"}}}
	assert.Equal(t, "bob: 📎 report.pdf", notificationFor(1, file).Body)

	hook := candidate(model.ChannelOpen, "C", "webhook-owner", "deployed")
	hook.Post.Props.FromWebhook, hook.Post.Props.OverrideUsername = true, "Deploy Bot"
	assert.Equal(t, "Deploy Bot: deployed", notificationFor(1, hook).Body)
}

type collect struct {
	mu  sync.Mutex
	got []Notification
}

func (c *collect) add(n Notification) { c.mu.Lock(); c.got = append(c.got, n); c.mu.Unlock() }
func (c *collect) list() []Notification {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Notification(nil), c.got...)
}

func TestNotifyQueueBurstsPerChannel(t *testing.T) {
	var c collect
	q := newNotifyQueue(80*time.Millisecond, c.add)
	defer q.close()
	q.push(Notification{ID: "a", Body: "a1"})
	require.Eventually(t, func() bool { return len(c.list()) == 1 }, time.Second, 5*time.Millisecond, "first one is immediate")
	q.push(Notification{ID: "a", Body: "a2"})
	q.push(Notification{ID: "a", Body: "a3"})
	q.push(Notification{ID: "a", Body: "a4"})
	q.push(Notification{ID: "b", Body: "b1"})
	require.Eventually(t, func() bool { return len(c.list()) == 3 }, time.Second, 5*time.Millisecond)
	got := c.list() // a1 and b1 are sent from goroutines: their order is not fixed
	assert.ElementsMatch(t, []string{"a1", "b1"}, []string{got[0].Body, got[1].Body})
	assert.Equal(t, "a4 (+2)", got[2].Body)

	time.Sleep(100 * time.Millisecond)
	q.push(Notification{ID: "b", Body: "b2"}) // b's window is over: immediate again
	require.Eventually(t, func() bool { return len(c.list()) == 4 }, time.Second, 5*time.Millisecond)
	assert.Equal(t, "b2", c.list()[3].Body)
}

func TestNotifyQueueSingleFollowerHasNoCounter(t *testing.T) {
	var c collect
	q := newNotifyQueue(30*time.Millisecond, c.add)
	defer q.close()
	q.push(Notification{ID: "a", Body: "one"})
	q.push(Notification{ID: "a", Body: "two"})
	require.Eventually(t, func() bool { return len(c.list()) == 2 }, time.Second, 5*time.Millisecond)
	assert.Equal(t, "two", c.list()[1].Body)
}
```

`internal/api/chat_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/mmsync"
	"github.com/spk/spk-mattermost/internal/store"
)

type chatFixture struct {
	t     *testing.T
	svc   *Service
	st    *store.Store
	evs   <-chan events.Event
	notes *RecordingNotifier
	badge chan Badge
}

func newChatFixture(t *testing.T) *chatFixture {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	em := events.NewEmitter()
	ch, unsub := em.Subscribe()
	t.Cleanup(unsub)
	f := &chatFixture{t: t, st: st, evs: ch, notes: &RecordingNotifier{}, badge: make(chan Badge, 64)}
	f.svc = NewService(st, em, func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
	f.svc.tune = func(c *mmsync.Config) {
		c.FlushEvery, c.MinBackoff, c.MaxBackoff, c.WakeCheck = 50*time.Millisecond, 20*time.Millisecond, 200*time.Millisecond, time.Hour
	}
	f.svc.nq.window = 150 * time.Millisecond
	f.svc.SetNotifier(f.notes)
	f.svc.OnBadge(func(b Badge) { f.badge <- b })
	require.NoError(t, f.svc.Start(context.Background()))
	t.Cleanup(f.svc.Close)
	return f
}

// signIn adds a fake server and signs alice in; returns the server id.
func (f *chatFixture) signIn(fake *mmfake.Server, user string) int64 {
	f.t.Helper()
	ctx := context.Background()
	srv, err := f.svc.AddServer(ctx, fake.URL())
	require.NoError(f.t, err)
	_, err = f.svc.LoginWithPassword(ctx, srv.ID, user, "secret")
	require.NoError(f.t, err)
	return srv.ID
}

func (f *chatFixture) server(id int64) ServerDTO {
	list, err := f.svc.ListServers(context.Background())
	require.NoError(f.t, err)
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	f.t.Fatalf("server %d not listed", id)
	return ServerDTO{}
}

func (f *chatFixture) eventually(cond func() bool, msg string) {
	f.t.Helper()
	require.Eventually(f.t, cond, 10*time.Second, 20*time.Millisecond, msg)
}

func (f *chatFixture) loaded(id int64, channelID string) bool {
	v, err := f.svc.GetChannel(context.Background(), id, channelID)
	return err == nil && v.Loaded && !v.Syncing
}

func (f *chatFixture) has(id int64, channelID, msg string) bool {
	v, err := f.svc.GetChannel(context.Background(), id, channelID)
	if err != nil {
		return false
	}
	for _, p := range v.Posts {
		if p.Message == msg && !p.Pending {
			return true
		}
	}
	return false
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func startFake(t *testing.T) *mmfake.Server {
	fake := mmfake.Start(mmfake.Options{})
	t.Cleanup(fake.Close)
	return fake
}

func TestChatThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "server never went live")
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "town square not prefetched")

	sb, err := f.svc.Sidebar(ctx, id, "")
	require.NoError(t, err)
	assert.Equal(t, "t-fake", sb.TeamID)
	assert.Equal(t, "c-town", sb.SelectedChannelID)

	v, err := f.svc.OpenChannel(ctx, id, "c-town")
	require.NoError(t, err)
	assert.Len(t, v.Posts, 60)
	assert.Equal(t, "t-fake", v.TeamID)
	assert.Equal(t, "fake", v.TeamName)

	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "hello from service"))
	f.eventually(func() bool { return f.has(id, "c-offtopic", "hello from service") }, "sent post not visible")

	assert.Equal(t, CodeEmptyMessage, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", "  \n ")))
	_, err = f.svc.OpenChannel(ctx, id, "c-nope")
	assert.Equal(t, CodeNoChannel, codeOf(err))
}

func TestSignedOutServerHasNoChat(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	srv, err := f.svc.AddServer(context.Background(), fake.URL())
	require.NoError(t, err)
	assert.Equal(t, "off", f.server(srv.ID).State)
	_, err = f.svc.Sidebar(context.Background(), srv.ID, "")
	assert.Equal(t, CodeNotSignedIn, codeOf(err))
	_, err = f.svc.Sidebar(context.Background(), 999, "")
	assert.Equal(t, CodeNotFound, codeOf(err))
}

func TestMentionUpdatesBadgeAndNotifies(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") }, "prefetch")
	fake.PostAs("c-offtopic", "bob", "hi @alice")
	f.eventually(func() bool { return f.server(id).Mentions == 1 }, "server badge")
	var last Badge
	f.eventually(func() bool {
		for {
			select {
			case last = <-f.badge:
			default:
				return last.Mentions == 1 && last.Unread
			}
		}
	}, "OnBadge total")
	f.eventually(func() bool { return len(f.notes.List()) == 1 }, "notification")
	n := f.notes.List()[0]
	assert.Equal(t, Notification{ID: "mm-" + itoa(id) + "-c-offtopic", Title: "Off-Topic", Body: "bob: hi @alice", ServerID: id, ChannelID: "c-offtopic"}, n)
}

func TestFocusedActiveChannelIsReadAndSilent(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") }, "prefetch")
	require.NoError(t, f.svc.SelectServer(ctx, id))
	_, err := f.svc.OpenChannel(ctx, id, "c-offtopic")
	require.NoError(t, err)
	require.NoError(t, f.svc.SetFocused(ctx, true))
	fake.PostAs("c-offtopic", "bob", "hi @alice while you look")
	f.eventually(func() bool { return f.has(id, "c-offtopic", "hi @alice while you look") }, "post")
	f.eventually(func() bool {
		return fake.Member("c-offtopic", "alice").MsgCount == fake.Channel("c-offtopic").TotalMsgCount
	}, "focused active channel is marked read")
	time.Sleep(300 * time.Millisecond)
	assert.Empty(t, f.notes.List(), "no notification for the channel on screen")
}

func TestFocusGoesOnlyToTheActiveServer(t *testing.T) {
	f := newChatFixture(t)
	fakeA, fakeB := startFake(t), startFake(t)
	a, b := f.signIn(fakeA, "alice"), f.signIn(fakeB, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(a, "c-town") && f.loaded(b, "c-offtopic") }, "prefetch")

	require.NoError(t, f.svc.SelectServer(ctx, b))
	_, err := f.svc.OpenChannel(ctx, b, "c-offtopic")
	require.NoError(t, err)
	require.NoError(t, f.svc.SelectServer(ctx, a))
	_, err = f.svc.OpenChannel(ctx, a, "c-town")
	require.NoError(t, err)
	require.NoError(t, f.svc.SetFocused(ctx, true))

	fakeB.PostAs("c-offtopic", "bob", "background server post")
	f.eventually(func() bool { return f.has(b, "c-offtopic", "background server post") }, "post on B")
	time.Sleep(300 * time.Millisecond)
	assert.True(t, f.server(b).Unread, "B's channel is not on screen: stays unread")
	assert.Less(t, fakeB.Member("c-offtopic", "alice").MsgCount, fakeB.Channel("c-offtopic").TotalMsgCount)

	require.NoError(t, f.svc.SelectServer(ctx, b))
	f.eventually(func() bool { return !f.server(b).Unread }, "switching to B reads its open channel")
}

func TestCoalescedChannelEvents(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") }, "prefetch")
	time.Sleep(300 * time.Millisecond)
	for len(f.evs) > 0 {
		<-f.evs
	}
	for i := 0; i < 5; i++ {
		fake.PostAs("c-offtopic", "bob", "burst")
	}
	deadline := time.After(time.Second)
	channelEvents, sidebarEvents := 0, 0
loop:
	for {
		select {
		case ev := <-f.evs:
			switch {
			case ev.Type == EventChannelChanged && ev.Payload["channel_id"] == "c-offtopic":
				channelEvents++
			case ev.Type == EventSidebarChanged:
				sidebarEvents++
			}
		case <-deadline:
			break loop
		}
	}
	assert.GreaterOrEqual(t, channelEvents, 1)
	assert.LessOrEqual(t, channelEvents, 2, "5 posts in a burst → 1–2 events, not 5")
	assert.GreaterOrEqual(t, sidebarEvents, 1)
}

func TestLogoutStopsSyncAndClearsCache(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	f.eventually(func() bool { e, _ := f.st.LoadCache(ctx, id); return len(e) > 0 }, "snapshot written")
	require.NoError(t, f.svc.Logout(ctx, id))
	assert.Equal(t, "off", f.server(id).State)
	entries, err := f.st.LoadCache(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, entries)
	_, err = f.svc.Sidebar(ctx, id, "")
	assert.Equal(t, CodeNotSignedIn, codeOf(err))
}

func TestSigningInAsAnotherUserDropsCache(t *testing.T) {
	f := newChatFixture(t)
	f.svc.tune = func(c *mmsync.Config) { c.FlushEvery, c.WakeCheck = time.Hour, time.Hour } // flush only on stop
	require.NoError(t, f.svc.Start(context.Background()))                                   // re-create the manager with the new tuning
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-secret") }, "alice's private channel loaded")
	_, err := f.svc.LoginWithPassword(ctx, id, "bob", "secret")
	require.NoError(t, err)
	entries, err := f.st.LoadCache(ctx, id)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, "c-secret", e.Key, "alice's cache must not survive bob's sign-in")
	}
}

func TestRemoveServerStopsItsWorker(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.server(id).State == "live" }, "live")
	require.NoError(t, f.svc.RemoveServer(context.Background(), id))
	assert.Nil(t, f.svc.manager().Worker(id))
}

func TestOpenURLAllowsOnlyWebAndMail(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.svc.OpenURL(ctx, "https://example.com/a?b=1"))
	require.NoError(t, f.svc.OpenURL(ctx, "mailto:bob@example.com"))
	for _, bad := range []string{"javascript:alert(1)", "file:///etc/passwd", "https://", "mmauth://callback", "::"} {
		assert.Equal(t, CodeInvalidURL, codeOf(f.svc.OpenURL(ctx, bad)), bad)
	}
	assert.Equal(t, []string{"https://example.com/a?b=1", "mailto:bob@example.com"}, f.opened)
}

func TestNotificationClickOpensChannel(t *testing.T) {
	f := newFixture(t)
	f.svc.NotificationClicked(3, "c-town")
	ev := f.nextEvent(t, EventOpenChannel)
	assert.Equal(t, map[string]any{"server_id": int64(3), "channel_id": "c-town"}, ev.Payload)
}
```
(`newFixture`, `codeOf`, `nextEvent` — из существующего `service_test.go`. `TestSigningInAsAnotherUserDropsCache` вызывает `Start` второй раз — `Start` обязан сначала закрыть прежний менеджер, см. Step 6.)

- [ ] **Step 5: Запустить — падает**

Run: `go test ./internal/api/`
Expected: FAIL (компиляция: нет `formatLocale`, `notificationFor`, `newNotifyQueue`, `Start`, …).

- [ ] **Step 6: Реализация**

`internal/state/view.go` — поле и заполнение:
```go
	TeamID   string     `json:"team_id"`
```
(в `ChannelView` после `TeamName`), и в `ChannelView(...)`: `TeamID: ch.Info.TeamID,`.

`internal/api/api.go` — заменить `ServerDTO`, дополнить интерфейс, события и коды:
```go
package api

import (
	"context"

	"github.com/spk/spk-mattermost/internal/state"
)

type ServerDTO struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	SignedIn bool   `json:"signed_in"`
	Username string `json:"username"`
	GitLab   bool   `json:"gitlab"`
	// State of the sync worker: off | connecting | live | reconnecting | needs_reauth.
	State    string `json:"state"`
	Unread   bool   `json:"unread"`
	Mentions int    `json:"mentions"`
}

// Views of the hot layer are the UI DTOs.
type (
	SidebarDTO = state.SidebarView
	ChannelDTO = state.ChannelView
	Badge      = state.Badge
)

type AppInfo struct {
	// FormatLocale is a BCP 47 tag for dates and times (from LC_TIME), "" = use the UI language.
	FormatLocale string `json:"format_locale"`
}

type API interface {
	ListServers(ctx context.Context) ([]ServerDTO, error)
	AddServer(ctx context.Context, rawURL string) (ServerDTO, error)
	RemoveServer(ctx context.Context, id int64) error
	StartGitLabLogin(ctx context.Context, id int64) error
	LoginWithPassword(ctx context.Context, id int64, login, password string) (ServerDTO, error)
	Logout(ctx context.Context, id int64) error

	AppInfo(ctx context.Context) (AppInfo, error)
	SelectServer(ctx context.Context, id int64) error
	SetFocused(ctx context.Context, focused bool) error
	NetworkChanged(ctx context.Context) error
	OpenURL(ctx context.Context, rawURL string) error
	Sidebar(ctx context.Context, id int64, teamID string) (SidebarDTO, error)
	OpenChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error)
	GetChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error)
	LoadOlder(ctx context.Context, id int64, channelID string) error
	SendPost(ctx context.Context, id int64, channelID, message string) error
	RetryPost(ctx context.Context, id int64, channelID, pendingID string) error
	DiscardPost(ctx context.Context, id int64, channelID, pendingID string) error
	EditPost(ctx context.Context, id int64, postID, message string) error
	DeletePost(ctx context.Context, id int64, postID string) error
	MarkUnread(ctx context.Context, id int64, postID string) error
	SaveDraft(ctx context.Context, id int64, channelID, text string) error
}

// Event types pushed to the UI.
const (
	EventServersChanged = "servers_changed" // also on connection state / badge changes
	EventLoginFailed    = "login_failed"    // payload: server_id (optional), code
	EventOpenExternal   = "open_external"   // payload: url — browser mode opens it in a new tab
	EventSidebarChanged = "sidebar_changed" // payload: server_id
	EventChannelChanged = "channel_changed" // payload: server_id, channel_id
	EventOpenChannel    = "open_channel"    // payload: server_id, channel_id — a notification was clicked
)
```
В блок кодов добавить:
```go
	CodeNotSignedIn    = "not_signed_in"
	CodeSessionExpired = "session_expired"
	CodeNoChannel      = "no_channel"
	CodeEmptyMessage   = "empty_message"
	CodeForbidden      = "forbidden"
```

`internal/api/service.go` — поля и конструктор:
```go
type Service struct {
	st   *store.Store
	em   *events.Emitter
	sso  *auth.SSO
	open Opener
	hc   *http.Client
	// callTimeout bounds each interactive call to a Mattermost server as a
	// whole (all REST requests and retries): Wails bindings pass
	// context.Background(), so without it a hung server would freeze the
	// action for minutes.
	callTimeout time.Duration

	co      *events.Coalescer
	nq      *notifyQueue
	getenv  func(string) string
	tune    func(*mmsync.Config) // tests: shorter intervals
	focusMu sync.Mutex           // serializes applyFocus

	mu       sync.Mutex
	mgr      *mmsync.Manager
	notifier Notifier
	badgeFns []func(Badge)
	active   int64 // server shown in the UI
	focused  bool  // window focused and visible
	marks    map[int64]serverMark
	total    Badge
}

func NewService(st *store.Store, em *events.Emitter, open Opener, hc *http.Client) *Service {
	s := &Service{st: st, em: em, sso: auth.NewSSO(), open: open, hc: hc, callTimeout: defaultCallTimeout,
		co: events.NewCoalescer(coalesceDelay), getenv: os.Getenv}
	s.nq = newNotifyQueue(notifyBurst, s.deliver)
	return s
}
```
`toDTO` заменить методом (все вызовы `toDTO(x)` → `s.dto(x)`):
```go
func (s *Service) dto(srv store.Server) ServerDTO {
	d := ServerDTO{ID: srv.ID, Name: srv.Name, URL: srv.URL, SignedIn: srv.SignedIn(), Username: srv.Username,
		GitLab: srv.GitLab, State: string(mmsync.StatusOff)}
	if m := s.manager(); m != nil {
		if w := m.Worker(srv.ID); w != nil {
			b := w.State().Badge()
			d.State, d.Unread, d.Mentions = string(w.Status()), b.Unread, b.Mentions
		}
	}
	return d
}
```
`RemoveServer` — первой строкой после `getServer`: `s.deactivate(id)`. `LoginWithPassword` — после успешного `SetSession` и перед `emit`: `s.activate(ctx, srv)` (`srv` — прочитанный до входа). `HandleDeepLink` — то же после `SetSession`: `s.activate(ctx, srv)`. `Logout` целиком:
```go
func (s *Service) Logout(ctx context.Context, id int64) error {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return err
	}
	s.deactivate(id) // final snapshot flush happens before the cache is dropped below
	s.revoke(ctx, srv)
	s.sso.Cancel(id) // a GitLab login still in flight must not sign back in
	if err := s.st.ClearSession(ctx, id); err != nil {
		return coded(CodeInternal, err)
	}
	if err := s.st.ClearCache(ctx, id); err != nil {
		slog.Warn("cache clear after sign-out failed", "srv", id, "err", err)
	}
	s.emit(EventServersChanged, nil)
	return nil
}
```
(импорты `os`, `sync`, `mmsync`.)

`internal/api/sync.go`:
```go
package api

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/spk/spk-mattermost/internal/mmsync"
	"github.com/spk/spk-mattermost/internal/notify"
	"github.com/spk/spk-mattermost/internal/state"
	"github.com/spk/spk-mattermost/internal/store"
)

const coalesceDelay = 100 * time.Millisecond

type serverMark struct {
	status mmsync.Status
	badge  Badge
}

// Start launches a sync worker for every signed-in server. It only reads the
// local database — network work happens in the workers' goroutines — so it
// never delays application startup. Calling it again replaces the manager.
func (s *Service) Start(ctx context.Context) error {
	cfg := mmsync.Config{Store: s.st, HTTPClient: s.hc, Hooks: mmsync.Hooks{
		Changed: s.onChanged, Status: s.onStatus, Notify: s.onNotify,
	}}
	if s.tune != nil {
		s.tune(&cfg)
	}
	m := mmsync.NewManager(context.Background(), cfg)
	s.mu.Lock()
	old := s.mgr
	s.mgr = m
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return m.StartAll(ctx)
}

// Close stops every worker (each flushes its snapshot) and drops pending UI
// events and notifications.
func (s *Service) Close() {
	if m := s.manager(); m != nil {
		m.Close()
	}
	s.co.Close()
	s.nq.close()
}

func (s *Service) manager() *mmsync.Manager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mgr
}

// OnBadge registers fn for the badge summed over all servers; it is called
// from a background goroutine whenever the sum changes (tray).
func (s *Service) OnBadge(fn func(Badge)) {
	s.mu.Lock()
	s.badgeFns = append(s.badgeFns, fn)
	s.mu.Unlock()
}

// SetNotifier sets where notifications go; nil drops them.
func (s *Service) SetNotifier(n Notifier) {
	s.mu.Lock()
	s.notifier = n
	s.mu.Unlock()
}

// activate (re)starts sync after a sign-in with the new token. Signing in
// as a different user drops the previous user's cached chats first.
func (s *Service) activate(ctx context.Context, before store.Server) {
	m := s.manager()
	if m == nil {
		return
	}
	after, err := s.st.GetServer(ctx, before.ID)
	if err != nil {
		slog.Warn("cannot start sync after sign-in", "srv", before.ID, "err", err)
		return
	}
	m.Stop(before.ID)
	if before.UserID != after.UserID {
		if err := s.st.ClearCache(ctx, before.ID); err != nil {
			slog.Warn("cache clear failed", "srv", before.ID, "err", err)
		}
	}
	m.Start(after)
	s.applyFocus()
	s.co.Schedule("badge", s.refreshBadges)
}

func (s *Service) deactivate(id int64) {
	if m := s.manager(); m != nil {
		m.Stop(id)
	}
	s.co.Schedule("badge", s.refreshBadges)
}

func (s *Service) SelectServer(_ context.Context, id int64) error {
	s.mu.Lock()
	s.active = id
	s.mu.Unlock()
	s.applyFocus()
	return nil
}

func (s *Service) SetFocused(_ context.Context, focused bool) error {
	s.mu.Lock()
	s.focused = focused
	s.mu.Unlock()
	s.applyFocus()
	return nil
}

// applyFocus: only the server on screen may treat its open channel as seen.
func (s *Service) applyFocus() {
	s.focusMu.Lock()
	defer s.focusMu.Unlock()
	s.mu.Lock()
	active, focused, m := s.active, s.focused, s.mgr
	s.mu.Unlock()
	if m == nil {
		return
	}
	m.Each(func(id int64, w *mmsync.Worker) { w.SetFocused(focused && id == active) })
}

func (s *Service) NetworkChanged(context.Context) error {
	if m := s.manager(); m != nil {
		m.NudgeAll()
	}
	return nil
}

func (s *Service) onChanged(id int64, ch state.Change) {
	if ch.Sidebar || ch.Badge {
		s.co.Schedule(fmt.Sprintf("sidebar/%d", id), func() {
			s.emit(EventSidebarChanged, map[string]any{"server_id": id})
		})
		s.co.Schedule("badge", s.refreshBadges)
	}
	for _, c := range ch.Channels {
		if c == "" {
			continue
		}
		s.co.Schedule(fmt.Sprintf("channel/%d/%s", id, c), func() {
			s.emit(EventChannelChanged, map[string]any{"server_id": id, "channel_id": c})
		})
	}
}

func (s *Service) onStatus(int64, mmsync.Status) { s.co.Schedule("badge", s.refreshBadges) }

// refreshBadges runs on the coalescer: servers_changed only when a server's
// state or badge really changed, OnBadge only when the total did.
func (s *Service) refreshBadges() {
	marks := map[int64]serverMark{}
	var total Badge
	if m := s.manager(); m != nil {
		m.Each(func(id int64, w *mmsync.Worker) {
			b := w.State().Badge()
			marks[id] = serverMark{status: w.Status(), badge: b}
			total.Unread = total.Unread || b.Unread
			total.Mentions += b.Mentions
		})
	}
	s.mu.Lock()
	changed := !maps.Equal(marks, s.marks)
	totalChanged := total != s.total
	s.marks, s.total = marks, total
	fns := slices.Clone(s.badgeFns)
	s.mu.Unlock()
	if changed {
		s.emit(EventServersChanged, nil)
	}
	if totalChanged {
		for _, fn := range fns {
			fn(total)
		}
	}
}

func (s *Service) onNotify(id int64, c state.NotifyCandidate) {
	ok, why := notify.Decide(notify.Input{
		MeID: c.Me.ID, MeUsername: c.Me.Username, MeFirstName: c.Me.FirstName,
		UserNotify: c.Me.NotifyProps, MemberNotify: c.Member.NotifyProps,
		Status: c.Status.Status, DNDEndSec: c.Status.DNDEndTime, NowMs: time.Now().UnixMilli(),
		Post: c.Post, ChannelType: c.Channel.Type, Mentions: c.Mentions, Followers: c.Followers,
		CRT: c.CRT, Focused: c.Focused, ActiveChannelID: c.Active,
	})
	if !ok {
		slog.Debug("notification skipped", "srv", id, "channel", c.Post.ChannelID, "reason", why)
		return
	}
	s.nq.push(notificationFor(id, c))
}

func (s *Service) deliver(n Notification) {
	s.mu.Lock()
	nt := s.notifier
	s.mu.Unlock()
	if nt != nil {
		nt.Notify(n)
	}
}

// NotificationClicked asks the UI to open the channel of a clicked
// notification (the desktop layer also raises the window).
func (s *Service) NotificationClicked(serverID int64, channelID string) {
	s.emit(EventOpenChannel, map[string]any{"server_id": serverID, "channel_id": channelID})
}
```

`internal/api/notifications.go`:
```go
package api

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
	"github.com/spk/spk-mattermost/internal/state"
)

const (
	notifyBurst     = 10 * time.Second
	notifyBodyRunes = 200
)

type Notification struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	ServerID  int64  `json:"server_id"`
	ChannelID string `json:"channel_id"`
}

// Notifier shows a notification; it may block (D-Bus), the service calls it
// from its own goroutine.
type Notifier interface{ Notify(Notification) }

// RecordingNotifier keeps notifications in memory (browser mode, tests).
type RecordingNotifier struct {
	mu   sync.Mutex
	list []Notification
}

func (r *RecordingNotifier) Notify(n Notification) {
	r.mu.Lock()
	r.list = append(r.list, n)
	r.mu.Unlock()
}

func (r *RecordingNotifier) List() []Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Notification{}, r.list...)
}

func notificationFor(serverID int64, c state.NotifyCandidate) Notification {
	sender := c.SenderName
	if bool(c.Post.Props.FromWebhook) && c.Post.Props.OverrideUsername != "" {
		sender = string(c.Post.Props.OverrideUsername)
	}
	body := postText(c.Post)
	if c.Channel.Type != model.ChannelDirect {
		body = sender + ": " + body
	}
	return Notification{
		ID: fmt.Sprintf("mm-%d-%s", serverID, c.Post.ChannelID), Title: c.ChannelName,
		Body: truncateRunes(body, notifyBodyRunes), ServerID: serverID, ChannelID: c.Post.ChannelID,
	}
}

func postText(p model.Post) string {
	if t := strings.Join(strings.Fields(p.Message), " "); t != "" {
		return t
	}
	for _, a := range p.Props.Attachments {
		for _, s := range []string{a.Fallback, a.Pretext, a.Title, a.Text} {
			if t := strings.Join(strings.Fields(s), " "); t != "" {
				return t
			}
		}
	}
	if p.Metadata != nil && len(p.Metadata.Files) > 0 {
		return "📎 " + p.Metadata.Files[0].Name
	}
	if len(p.FileIDs) > 0 {
		return "📎"
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// notifyQueue shows the first notification of a channel at once and folds
// the rest of a burst (within window) into one "latest (+N)".
type notifyQueue struct {
	window time.Duration
	out    func(Notification)
	mu     sync.Mutex
	bursts map[string]*burst
	closed bool
}

type burst struct {
	extra  int
	latest Notification
	timer  *time.Timer
}

func newNotifyQueue(window time.Duration, out func(Notification)) *notifyQueue {
	return &notifyQueue{window: window, out: out, bursts: map[string]*burst{}}
}

func (q *notifyQueue) push(n Notification) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if b := q.bursts[n.ID]; b != nil {
		b.extra++
		b.latest = n
		return
	}
	b := &burst{}
	q.bursts[n.ID] = b
	b.timer = time.AfterFunc(q.window, func() { q.flush(n.ID, b) })
	go q.out(n)
}

func (q *notifyQueue) flush(id string, b *burst) {
	q.mu.Lock()
	if q.closed || q.bursts[id] != b {
		q.mu.Unlock()
		return
	}
	delete(q.bursts, id)
	extra, n := b.extra, b.latest
	q.mu.Unlock()
	if extra == 0 {
		return
	}
	if extra > 1 {
		n.Body = fmt.Sprintf("%s (+%d)", n.Body, extra-1)
	}
	q.out(n)
}

func (q *notifyQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	for _, b := range q.bursts {
		b.timer.Stop()
	}
	q.bursts = map[string]*burst{}
}
```
`internal/api/locale.go`:
```go
package api

import (
	"context"
	"strings"
)

// formatLocale turns the POSIX locale used for dates (LC_ALL, LC_TIME, LANG
// — the first one set) into a BCP 47 tag: "ru_RU.UTF-8" → "ru-RU". "" for
// C/POSIX/unset: the UI then formats with its own language.
func formatLocale(getenv func(string) string) string {
	var loc string
	for _, k := range []string{"LC_ALL", "LC_TIME", "LANG"} {
		if loc = getenv(k); loc != "" {
			break
		}
	}
	loc, _, _ = strings.Cut(loc, ".")
	loc, _, _ = strings.Cut(loc, "@")
	if loc == "" || loc == "C" || loc == "POSIX" {
		return ""
	}
	return strings.ReplaceAll(loc, "_", "-")
}

func (s *Service) AppInfo(context.Context) (AppInfo, error) {
	return AppInfo{FormatLocale: formatLocale(s.getenv)}, nil
}
```

`internal/api/chat.go`:
```go
package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/mmsync"
)

// worker returns the sync worker of a signed-in server.
func (s *Service) worker(ctx context.Context, id int64) (*mmsync.Worker, error) {
	if m := s.manager(); m != nil {
		if w := m.Worker(id); w != nil {
			return w, nil
		}
	}
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return nil, err
	}
	if !srv.SignedIn() {
		return nil, coded(CodeNotSignedIn, nil)
	}
	return nil, coded(CodeInternal, errors.New("sync is not running"))
}

// writer is worker for actions that write: a dead session fails fast.
func (s *Service) writer(ctx context.Context, id int64) (*mmsync.Worker, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.Status() == mmsync.StatusNeedsReauth {
		return nil, coded(CodeSessionExpired, nil)
	}
	return w, nil
}

func actionError(err error) error {
	var re *rest.Error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mmsync.ErrEmptyMessage):
		return coded(CodeEmptyMessage, nil)
	case errors.As(err, &re) && re.Status == http.StatusUnauthorized:
		return coded(CodeSessionExpired, err)
	case errors.As(err, &re) && re.Status == http.StatusForbidden:
		return coded(CodeForbidden, err)
	case rest.IsNetwork(err):
		return coded(CodeUnreachable, err)
	}
	return coded(CodeInternal, err)
}

func (s *Service) Sidebar(ctx context.Context, id int64, teamID string) (SidebarDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return SidebarDTO{}, err
	}
	return w.State().Sidebar(teamID), nil
}

func (s *Service) OpenChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return ChannelDTO{}, err
	}
	if _, ok := w.State().ChannelView(channelID); !ok {
		return ChannelDTO{}, coded(CodeNoChannel, nil)
	}
	s.mu.Lock()
	switched := s.active != id
	s.active = id
	s.mu.Unlock()
	if switched {
		s.applyFocus() // before OpenChannel: it marks read only a focused worker's channel
	}
	v, _ := w.OpenChannel(channelID)
	return v, nil
}

func (s *Service) GetChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error) {
	w, err := s.worker(ctx, id)
	if err != nil {
		return ChannelDTO{}, err
	}
	v, ok := w.State().ChannelView(channelID)
	if !ok {
		return ChannelDTO{}, coded(CodeNoChannel, nil)
	}
	return v, nil
}

func (s *Service) LoadOlder(ctx context.Context, id int64, channelID string) error {
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.LoadOlder(rctx, channelID))
}

func (s *Service) SendPost(ctx context.Context, id int64, channelID, message string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	return actionError(w.Send(channelID, message))
}

func (s *Service) RetryPost(ctx context.Context, id int64, channelID, pendingID string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	w.Retry(channelID, pendingID)
	return nil
}

func (s *Service) DiscardPost(ctx context.Context, id int64, channelID, pendingID string) error {
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	w.Discard(channelID, pendingID)
	return nil
}

func (s *Service) EditPost(ctx context.Context, id int64, postID, message string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.Edit(rctx, postID, message))
}

func (s *Service) DeletePost(ctx context.Context, id int64, postID string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.Delete(rctx, postID))
}

func (s *Service) MarkUnread(ctx context.Context, id int64, postID string) error {
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	return actionError(w.MarkUnread(rctx, postID))
}

func (s *Service) SaveDraft(ctx context.Context, id int64, channelID, text string) error {
	w, err := s.worker(ctx, id)
	if err != nil {
		return err
	}
	w.SaveDraft(channelID, text)
	return nil
}

// OpenURL opens a link from a message in the system browser; only web and
// mail links — never file://, javascript: or custom schemes.
func (s *Service) OpenURL(_ context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return coded(CodeInvalidURL, err)
	}
	switch {
	case (u.Scheme == "http" || u.Scheme == "https") && u.Host != "":
	case u.Scheme == "mailto" && u.Opaque != "":
	default:
		return coded(CodeInvalidURL, nil)
	}
	if err := s.open(u.String()); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}
```

- [ ] **Step 7: Тесты сервиса проходят**

Run: `go test -race -count=2 ./internal/api/ ./internal/events/ ./internal/state/`
Expected: PASS (включая старые тесты этапа 1).

- [ ] **Step 8: Транспорты — тест HTTP-маршрутов**

`internal/api/transport/http_test.go`: `fakeAPI` встраивает интерфейс, чтобы не реализовывать всё, и запоминает вызовы чата:
```go
type fakeAPI struct {
	api.API // unimplemented methods panic: tests call only what they set up
	added   string
	sent    []string
}

func (f *fakeAPI) SendPost(_ context.Context, id int64, channelID, message string) error {
	f.sent = append(f.sent, fmt.Sprintf("%d/%s/%s", id, channelID, message))
	return nil
}

func (f *fakeAPI) GetChannel(_ context.Context, id int64, channelID string) (api.ChannelDTO, error) {
	return api.ChannelDTO{ID: channelID, Name: "Town", Posts: []state.PostView{}}, nil
}
```
(остальные существующие методы `fakeAPI` остаются; импорты `fmt`, `internal/state`) и тест:
```go
func TestChatRoutes(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "SendPost", `{"id":3,"channel_id":"c1","message":"hi"}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []string{"3/c1/hi"}, f.sent)

	resp = call(t, h, ts.URL, "GetChannel", `{"id":3,"channel_id":"c1"}`)
	var ch map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ch))
	assert.Equal(t, "c1", ch["id"])
	assert.Equal(t, []any{}, ch["posts"])
}
```

Run: `go test ./internal/api/transport/`
Expected: FAIL (`404` на `SendPost`).

- [ ] **Step 9: Транспорты — реализация**

`internal/api/transport/http.go` — типы запросов и маршруты (добавить в `routes()` перед `GET /api/events`):
```go
type chanReq struct {
	ID        int64  `json:"id"`
	ChannelID string `json:"channel_id"`
}

type postReq struct {
	ID     int64  `json:"id"`
	PostID string `json:"post_id"`
}
```
```go
	h.mux.HandleFunc("POST /api/AppInfo", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.AppInfo(ctx)
	}))
	h.mux.HandleFunc("POST /api/SelectServer", handle(func(ctx context.Context, r *idReq) (any, error) {
		return nil, h.api.SelectServer(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/SetFocused", handle(func(ctx context.Context, r *struct {
		Focused bool `json:"focused"`
	}) (any, error) {
		return nil, h.api.SetFocused(ctx, r.Focused)
	}))
	h.mux.HandleFunc("POST /api/NetworkChanged", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return nil, h.api.NetworkChanged(ctx)
	}))
	h.mux.HandleFunc("POST /api/OpenURL", handle(func(ctx context.Context, r *struct {
		URL string `json:"url"`
	}) (any, error) {
		return nil, h.api.OpenURL(ctx, r.URL)
	}))
	h.mux.HandleFunc("POST /api/Sidebar", handle(func(ctx context.Context, r *struct {
		ID     int64  `json:"id"`
		TeamID string `json:"team_id"`
	}) (any, error) {
		return h.api.Sidebar(ctx, r.ID, r.TeamID)
	}))
	h.mux.HandleFunc("POST /api/OpenChannel", handle(func(ctx context.Context, r *chanReq) (any, error) {
		return h.api.OpenChannel(ctx, r.ID, r.ChannelID)
	}))
	h.mux.HandleFunc("POST /api/GetChannel", handle(func(ctx context.Context, r *chanReq) (any, error) {
		return h.api.GetChannel(ctx, r.ID, r.ChannelID)
	}))
	h.mux.HandleFunc("POST /api/LoadOlder", handle(func(ctx context.Context, r *chanReq) (any, error) {
		return nil, h.api.LoadOlder(ctx, r.ID, r.ChannelID)
	}))
	h.mux.HandleFunc("POST /api/SendPost", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		Message   string `json:"message"`
	}) (any, error) {
		return nil, h.api.SendPost(ctx, r.ID, r.ChannelID, r.Message)
	}))
	h.mux.HandleFunc("POST /api/RetryPost", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		PendingID string `json:"pending_id"`
	}) (any, error) {
		return nil, h.api.RetryPost(ctx, r.ID, r.ChannelID, r.PendingID)
	}))
	h.mux.HandleFunc("POST /api/DiscardPost", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		PendingID string `json:"pending_id"`
	}) (any, error) {
		return nil, h.api.DiscardPost(ctx, r.ID, r.ChannelID, r.PendingID)
	}))
	h.mux.HandleFunc("POST /api/EditPost", handle(func(ctx context.Context, r *struct {
		ID      int64  `json:"id"`
		PostID  string `json:"post_id"`
		Message string `json:"message"`
	}) (any, error) {
		return nil, h.api.EditPost(ctx, r.ID, r.PostID, r.Message)
	}))
	h.mux.HandleFunc("POST /api/DeletePost", handle(func(ctx context.Context, r *postReq) (any, error) {
		return nil, h.api.DeletePost(ctx, r.ID, r.PostID)
	}))
	h.mux.HandleFunc("POST /api/MarkUnread", handle(func(ctx context.Context, r *postReq) (any, error) {
		return nil, h.api.MarkUnread(ctx, r.ID, r.PostID)
	}))
	h.mux.HandleFunc("POST /api/SaveDraft", handle(func(ctx context.Context, r *struct {
		ID        int64  `json:"id"`
		ChannelID string `json:"channel_id"`
		Text      string `json:"text"`
	}) (any, error) {
		return nil, h.api.SaveDraft(ctx, r.ID, r.ChannelID, r.Text)
	}))
```

`internal/api/transport/wails.go` — дописать:
```go
func (w *API) AppInfo() (api.AppInfo, error)   { return w.a.AppInfo(context.Background()) }
func (w *API) SelectServer(id int64) error      { return w.a.SelectServer(context.Background(), id) }
func (w *API) SetFocused(focused bool) error    { return w.a.SetFocused(context.Background(), focused) }
func (w *API) NetworkChanged() error            { return w.a.NetworkChanged(context.Background()) }
func (w *API) OpenURL(url string) error         { return w.a.OpenURL(context.Background(), url) }
func (w *API) Sidebar(id int64, teamID string) (api.SidebarDTO, error) {
	return w.a.Sidebar(context.Background(), id, teamID)
}
func (w *API) OpenChannel(id int64, channelID string) (api.ChannelDTO, error) {
	return w.a.OpenChannel(context.Background(), id, channelID)
}
func (w *API) GetChannel(id int64, channelID string) (api.ChannelDTO, error) {
	return w.a.GetChannel(context.Background(), id, channelID)
}
func (w *API) LoadOlder(id int64, channelID string) error {
	return w.a.LoadOlder(context.Background(), id, channelID)
}
func (w *API) SendPost(id int64, channelID, message string) error {
	return w.a.SendPost(context.Background(), id, channelID, message)
}
func (w *API) RetryPost(id int64, channelID, pendingID string) error {
	return w.a.RetryPost(context.Background(), id, channelID, pendingID)
}
func (w *API) DiscardPost(id int64, channelID, pendingID string) error {
	return w.a.DiscardPost(context.Background(), id, channelID, pendingID)
}
func (w *API) EditPost(id int64, postID, message string) error {
	return w.a.EditPost(context.Background(), id, postID, message)
}
func (w *API) DeletePost(id int64, postID string) error {
	return w.a.DeletePost(context.Background(), id, postID)
}
func (w *API) MarkUnread(id int64, postID string) error {
	return w.a.MarkUnread(context.Background(), id, postID)
}
func (w *API) SaveDraft(id int64, channelID, text string) error {
	return w.a.SaveDraft(context.Background(), id, channelID, text)
}
```

Run: `go test ./internal/api/... && go vet -tags wails ./internal/api/...`
Expected: PASS.

- [ ] **Step 10: Браузерный режим — тест test-API фейка**

`cmd/spk-mattermost/browser_test.go`: в `setup` сервис запускается и закрывается, в хендлер передаётся записывающий notifier:
```go
	notes := &api.RecordingNotifier{}
	svc.SetNotifier(notes)
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(svc.Close)
	h, token := newBrowserHandler(svc, em, dist, fake, testAPI, notes)
```
Новый тест:
```go
func TestTestAPIFakeControlsAndNotifications(t *testing.T) {
	ts, token, fake := setup(t, true)
	post := func(path, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", ts.URL)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}
	resp := post("/api/_test/fake/post", `{"channel_id":"c-offtopic","username":"bob","message":"from test"}`)
	require.Equal(t, 200, resp.StatusCode)
	found := false
	for _, p := range fake.VisiblePosts("c-offtopic") {
		found = found || p.Message == "from test"
	}
	assert.True(t, found)
	assert.Equal(t, 200, post("/api/_test/fake/drop", `{"lose":true}`).StatusCode)
	assert.Equal(t, 200, post("/api/_test/fake/revoke", `{}`).StatusCode)
	assert.Equal(t, 0, fake.ActiveSessions())
	assert.Equal(t, 200, post("/api/_test/notification-click", `{"server_id":1,"channel_id":"c-town"}`).StatusCode)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/_test/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var list []api.Notification
	require.NoError(t, json.NewDecoder(r.Body).Decode(&list))
	assert.Empty(t, list)
}
```

Run: `go test ./cmd/spk-mattermost/`
Expected: FAIL (сигнатура `newBrowserHandler`, маршрутов нет).

- [ ] **Step 11: Браузерный режим и desktop-раннер — реализация**

`cmd/spk-mattermost/main.go`: поле `FakeChannels int` в `browserOpts` и флаг
```go
	root.Flags().IntVar(&o.FakeChannels, "mm-fake-channels", 0, "Extra open channels (20 posts each) in the fake server — memory checks")
```
`cmd/spk-mattermost/browser.go`, в `buildBrowserServer`:
```go
	if o.MMFake {
		fake = mmfake.Start(mmfake.Options{ExtraChannels: o.FakeChannels})
		...
	}
	...
	svc := api.NewService(st, em, open, &http.Client{Timeout: 30 * time.Second})
	notes := &api.RecordingNotifier{} // browser mode has no OS notifications; e2e reads them via test-API
	svc.SetNotifier(notes)
	if err := svc.Start(ctx); err != nil {
		cleanup()
		return nil, nil, "", nil, fmt.Errorf("start sync: %w", err)
	}
	closers = append(closers, svc.Close) // runs first: workers flush before the DB closes
	h, token := newBrowserHandler(svc, em, frontendFS(), fake, o.TestAPI, notes)
```
`newBrowserHandler(svc *api.Service, em *events.Emitter, dist fs.FS, fake *mmfake.Server, testAPI bool, notes *api.RecordingNotifier)`; в блок `if testAPI` добавить:
```go
		writeJSON := func(w http.ResponseWriter, status int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		withFake := func(fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if fake == nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"code": "no_fake_server"})
					return
				}
				fn(w, r)
			}
		}
		tm.HandleFunc("GET /api/_test/notifications", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, notes.List())
		})
		tm.HandleFunc("POST /api/_test/notification-click", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				ServerID  int64  `json:"server_id"`
				ChannelID string `json:"channel_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			svc.NotificationClicked(in.ServerID, in.ChannelID)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
		tm.HandleFunc("POST /api/_test/fake/post", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				ChannelID string `json:"channel_id"`
				Username  string `json:"username"`
				Message   string `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			p := fake.PostAs(in.ChannelID, in.Username, in.Message)
			writeJSON(w, http.StatusOK, map[string]string{"id": p.ID})
		}))
		tm.HandleFunc("POST /api/_test/fake/drop", withFake(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Lose bool `json:"lose"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			fake.DropConnections(in.Lose)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}))
		tm.HandleFunc("POST /api/_test/fake/revoke", withFake(func(w http.ResponseWriter, _ *http.Request) {
			fake.RevokeAll()
			fake.DropConnections(true)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}))
```
`cmd/spk-mattermost/run_desktop_wails.go` после создания `svc`:
```go
	if err := svc.Start(ctx); err != nil {
		slog.Error("sync did not start; chats stay offline", "err", err) // never block the window on it
	}
	defer svc.Close()
```
(импорт `log/slog`). Notifier и бейдж трея подключает Task 15.

- [ ] **Step 12: Всё зелёное**

Run: `go test -race ./... && go vet -tags "wails gtk3" ./... && golangci-lint run --build-tags "wails gtk3"`
Expected: PASS, без замечаний линтера.

- [ ] **Step 13: Коммит**

```bash
git add internal/events internal/api internal/state/view.go cmd/spk-mattermost
git commit -m "api: chat methods over sync workers, coalesced change events, notification decisions and bursts, focus per active server"
```

---

### Task 12: Фронтенд — клиент API, навигация, рейл серверов, сайдбар

**Files:**
- Modify: `frontend/src/api/types.ts`, `frontend/src/api/client.ts` (полные новые версии ниже)
- Modify: `frontend/src/store.ts` (полная новая версия), `frontend/src/App.tsx`, `frontend/src/App.test.tsx`, `frontend/src/i18n.ts`
- Create: `frontend/src/chat.ts` (контроллер: загрузка сайдбара и канала с защитой от устаревших ответов), `frontend/src/chat.test.ts`, `frontend/src/store.test.ts`
- Create: `frontend/src/components/glyph.ts`, `frontend/src/components/Sidebar.tsx`, `frontend/src/components/Sidebar.test.tsx`, `frontend/src/components/ChannelPane.tsx`, `frontend/src/components/ServerRail.test.tsx`
- Modify: `frontend/src/components/ServerRail.tsx`, `frontend/src/components/ServerPanel.tsx` (+ режим повторного входа), `frontend/src/components/ServerPanel.test.tsx`

**Interfaces:**
- Consumes: Task 11 (HTTP-методы и Wails-биндинги с этими именами и полями; события `sidebar_changed`, `channel_changed`, `open_channel`; DTO `SidebarDTO`/`ChannelDTO` — json-имена из Task 6/7 + `team_id`).
- Produces (TS):
  - `types.ts`: `ServerState`, `ServerDTO` (+ `state`, `unread`, `mentions`), `AppInfo`, `TeamItem`, `ChannelItem`, `CategoryView`, `SidebarDTO`, `AttachmentField`, `Attachment`, `FileView`, `ReactionView`, `PostView`, `ChannelDTO`, `EventType`, `ApiEvent`.
  - `Client` + `appInfo()`, `selectServer(id)`, `setFocused(focused)`, `networkChanged()`, `openURL(url)`, `sidebar(id, teamId)`, `openChannel(id, channelId)`, `getChannel(id, channelId)`, `loadOlder(id, channelId)`, `sendPost(id, channelId, message)`, `retryPost(id, channelId, pendingId)`, `discardPost(id, channelId, pendingId)`, `editPost(id, postId, message)`, `deletePost(id, postId)`, `markUnread(id, postId)`, `saveDraft(id, channelId, text)` — все `Promise`.
  - `store.ts`: `useStore` с полями `servers, selectedId, adding, lastError, loginFailures, info, signInFor, sidebar, channel, editingId` и действиями `setServers, select, setError, loginFailed, setInfo, showSignIn, setSidebar(serverId, sb), setChannel(serverId, ch), setEditing`.
  - `chat.ts`: `resetChat()`, `selectServer(id | null)`, `refreshServers()`, `loadSidebar(serverId, teamId = '', openSelected = false)`, `openChannel(serverId, channelId)`, `refreshChannel(serverId, channelId)`, `openFromNotification(serverId, channelId)`, `report(err)`.
  - `glyph.ts`: `channelGlyph(type: string): string`.
  - Компоненты: `Sidebar({ server, sidebar, activeChannelId, onTeam, onChannel, onSignOut, onRemove, onReauth })`, `ChannelPane({ server, channel, onReauth })` (здесь — шапка и баннеры; ленту и редактор добавят Tasks 13–14), `ServerPanel` + `reauth?: boolean`, `onCancel?: () => void`.

Правила UI:
- Навигацию (команда, последний канал в команде) помнит Go (`state.Nav`, в снимке): `Sidebar(id, "")` отдаёт последнюю команду и её выбранный канал. UI хранит только текущее.
- Ответы могут прийти не по порядку (клик во время обновления): каждый запрос несёт номер, применяется только последний. Обновление канала во время открытия не теряется — выполняется сразу после.
- `servers_changed` теперь частое (бейджи, состояние), поэтому: не сбрасывает ошибку входа, если вход/выход не менялся; не выкидывает с экрана «Добавить сервер».
- `needs_reauth`: чат остаётся на экране (кэш читаем), в сайдбаре строка «Сессия истекла» и «Войти снова», в канале баннер; «Войти снова» открывает форму входа поверх (`signInFor`), «Назад» возвращает.
- Свёрнутая категория показывает только непрочитанные каналы и открытый канал (как веб-клиент). Имена стандартных категорий — локализованные по типу; пользовательские — как на сервере.

- [ ] **Step 1: Типы и клиент**

`frontend/src/api/types.ts` (целиком):
```ts
export type ServerState = 'off' | 'connecting' | 'live' | 'reconnecting' | 'needs_reauth'

export interface ServerDTO {
  id: number
  name: string
  url: string
  signed_in: boolean
  username: string
  gitlab: boolean
  state: ServerState
  unread: boolean
  mentions: number
}

export interface AppInfo {
  format_locale: string // BCP 47 for dates/times; '' = navigator.language
}

export interface TeamItem {
  id: string
  name: string
  display_name: string
  unread: boolean
  mentions: number
}

export interface ChannelItem {
  id: string
  name: string
  type: string // O | P | D | G
  unread: boolean
  mentions: number
  muted: boolean
}

// Go nil slices arrive as null.
export interface CategoryView {
  id: string
  type: string // favorites | channels | direct_messages | custom
  name: string
  collapsed: boolean
  channels: ChannelItem[] | null
}

export interface SidebarDTO {
  team_id: string
  selected_channel_id: string
  teams: TeamItem[] | null
  categories: CategoryView[] | null
}

export interface AttachmentField {
  title?: string
  value?: string
  short?: boolean
}

export interface Attachment {
  fallback?: string
  color?: string
  pretext?: string
  author_name?: string
  title?: string
  title_link?: string
  text?: string
  footer?: string
  fields?: AttachmentField[]
}

export interface FileView {
  name: string
  size: number
  mime: string
}

export interface ReactionView {
  emoji: string
  count: number
  mine: boolean
}

export interface PostView {
  id: string
  user_id: string
  author: string
  root_id?: string
  message: string
  create_at: number
  edit_at?: number
  reply_count?: number
  system?: boolean
  bot?: boolean
  pending?: boolean
  failed?: boolean
  attachments?: Attachment[]
  files?: FileView[]
  reactions?: ReactionView[]
}

export interface ChannelDTO {
  id: string
  name: string
  type: string
  header: string
  purpose: string
  team_id: string
  team_name: string
  posts: PostView[]
  new_since: number
  has_more: boolean
  loaded: boolean
  syncing: boolean
  gap_after: string
  draft: string
  me_id: string
  crt: boolean
  muted: boolean
}

export type EventType =
  | 'servers_changed'
  | 'login_failed'
  | 'open_external'
  | 'sidebar_changed'
  | 'channel_changed'
  | 'open_channel'

export interface ApiEvent {
  type: EventType
  payload?: Record<string, unknown>
}
```

`frontend/src/api/client.ts` (целиком):
```ts
import { Call, Events } from '@wailsio/runtime'
import type { ApiEvent, AppInfo, ChannelDTO, EventType, ServerDTO, SidebarDTO } from './types'

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
  appInfo(): Promise<AppInfo>
  selectServer(id: number): Promise<void>
  setFocused(focused: boolean): Promise<void>
  networkChanged(): Promise<void>
  openURL(url: string): Promise<void>
  sidebar(id: number, teamId: string): Promise<SidebarDTO>
  openChannel(id: number, channelId: string): Promise<ChannelDTO>
  getChannel(id: number, channelId: string): Promise<ChannelDTO>
  loadOlder(id: number, channelId: string): Promise<void>
  sendPost(id: number, channelId: string, message: string): Promise<void>
  retryPost(id: number, channelId: string, pendingId: string): Promise<void>
  discardPost(id: number, channelId: string, pendingId: string): Promise<void>
  editPost(id: number, postId: string, message: string): Promise<void>
  deletePost(id: number, postId: string): Promise<void>
  markUnread(id: number, postId: string): Promise<void>
  saveDraft(id: number, channelId: string, text: string): Promise<void>
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

// HTTP bodies return {} for void methods; the Client contract is void.
const done = async (p: Promise<unknown>) => {
  await p
}

export const httpClient: Client = {
  listServers: () => post('ListServers', {}),
  addServer: (url) => post('AddServer', { url }),
  removeServer: (id) => done(post('RemoveServer', { id })),
  startGitLabLogin: (id) => done(post('StartGitLabLogin', { id })),
  loginWithPassword: (id, login, password) => post('LoginWithPassword', { id, login, password }),
  logout: (id) => done(post('Logout', { id })),
  appInfo: () => post('AppInfo', {}),
  selectServer: (id) => done(post('SelectServer', { id })),
  setFocused: (focused) => done(post('SetFocused', { focused })),
  networkChanged: () => done(post('NetworkChanged', {})),
  openURL: (url) => done(post('OpenURL', { url })),
  sidebar: (id, team_id) => post('Sidebar', { id, team_id }),
  openChannel: (id, channel_id) => post('OpenChannel', { id, channel_id }),
  getChannel: (id, channel_id) => post('GetChannel', { id, channel_id }),
  loadOlder: (id, channel_id) => done(post('LoadOlder', { id, channel_id })),
  sendPost: (id, channel_id, message) => done(post('SendPost', { id, channel_id, message })),
  retryPost: (id, channel_id, pending_id) => done(post('RetryPost', { id, channel_id, pending_id })),
  discardPost: (id, channel_id, pending_id) => done(post('DiscardPost', { id, channel_id, pending_id })),
  editPost: (id, post_id, message) => done(post('EditPost', { id, post_id, message })),
  deletePost: (id, post_id) => done(post('DeletePost', { id, post_id })),
  markUnread: (id, post_id) => done(post('MarkUnread', { id, post_id })),
  saveDraft: (id, channel_id, text) => done(post('SaveDraft', { id, channel_id, text })),
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

const EVENT_TYPES: EventType[] = [
  'servers_changed',
  'login_failed',
  'open_external',
  'sidebar_changed',
  'channel_changed',
  'open_channel',
]

export const wailsClient: Client = {
  listServers: () => wcall('ListServers'),
  addServer: (url) => wcall('AddServer', url),
  removeServer: (id) => wcall('RemoveServer', id),
  startGitLabLogin: (id) => wcall('StartGitLabLogin', id),
  loginWithPassword: (id, login, password) => wcall('LoginWithPassword', id, login, password),
  logout: (id) => wcall('Logout', id),
  appInfo: () => wcall('AppInfo'),
  selectServer: (id) => wcall('SelectServer', id),
  setFocused: (focused) => wcall('SetFocused', focused),
  networkChanged: () => wcall('NetworkChanged'),
  openURL: (url) => wcall('OpenURL', url),
  sidebar: (id, teamId) => wcall('Sidebar', id, teamId),
  openChannel: (id, channelId) => wcall('OpenChannel', id, channelId),
  getChannel: (id, channelId) => wcall('GetChannel', id, channelId),
  loadOlder: (id, channelId) => wcall('LoadOlder', id, channelId),
  sendPost: (id, channelId, message) => wcall('SendPost', id, channelId, message),
  retryPost: (id, channelId, pendingId) => wcall('RetryPost', id, channelId, pendingId),
  discardPost: (id, channelId, pendingId) => wcall('DiscardPost', id, channelId, pendingId),
  editPost: (id, postId, message) => wcall('EditPost', id, postId, message),
  deletePost: (id, postId) => wcall('DeletePost', id, postId),
  markUnread: (id, postId) => wcall('MarkUnread', id, postId),
  saveDraft: (id, channelId, text) => wcall('SaveDraft', id, channelId, text),
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

// Wails v3 beta.25 serves the UI from wails://localhost on Linux and macOS
// (internal/assetserver/assetserver_{linux,darwin}.go) and from
// http://wails.localhost on Windows (assetserver_windows.go), optionally with a port.
export const isDesktopLocation = (loc: Pick<Location, 'protocol' | 'hostname'>) =>
  loc.protocol === 'wails:' || loc.hostname === 'wails.localhost'
export const isDesktop = () => isDesktopLocation(window.location)
export const client: Client = isDesktop() ? wailsClient : httpClient
```

В `frontend/src/api/client.test.ts` добавить:
```ts
test('http client chat methods post snake_case bodies', async () => {
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response('{}', { headers: { 'content-type': 'application/json' } }),
  )
  await httpClient.sendPost(3, 'c1', 'hi')
  await httpClient.editPost(3, 'p1', 'v2')
  await httpClient.sidebar(3, '')
  expect(fetchMock.mock.calls.map(([p, i]) => [p, JSON.parse(i!.body as string)])).toEqual([
    ['/api/SendPost', { id: 3, channel_id: 'c1', message: 'hi' }],
    ['/api/EditPost', { id: 3, post_id: 'p1', message: 'v2' }],
    ['/api/Sidebar', { id: 3, team_id: '' }],
  ])
})
```
В `ServerPanel.test.tsx` `base` дополнить `state: 'off' as const, unread: false, mentions: 0`.

Run: `cd frontend && pnpm test src/api`
Expected: PASS.

- [ ] **Step 2: Падающие тесты store и контроллера**

`frontend/src/store.test.ts`:
```ts
import type { ServerDTO } from './api/types'
import { useStore } from './store'

const srv = (o: Partial<ServerDTO> = {}): ServerDTO => ({
  id: 1, name: 'A', url: 'https://a', signed_in: true, username: 'alice', gitlab: false,
  state: 'live', unread: false, mentions: 0, ...o,
})

beforeEach(() =>
  useStore.setState({ servers: [], selectedId: null, adding: false, lastError: null, signInFor: null, sidebar: null, channel: null }),
)

test('first server is selected on load, the add-server screen survives refreshes', () => {
  const s = useStore.getState()
  s.setServers([srv()])
  expect(useStore.getState().selectedId).toBe(1)
  useStore.getState().select(null)
  useStore.getState().setServers([srv({ mentions: 2 })])
  expect(useStore.getState().selectedId).toBeNull()
})

test('removed selected server falls back to the first one', () => {
  useStore.getState().setServers([srv(), srv({ id: 2 })])
  useStore.getState().select(2)
  useStore.getState().setServers([srv()])
  expect(useStore.getState().selectedId).toBe(1)
})

test('a login error survives badge updates and clears when sign-in state changes', () => {
  useStore.getState().setServers([srv({ signed_in: false, state: 'off' })])
  useStore.getState().loginFailed('boom')
  useStore.getState().setServers([srv({ signed_in: false, state: 'off', name: 'A2' })])
  expect(useStore.getState().lastError).toBe('boom')
  useStore.getState().setServers([srv({ state: 'connecting' })])
  expect(useStore.getState().lastError).toBeNull()
})

test('re-auth form closes once the server is no longer needs_reauth', () => {
  useStore.getState().setServers([srv({ state: 'needs_reauth' })])
  useStore.getState().showSignIn(1)
  useStore.getState().setServers([srv({ state: 'needs_reauth', mentions: 1 })])
  expect(useStore.getState().signInFor).toBe(1)
  useStore.getState().setServers([srv({ state: 'connecting' })])
  expect(useStore.getState().signInFor).toBeNull()
})

test('views of another server are ignored', () => {
  useStore.getState().setServers([srv(), srv({ id: 2 })])
  useStore.getState().setSidebar(2, { team_id: 't', selected_channel_id: '', teams: null, categories: null })
  expect(useStore.getState().sidebar).toBeNull()
})
```

`frontend/src/chat.test.ts`:
```ts
import { vi } from 'vitest'
import type { ChannelDTO, ServerDTO, SidebarDTO } from './api/types'
import { useStore } from './store'

vi.mock('./api/client', () => ({
  client: {
    openChannel: vi.fn(),
    getChannel: vi.fn(),
    sidebar: vi.fn(),
    selectServer: vi.fn().mockResolvedValue(undefined),
    listServers: vi.fn(),
  },
}))

const { client } = await import('./api/client')
const { loadSidebar, openChannel, refreshChannel, resetChat, selectServer } = await import('./chat')

function deferred<T>() {
  let resolve!: (v: T) => void
  const p = new Promise<T>((r) => (resolve = r))
  return { p, resolve }
}

const srv = (id: number): ServerDTO => ({
  id, name: 'S' + id, url: 'https://s', signed_in: true, username: 'alice', gitlab: false,
  state: 'live', unread: false, mentions: 0,
})

const chan = (id: string, over: Partial<ChannelDTO> = {}): ChannelDTO => ({
  id, name: id.toUpperCase(), type: 'O', header: '', purpose: '', team_id: 't1', team_name: 'team', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'u-alice',
  crt: false, muted: false, ...over,
})

const sidebar = (over: Partial<SidebarDTO> = {}): SidebarDTO => ({
  team_id: 't1', selected_channel_id: 'a', teams: [], categories: [], ...over,
})

beforeEach(() => {
  resetChat()
  vi.mocked(client.openChannel).mockReset()
  vi.mocked(client.getChannel).mockReset()
  vi.mocked(client.sidebar).mockReset()
  useStore.setState({ servers: [srv(1), srv(2)], selectedId: 1, adding: false, sidebar: null, channel: null, lastError: null })
})

test('loading the sidebar opens its selected channel', async () => {
  vi.mocked(client.sidebar).mockResolvedValue(sidebar())
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  await loadSidebar(1)
  expect(client.openChannel).toHaveBeenCalledWith(1, 'a')
  expect(useStore.getState().channel?.id).toBe('a')
})

test('a click during a refresh wins', async () => {
  vi.mocked(client.openChannel).mockResolvedValueOnce(chan('a'))
  await openChannel(1, 'a')
  const stale = deferred<ChannelDTO>()
  vi.mocked(client.getChannel).mockReturnValue(stale.p)
  const refresh = refreshChannel(1, 'a')
  vi.mocked(client.openChannel).mockResolvedValueOnce(chan('b'))
  await openChannel(1, 'b')
  stale.resolve(chan('a'))
  await refresh
  expect(useStore.getState().channel?.id).toBe('b')
})

test('a change during an open is fetched right after it', async () => {
  const open = deferred<ChannelDTO>()
  vi.mocked(client.openChannel).mockReturnValue(open.p)
  vi.mocked(client.getChannel).mockResolvedValue(chan('a', { name: 'fresh' }))
  const o = openChannel(1, 'a')
  await refreshChannel(1, 'a')
  expect(client.getChannel).not.toHaveBeenCalled()
  open.resolve(chan('a'))
  await o
  await vi.waitFor(() => expect(useStore.getState().channel?.name).toBe('fresh'))
})

test('responses for a server no longer selected are dropped', async () => {
  const sb = deferred<SidebarDTO>()
  vi.mocked(client.sidebar).mockReturnValueOnce(sb.p).mockResolvedValue(sidebar({ selected_channel_id: '' }))
  const l = loadSidebar(1)
  selectServer(2)
  sb.resolve(sidebar())
  await l
  expect(client.openChannel).not.toHaveBeenCalled()
  expect(useStore.getState().selectedId).toBe(2)
})

test('opening a channel of another team switches the sidebar team', async () => {
  vi.mocked(client.sidebar).mockResolvedValueOnce(sidebar()).mockResolvedValueOnce(sidebar({ team_id: 't2' }))
  vi.mocked(client.openChannel).mockResolvedValueOnce(chan('a')).mockResolvedValueOnce(chan('x', { team_id: 't2' }))
  await loadSidebar(1)
  await openChannel(1, 'x')
  await vi.waitFor(() => expect(useStore.getState().sidebar?.team_id).toBe('t2'))
  expect(client.sidebar).toHaveBeenLastCalledWith(1, 't2')
})
```

Run: `cd frontend && pnpm test src/store.test.ts src/chat.test.ts`
Expected: FAIL (нет `chat.ts`, новых полей store).

- [ ] **Step 3: store и контроллер**

`frontend/src/store.ts` (целиком):
```ts
import { create } from 'zustand'
import type { AppInfo, ChannelDTO, ServerDTO, SidebarDTO } from './api/types'

interface State {
  servers: ServerDTO[]
  selectedId: number | null // null = "add server" screen
  adding: boolean // the user chose "add server": list refreshes keep that screen
  lastError: string | null
  loginFailures: number // bumped on every login_failed event
  info: AppInfo
  signInFor: number | null // "Sign in again" opened the sign-in form over this server's cached chats
  sidebar: SidebarDTO | null // of the selected server
  channel: ChannelDTO | null // open channel of the selected server
  editingId: string | null // post being edited inline
  setServers(list: ServerDTO[]): void
  select(id: number | null): void
  setError(msg: string | null): void
  loginFailed(msg: string): void
  setInfo(info: AppInfo): void
  showSignIn(id: number | null): void
  setSidebar(serverId: number, sb: SidebarDTO): void
  setChannel(serverId: number, ch: ChannelDTO): void
  setEditing(id: string | null): void
}

const cleared = { sidebar: null, channel: null, editingId: null }

export const useStore = create<State>((set, get) => ({
  servers: [],
  selectedId: null,
  adding: false,
  lastError: null,
  loginFailures: 0,
  info: { format_locale: '' },
  signInFor: null,
  sidebar: null,
  channel: null,
  editingId: null,
  setServers(list) {
    const { selectedId: sel, adding, servers: prev, signInFor, lastError } = get()
    const stillThere = sel !== null && list.some((s) => s.id === sel)
    const selectedId = stillThere ? sel : adding ? null : (list[0]?.id ?? null)
    // A sign-in or sign-out supersedes an earlier error; badge and
    // connection-state updates (now frequent) do not.
    const signInChanged = list.some((s) => prev.find((p) => p.id === s.id)?.signed_in !== s.signed_in)
    const reauth = list.find((s) => s.id === signInFor)
    set({
      servers: list,
      selectedId,
      lastError: signInChanged ? null : lastError,
      signInFor: reauth?.state === 'needs_reauth' ? signInFor : null,
      ...(selectedId !== sel ? cleared : {}),
    })
  },
  select: (id) => set({ selectedId: id, adding: id === null, lastError: null, signInFor: null, ...cleared }),
  setError: (msg) => set({ lastError: msg }),
  loginFailed: (msg) => set((s) => ({ lastError: msg, loginFailures: s.loginFailures + 1 })),
  setInfo: (info) => set({ info }),
  showSignIn: (id) => set({ signInFor: id }),
  setSidebar(serverId, sb) {
    if (get().selectedId === serverId) set({ sidebar: sb })
  },
  setChannel(serverId, ch) {
    if (get().selectedId === serverId) set({ channel: ch })
  },
  setEditing: (id) => set({ editingId: id }),
}))
```

`frontend/src/chat.ts`:
```ts
import { client } from './api/client'
import { errorMessage } from './errors'
import { useStore } from './store'

// Responses can land out of order (a click while a refresh is in flight):
// each request takes a sequence number and only the latest one lands.
let sidebarSeq = 0
let channelSeq = 0
let wanted: { serverId: number; channelId: string } | null = null
let inFlight = false
let again = false

export const report = (e: unknown) => useStore.getState().setError(errorMessage(e))

export function resetChat() {
  sidebarSeq++
  channelSeq++
  wanted = null
  inFlight = false
  again = false
}

export function selectServer(id: number | null) {
  resetChat()
  const s = useStore.getState()
  s.select(id)
  client.selectServer(id ?? 0).catch(report)
  const srv = s.servers.find((x) => x.id === id)
  if (srv?.signed_in) void loadSidebar(srv.id)
}

export async function refreshServers() {
  try {
    const list = await client.listServers()
    const before = useStore.getState().selectedId
    useStore.getState().setServers(list)
    const s = useStore.getState()
    if (s.selectedId !== before) {
      selectServer(s.selectedId)
      return
    }
    const sel = s.servers.find((x) => x.id === s.selectedId)
    if (!sel) return
    if (!sel.signed_in && (s.sidebar || s.channel)) {
      resetChat() // signed out: drop the views, a new sign-in starts fresh
      s.select(sel.id)
      return
    }
    if (sel.signed_in && !s.sidebar) void loadSidebar(sel.id)
  } catch (e) {
    report(e)
  }
}

export async function loadSidebar(serverId: number, teamId = '', openSelected = false) {
  const my = ++sidebarSeq
  try {
    const sb = await client.sidebar(serverId, teamId)
    if (my !== sidebarSeq) return
    useStore.getState().setSidebar(serverId, sb)
    const nothingOpen = !wanted || wanted.serverId !== serverId
    if ((openSelected || nothingOpen) && sb.selected_channel_id) await openChannel(serverId, sb.selected_channel_id)
  } catch (e) {
    if (my === sidebarSeq) report(e)
  }
}

export async function openChannel(serverId: number, channelId: string) {
  wanted = { serverId, channelId }
  useStore.getState().setEditing(null)
  await fetchChannel(serverId, channelId, true)
}

// refreshChannel re-reads the open channel after a channel_changed event.
export async function refreshChannel(serverId: number, channelId: string) {
  if (wanted?.serverId !== serverId || wanted.channelId !== channelId) return
  if (inFlight) {
    again = true // fetched right after the request in flight
    return
  }
  await fetchChannel(serverId, channelId, false)
}

async function fetchChannel(serverId: number, channelId: string, open: boolean) {
  const my = ++channelSeq
  inFlight = true
  again = false
  try {
    const ch = open ? await client.openChannel(serverId, channelId) : await client.getChannel(serverId, channelId)
    if (my !== channelSeq) return
    const s = useStore.getState()
    s.setChannel(serverId, ch)
    // Opened a channel of another team (e.g. from a notification): follow it.
    if (open && ch.team_id && s.sidebar && s.sidebar.team_id !== ch.team_id) void loadSidebar(serverId, ch.team_id)
  } catch (e) {
    if (my === channelSeq) report(e)
  } finally {
    if (my === channelSeq) {
      inFlight = false
      if (again) {
        again = false
        void refreshChannel(serverId, channelId)
      }
    }
  }
}

export function openFromNotification(serverId: number, channelId: string) {
  if (useStore.getState().selectedId !== serverId) selectServer(serverId)
  void openChannel(serverId, channelId)
}
```

Run: `cd frontend && pnpm test src/store.test.ts src/chat.test.ts`
Expected: PASS.

- [ ] **Step 4: Строки i18n**

В `frontend/src/i18n.ts` добавить в `ru`:
```ts
  'app.dismiss': 'Скрыть',
  'rail.unread': 'Есть непрочитанные',
  'rail.mentions': 'Упоминаний: {n}',
  'sidebar.label': 'Каналы сервера',
  'sidebar.menu': 'Меню сервера',
  'sidebar.teams': 'Команды',
  'sidebar.loading': 'Загрузка…',
  'cat.favorites': 'Избранное',
  'cat.channels': 'Каналы',
  'cat.dms': 'Личные сообщения',
  'status.connecting': 'Подключение…',
  'status.reconnecting': 'Нет связи — переподключаюсь…',
  'status.needsReauth': 'Сессия истекла',
  'status.signInAgain': 'Войти снова',
  'channel.none': 'Выберите канал',
  'channel.sessionExpired': 'Сессия истекла — показаны сохранённые сообщения.',
  'server.reauthHint': 'Сессия на этом сервере истекла — войдите снова',
  'server.back': 'Назад',
  'err.not_signed_in': 'Вы не вошли на этот сервер',
  'err.session_expired': 'Сессия истекла — войдите снова',
  'err.no_channel': 'Канал не найден',
  'err.empty_message': 'Пустое сообщение',
  'err.forbidden': 'Нет прав на это действие',
```
и в `en`:
```ts
  'app.dismiss': 'Dismiss',
  'rail.unread': 'Unread messages',
  'rail.mentions': 'Mentions: {n}',
  'sidebar.label': 'Server channels',
  'sidebar.menu': 'Server menu',
  'sidebar.teams': 'Teams',
  'sidebar.loading': 'Loading…',
  'cat.favorites': 'Favorites',
  'cat.channels': 'Channels',
  'cat.dms': 'Direct messages',
  'status.connecting': 'Connecting…',
  'status.reconnecting': 'Offline — reconnecting…',
  'status.needsReauth': 'Session expired',
  'status.signInAgain': 'Sign in again',
  'channel.none': 'Pick a channel',
  'channel.sessionExpired': 'Session expired — showing saved messages.',
  'server.reauthHint': 'Your session on this server expired — sign in again',
  'server.back': 'Back',
  'err.not_signed_in': 'You are not signed in to this server',
  'err.session_expired': 'Session expired — sign in again',
  'err.no_channel': 'Channel not found',
  'err.empty_message': 'The message is empty',
  'err.forbidden': 'You are not allowed to do that',
```

- [ ] **Step 5: Падающие тесты компонентов**

`frontend/src/components/ServerRail.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { ServerDTO } from '../api/types'
import { setLocale } from '../i18n'
import { ServerRail } from './ServerRail'

const srv = (o: Partial<ServerDTO>): ServerDTO => ({
  id: 1, name: 'Acme', url: 'u', signed_in: true, username: 'a', gitlab: false, state: 'live', unread: false, mentions: 0, ...o,
})

beforeEach(() => setLocale('en'))

test('mentions pill beats the unread dot; 99+ cap', async () => {
  const onSelect = vi.fn()
  render(<ServerRail servers={[srv({ mentions: 3, unread: true }), srv({ id: 2, name: 'Beta', unread: true }), srv({ id: 3, name: 'Gamma', mentions: 150 })]} selectedId={1} onSelect={onSelect} />)
  expect(screen.getByLabelText('Mentions: 3')).toHaveTextContent('3')
  expect(screen.getAllByLabelText('Unread messages')).toHaveLength(1)
  expect(screen.getByLabelText('Mentions: 150')).toHaveTextContent('99+')
  await userEvent.click(screen.getByRole('button', { name: 'Beta' }))
  expect(onSelect).toHaveBeenCalledWith(2)
})

test('connection problems are visible on the server button', () => {
  render(<ServerRail servers={[srv({ state: 'reconnecting' }), srv({ id: 2, name: 'Beta', state: 'needs_reauth' })]} selectedId={1} onSelect={() => {}} />)
  expect(screen.getByRole('button', { name: 'Acme' })).toHaveAttribute('title', 'Acme — Offline — reconnecting…')
  expect(screen.getByRole('button', { name: 'Beta' })).toHaveAttribute('title', 'Beta — Session expired')
})
```

`frontend/src/components/Sidebar.test.tsx`:
```tsx
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { ServerDTO, SidebarDTO } from '../api/types'
import { setLocale } from '../i18n'
import { Sidebar } from './Sidebar'

const server: ServerDTO = { id: 1, name: 'Acme', url: 'u', signed_in: true, username: 'alice', gitlab: false, state: 'live', unread: true, mentions: 1 }

const sb: SidebarDTO = {
  team_id: 't1',
  selected_channel_id: 'c-town',
  teams: [
    { id: 't1', name: 'one', display_name: 'One', unread: true, mentions: 1 },
    { id: 't2', name: 'two', display_name: 'Two', unread: true, mentions: 0 },
  ],
  categories: [
    { id: 'fav', type: 'favorites', name: 'Favorites', collapsed: false, channels: null },
    {
      id: 'ch', type: 'channels', name: 'Channels', collapsed: false,
      channels: [
        { id: 'c-town', name: 'Town Square', type: 'O', unread: false, mentions: 0, muted: false },
        { id: 'c-off', name: 'Off-Topic', type: 'O', unread: true, mentions: 1, muted: false },
        { id: 'c-noise', name: 'Noise', type: 'O', unread: false, mentions: 0, muted: true },
      ],
    },
    {
      id: 'my', type: 'custom', name: 'Work', collapsed: true,
      channels: [
        { id: 'c-a', name: 'Quiet', type: 'P', unread: false, mentions: 0, muted: false },
        { id: 'c-b', name: 'Busy', type: 'P', unread: true, mentions: 0, muted: false },
      ],
    },
    { id: 'dm', type: 'direct_messages', name: 'Direct Messages', collapsed: false, channels: [{ id: 'c-dm', name: 'bob', type: 'D', unread: false, mentions: 0, muted: false }] },
  ],
}

function renderSidebar(over: Partial<Parameters<typeof Sidebar>[0]> = {}) {
  const props = {
    server, sidebar: sb, activeChannelId: 'c-town',
    onTeam: vi.fn(), onChannel: vi.fn(), onSignOut: vi.fn(), onRemove: vi.fn(), onReauth: vi.fn(),
    ...over,
  }
  render(<Sidebar {...props} />)
  return props
}

beforeEach(() => setLocale('en'))

test('categories: localized default names, custom as is, collapsed shows only unread', async () => {
  const p = renderSidebar()
  expect(screen.getByRole('button', { name: /Channels/ })).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByRole('button', { name: /Direct messages/ })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /Work/ })).toHaveAttribute('aria-expanded', 'false')
  expect(screen.queryByRole('button', { name: /Quiet/ })).toBeNull()
  expect(screen.getByRole('button', { name: /Busy/ })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /Work/ }))
  expect(screen.getByRole('button', { name: /Quiet/ })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /Off-Topic/ }))
  expect(p.onChannel).toHaveBeenCalledWith('c-off')
})

test('unread, mentions, muted and active are visible', () => {
  renderSidebar()
  const off = screen.getByRole('button', { name: /Off-Topic/ })
  expect(off).toHaveClass('font-semibold')
  expect(within(off).getByLabelText('Mentions: 1')).toHaveTextContent('1')
  expect(screen.getByRole('button', { name: /Noise/ })).toHaveClass('opacity-50')
  expect(screen.getByRole('button', { name: /Town Square/ })).toHaveAttribute('aria-current', 'true')
})

test('team switcher appears with more than one team', async () => {
  const p = renderSidebar()
  await userEvent.click(screen.getByRole('button', { name: /Two/ }))
  expect(p.onTeam).toHaveBeenCalledWith('t2')
})

test('needs_reauth shows the sign-in-again action', async () => {
  const p = renderSidebar({ server: { ...server, state: 'needs_reauth' } })
  expect(screen.getByText('Session expired')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Sign in again' }))
  expect(p.onReauth).toHaveBeenCalled()
})

test('server menu signs out and removes', async () => {
  const p = renderSidebar()
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  await userEvent.click(screen.getByRole('menuitem', { name: 'Sign out' }))
  expect(p.onSignOut).toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  await userEvent.click(screen.getByRole('menuitem', { name: 'Remove server' }))
  expect(p.onRemove).toHaveBeenCalled()
})
```

В `ServerPanel.test.tsx` добавить:
```tsx
test('re-auth mode shows the form over a signed-in server and can go back', async () => {
  const onCancel = vi.fn()
  render(<ServerPanel server={{ ...base, signed_in: true, username: 'alice', state: 'needs_reauth' }} client={fakeClient()} reauth onCancel={onCancel} />)
  expect(screen.getByText('Your session on this server expired — sign in again')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Back' }))
  expect(onCancel).toHaveBeenCalled()
})
```

`frontend/src/App.test.tsx` (целиком — старый тест «servers_changed clears a stale login error» больше не верен):
```tsx
import { act, render, screen } from '@testing-library/react'
import { vi } from 'vitest'
import type { ApiEvent, ChannelDTO, ServerDTO, SidebarDTO } from './api/types'
import { setLocale } from './i18n'
import { useStore } from './store'

const h = vi.hoisted(() => ({ emit: null as ((e: ApiEvent) => void) | null, list: [] as unknown[] }))

const sb: SidebarDTO = {
  team_id: 't1', selected_channel_id: 'c-town', teams: [],
  categories: [{ id: 'ch', type: 'channels', name: 'Channels', collapsed: false, channels: [{ id: 'c-town', name: 'Town Square', type: 'O', unread: false, mentions: 0, muted: false }] }],
}
const town: ChannelDTO = {
  id: 'c-town', name: 'Town Square', type: 'O', header: 'Everything', purpose: '', team_id: 't1', team_name: 'one', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'u-alice', crt: false, muted: false,
}

vi.mock('./api/client', async (orig) => {
  const real = await orig<typeof import('./api/client')>()
  return {
    ...real,
    client: {
      ...real.httpClient,
      appInfo: vi.fn().mockResolvedValue({ format_locale: '' }),
      listServers: vi.fn(async () => h.list),
      selectServer: vi.fn().mockResolvedValue(undefined),
      setFocused: vi.fn().mockResolvedValue(undefined),
      sidebar: vi.fn(async () => sb),
      openChannel: vi.fn(async () => town),
      getChannel: vi.fn(async () => town),
      subscribeEvents: (fn: (e: ApiEvent) => void) => {
        h.emit = fn
        return () => {}
      },
    },
  }
})

const { App } = await import('./App')
const { resetChat } = await import('./chat')

const srv = (o: Partial<ServerDTO> = {}): ServerDTO => ({
  id: 1, name: 'Acme', url: 'https://mm.acme', signed_in: false, username: '', gitlab: false,
  state: 'off', unread: false, mentions: 0, ...o,
})

beforeEach(() => {
  setLocale('en')
  resetChat()
  useStore.setState({ servers: [], selectedId: null, adding: false, lastError: null, signInFor: null, sidebar: null, channel: null })
})

test('a login error survives status updates and clears when the sign-in state changes', async () => {
  h.list = [srv()]
  render(<App />)
  await act(async () => h.emit!({ type: 'login_failed', payload: { code: 'no_pending_login' } }))
  expect(screen.getByRole('alert')).toBeInTheDocument()
  await act(async () => h.emit!({ type: 'servers_changed' }))
  expect(screen.getByRole('alert')).toBeInTheDocument()
  h.list = [srv({ signed_in: true, username: 'alice', state: 'connecting' })]
  await act(async () => h.emit!({ type: 'servers_changed' }))
  expect(screen.queryByRole('alert')).toBeNull()
})

test('a signed-in server shows its sidebar and the selected channel', async () => {
  h.list = [srv({ signed_in: true, username: 'alice', state: 'live' })]
  render(<App />)
  expect(await screen.findByRole('heading', { name: /Town Square/ })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /Town Square/ })).toHaveAttribute('aria-current', 'true')
})

test('needs_reauth keeps the chat and offers the sign-in form', async () => {
  h.list = [srv({ signed_in: true, username: 'alice', state: 'needs_reauth' })]
  render(<App />)
  await screen.findByRole('heading', { name: /Town Square/ })
  expect(screen.getByText('Session expired — showing saved messages.')).toBeInTheDocument()
  await act(async () => screen.getAllByRole('button', { name: 'Sign in again' })[0].click())
  expect(screen.getByText('Your session on this server expired — sign in again')).toBeInTheDocument()
})
```

Run: `cd frontend && pnpm test`
Expected: FAIL (нет `Sidebar`, `ChannelPane`, новых пропсов).

- [ ] **Step 6: Компоненты**

`frontend/src/components/glyph.ts`:
```ts
// Channel type marker shown before names (no icon font in stage 2).
export function channelGlyph(type: string): string {
  switch (type) {
    case 'P':
      return '🔒'
    case 'D':
      return '@'
    case 'G':
      return '👥'
    default:
      return '#'
  }
}
```

`frontend/src/components/ServerRail.tsx` (целиком):
```tsx
import type { ServerDTO } from '../api/types'
import { t } from '../i18n'

const stateLabel: Partial<Record<ServerDTO['state'], Parameters<typeof t>[0]>> = {
  connecting: 'status.connecting',
  reconnecting: 'status.reconnecting',
  needs_reauth: 'status.needsReauth',
}

export function ServerRail(props: { servers: ServerDTO[]; selectedId: number | null; onSelect: (id: number | null) => void }) {
  return (
    <nav className="flex w-16 shrink-0 flex-col items-center gap-3 bg-neutral-800 py-3">
      {props.servers.map((s) => {
        const problem = s.signed_in ? stateLabel[s.state] : undefined
        const dim = !s.signed_in || s.state === 'needs_reauth'
        return (
          <div key={s.id} className="relative">
            <button
              title={problem ? `${s.name} — ${t(problem)}` : s.name}
              aria-label={s.name}
              aria-current={s.id === props.selectedId}
              onClick={() => props.onSelect(s.id)}
              className={`h-11 w-11 rounded-xl text-sm font-semibold text-white ${s.id === props.selectedId ? 'bg-blue-600' : 'bg-neutral-600'} ${dim ? 'opacity-60' : ''} ${s.state === 'reconnecting' ? 'ring-2 ring-amber-400' : ''}`}
            >
              {s.name.slice(0, 2).toUpperCase()}
            </button>
            {s.mentions > 0 ? (
              <span
                aria-label={t('rail.mentions', { n: String(s.mentions) })}
                className="absolute -right-1.5 -top-1.5 min-w-5 rounded-full bg-red-600 px-1 text-center text-[10px] font-bold leading-5 text-white"
              >
                {s.mentions > 99 ? '99+' : s.mentions}
              </span>
            ) : s.unread ? (
              <span aria-label={t('rail.unread')} className="absolute -left-2 top-1/2 h-2 w-2 -translate-y-1/2 rounded-full bg-white" />
            ) : null}
          </div>
        )
      })}
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

`frontend/src/components/Sidebar.tsx`:
```tsx
import { useState } from 'react'
import type { CategoryView, ChannelItem, ServerDTO, SidebarDTO } from '../api/types'
import { t } from '../i18n'
import { channelGlyph } from './glyph'

interface Props {
  server: ServerDTO
  sidebar: SidebarDTO | null
  activeChannelId: string | null
  onTeam(teamId: string): void
  onChannel(channelId: string): void
  onSignOut(): void
  onRemove(): void
  onReauth(): void
}

function categoryName(c: CategoryView): string {
  switch (c.type) {
    case 'favorites':
      return t('cat.favorites')
    case 'channels':
      return t('cat.channels')
    case 'direct_messages':
      return t('cat.dms')
    default:
      return c.name
  }
}

function MentionPill({ n }: { n: number }) {
  return (
    <span aria-label={t('rail.mentions', { n: String(n) })} className="ml-auto rounded-full bg-red-600 px-1.5 text-[10px] font-bold leading-4 text-white">
      {n > 99 ? '99+' : n}
    </span>
  )
}

function ChannelRow({ item, active, onClick }: { item: ChannelItem; active: boolean; onClick(): void }) {
  const tone = active ? 'bg-blue-700 text-white' : item.unread ? 'font-semibold text-white' : 'text-neutral-400'
  return (
    <button
      aria-current={active}
      onClick={onClick}
      className={`flex w-full items-center gap-2 rounded px-3 py-1 text-left hover:bg-neutral-800 ${tone} ${item.muted ? 'opacity-50' : ''}`}
    >
      <span className="w-4 shrink-0 text-center text-xs opacity-70">{channelGlyph(item.type)}</span>
      <span className="truncate">{item.name}</span>
      {item.mentions > 0 && <MentionPill n={item.mentions} />}
    </button>
  )
}

function StatusLine({ state, onReauth }: { state: ServerDTO['state']; onReauth(): void }) {
  if (state === 'connecting' || state === 'reconnecting') {
    return <p role="status" className="px-3 pb-2 text-xs text-amber-300">{t(state === 'connecting' ? 'status.connecting' : 'status.reconnecting')}</p>
  }
  if (state === 'needs_reauth') {
    return (
      <p role="status" className="flex items-center gap-2 px-3 pb-2 text-xs text-amber-300">
        {t('status.needsReauth')}
        <button className="rounded bg-amber-500 px-2 py-0.5 font-medium text-neutral-900" onClick={onReauth}>
          {t('status.signInAgain')}
        </button>
      </p>
    )
  }
  return null
}

export function Sidebar(p: Props) {
  const [menu, setMenu] = useState(false)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const teams = p.sidebar?.teams ?? []
  return (
    <aside aria-label={t('sidebar.label')} className="flex w-64 shrink-0 flex-col bg-neutral-900 text-sm text-neutral-200">
      <header className="relative flex items-center justify-between gap-2 px-3 py-2">
        <div className="min-w-0">
          <div className="truncate font-semibold text-white">{p.server.name}</div>
          <div className="truncate text-xs text-neutral-400">@{p.server.username}</div>
        </div>
        <button aria-label={t('sidebar.menu')} aria-expanded={menu} className="rounded px-2 text-lg hover:bg-neutral-800" onClick={() => setMenu(!menu)}>
          ⋯
        </button>
        {menu && (
          <div role="menu" className="absolute right-2 top-11 z-10 flex w-44 flex-col rounded border border-neutral-700 bg-neutral-800 py-1 shadow-lg">
            <button role="menuitem" className="px-3 py-1.5 text-left hover:bg-neutral-700" onClick={() => { setMenu(false); p.onSignOut() }}>
              {t('server.signOut')}
            </button>
            <button role="menuitem" className="px-3 py-1.5 text-left text-red-400 hover:bg-neutral-700" onClick={() => { setMenu(false); p.onRemove() }}>
              {t('server.remove')}
            </button>
          </div>
        )}
      </header>
      <StatusLine state={p.server.state} onReauth={p.onReauth} />
      {teams.length > 1 && (
        <nav aria-label={t('sidebar.teams')} className="flex flex-wrap gap-1 px-2 pb-2">
          {teams.map((tm) => (
            <button
              key={tm.id}
              aria-current={tm.id === p.sidebar?.team_id}
              onClick={() => p.onTeam(tm.id)}
              className={`flex items-center gap-1 rounded px-2 py-0.5 text-xs ${tm.id === p.sidebar?.team_id ? 'bg-neutral-700 text-white' : tm.unread ? 'font-semibold text-white' : 'text-neutral-400'}`}
            >
              {tm.display_name}
              {tm.mentions > 0 && <MentionPill n={tm.mentions} />}
            </button>
          ))}
        </nav>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto pb-4">
        {!p.sidebar && <p className="px-3 py-2 text-neutral-500">{t('sidebar.loading')}</p>}
        {(p.sidebar?.categories ?? []).map((cat) => {
          const isCollapsed = collapsed[cat.id] ?? cat.collapsed
          const all = cat.channels ?? []
          const shown = isCollapsed ? all.filter((c) => c.unread || c.id === p.activeChannelId) : all
          return (
            <section key={cat.id} className="mt-3">
              <button
                aria-expanded={!isCollapsed}
                onClick={() => setCollapsed({ ...collapsed, [cat.id]: !isCollapsed })}
                className="flex w-full items-center gap-1 px-3 py-0.5 text-left text-xs font-semibold uppercase tracking-wide text-neutral-500 hover:text-neutral-300"
              >
                <span className="w-3">{isCollapsed ? '▸' : '▾'}</span>
                {categoryName(cat)}
              </button>
              <ul>
                {shown.map((c) => (
                  <li key={c.id}>
                    <ChannelRow item={c} active={c.id === p.activeChannelId} onClick={() => p.onChannel(c.id)} />
                  </li>
                ))}
              </ul>
            </section>
          )
        })}
      </div>
    </aside>
  )
}
```

`frontend/src/components/ChannelPane.tsx`:
```tsx
import type { ChannelDTO, ServerDTO } from '../api/types'
import { t } from '../i18n'
import { channelGlyph } from './glyph'

export function ChannelPane({ server, channel, onReauth }: { server: ServerDTO; channel: ChannelDTO | null; onReauth(): void }) {
  if (!channel) {
    return <div className="flex flex-1 items-center justify-center text-neutral-500">{t('channel.none')}</div>
  }
  return (
    <section aria-label={channel.name} className="flex min-h-0 flex-1 flex-col">
      <header className="flex min-w-0 items-baseline gap-3 border-b border-neutral-200 px-4 py-2">
        <h1 className="shrink-0 font-semibold">
          <span className="mr-1 text-neutral-400">{channelGlyph(channel.type)}</span>
          {channel.name}
        </h1>
        {channel.header && (
          <p className="truncate text-xs text-neutral-500" title={channel.header}>
            {channel.header}
          </p>
        )}
      </header>
      {server.state === 'needs_reauth' && (
        <div role="status" className="flex items-center gap-3 bg-amber-50 px-4 py-1.5 text-sm text-amber-900">
          {t('channel.sessionExpired')}
          <button className="font-medium underline" onClick={onReauth}>
            {t('status.signInAgain')}
          </button>
        </div>
      )}
      <div className="min-h-0 flex-1" />
    </section>
  )
}
```

`frontend/src/components/ServerPanel.tsx`: сигнатура
```tsx
export function ServerPanel({ server, client, loginFailures = 0, reauth = false, onCancel }: {
  server: ServerDTO; client: Client; loginFailures?: number; reauth?: boolean; onCancel?: () => void
}) {
```
условие показа формы `server.signed_in && !reauth ? (…вы вошли…) : (…форма…)`; в начало ветки формы:
```tsx
          {reauth ? (
            <p className="text-sm text-amber-700">{t('server.reauthHint')}</p>
          ) : (
            <p className="text-sm text-neutral-500">{t('server.signedOut')}</p>
          )}
```
и перед кнопкой «Удалить сервер»:
```tsx
      {onCancel && (
        <button className="self-start text-sm underline" onClick={onCancel}>
          {t('server.back')}
        </button>
      )}
```

`frontend/src/App.tsx` (целиком):
```tsx
import { useEffect } from 'react'
import { ApiError, client, isDesktop } from './api/client'
import type { ServerDTO } from './api/types'
import { loadSidebar, openChannel, openFromNotification, refreshChannel, refreshServers, report, selectServer } from './chat'
import { AddServerForm } from './components/AddServerForm'
import { ChannelPane } from './components/ChannelPane'
import { ServerPanel } from './components/ServerPanel'
import { ServerRail } from './components/ServerRail'
import { Sidebar } from './components/Sidebar'
import { errorMessage } from './errors'
import { t } from './i18n'
import { useStore } from './store'

export function App() {
  const { servers, selectedId, lastError, loginFailures, signInFor, sidebar, channel, setError, loginFailed, setInfo, showSignIn } = useStore()

  useEffect(() => {
    client.appInfo().then(setInfo).catch(() => {})
    void refreshServers()
    return client.subscribeEvents((ev) => {
      const p = ev.payload ?? {}
      const s = useStore.getState()
      switch (ev.type) {
        case 'servers_changed':
          void refreshServers()
          break
        case 'sidebar_changed':
          if (p.server_id === s.selectedId) void loadSidebar(Number(p.server_id), s.sidebar?.team_id ?? '')
          break
        case 'channel_changed':
          void refreshChannel(Number(p.server_id), String(p.channel_id))
          break
        case 'open_channel':
          openFromNotification(Number(p.server_id), String(p.channel_id))
          break
        case 'login_failed':
          loginFailed(errorMessage(new ApiError(String(p.code ?? 'internal'), '')))
          break
        case 'open_external':
          // Browser mode is dev/e2e only: window.open without a user gesture is
          // allowed under Playwright (popup blocking off) but may be blocked in a
          // regular browser — acceptable there. Desktop opens the OS browser in Go.
          if (!isDesktop()) window.open(String(p.url), '_blank')
          break
      }
    })
  }, [setInfo, loginFailed])

  const selected = servers.find((s) => s.id === selectedId)
  const chat = selected && selected.signed_in && signInFor !== selected.id
  const signOut = (s: ServerDTO) => client.logout(s.id).catch(report)
  const remove = (s: ServerDTO) => {
    if (confirm(t('server.removeConfirm', { name: s.name }))) client.removeServer(s.id).catch(report)
  }
  const banner = lastError && (
    <p role="alert" className="flex items-center justify-between bg-red-50 px-4 py-2 text-sm text-red-700">
      {lastError}
      <button aria-label={t('app.dismiss')} className="px-2" onClick={() => setError(null)}>
        ×
      </button>
    </p>
  )

  return (
    <div className="flex h-screen text-sm text-neutral-900">
      <ServerRail servers={servers} selectedId={selectedId} onSelect={selectServer} />
      {chat ? (
        <>
          <Sidebar
            server={selected}
            sidebar={sidebar}
            activeChannelId={channel?.id ?? null}
            onTeam={(teamId) => void loadSidebar(selected.id, teamId, true)}
            onChannel={(channelId) => void openChannel(selected.id, channelId)}
            onSignOut={() => void signOut(selected)}
            onRemove={() => remove(selected)}
            onReauth={() => showSignIn(selected.id)}
          />
          <main className="flex min-w-0 flex-1 flex-col">
            {banner}
            <ChannelPane server={selected} channel={channel} onReauth={() => showSignIn(selected.id)} />
          </main>
        </>
      ) : (
        <main className="flex-1 overflow-auto">
          {banner}
          {selected ? (
            <ServerPanel
              key={selected.id}
              server={selected}
              client={client}
              loginFailures={loginFailures}
              reauth={signInFor === selected.id}
              onCancel={signInFor === selected.id ? () => showSignIn(null) : undefined}
            />
          ) : (
            <AddServerForm add={client.addServer} onAdded={selectServer} />
          )}
        </main>
      )}
    </div>
  )
}
```
(`AddServerForm.onAdded` после добавления вызывает `selectServer(id)`; сервер ещё без входа — сайдбар не грузится, показывается форма входа.)

- [ ] **Step 7: Тесты, типы, линт**

Run: `cd frontend && pnpm test && pnpm build && pnpm lint`
Expected: PASS; `tsc` без ошибок.

- [ ] **Step 8: Проверка глазами (browser mode)**

Run: `make build && SPK_MATTERMOST_HOME=$(mktemp -d) build/bin/spk-mattermost --browser --port 5182 --mm-fake --test-api` (порт проверить `ss -lptn 'sport = :5182'`), в браузере через Playwright: добавить сервер из `/api/_test/fake-url`, войти alice/secret, дождаться сайдбара, сделать скриншот `/tmp/spkmm-stage2-t12.png` и посмотреть его. Ожидается: рейл с сервером, сайдбар (Каналы: Town Square, Off-Topic, Secret; Личные сообщения: bob, группа), шапка Town Square. Отправить через test-API `fake/post` в `c-offtopic` с `@alice` — в рейле и сайдбаре появляется «1».

- [ ] **Step 9: Коммит**

```bash
git add frontend
git commit -m "frontend: chat API client, navigation store with stale-response guards, server badges, sidebar with categories"
```

---

### Task 13: Лента — строки, markdown, пост, виртуализация, история

**Files:**
- Modify: `frontend/package.json` (+ `@tanstack/react-virtual`, `react-markdown`, `remark-gfm`, `remark-breaks`), `frontend/pnpm-lock.yaml`
- Create: `frontend/src/format.ts`, `frontend/src/format.test.ts`
- Create: `frontend/src/components/feedRows.ts`, `frontend/src/components/feedRows.test.ts`
- Create: `frontend/src/components/remarkMentions.ts`, `frontend/src/components/Markdown.tsx`, `frontend/src/components/Markdown.test.tsx`
- Create: `frontend/src/components/emoji.ts`, `frontend/src/components/PostItem.tsx`, `frontend/src/components/PostItem.test.tsx`
- Create: `frontend/src/components/Feed.tsx`, `frontend/src/components/Feed.test.tsx`
- Modify: `frontend/src/components/ChannelPane.tsx`, `frontend/src/chat.ts` (+ `openLink`, `loadOlder`, `retryPost`, `discardPost`), `frontend/src/i18n.ts`, `frontend/src/index.css`

**Interfaces:**
- Consumes: Task 12 (`ChannelDTO`, `PostView`, `Attachment`, `client.loadOlder/openURL/retryPost/discardPost`, `refreshChannel`, `report`, `useStore().info`).
- Produces:
  - `format.ts`: `formatLocale(): string` (`info.format_locale` или `navigator.language`), `formatTime(ms, locale)`, `formatDay(ms, locale, now = Date.now())` («Сегодня»/«Вчера»/«воскресенье, 20 сентября»[ + год]), `formatSize(bytes)`.
  - `feedRows.ts`: `type Row = { kind: 'more'; key: 'more' } | { kind: 'day'; key: string; ms: number } | { kind: 'new'; key: 'new' } | { kind: 'gap'; key: 'gap' } | { kind: 'post'; key: string; post: PostView; head: boolean }`; `buildRows(ch: Pick<ChannelDTO, 'posts' | 'new_since' | 'me_id' | 'gap_after' | 'has_more'>): Row[]`.
  - `remarkMentions.ts`: `splitMentions(text): Part[]` (`{ text } | { mention, raw }`), `remarkMentions()` — remark-плагин.
  - `Markdown({ text, me, onLink })` (memo).
  - `PostItem({ post, head, me, locale, crt, actions })` (memo); `interface PostActions { link(href: string): void; retry(post: PostView): void; discard(post: PostView): void }` — Task 14 расширит.
  - `Feed({ channel, me, locale, actions, onLoadOlder })`, `onLoadOlder(): Promise<boolean>` (true — страница загружена).
  - `chat.ts`: `openLink(href)`, `loadOlder(serverId, channelId): Promise<boolean>`, `retryPost(serverId, channelId, pendingId)`, `discardPost(serverId, channelId, pendingId)`.
  - `emoji.ts`: `emojiFor(name: string): string` (частые имена → символ, иначе `:name:`).

Правила ленты:
- Разделитель дня — перед первым постом каждого локального дня. Посты одного автора подряд в пределах 5 минут группируются (без аватара и имени); группу рвут: другой автор (или другое имя у вебхука), пауза > 5 мин, системный пост, новый день, линия «новые», дыра.
- Линия «Новые сообщения» — один раз, перед первым постом **не от меня** с `create_at > new_since` (и не pending/failed); `new_since = 0` — линии нет.
- Строка «дыры» — сразу после поста `gap_after` («Загружаю пропущенные сообщения…»); строка «more» сверху, если `has_more`.
- Первое появление строк: прокрутка к линии «новые», иначе в самый низ. Потом: если пользователь внизу (≤ 48 px) — держаться низа; при появлении своего pending-поста — вниз; после подгрузки истории — пост, бывший верхним, остаётся на месте. Прокрутка к верху (< 300 px) подгружает историю, одновременно не больше одного запроса; если лента короче окна и есть история — подгрузить сразу.
- Markdown: GFM (таблицы, зачёркивание, списки задач), **одиночный перевод строки — перенос** (как в Mattermost, `remark-breaks`), сырой HTML не рендерится (`skipHtml`), картинки не загружаются — ссылка «🖼 alt», все ссылки открываются через `OpenURL` в системном браузере. `@имя` подсвечивается; `@моё_имя`, `@channel`, `@here`, `@all` — ярче. Внутри кода и ссылок упоминания не трогаются.
- Вложения ботов: цветная полоса (`good`/`warning`/`danger` или `#hex`, иначе серая), pretext, автор, заголовок (ссылка, если есть), текст и поля (markdown; `short` — в две колонки), footer. Файлы — карточка «📎 имя размер» (скачивание — этап 3). Реакции — чипы «эмодзи счётчик», свои выделены (ставить — этап 3). Под CRT у корня — «Ответов: N».

- [ ] **Step 1: Зависимости**

Run: `cd frontend && pnpm add @tanstack/react-virtual@^3.14.13 react-markdown@^10.1.0 remark-gfm@^4.0.1 remark-breaks@^4.0.0`
Expected: `package.json` и `pnpm-lock.yaml` обновлены.

- [ ] **Step 2: Падающие тесты — формат, строки, упоминания**

`frontend/src/format.test.ts`:
```ts
import { setLocale } from './i18n'
import { formatDay, formatSize, formatTime } from './format'

const at = (y: number, m: number, d: number, h = 12, min = 0) => new Date(y, m - 1, d, h, min).getTime()
const norm = (s: string) => s.replace(/\s/g, ' ')

beforeEach(() => setLocale('ru'))

test('time follows the format locale', () => {
  expect(formatTime(at(2026, 9, 24, 13, 5), 'ru-RU')).toBe('13:05')
  expect(norm(formatTime(at(2026, 9, 24, 13, 5), 'en-US'))).toBe('01:05 PM')
})

test('day labels: today, yesterday, weekday date, other year', () => {
  const now = at(2026, 9, 24, 9)
  expect(formatDay(at(2026, 9, 24, 23, 59), 'ru-RU', now)).toBe('Сегодня')
  expect(formatDay(at(2026, 9, 23, 0, 1), 'ru-RU', now)).toBe('Вчера')
  expect(formatDay(at(2026, 9, 20), 'ru-RU', now)).toBe('воскресенье, 20 сентября')
  expect(formatDay(at(2025, 12, 31), 'ru-RU', now)).toBe('среда, 31 декабря 2025 г.')
})

test('sizes', () => {
  expect(formatSize(512)).toBe('512 B')
  expect(formatSize(1536)).toBe('1.5 KB')
  expect(formatSize(3 * 1024 * 1024)).toBe('3.0 MB')
})
```

`frontend/src/components/feedRows.test.ts`:
```ts
import type { PostView } from '../api/types'
import { buildRows, type Row } from './feedRows'

const base = new Date(2026, 8, 24, 10, 0).getTime()
const P = (id: string, user: string, min: number, o: Partial<PostView> = {}): PostView => ({
  id, user_id: user, author: user, message: id, create_at: base + min * 60_000, ...o,
})
const ch = (posts: PostView[], o: Partial<{ new_since: number; gap_after: string; has_more: boolean }> = {}) => ({
  posts, new_since: 0, me_id: 'me', gap_after: '', has_more: false, ...o,
})
const shape = (rows: Row[]) => rows.map((r) => (r.kind === 'post' ? `${r.key}${r.head ? '*' : ''}` : r.kind))

test('groups one author within 5 minutes; author, pause, system and day break groups', () => {
  const rows = buildRows(ch([
    P('a1', 'bob', 0), P('a2', 'bob', 3), P('a3', 'bob', 9), P('b1', 'carol', 10),
    P('s1', 'carol', 11, { system: true }), P('b2', 'carol', 12), P('d1', 'carol', 24 * 60),
  ]))
  expect(shape(rows)).toEqual(['day', 'a1*', 'a2', 'a3*', 'b1*', 's1*', 'b2*', 'day', 'd1*'])
})

test('webhook posts of one user with different names are not grouped', () => {
  const rows = buildRows(ch([P('w1', 'hook', 0, { author: 'CI' }), P('w2', 'hook', 1, { author: 'Deploy' })]))
  expect(shape(rows)).toEqual(['day', 'w1*', 'w2*'])
})

test('new-messages line: before the first newer post from someone else, once', () => {
  const rows = buildRows(ch([P('old', 'bob', 0), P('mine', 'me', 2), P('n1', 'bob', 3), P('n2', 'bob', 4)], { new_since: base + 60_000 }))
  expect(shape(rows)).toEqual(['day', 'old*', 'mine*', 'new', 'n1*', 'n2'])
  expect(shape(buildRows(ch([P('x', 'bob', 5)])))).toEqual(['day', 'x*'])
})

test('pending own posts never get the line', () => {
  const rows = buildRows(ch([P('p', 'me', 5, { pending: true })], { new_since: base }))
  expect(shape(rows)).toEqual(['day', 'p*'])
})

test('history row on top, gap row after the last post before the loss', () => {
  const rows = buildRows(ch([P('a', 'bob', 0), P('b', 'bob', 1), P('c', 'bob', 2)], { has_more: true, gap_after: 'b' }))
  expect(shape(rows)).toEqual(['more', 'day', 'a*', 'b', 'gap', 'c*'])
})
```

`frontend/src/components/Markdown.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { Markdown } from './Markdown'
import { splitMentions } from './remarkMentions'

test('splitMentions finds usernames, keeps emails and trailing dots out', () => {
  expect(splitMentions('hi @alice, ask @Bob.jones. mail bob@example.com')).toEqual([
    { text: 'hi ' },
    { mention: 'alice', raw: 'alice' },
    { text: ', ask ' },
    { mention: 'bob.jones', raw: 'Bob.jones' },
    { text: '. mail bob@example.com' },
  ])
})

test('markdown: gfm, single newlines break, links via onLink, no raw html, no remote images', async () => {
  const onLink = vi.fn()
  const { container } = render(
    <Markdown
      text={'**bold** and [site](https://example.com)\nnext line\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n<b>raw</b> ![pic](https://evil.test/x.png)'}
      me="alice"
      onLink={onLink}
    />,
  )
  expect(screen.getByText('bold').tagName).toBe('STRONG')
  expect(container.querySelector('br')).not.toBeNull()
  expect(container.querySelector('table')).not.toBeNull()
  expect(container.querySelector('b')).toBeNull()
  expect(container.querySelector('img')).toBeNull()
  await userEvent.click(screen.getByRole('link', { name: 'site' }))
  expect(onLink).toHaveBeenCalledWith('https://example.com')
  await userEvent.click(screen.getByRole('link', { name: /pic/ }))
  expect(onLink).toHaveBeenCalledWith('https://evil.test/x.png')
})

test('mentions: me and @channel stand out, others are plain highlights, code is untouched', () => {
  const { container } = render(<Markdown text={'@alice @bob @channel `@alice`'} me="alice" onLink={() => {}} />)
  const spans = [...container.querySelectorAll('[data-mention]')]
  expect(spans.map((s) => s.getAttribute('data-mention'))).toEqual(['alice', 'bob', 'channel'])
  expect(spans[0]).toHaveClass('bg-amber-100')
  expect(spans[1]).not.toHaveClass('bg-amber-100')
  expect(spans[2]).toHaveClass('bg-amber-100')
  expect(container.querySelector('code')!.textContent).toBe('@alice')
})
```

Run: `cd frontend && pnpm test src/format.test.ts src/components/feedRows.test.ts src/components/Markdown.test.tsx`
Expected: FAIL (модулей нет).

- [ ] **Step 3: Реализация — формат, строки, markdown**

`frontend/src/format.ts`:
```ts
import { t } from './i18n'
import { useStore } from './store'

// Dates follow the system format locale (LC_TIME from Go), not the UI language.
export const formatLocale = () => useStore.getState().info.format_locale || navigator.language || 'en-US'

export function formatTime(ms: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit' }).format(ms)
}

const dayStart = (ms: number) => {
  const d = new Date(ms)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

export function formatDay(ms: number, locale: string, now = Date.now()): string {
  const days = Math.round((dayStart(now) - dayStart(ms)) / 86_400_000) // round: DST days are 23/25 h
  if (days === 0) return t('day.today')
  if (days === 1) return t('day.yesterday')
  const sameYear = new Date(ms).getFullYear() === new Date(now).getFullYear()
  return new Intl.DateTimeFormat(locale, { weekday: 'long', day: 'numeric', month: 'long', ...(sameYear ? {} : { year: 'numeric' }) }).format(ms)
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
```

`frontend/src/components/feedRows.ts`:
```ts
import type { ChannelDTO, PostView } from '../api/types'

export type Row =
  | { kind: 'more'; key: 'more' }
  | { kind: 'day'; key: string; ms: number }
  | { kind: 'new'; key: 'new' }
  | { kind: 'gap'; key: 'gap' }
  | { kind: 'post'; key: string; post: PostView; head: boolean }

const GROUP_MS = 5 * 60 * 1000
const dayKey = (ms: number) => new Date(ms).toDateString()

// buildRows turns the channel's posts (oldest first) into feed rows: day
// separators, the "new messages" line, the gap marker and author groups.
export function buildRows(ch: Pick<ChannelDTO, 'posts' | 'new_since' | 'me_id' | 'gap_after' | 'has_more'>): Row[] {
  const rows: Row[] = []
  if (ch.has_more) rows.push({ kind: 'more', key: 'more' })
  let prev: PostView | null = null
  let lineDone = false
  let broken = false // a gap row ended the previous group
  for (const p of ch.posts) {
    let head = true
    if (!prev || dayKey(prev.create_at) !== dayKey(p.create_at)) {
      rows.push({ kind: 'day', key: `day-${dayKey(p.create_at)}`, ms: p.create_at })
    } else {
      head =
        broken ||
        prev.user_id !== p.user_id ||
        prev.author !== p.author ||
        p.create_at - prev.create_at > GROUP_MS ||
        !!p.system ||
        !!prev.system
    }
    if (!lineDone && ch.new_since > 0 && p.create_at > ch.new_since && p.user_id !== ch.me_id && !p.pending && !p.failed) {
      rows.push({ kind: 'new', key: 'new' })
      lineDone = true
      head = true
    }
    rows.push({ kind: 'post', key: p.id, post: p, head })
    broken = false
    if (ch.gap_after && p.id === ch.gap_after) {
      rows.push({ kind: 'gap', key: 'gap' })
      broken = true
    }
    prev = p
  }
  return rows
}
```

`frontend/src/components/remarkMentions.ts`:
```ts
// A remark plugin: @username, @channel, @here, @all in text become
// <span data-mention="name">. Code and link texts are left alone.

interface MdNode {
  type: string
  value?: string
  children?: MdNode[]
  data?: Record<string, unknown>
}

export type Part = { text: string } | { mention: string; raw: string }

// Mattermost usernames: letters, digits, '.', '-', '_'. The character before
// '@' must not be part of a word or an address (bob@example.com).
const MENTION = /(^|[^\p{L}\p{N}_.@-])@([a-z0-9][a-z0-9._-]*)/giu

export function splitMentions(text: string): Part[] {
  const out: Part[] = []
  let last = 0
  for (const m of text.matchAll(MENTION)) {
    const raw = m[2].replace(/\.+$/, '') // "@bob." at the end of a sentence
    const at = m.index! + m[1].length
    if (at > last) out.push({ text: text.slice(last, at) })
    out.push({ mention: raw.toLowerCase(), raw })
    last = at + 1 + raw.length
  }
  if (last < text.length) out.push({ text: text.slice(last) })
  return out
}

function walk(node: MdNode) {
  if (!node.children || node.type === 'link' || node.type === 'linkReference') return
  const out: MdNode[] = []
  for (const child of node.children) {
    if (child.type === 'text' && child.value?.includes('@')) {
      for (const part of splitMentions(child.value)) {
        out.push(
          'mention' in part
            ? {
                type: 'mention',
                data: { hName: 'span', hProperties: { dataMention: part.mention } },
                children: [{ type: 'text', value: '@' + part.raw }],
              }
            : { type: 'text', value: part.text },
        )
      }
    } else {
      walk(child)
      out.push(child)
    }
  }
  node.children = out
}

export function remarkMentions() {
  return (tree: MdNode) => {
    walk(tree)
  }
}
```

`frontend/src/components/Markdown.tsx`:
```tsx
import { memo } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
import { remarkMentions } from './remarkMentions'

const plugins = [remarkGfm, remarkBreaks, remarkMentions]
const SPECIAL = new Set(['channel', 'here', 'all'])

function linkTo(href: string | undefined, onLink: (href: string) => void) {
  return (e: React.MouseEvent) => {
    e.preventDefault()
    if (href) onLink(href)
  }
}

// Markdown renders a message the way Mattermost does, without ever loading
// remote content: images become links, every link opens in the system browser.
export const Markdown = memo(function Markdown({ text, me, onLink }: { text: string; me: string; onLink(href: string): void }) {
  const components: Components = {
    a: ({ href, children }) => (
      <a href={href} title={href} className="text-blue-700 hover:underline" onClick={linkTo(href, onLink)}>
        {children}
      </a>
    ),
    img: ({ src, alt }) => {
      const href = typeof src === 'string' ? src : ''
      return (
        <a href={href} title={href} className="text-blue-700 hover:underline" onClick={linkTo(href, onLink)}>
          🖼 {alt || href}
        </a>
      )
    },
    span: (props) => {
      const name = (props as Record<string, unknown>)['data-mention']
      if (typeof name !== 'string') return <span className={props.className}>{props.children}</span>
      const loud = name === me.toLowerCase() || SPECIAL.has(name)
      return (
        <span data-mention={name} className={loud ? 'rounded bg-amber-100 px-0.5 font-medium text-amber-900' : 'font-medium text-blue-700'}>
          {props.children}
        </span>
      )
    },
    table: ({ children }) => (
      <div className="overflow-x-auto">
        <table>{children}</table>
      </div>
    ),
  }
  return (
    <div className="md break-words">
      <ReactMarkdown remarkPlugins={plugins} skipHtml components={components}>
        {text}
      </ReactMarkdown>
    </div>
  )
})
```

`frontend/src/index.css` (целиком):
```css
@import "tailwindcss";

/* Message markdown: Tailwind's preflight resets these elements. */
.md p { margin: 0.125rem 0; }
.md ul { list-style: disc; padding-left: 1.5rem; }
.md ol { list-style: decimal; padding-left: 1.5rem; }
.md li > input[type="checkbox"] { margin-right: 0.25rem; }
.md blockquote { border-left: 3px solid #d4d4d4; padding-left: 0.75rem; color: #525252; }
.md code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.85em; background: #f5f5f5; border-radius: 3px; padding: 0 0.25rem; }
.md pre { background: #f5f5f5; border-radius: 4px; padding: 0.5rem 0.75rem; overflow-x: auto; margin: 0.25rem 0; }
.md pre code { background: none; padding: 0; }
.md table { border-collapse: collapse; margin: 0.25rem 0; }
.md th, .md td { border: 1px solid #e5e5e5; padding: 0.125rem 0.5rem; }
.md th { background: #fafafa; font-weight: 600; }
.md h1 { font-size: 1.25rem; font-weight: 600; }
.md h2 { font-size: 1.125rem; font-weight: 600; }
.md h3, .md h4, .md h5, .md h6 { font-size: 1rem; font-weight: 600; }
.md hr { border-color: #e5e5e5; margin: 0.5rem 0; }
.md del { color: #737373; }
```

Строки i18n — в `ru`:
```ts
  'day.today': 'Сегодня',
  'day.yesterday': 'Вчера',
  'feed.label': 'Сообщения',
  'feed.new': 'Новые сообщения',
  'feed.gap': 'Загружаю пропущенные сообщения…',
  'feed.loadingOlder': 'Загрузка истории…',
  'feed.empty': 'Сообщений пока нет',
  'feed.loading': 'Загрузка сообщений…',
  'channel.syncing': 'Синхронизация…',
  'post.edited': '(изменено)',
  'post.replies': 'Ответов: {n}',
  'post.sending': 'Отправка…',
  'post.failed': 'Не отправлено.',
  'post.retry': 'Повторить',
  'post.discard': 'Удалить',
```
в `en`:
```ts
  'day.today': 'Today',
  'day.yesterday': 'Yesterday',
  'feed.label': 'Messages',
  'feed.new': 'New messages',
  'feed.gap': 'Loading missed messages…',
  'feed.loadingOlder': 'Loading history…',
  'feed.empty': 'No messages yet',
  'feed.loading': 'Loading messages…',
  'channel.syncing': 'Syncing…',
  'post.edited': '(edited)',
  'post.replies': 'Replies: {n}',
  'post.sending': 'Sending…',
  'post.failed': 'Not sent.',
  'post.retry': 'Retry',
  'post.discard': 'Discard',
```

Run: `cd frontend && pnpm test src/format.test.ts src/components/feedRows.test.ts src/components/Markdown.test.tsx`
Expected: PASS.

- [ ] **Step 4: Падающие тесты поста и ленты**

`frontend/src/components/PostItem.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { PostView } from '../api/types'
import { setLocale } from '../i18n'
import { PostItem, type PostActions } from './PostItem'

const post = (o: Partial<PostView> = {}): PostView => ({
  id: 'p1', user_id: 'u-bob', author: 'bob', message: 'hello', create_at: new Date(2026, 8, 24, 13, 5).getTime(), ...o,
})
const actions = (): PostActions => ({ link: vi.fn(), retry: vi.fn(), discard: vi.fn() })
const me = { id: 'u-alice', username: 'alice' }

beforeEach(() => setLocale('en'))

test('head shows author, time and bot badge; follow-up hides them', () => {
  const { rerender } = render(<PostItem post={post({ bot: true, edit_at: 1 })} head me={me} locale="ru-RU" crt={false} actions={actions()} />)
  expect(screen.getByText('bob')).toBeInTheDocument()
  expect(screen.getByText('BOT')).toBeInTheDocument()
  expect(screen.getAllByText('13:05')).toHaveLength(1)
  expect(screen.getByText('(edited)')).toBeInTheDocument()
  rerender(<PostItem post={post()} head={false} me={me} locale="ru-RU" crt={false} actions={actions()} />)
  expect(screen.queryByText('bob')).toBeNull()
})

test('attachments, files, reactions and reply count', async () => {
  const a = actions()
  render(
    <PostItem
      post={post({
        message: '',
        attachments: [{ color: 'danger', pretext: 'Build', title: 'Pipeline #7', title_link: 'https://ci/7', text: 'failed on **test**', fields: [{ title: 'Branch', value: 'main', short: true }] }],
        files: [{ name: 'report.pdf', size: 2048, mime: 'application/pdf' }],
        reactions: [{ emoji: '+1', count: 2, mine: true }, { emoji: 'custom_party', count: 1, mine: false }],
        reply_count: 3,
      })}
      head
      me={me}
      locale="en-US"
      crt
      actions={a}
    />,
  )
  expect(screen.getByText('test').tagName).toBe('STRONG')
  expect(screen.getByText('Branch')).toBeInTheDocument()
  expect(screen.getByText('report.pdf')).toBeInTheDocument()
  expect(screen.getByText('2.0 KB')).toBeInTheDocument()
  expect(screen.getByTitle(':+1:')).toHaveTextContent('👍 2')
  expect(screen.getByTitle(':custom_party:')).toHaveTextContent(':custom_party: 1')
  expect(screen.getByText('Replies: 3')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('link', { name: 'Pipeline #7' }))
  expect(a.link).toHaveBeenCalledWith('https://ci/7')
})

test('pending and failed posts', async () => {
  const a = actions()
  const { rerender } = render(<PostItem post={post({ pending: true, user_id: 'u-alice' })} head me={me} locale="en-US" crt={false} actions={a} />)
  expect(screen.getByText('Sending…')).toBeInTheDocument()
  const failed = post({ failed: true, user_id: 'u-alice' })
  rerender(<PostItem post={failed} head me={me} locale="en-US" crt={false} actions={a} />)
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(a.retry).toHaveBeenCalledWith(failed)
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
  expect(a.discard).toHaveBeenCalledWith(failed)
})
```

`frontend/src/components/Feed.test.tsx`:
```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { vi } from 'vitest'
import type { ChannelDTO, PostView } from '../api/types'
import { setLocale } from '../i18n'
import { Feed } from './Feed'

// jsdom has no layout: give the scroller and rows sizes so the virtualizer renders.
const saved = {
  h: Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight'),
  w: Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth'),
  scrollTo: HTMLElement.prototype.scrollTo,
}
beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get() {
      return (this as HTMLElement).getAttribute('role') === 'log' ? 600 : 40
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get: () => 800 })
  HTMLElement.prototype.scrollTo = vi.fn() as unknown as typeof HTMLElement.prototype.scrollTo
})
afterAll(() => {
  if (saved.h) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', saved.h)
  if (saved.w) Object.defineProperty(HTMLElement.prototype, 'offsetWidth', saved.w)
  HTMLElement.prototype.scrollTo = saved.scrollTo
})
beforeEach(() => setLocale('en'))

const now = Date.now()
const P = (id: string, user: string, minAgo: number): PostView => ({ id, user_id: user, author: user, message: `text ${id}`, create_at: now - minAgo * 60_000 })
const channel = (o: Partial<ChannelDTO> = {}): ChannelDTO => ({
  id: 'c1', name: 'C', type: 'O', header: '', purpose: '', team_id: 't', team_name: 'team',
  posts: [P('a', 'bob', 30), P('b', 'bob', 29), P('c', 'carol', 5)],
  new_since: now - 10 * 60_000, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'me', crt: false, muted: false,
  ...o,
})
const props = (o: Partial<ChannelDTO> = {}, onLoadOlder = vi.fn().mockResolvedValue(true)) => ({
  channel: channel(o), me: { id: 'me', username: 'me' }, locale: 'en-US',
  actions: { link: vi.fn(), retry: vi.fn(), discard: vi.fn() }, onLoadOlder,
})

test('renders posts, the day separator and the new-messages line', () => {
  render(<Feed {...props()} />)
  expect(screen.getByRole('log', { name: 'Messages' })).toBeInTheDocument()
  expect(screen.getByText('text a')).toBeInTheDocument()
  expect(screen.getByText('text c')).toBeInTheDocument()
  expect(screen.getByText('New messages')).toBeInTheDocument()
})

test('scrolling to the top loads history once at a time', async () => {
  let finish!: (v: boolean) => void
  const onLoadOlder = vi.fn(() => new Promise<boolean>((r) => (finish = r)))
  render(<Feed {...props({ has_more: true }, onLoadOlder)} />)
  const log = screen.getByRole('log')
  log.scrollTop = 0
  fireEvent.scroll(log)
  fireEvent.scroll(log)
  expect(onLoadOlder).toHaveBeenCalledTimes(1)
  finish(true)
})

test('empty channel says so', () => {
  render(<Feed {...props({ posts: [] })} />)
  expect(screen.getByText('No messages yet')).toBeInTheDocument()
})
```

Run: `cd frontend && pnpm test src/components/PostItem.test.tsx src/components/Feed.test.tsx`
Expected: FAIL (компонентов нет).

- [ ] **Step 5: Реализация поста и ленты**

`frontend/src/components/emoji.ts`:
```ts
// The most common reaction names; the full emoji set (and custom emoji) is stage 3.
const EMOJI: Record<string, string> = {
  '+1': '👍', thumbsup: '👍', '-1': '👎', thumbsdown: '👎', heart: '❤️', smile: '😄', slightly_smiling_face: '🙂',
  grinning: '😀', laughing: '😆', joy: '😂', wink: '😉', tada: '🎉', eyes: '👀', fire: '🔥', pray: '🙏',
  clap: '👏', ok_hand: '👌', rocket: '🚀', thinking_face: '🤔', white_check_mark: '✅', heavy_check_mark: '✔️',
  x: '❌', '100': '💯', warning: '⚠️', muscle: '💪', wave: '👋', sob: '😭', cry: '😢', raised_hands: '🙌',
}

export const emojiFor = (name: string) => EMOJI[name] ?? `:${name}:`
```

`frontend/src/components/PostItem.tsx`:
```tsx
import { memo } from 'react'
import type { Attachment, PostView } from '../api/types'
import { formatSize, formatTime } from '../format'
import { t } from '../i18n'
import { emojiFor } from './emoji'
import { Markdown } from './Markdown'

export interface PostActions {
  link(href: string): void
  retry(post: PostView): void
  discard(post: PostView): void
}

interface Props {
  post: PostView
  head: boolean
  me: { id: string; username: string }
  locale: string
  crt: boolean
  actions: PostActions
}

const COLORS = ['bg-rose-500', 'bg-orange-500', 'bg-amber-600', 'bg-lime-600', 'bg-emerald-600', 'bg-teal-600', 'bg-sky-600', 'bg-indigo-500', 'bg-violet-500', 'bg-fuchsia-600']

// Initials until avatars (stage 3); the color is stable per user.
function Avatar({ id, name }: { id: string; name: string }) {
  let h = 0
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) | 0
  return (
    <div aria-hidden className={`flex h-9 w-9 items-center justify-center rounded-full text-sm font-semibold text-white ${COLORS[Math.abs(h) % COLORS.length]}`}>
      {(name[0] ?? '?').toUpperCase()}
    </div>
  )
}

const NAMED_COLORS: Record<string, string> = { good: '#2eb886', warning: '#daa038', danger: '#a30200' }
const barColor = (c?: string) => (c && (NAMED_COLORS[c] ?? (/^#[0-9a-f]{3,8}$/i.test(c) ? c : undefined))) || '#d4d4d4'

function AttachmentView({ a, me, onLink }: { a: Attachment; me: string; onLink(href: string): void }) {
  return (
    <div className="mt-1 max-w-3xl border-l-4 pl-3" style={{ borderColor: barColor(a.color) }}>
      {a.pretext && <Markdown text={a.pretext} me={me} onLink={onLink} />}
      {a.author_name && <div className="text-xs font-medium text-neutral-600">{a.author_name}</div>}
      {a.title &&
        (a.title_link ? (
          <a href={a.title_link} className="font-semibold text-blue-700 hover:underline" onClick={(e) => { e.preventDefault(); onLink(a.title_link!) }}>
            {a.title}
          </a>
        ) : (
          <div className="font-semibold">{a.title}</div>
        ))}
      {a.text && <Markdown text={a.text} me={me} onLink={onLink} />}
      {a.fields && a.fields.length > 0 && (
        <div className="mt-1 grid grid-cols-2 gap-x-4 gap-y-1">
          {a.fields.map((f, i) => (
            <div key={i} className={f.short ? '' : 'col-span-2'}>
              {f.title && <div className="text-xs font-semibold">{f.title}</div>}
              {f.value && <Markdown text={f.value} me={me} onLink={onLink} />}
            </div>
          ))}
        </div>
      )}
      {a.footer && <div className="mt-0.5 text-xs text-neutral-500">{a.footer}</div>}
    </div>
  )
}

export const PostItem = memo(function PostItem({ post, head, me, locale, crt, actions }: Props) {
  const time = formatTime(post.create_at, locale)
  return (
    <article
      data-post-id={post.id}
      className={`group relative flex gap-3 px-4 py-0.5 hover:bg-neutral-50 ${head ? 'mt-2' : ''} ${post.pending ? 'opacity-60' : ''}`}
    >
      <div className="w-9 shrink-0 pt-0.5">
        {head ? <Avatar id={post.user_id} name={post.author} /> : <time className="invisible block pt-1 text-right text-[10px] text-neutral-400 group-hover:visible">{time}</time>}
      </div>
      <div className="min-w-0 flex-1">
        {head && (
          <header className="flex items-baseline gap-2">
            <span className="font-semibold">{post.author}</span>
            {post.bot && <span className="rounded bg-neutral-200 px-1 text-[10px] font-semibold text-neutral-600">BOT</span>}
            <time className="text-xs text-neutral-500">{time}</time>
          </header>
        )}
        <div className={post.system ? 'italic text-neutral-500' : ''}>
          {post.message && <Markdown text={post.message} me={me.username} onLink={actions.link} />}
          {post.edit_at ? <span className="text-xs text-neutral-400">{t('post.edited')}</span> : null}
        </div>
        {post.attachments?.map((a, i) => <AttachmentView key={i} a={a} me={me.username} onLink={actions.link} />)}
        {post.files && post.files.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-2">
            {post.files.map((f, i) => (
              <div key={i} className="flex items-center gap-2 rounded border border-neutral-200 px-2 py-1 text-xs">
                <span aria-hidden>📎</span>
                <span className="max-w-64 truncate">{f.name}</span>
                <span className="text-neutral-500">{formatSize(f.size)}</span>
              </div>
            ))}
          </div>
        )}
        {post.reactions && post.reactions.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {post.reactions.map((r) => (
              <span key={r.emoji} title={`:${r.emoji}:`} className={`rounded-full border px-1.5 text-xs ${r.mine ? 'border-blue-400 bg-blue-50' : 'border-neutral-200'}`}>
                {emojiFor(r.emoji)} {r.count}
              </span>
            ))}
          </div>
        )}
        {crt && (post.reply_count ?? 0) > 0 && <div className="mt-0.5 text-xs font-medium text-blue-700">{t('post.replies', { n: String(post.reply_count) })}</div>}
        {post.pending && <div className="text-xs text-neutral-500">{t('post.sending')}</div>}
        {post.failed && (
          <div role="alert" className="flex gap-2 text-xs text-red-600">
            {t('post.failed')}
            <button className="underline" onClick={() => actions.retry(post)}>{t('post.retry')}</button>
            <button className="underline" onClick={() => actions.discard(post)}>{t('post.discard')}</button>
          </div>
        )}
      </div>
    </article>
  )
})
```

`frontend/src/components/Feed.tsx`:
```tsx
import { useVirtualizer } from '@tanstack/react-virtual'
import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { ChannelDTO } from '../api/types'
import { formatDay } from '../format'
import { t } from '../i18n'
import { buildRows, type Row } from './feedRows'
import { PostItem, type PostActions } from './PostItem'

interface Props {
  channel: ChannelDTO
  me: { id: string; username: string }
  locale: string
  actions: PostActions
  onLoadOlder(): Promise<boolean>
}

const NEAR_TOP = 300
const NEAR_BOTTOM = 48
const estimate = (r: Row) => (r.kind === 'post' ? (r.head ? 64 : 28) : 36)

// Feed must be keyed by channel id: another channel is a fresh mount, so
// the scroll bookkeeping below never leaks between channels.
export function Feed({ channel, me, locale, actions, onLoadOlder }: Props) {
  const rows = useMemo(() => buildRows(channel), [channel])
  const scroller = useRef<HTMLDivElement>(null)
  const ready = useRef(false)
  const atBottom = useRef(true)
  const anchor = useRef<string | null>(null)
  const loading = useRef(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const v = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: (i) => estimate(rows[i]),
    getItemKey: (i) => rows[i].key,
    overscan: 8,
  })

  const loadOlder = async () => {
    if (loading.current || !channel.has_more) return
    loading.current = true
    setLoadingOlder(true)
    const top = v.getVirtualItems().find((it) => rows[it.index]?.kind === 'post')
    anchor.current = top ? rows[top.index].key : null
    try {
      if (!(await onLoadOlder())) anchor.current = null
    } finally {
      loading.current = false
      setLoadingOlder(false)
    }
  }

  useLayoutEffect(() => {
    if (!rows.length) return
    if (!ready.current) {
      // First rows: the "new messages" line, else the latest post.
      ready.current = true
      const i = rows.findIndex((r) => r.kind === 'new')
      if (i >= 0) {
        atBottom.current = false
        v.scrollToIndex(i, { align: 'start' })
      } else {
        v.scrollToIndex(rows.length - 1, { align: 'end' })
      }
      requestAnimationFrame(() => {
        const el = scroller.current
        if (el && el.scrollHeight <= el.clientHeight) void loadOlder() // too short to scroll: fetch history now
      })
      return
    }
    if (anchor.current) {
      // History landed above: keep the post that was on top in place.
      const i = rows.findIndex((r) => r.key === anchor.current)
      anchor.current = null
      if (i >= 0) v.scrollToIndex(i, { align: 'start' })
      return
    }
    const last = rows[rows.length - 1]
    if (atBottom.current || (last.kind === 'post' && last.post.pending)) v.scrollToIndex(rows.length - 1, { align: 'end' })
  }, [rows, v]) // loadOlder is recreated every render; the effect only needs rows

  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM
    if (el.scrollTop < NEAR_TOP) void loadOlder()
  }

  const renderRow = (r: Row) => {
    switch (r.kind) {
      case 'more':
        return <div className="py-3 text-center text-xs text-neutral-500">{loadingOlder ? t('feed.loadingOlder') : ''}</div>
      case 'day':
        return (
          <div className="flex items-center px-4 py-2">
            <div className="h-px flex-1 bg-neutral-200" />
            <span className="px-3 text-xs font-semibold text-neutral-500">{formatDay(r.ms, locale)}</span>
            <div className="h-px flex-1 bg-neutral-200" />
          </div>
        )
      case 'new':
        return (
          <div className="flex items-center px-4 py-1">
            <div className="h-px flex-1 bg-red-400" />
            <span className="px-3 text-xs font-semibold text-red-600">{t('feed.new')}</span>
            <div className="h-px flex-1 bg-red-400" />
          </div>
        )
      case 'gap':
        return <div role="status" className="py-2 text-center text-xs text-neutral-500">{t('feed.gap')}</div>
      case 'post':
        return <PostItem post={r.post} head={r.head} me={me} locale={locale} crt={channel.crt} actions={actions} />
    }
  }

  return (
    <div ref={scroller} onScroll={onScroll} role="log" aria-label={t('feed.label')} className="relative min-h-0 flex-1 overflow-y-auto pb-2">
      {!rows.length && (
        <div className="absolute inset-0 flex items-center justify-center text-neutral-500">{t(channel.loaded ? 'feed.empty' : 'feed.loading')}</div>
      )}
      <div style={{ height: v.getTotalSize(), position: 'relative', width: '100%' }}>
        {v.getVirtualItems().map((it) => (
          <div
            key={it.key}
            data-index={it.index}
            ref={v.measureElement}
            style={{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${it.start}px)` }}
          >
            {renderRow(rows[it.index])}
          </div>
        ))}
      </div>
    </div>
  )
}
```
`frontend/src/chat.ts` — дописать:
```ts
export const openLink = (href: string) => {
  client.openURL(href).catch(report)
}

// loadOlder fetches one page of history above the window; resolves true
// when the channel was re-read with it.
export async function loadOlder(serverId: number, channelId: string): Promise<boolean> {
  try {
    await client.loadOlder(serverId, channelId)
    await refreshChannel(serverId, channelId)
    return true
  } catch (e) {
    report(e)
    return false
  }
}

export const retryPost = (serverId: number, channelId: string, pendingId: string) => {
  client.retryPost(serverId, channelId, pendingId).catch(report)
}

export const discardPost = (serverId: number, channelId: string, pendingId: string) => {
  client.discardPost(serverId, channelId, pendingId).catch(report)
}
```

`frontend/src/components/ChannelPane.tsx` (целиком):
```tsx
import { useMemo } from 'react'
import type { ChannelDTO, ServerDTO } from '../api/types'
import { discardPost, loadOlder, openLink, retryPost } from '../chat'
import { formatLocale } from '../format'
import { t } from '../i18n'
import { Feed } from './Feed'
import { channelGlyph } from './glyph'
import type { PostActions } from './PostItem'

export function ChannelPane({ server, channel, onReauth }: { server: ServerDTO; channel: ChannelDTO | null; onReauth(): void }) {
  const channelId = channel?.id ?? ''
  // Stable per channel: PostItem is memoized on its props.
  const actions = useMemo<PostActions>(
    () => ({
      link: openLink,
      retry: (p) => retryPost(server.id, channelId, p.id),
      discard: (p) => discardPost(server.id, channelId, p.id),
    }),
    [server.id, channelId],
  )
  const me = useMemo(() => ({ id: channel?.me_id ?? '', username: server.username }), [channel?.me_id, server.username])
  if (!channel) {
    return <div className="flex flex-1 items-center justify-center text-neutral-500">{t('channel.none')}</div>
  }
  return (
    <section aria-label={channel.name} className="flex min-h-0 flex-1 flex-col">
      <header className="flex min-w-0 items-baseline gap-3 border-b border-neutral-200 px-4 py-2">
        <h1 className="shrink-0 font-semibold">
          <span className="mr-1 text-neutral-400">{channelGlyph(channel.type)}</span>
          {channel.name}
        </h1>
        {channel.header && (
          <p className="truncate text-xs text-neutral-500" title={channel.header}>
            {channel.header}
          </p>
        )}
      </header>
      {server.state === 'needs_reauth' && (
        <div role="status" className="flex items-center gap-3 bg-amber-50 px-4 py-1.5 text-sm text-amber-900">
          {t('channel.sessionExpired')}
          <button className="font-medium underline" onClick={onReauth}>
            {t('status.signInAgain')}
          </button>
        </div>
      )}
      {channel.loaded && channel.syncing && server.state !== 'needs_reauth' && (
        <div role="status" className="border-b border-neutral-100 px-4 py-0.5 text-xs text-neutral-500">
          {t('channel.syncing')}
        </div>
      )}
      <Feed key={channel.id} channel={channel} me={me} locale={formatLocale()} actions={actions} onLoadOlder={() => loadOlder(server.id, channel.id)} />
    </section>
  )
}
```

- [ ] **Step 6: Тесты, сборка, линт**

Run: `cd frontend && pnpm test && pnpm build && pnpm lint`
Expected: PASS.

- [ ] **Step 7: Проверка глазами и памяти ленты (browser mode)**

Собрать и запустить browser mode с `--mm-fake --mm-fake-channels 100 --test-api` на свободном порту, войти alice/secret, открыть Town Square, скриншот → посмотреть: разделитель дня, группировка bob/carol, ссылки в `load-NNN` синие, `**some**` жирное. Прокрутить наверх до «Message #1» (история подгружается, прыжков нет — второй скриншот в середине подгрузки). Отправить через test-API пост с таблицей, списком, `@alice`, ссылкой-картинкой — скриншот. Замерить `scripts/pss.sh <pid Go-процесса>` после предзагрузки 100 каналов и записать в `docs/spikes/2026-09-24-stage1-spikes.md` S4 (раздел «Этап 2») — Go-часть должна быть заметно меньше 75 МБ.

- [ ] **Step 8: Коммит**

```bash
git add frontend docs/spikes
git commit -m "frontend: virtualized feed with day separators, author groups, new-messages line, history paging; markdown with mentions; post attachments/files/reactions"
```

---

### Task 14: Редактор, действия с постом, фокус и сеть

**Files:**
- Create: `frontend/src/components/Composer.tsx`, `frontend/src/components/Composer.test.tsx`
- Modify: `frontend/src/components/PostItem.tsx` (панель действий, правка на месте), `frontend/src/components/PostItem.test.tsx`
- Modify: `frontend/src/components/Feed.tsx` (`editingId`), `frontend/src/components/Feed.test.tsx` (полный `PostActions`, `editingId`), `frontend/src/components/ChannelPane.tsx` (редактор, действия), `frontend/src/chat.ts` (+ `sendPost`, `saveDraft`, `editPost`, `deletePost`, `markUnread`, `copyLink`, `editLastOwn`)
- Modify: `frontend/src/App.tsx` (фокус окна и «сеть появилась»), `frontend/src/App.test.tsx`, `frontend/src/i18n.ts`

**Interfaces:**
- Consumes: Task 12 (`client.*`, `useStore().editingId/setEditing`), Task 13 (`PostItem`, `PostActions`, `Feed`).
- Produces:
  - `PostActions` + `edit(post)`, `saveEdit(post, message): Promise<void>` (бросает `ApiError`), `cancelEdit()`, `remove(post)` (со своим подтверждением), `markUnread(post)`, `copyLink(post)`.
  - `PostItem` + проп `editing: boolean`; `Feed` + проп `editingId: string | null`.
  - `Composer({ channel, onSend, onDraft, onEditLast })`: `onSend(message): Promise<void>` (бросает `ApiError`), `onDraft(text): void`, `onEditLast(): void`. Монтируется с `key={channel.id}`.
  - `chat.ts`: `sendPost(serverId, channelId, message)`, `saveDraft(serverId, channelId, text)`, `editPost(serverId, postId, message)`, `deletePost(serverId, postId)`, `markUnread(serverId, postId)`, `copyLink(serverURL, teamName, postId)`, `editLastOwn(channel: ChannelDTO)`.

Правила:
- Enter — отправить, Shift+Enter — новая строка, при наборе через IME (`isComposing`) Enter не отправляет. Пустое (только пробелы) не отправляется. Поле очищается сразу; если Go отказал (`not_signed_in`, `session_expired`, …) — текст возвращается и показывается ошибка. Сбой сети при отправке — это не отказ: пост уже в ленте как «не отправлено» с «Повторить».
- ↑ в пустом поле — правка последнего своего поста (не системного, не pending/failed); лента прокручивается к нему. В правке: Enter — сохранить, Esc — отмена, Shift+Enter — перенос.
- Черновик сохраняется через 500 мс после последнего изменения и сразу при уходе из канала; после отправки черновик пустой.
- Панель действий появляется при наведении или фокусе внутри поста: «Изменить» и «Удалить» — только свои несистемные; «Отметить непрочитанным» и «Копировать ссылку» — любые. Удаление — с подтверждением. Ссылка — `${server.url}/${team_name}/pl/${post_id}` (как у Mattermost).
- Фокус: `focus` → `SetFocused(document.visibilityState === 'visible')`, `blur` → `SetFocused(false)`, `visibilitychange` → видно и `document.hasFocus()`; при старте — текущее состояние. `online` → `NetworkChanged()`.

- [ ] **Step 1: Падающие тесты**

`frontend/src/components/Composer.test.tsx`:
```tsx
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { ApiError } from '../api/client'
import type { ChannelDTO } from '../api/types'
import { setLocale } from '../i18n'
import { Composer } from './Composer'

const channel = (o: Partial<ChannelDTO> = {}): ChannelDTO => ({
  id: 'c1', name: 'Off-Topic', type: 'O', header: '', purpose: '', team_id: 't', team_name: 'team', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'me', crt: false, muted: false, ...o,
})

beforeEach(() => setLocale('en'))
afterEach(() => vi.useRealTimers())

test('Enter sends and clears, Shift+Enter makes a new line, blank is not sent', async () => {
  const onSend = vi.fn().mockResolvedValue(undefined)
  render(<Composer channel={channel()} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  expect(box).toHaveAttribute('placeholder', 'Write to Off-Topic')
  await userEvent.type(box, '   {Enter}')
  expect(onSend).not.toHaveBeenCalled()
  await userEvent.clear(box)
  await userEvent.type(box, 'line one{Shift>}{Enter}{/Shift}line two{Enter}')
  expect(onSend).toHaveBeenCalledWith('line one\nline two')
  expect(box).toHaveValue('')
})

test('a refused send puts the text back with the error', async () => {
  const onSend = vi.fn().mockRejectedValue(new ApiError('session_expired', ''))
  render(<Composer channel={channel()} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'hello{Enter}')
  expect(await screen.findByRole('alert')).toHaveTextContent('Session expired — sign in again')
  expect(box).toHaveValue('hello')
})

test('ArrowUp in an empty box edits the last own post', async () => {
  const onEditLast = vi.fn()
  render(<Composer channel={channel()} onSend={vi.fn()} onDraft={() => {}} onEditLast={onEditLast} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'x{ArrowUp}')
  expect(onEditLast).not.toHaveBeenCalled()
  await userEvent.clear(box)
  await userEvent.type(box, '{ArrowUp}')
  expect(onEditLast).toHaveBeenCalledTimes(1)
})

test('draft: restored, saved 500 ms after typing, flushed when leaving the channel', async () => {
  vi.useFakeTimers()
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const onDraft = vi.fn()
  const { unmount } = render(<Composer channel={channel({ draft: 'saved' })} onSend={vi.fn()} onDraft={onDraft} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  expect(box).toHaveValue('saved')
  await user.type(box, '!')
  expect(onDraft).not.toHaveBeenCalled()
  await act(async () => vi.advanceTimersByTime(500))
  expect(onDraft).toHaveBeenLastCalledWith('saved!')
  await user.type(box, '?')
  unmount()
  expect(onDraft).toHaveBeenLastCalledWith('saved!?')
})
```

`frontend/src/components/PostItem.test.tsx` — фабрику действий заменить полной и добавить тесты:
```tsx
const actions = (): PostActions => ({
  link: vi.fn(), retry: vi.fn(), discard: vi.fn(), edit: vi.fn(), saveEdit: vi.fn().mockResolvedValue(undefined),
  cancelEdit: vi.fn(), remove: vi.fn(), markUnread: vi.fn(), copyLink: vi.fn(),
})
```
(во всех существующих рендерах добавить `editing={false}`), и:
```tsx
test('own post offers edit and delete; others only mark unread and copy link', async () => {
  const a = actions()
  const own = post({ user_id: 'u-alice' })
  const { rerender } = render(<PostItem post={own} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
  expect(a.edit).toHaveBeenCalledWith(own)
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  expect(a.remove).toHaveBeenCalledWith(own)
  const other = post()
  rerender(<PostItem post={other} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
  await userEvent.click(screen.getByRole('button', { name: 'Mark as unread' }))
  expect(a.markUnread).toHaveBeenCalledWith(other)
  await userEvent.click(screen.getByRole('button', { name: 'Copy link' }))
  expect(a.copyLink).toHaveBeenCalledWith(other)
})

test('inline edit: Enter saves, Escape cancels, errors stay visible', async () => {
  const a = actions()
  const own = post({ user_id: 'u-alice', message: 'v1' })
  const { rerender } = render(<PostItem post={own} head me={me} locale="en-US" crt={false} actions={a} editing />)
  const box = screen.getByRole('textbox', { name: 'Edit message' })
  expect(box).toHaveValue('v1')
  expect(screen.queryByRole('toolbar')).toBeNull()
  await userEvent.type(box, ' v2{Enter}')
  expect(a.saveEdit).toHaveBeenCalledWith(own, 'v1 v2')
  await userEvent.type(box, '{Escape}')
  expect(a.cancelEdit).toHaveBeenCalled()
  a.saveEdit = vi.fn().mockRejectedValue(new ApiError('forbidden', ''))
  rerender(<PostItem post={own} head me={me} locale="en-US" crt={false} actions={{ ...a }} editing />)
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('You are not allowed to do that')
})
```
(импорт `ApiError` из `'../api/client'`.)

`frontend/src/App.test.tsx` — добавить:
```tsx
test('window focus and network changes are reported to Go', async () => {
  const { client } = await import('./api/client')
  h.list = []
  render(<App />)
  vi.mocked(client.setFocused).mockClear()
  await act(async () => window.dispatchEvent(new Event('blur')))
  expect(client.setFocused).toHaveBeenLastCalledWith(false)
  await act(async () => window.dispatchEvent(new Event('focus')))
  expect(client.setFocused).toHaveBeenLastCalledWith(true)
  await act(async () => window.dispatchEvent(new Event('online')))
  expect(client.networkChanged).toHaveBeenCalled()
})
```
и в мок клиента добавить `networkChanged: vi.fn().mockResolvedValue(undefined)`.

Run: `cd frontend && pnpm test`
Expected: FAIL (нет `Composer`, новых действий, обработчиков фокуса).

- [ ] **Step 2: Строки i18n**

`ru`:
```ts
  'composer.label': 'Сообщение',
  'composer.placeholder': 'Написать в {name}',
  'post.actions': 'Действия с сообщением',
  'post.edit': 'Изменить',
  'post.delete': 'Удалить сообщение',
  'post.deleteConfirm': 'Удалить это сообщение?',
  'post.markUnread': 'Отметить непрочитанным',
  'post.copyLink': 'Копировать ссылку',
  'post.editLabel': 'Правка сообщения',
  'post.save': 'Сохранить',
  'post.cancel': 'Отмена',
```
`en`:
```ts
  'composer.label': 'Message',
  'composer.placeholder': 'Write to {name}',
  'post.actions': 'Message actions',
  'post.edit': 'Edit',
  'post.delete': 'Delete',
  'post.deleteConfirm': 'Delete this message?',
  'post.markUnread': 'Mark as unread',
  'post.copyLink': 'Copy link',
  'post.editLabel': 'Edit message',
  'post.save': 'Save',
  'post.cancel': 'Cancel',
```

- [ ] **Step 3: Реализация**

`frontend/src/components/Composer.tsx`:
```tsx
import { useEffect, useRef, useState } from 'react'
import type { ChannelDTO } from '../api/types'
import { errorMessage } from '../errors'
import { t } from '../i18n'

const DRAFT_DELAY = 500

interface Props {
  channel: ChannelDTO
  onSend(message: string): Promise<void>
  onDraft(text: string): void
  onEditLast(): void
}

// Composer must be keyed by channel id: its draft belongs to one channel.
export function Composer({ channel, onSend, onDraft, onEditLast }: Props) {
  const [text, setText] = useState(channel.draft)
  const [error, setError] = useState<string | null>(null)
  const latest = useRef(channel.draft)
  const saved = useRef(channel.draft)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const flush = () => {
    clearTimeout(timer.current)
    timer.current = undefined
    if (latest.current !== saved.current) {
      saved.current = latest.current
      onDraft(latest.current)
    }
  }
  const flushRef = useRef(flush)
  flushRef.current = flush
  useEffect(() => () => flushRef.current(), []) // leaving the channel saves at once

  const change = (v: string) => {
    setText(v)
    latest.current = v
    clearTimeout(timer.current)
    timer.current = setTimeout(flush, DRAFT_DELAY)
  }

  const send = async () => {
    const msg = text
    if (!msg.trim()) return
    setText('')
    latest.current = ''
    flush()
    setError(null)
    try {
      await onSend(msg)
    } catch (e) {
      setText(msg)
      latest.current = msg
      setError(errorMessage(e))
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      void send()
    } else if (e.key === 'ArrowUp' && text === '') {
      e.preventDefault()
      onEditLast()
    }
  }

  return (
    <div className="border-t border-neutral-200 px-4 py-3">
      {error && (
        <p role="alert" className="pb-1 text-xs text-red-600">
          {error}
        </p>
      )}
      <textarea
        aria-label={t('composer.label')}
        placeholder={t('composer.placeholder', { name: channel.name })}
        value={text}
        rows={Math.min(10, text.split('\n').length)}
        onChange={(e) => change(e.target.value)}
        onKeyDown={onKeyDown}
        autoFocus
        className="w-full resize-none rounded border border-neutral-300 px-3 py-2 focus:border-blue-500 focus:outline-none"
      />
    </div>
  )
}
```

`frontend/src/components/PostItem.tsx` — расширить `PostActions` и `Props`:
```tsx
export interface PostActions {
  link(href: string): void
  retry(post: PostView): void
  discard(post: PostView): void
  edit(post: PostView): void
  saveEdit(post: PostView, message: string): Promise<void>
  cancelEdit(): void
  remove(post: PostView): void
  markUnread(post: PostView): void
  copyLink(post: PostView): void
}
```
(`Props` + `editing: boolean`), добавить компоненты:
```tsx
function EditBox({ post, actions }: { post: PostView; actions: PostActions }) {
  const [text, setText] = useState(post.message)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const save = async () => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await actions.saveEdit(post, text)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="mt-1">
      <textarea
        aria-label={t('post.editLabel')}
        autoFocus
        value={text}
        rows={Math.min(10, text.split('\n').length + 1)}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            actions.cancelEdit()
          } else if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault()
            void save()
          }
        }}
        className="w-full resize-none rounded border border-blue-400 px-2 py-1 focus:outline-none"
      />
      <div className="flex items-center gap-3 text-xs">
        <button className="rounded bg-blue-600 px-2 py-0.5 text-white disabled:opacity-50" disabled={busy} onClick={() => void save()}>
          {t('post.save')}
        </button>
        <button className="underline" onClick={actions.cancelEdit}>
          {t('post.cancel')}
        </button>
        {error && (
          <span role="alert" className="text-red-600">
            {error}
          </span>
        )}
      </div>
    </div>
  )
}

function ToolButton({ label, onClick, children }: { label: string; onClick(): void; children: React.ReactNode }) {
  return (
    <button aria-label={label} title={label} onClick={onClick} className="rounded px-1.5 py-0.5 text-neutral-600 hover:bg-neutral-100">
      {children}
    </button>
  )
}
```
(импорты `useState` из `react`, `errorMessage` из `'../errors'`). В `PostItem`: сигнатура `({ post, head, me, locale, crt, actions, editing })`; блок тела сообщения заменить на
```tsx
        {editing ? (
          <EditBox post={post} actions={actions} />
        ) : (
          <div className={post.system ? 'italic text-neutral-500' : ''}>
            {post.message && <Markdown text={post.message} me={me.username} onLink={actions.link} />}
            {post.edit_at ? <span className="text-xs text-neutral-400">{t('post.edited')}</span> : null}
          </div>
        )}
```
и перед закрывающим `</article>`:
```tsx
      {!post.pending && !post.failed && !editing && (
        <div
          role="toolbar"
          aria-label={t('post.actions')}
          className="absolute -top-3 right-3 hidden gap-0.5 rounded border border-neutral-200 bg-white px-1 shadow-sm group-focus-within:flex group-hover:flex"
        >
          {post.user_id === me.id && !post.system && (
            <ToolButton label={t('post.edit')} onClick={() => actions.edit(post)}>✎</ToolButton>
          )}
          <ToolButton label={t('post.markUnread')} onClick={() => actions.markUnread(post)}>◉</ToolButton>
          <ToolButton label={t('post.copyLink')} onClick={() => actions.copyLink(post)}>🔗</ToolButton>
          {post.user_id === me.id && !post.system && (
            <ToolButton label={t('post.delete')} onClick={() => actions.remove(post)}>🗑</ToolButton>
          )}
        </div>
      )}
```

`frontend/src/components/Feed.test.tsx`: в `props()` — `actions` со всеми методами `PostActions` (как фабрика в `PostItem.test.tsx`) и `editingId: null`, иначе `tsc` в `pnpm build` не пропустит тест.

`frontend/src/components/Feed.tsx`: проп `editingId: string | null`; в `renderRow` для поста `editing={r.post.id === editingId}`; эффект прокрутки к редактируемому посту (после основного эффекта):
```tsx
  useLayoutEffect(() => {
    if (!editingId) return
    const i = rows.findIndex((r) => r.key === editingId)
    if (i >= 0) v.scrollToIndex(i, { align: 'auto' })
  }, [editingId]) // only when editing starts, not on every new post
```

`frontend/src/chat.ts` — дописать:
```ts
export const sendPost = (serverId: number, channelId: string, message: string) => client.sendPost(serverId, channelId, message)

export const saveDraft = (serverId: number, channelId: string, text: string) => {
  client.saveDraft(serverId, channelId, text).catch(() => {}) // a lost draft is not worth an error banner
}

export async function editPost(serverId: number, postId: string, message: string) {
  await client.editPost(serverId, postId, message)
  useStore.getState().setEditing(null)
}

export const deletePost = (serverId: number, postId: string) => {
  client.deletePost(serverId, postId).catch(report)
}

export const markUnread = (serverId: number, postId: string) => {
  client.markUnread(serverId, postId).catch(report)
}

// Same permalink form as Mattermost: <server>/<team>/pl/<post id>.
export const copyLink = (serverURL: string, teamName: string, postId: string) => {
  navigator.clipboard?.writeText(`${serverURL}/${teamName}/pl/${postId}`).catch(report)
}

export function editLastOwn(ch: ChannelDTO) {
  for (let i = ch.posts.length - 1; i >= 0; i--) {
    const p = ch.posts[i]
    if (p.user_id === ch.me_id && !p.pending && !p.failed && !p.system) {
      useStore.getState().setEditing(p.id)
      return
    }
  }
}
```
(импорт `type ChannelDTO`.)

`frontend/src/components/ChannelPane.tsx`: `actions` дополнить
```tsx
      edit: (p) => useStore.getState().setEditing(p.id),
      saveEdit: (p, message) => editPost(server.id, p.id, message),
      cancelEdit: () => useStore.getState().setEditing(null),
      remove: (p) => {
        if (confirm(t('post.deleteConfirm'))) deletePost(server.id, p.id)
      },
      markUnread: (p) => markUnread(server.id, p.id),
      copyLink: (p) => copyLink(server.url, teamName, p.id),
```
где `const teamName = channel?.team_name ?? ''` и зависимости `useMemo` — `[server.id, server.url, channelId, teamName]`; `const editingId = useStore((s) => s.editingId)`; `Feed` получает `editingId={editingId}`; после `Feed`:
```tsx
      <Composer
        key={channel.id}
        channel={channel}
        onSend={(m) => sendPost(server.id, channel.id, m)}
        onDraft={(text) => saveDraft(server.id, channel.id, text)}
        onEditLast={() => editLastOwn(channel)}
      />
```
(импорты `Composer`, `useStore`, функций из `../chat`.)

`frontend/src/App.tsx` — второй эффект:
```tsx
  useEffect(() => {
    // Go marks the open channel read only while the window is focused and visible.
    const set = (focused: boolean) => void client.setFocused(focused).catch(() => {})
    const visible = () => document.visibilityState === 'visible'
    const onFocus = () => set(visible())
    const onBlur = () => set(false)
    const onVisibility = () => set(visible() && document.hasFocus())
    const onOnline = () => void client.networkChanged().catch(() => {})
    set(visible() && document.hasFocus())
    window.addEventListener('focus', onFocus)
    window.addEventListener('blur', onBlur)
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('online', onOnline)
    return () => {
      window.removeEventListener('focus', onFocus)
      window.removeEventListener('blur', onBlur)
      document.removeEventListener('visibilitychange', onVisibility)
      window.removeEventListener('online', onOnline)
    }
  }, [])
```

- [ ] **Step 4: Тесты, сборка, линт**

Run: `cd frontend && pnpm test && pnpm build && pnpm lint`
Expected: PASS.

- [ ] **Step 5: Проверка глазами (browser mode)**

Browser mode с фейком: отправить сообщение с переносом строки (Shift+Enter) — скриншот; ↑ — правка последнего своего, Enter — «(изменено)»; удалить с подтверждением; «Отметить непрочитанным» на старом посте Town Square — в рейле точка непрочитанного и она не исчезает, пока канал открыт; переключиться в другой канал и обратно — черновик на месте. Скриншоты посмотреть.

- [ ] **Step 6: Коммит**

```bash
git add frontend
git commit -m "frontend: composer with drafts and edit-last, post actions (edit/delete/mark unread/copy link), focus and network reporting"
```

---

### Task 15: Десктоп — уведомления с переходом в канал, бейдж трея, dev-режим с фейком

**Files:**
- Create: `internal/desktop/notifyclick.go`, `internal/desktop/notifyclick_test.go` (без тега `wails`)
- Modify: `internal/desktop/notify.go` (`Notify` — реализация `api.Notifier`), `internal/desktop/run.go` (клик → `NotificationClicked`, notifier в сервис, бейдж трея, dev-действия), `internal/desktop/tray.go` (иконки, обновление бейджа)
- Modify: `internal/desktop/traylabels.go`, `internal/desktop/traylabels_test.go` (подсказка трея, выбор иконки)
- Modify: `internal/desktop/devtools_dev.go`, `internal/desktop/devtools_prod.go` (`DevBuild`)
- Modify: `scripts/gen-icon.go` (варианты с точкой), `internal/appfiles/embed.go`; Create: `internal/appfiles/icons/icon-unread.png`, `internal/appfiles/icons/icon-mention.png`
- Modify: `cmd/spk-mattermost/main.go`, `cmd/spk-mattermost/main_test.go`, `cmd/spk-mattermost/run_desktop_wails.go`, `cmd/spk-mattermost/run_desktop_nowails.go` (`desktopOpts`, dev-режим `--mm-fake`)

**Interfaces:**
- Consumes: Task 11 (`api.Notifier`, `api.Notification`, `(*Service).SetNotifier`, `NotificationClicked`, `OnBadge`, `api.Badge`, `Start`/`Close`), Task 3 (`mmfake.PostAs`).
- Produces:
  - `desktop.clickTarget(data map[string]any) (serverID int64, channelID string, ok bool)`.
  - `desktop.trayTooltip(lang string, unread bool, mentions int) string`; `type trayIcon int` (`iconPlain`, `iconUnread`, `iconMention`); `trayIconFor(unread bool, mentions int) trayIcon`.
  - `desktop.Options` + `IconUnreadPNG, IconMentionPNG []byte`, `DevActions []DevAction` (`type DevAction struct{ Label string; Run func() }`).
  - `desktop.DevBuild` (`true` без тега `production`).
  - `appfiles.IconUnreadPNG`, `appfiles.IconMentionPNG`.
  - `cmd`: `type desktopOpts struct{ MMFake bool; FakeChannels int }`; `runners.desktop func(ctx context.Context, o desktopOpts) error`. `--mm-fake` в десктопе — только в dev-сборке: поднимает фейк, добавляет его сервер и входит как alice, в меню трея — «Fake: упоминание от bob».

Поведение:
- Уведомление несёт `Data {server_id, channel_id}`; клик → окно на передний план и `NotificationClicked` → UI открывает канал. На Linux `Data` возвращается Go-значениями, на других ОС может прийти JSON-числом или строкой — разбор терпимый.
- Одинаковый `ID` (`mm-<server>-<channel>`) на Linux заменяет прежнее уведомление канала (`replaces_id`), а не плодит новые.
- Бейдж трея: подсказка «spk-mattermost — 3 упоминания» / «— есть непрочитанные» / просто имя; иконка — с красной точкой при упоминаниях, с жёлтой — при непрочитанном, обычная — иначе. `SetIcon`/`SetTooltip` уходят в `InvokeSync` (главный поток GTK), поэтому обновление идёт в своей горутине и только после `ApplicationStarted` + 200 мс (S3); из очереди берётся только последнее значение — вызывающая горутина сервиса никогда не ждёт GTK.

- [ ] **Step 1: Падающие тесты (без тега `wails`)**

`internal/desktop/notifyclick_test.go`:
```go
package desktop

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClickTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		data map[string]any
		id   int64
		ch   string
		ok   bool
	}{
		{"linux keeps go values", map[string]any{"server_id": int64(3), "channel_id": "c1"}, 3, "c1", true},
		{"json number", map[string]any{"server_id": float64(4), "channel_id": "c2"}, 4, "c2", true},
		{"json.Number", map[string]any{"server_id": json.Number("5"), "channel_id": "c3"}, 5, "c3", true},
		{"string", map[string]any{"server_id": "6", "channel_id": "c4"}, 6, "c4", true},
		{"test notification", map[string]any{"target": "test", "id": "x"}, 0, "", false},
		{"no channel", map[string]any{"server_id": int64(3)}, 3, "", false},
		{"nil", nil, 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ch, ok := clickTarget(tc.data)
			assert.Equal(t, tc.id, id)
			assert.Equal(t, tc.ch, ch)
			assert.Equal(t, tc.ok, ok)
		})
	}
}
```

`internal/desktop/traylabels_test.go` — добавить:
```go
func TestTrayTooltip(t *testing.T) {
	assert.Equal(t, "spk-mattermost", trayTooltip("ru", false, 0))
	assert.Equal(t, "spk-mattermost — есть непрочитанные", trayTooltip("ru", true, 0))
	assert.Equal(t, "spk-mattermost — 1 упоминание", trayTooltip("ru", true, 1))
	assert.Equal(t, "spk-mattermost — 3 упоминания", trayTooltip("ru", true, 3))
	assert.Equal(t, "spk-mattermost — 11 упоминаний", trayTooltip("ru", false, 11))
	assert.Equal(t, "spk-mattermost — 21 упоминание", trayTooltip("ru", false, 21))
	assert.Equal(t, "spk-mattermost — 25 упоминаний", trayTooltip("ru", false, 25))
	assert.Equal(t, "spk-mattermost — unread messages", trayTooltip("en", true, 0))
	assert.Equal(t, "spk-mattermost — 1 mention", trayTooltip("en", false, 1))
	assert.Equal(t, "spk-mattermost — 2 mentions", trayTooltip("en", false, 2))
}

func TestTrayIconFor(t *testing.T) {
	assert.Equal(t, iconPlain, trayIconFor(false, 0))
	assert.Equal(t, iconUnread, trayIconFor(true, 0))
	assert.Equal(t, iconMention, trayIconFor(true, 2))
	assert.Equal(t, iconMention, trayIconFor(false, 1))
}
```

`cmd/spk-mattermost/main_test.go` — `desktop` раннеры получают `desktopOpts`:
```go
		desktop: func(context.Context, desktopOpts) error { desktopCalled = true; return nil },
```
(во всех трёх тестах) и новый тест:
```go
func TestDesktopDevFakeFlags(t *testing.T) {
	var got desktopOpts
	cmd := newRootCmd(runners{
		browser: func(context.Context, browserOpts) error { t.Fatal("browser runner called"); return nil },
		desktop: func(_ context.Context, o desktopOpts) error { got = o; return nil },
	})
	cmd.SetArgs([]string{"--mm-fake", "--mm-fake-channels", "100"})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	assert.Equal(t, desktopOpts{MMFake: true, FakeChannels: 100}, got)
}
```

Run: `go test ./internal/desktop/ ./cmd/spk-mattermost/`
Expected: FAIL (нет `clickTarget`, `trayTooltip`, `desktopOpts`).

- [ ] **Step 2: Реализация чистой логики**

`internal/desktop/notifyclick.go`:
```go
package desktop

import (
	"encoding/json"
	"strconv"
)

// clickTarget reads the channel a clicked chat notification points to. On
// Linux the Data map comes back as the Go values we sent; other platforms
// round-trip it through JSON (float64) — so ids are parsed leniently.
func clickTarget(data map[string]any) (serverID int64, channelID string, ok bool) {
	channelID, _ = data["channel_id"].(string)
	switch v := data["server_id"].(type) {
	case int64:
		serverID = v
	case int:
		serverID = int64(v)
	case float64:
		serverID = int64(v)
	case json.Number:
		serverID, _ = v.Int64()
	case string:
		serverID, _ = strconv.ParseInt(v, 10, 64)
	}
	return serverID, channelID, serverID > 0 && channelID != ""
}
```

`internal/desktop/traylabels.go` — дописать:
```go
type trayIcon int

const (
	iconPlain trayIcon = iota
	iconUnread
	iconMention
)

func trayIconFor(unread bool, mentions int) trayIcon {
	switch {
	case mentions > 0:
		return iconMention
	case unread:
		return iconUnread
	}
	return iconPlain
}

// ruPlural picks the Russian plural form for n (1 упоминание, 3 упоминания, 5 упоминаний).
func ruPlural(n int, one, few, many string) string {
	n10, n100 := n%10, n%100
	switch {
	case n10 == 1 && n100 != 11:
		return one
	case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
		return few
	}
	return many
}

func trayTooltip(lang string, unread bool, mentions int) string {
	const app = "spk-mattermost"
	switch {
	case mentions > 0 && lang == "ru":
		return fmt.Sprintf("%s — %d %s", app, mentions, ruPlural(mentions, "упоминание", "упоминания", "упоминаний"))
	case mentions > 0:
		word := "mentions"
		if mentions == 1 {
			word = "mention"
		}
		return fmt.Sprintf("%s — %d %s", app, mentions, word)
	case unread && lang == "ru":
		return app + " — есть непрочитанные"
	case unread:
		return app + " — unread messages"
	}
	return app
}
```
(импорт `fmt`.)

`cmd/spk-mattermost/main.go`:
```go
type desktopOpts struct {
	MMFake       bool // dev builds only: in-process fake server, signed in as alice
	FakeChannels int
}

type runners struct {
	browser func(ctx context.Context, o browserOpts) error
	desktop func(ctx context.Context, o desktopOpts) error
}
```
в `RunE`: `return run.desktop(cmd.Context(), desktopOpts{MMFake: o.MMFake, FakeChannels: o.FakeChannels})`; справку флага `--mm-fake` поменять на `"Start an in-process fake Mattermost server (development/e2e only; desktop: dev builds, signs in as alice)"`. `run_desktop_nowails.go`: сигнатура `runDesktop(context.Context, desktopOpts) error`.

Run: `go test ./internal/desktop/ ./cmd/spk-mattermost/`
Expected: PASS.

- [ ] **Step 3: Иконки с точкой**

`scripts/gen-icon.go` — рисование вынести в функцию и писать три файла:
```go
//go:build ignore

// gen-icon draws the app icon (blue rounded square, white speech bubble) and
// its tray variants with a status dot, so the repo needs no binary design
// assets. Run: go run scripts/gen-icon.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

const n = 256

var (
	blue   = color.NRGBA{0x1e, 0x5e, 0xd8, 0xff}
	white  = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	red    = color.NRGBA{0xe0, 0x24, 0x24, 0xff}
	yellow = color.NRGBA{0xf5, 0x9e, 0x0b, 0xff}
)

func inRounded(x, y, x0, y0, x1, y1, r int) bool {
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

// draw paints the icon; dot (if not nil) is a status dot in the top-right
// corner with a white ring, big enough to read at 22 px tray size.
func draw(dot *color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	const cx, cy, r, ring = 196, 60, 52, 12
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := x-cx, y-cy
			d2 := dx*dx + dy*dy
			switch {
			case dot != nil && d2 <= r*r:
				img.Set(x, y, *dot)
			case dot != nil && d2 <= (r+ring)*(r+ring):
				img.Set(x, y, white)
			case inRounded(x, y, 60, 64, 196, 164, 28):
				img.Set(x, y, white)
			case x >= 90 && x < 130 && y >= 160 && y < 200 && (x-90) <= (200-y):
				img.Set(x, y, white) // bubble tail
			case inRounded(x, y, 8, 8, n-8, n-8, 48):
				img.Set(x, y, blue)
			}
		}
	}
	return img
}

func write(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func main() {
	write("internal/appfiles/icons/icon.png", draw(nil))
	write("internal/appfiles/icons/icon-unread.png", draw(&yellow))
	write("internal/appfiles/icons/icon-mention.png", draw(&red))
}
```
Run: `go run scripts/gen-icon.go && git diff --stat internal/appfiles/icons/icon.png`
Expected: `icon.png` не изменился (побайтно тот же рисунок), появились два новых файла. Посмотреть оба PNG (Read по пути) — точка видна и не перекрывает пузырь.

`internal/appfiles/embed.go`:
```go
//go:embed icons/icon.png
var IconPNG []byte

//go:embed icons/icon-unread.png
var IconUnreadPNG []byte

//go:embed icons/icon-mention.png
var IconMentionPNG []byte
```

- [ ] **Step 4: Wails-часть — notifier, трей, запуск**

`internal/desktop/devtools_dev.go` / `devtools_prod.go` — добавить в блок констант `DevBuild = true` / `DevBuild = false` (экспорт для `cmd`).

`internal/desktop/notify.go` — дописать:
```go
// Notify implements api.Notifier: chat notifications carry the channel to
// open on click; the same ID replaces the channel's previous notification.
func (n *notifier) Notify(m api.Notification) {
	if !n.available.Load() {
		slog.Debug("notification dropped: notifications unavailable", "id", m.ID)
		return
	}
	err := n.svc.SendNotification(notifications.NotificationOptions{
		ID: m.ID, Title: m.Title, Body: m.Body,
		Data: map[string]any{"server_id": m.ServerID, "channel_id": m.ChannelID},
	})
	if err != nil {
		slog.Warn("notification failed", "err", err)
	}
}
```
(импорт `github.com/spk/spk-mattermost/internal/api`.)

`internal/desktop/tray.go` (целиком):
```go
//go:build wails

package desktop

import (
	"context"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mattermost/internal/api"
)

type trayIcons struct{ plain, unread, mention []byte }

func (t trayIcons) pick(i trayIcon) []byte {
	switch i {
	case iconMention:
		return t.mention
	case iconUnread:
		return t.unread
	}
	return t.plain
}

type DevAction struct {
	Label string
	Run   func()
}

func setupTray(app *application.App, icons trayIcons, show, toggle func(), n *notifier, dev []DevAction) *application.SystemTray {
	l := trayLabelsFor(os.Getenv)
	menu := app.NewMenu()
	menu.Add(l.Open).OnClick(func(*application.Context) { show() })
	if devMenu {
		menu.Add(l.TestNotification).OnClick(func(*application.Context) { n.test() })
		for _, a := range dev {
			menu.Add(a.Label).OnClick(func(*application.Context) { go a.Run() })
		}
	}
	menu.AddSeparator()
	menu.Add(l.Quit).OnClick(func(*application.Context) { app.Quit() })

	tray := app.SystemTray.New()
	tray.SetIcon(icons.plain)
	tray.SetTooltip("spk-mattermost")
	tray.SetMenu(menu)
	tray.OnClick(toggle)
	return tray
}

// trayBadge returns the Service.OnBadge callback. Tray updates go through
// InvokeSync on the GTK main thread, so they run on their own goroutine,
// start only after ApplicationStarted + 200 ms (spike S3), and apply just
// the latest badge — the service's goroutine never waits for GTK.
func trayBadge(ctx context.Context, tray *application.SystemTray, icons trayIcons, lang string, started <-chan struct{}) func(api.Badge) {
	ch := make(chan api.Badge, 1)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-started:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-ch:
				tray.SetTooltip(trayTooltip(lang, b.Unread, b.Mentions))
				tray.SetIcon(icons.pick(trayIconFor(b.Unread, b.Mentions)))
			}
		}
	}()
	return func(b api.Badge) { // single producer (the service's coalescer): drain, then send
		select {
		case <-ch:
		default:
		}
		ch <- b
	}
}
```

`internal/desktop/run.go`:
- `Options` + поля:
```go
	IconUnreadPNG  []byte
	IconMentionPNG []byte
	DevActions     []DevAction // dev builds: extra tray items (fake server controls)
```
- колбэк клика:
```go
	n := newNotifier(func(data map[string]any) {
		if id, ch, ok := clickTarget(data); ok {
			o.Service.NotificationClicked(id, ch)
		}
		show()
	})
	if !feat.notifications {
		n.disable()
	}
	o.Service.SetNotifier(n)
```
- после создания `app` (до `app.Run`), вместо старого вызова `setupTray`:
```go
	started := make(chan struct{})
	var startedOnce sync.Once
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		startedOnce.Do(func() { close(started) })
	})
	...
	if feat.tray {
		icons := trayIcons{plain: o.IconPNG, unread: o.IconUnreadPNG, mention: o.IconMentionPNG}
		tray := setupTray(app, icons, show, toggle, n, o.DevActions)
		o.Service.OnBadge(trayBadge(ctx, tray, icons, messagesLanguage(os.Getenv), started))
	}
```

`cmd/spk-mattermost/run_desktop_wails.go` (целиком):
```go
//go:build wails

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/appfiles"
	"github.com/spk/spk-mattermost/internal/desktop"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/paths"
	"github.com/spk/spk-mattermost/internal/store"
)

func runDesktop(ctx context.Context, o desktopOpts) error {
	if o.MMFake && !desktop.DevBuild {
		return errors.New("--mm-fake is available in development builds only")
	}
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
	if err := svc.Start(ctx); err != nil {
		slog.Error("sync did not start; chats stay offline", "err", err) // never keep the window from opening
	}
	defer svc.Close()

	var dev []desktop.DevAction
	if o.MMFake {
		fake := mmfake.Start(mmfake.Options{ExtraChannels: o.FakeChannels})
		defer fake.Close()
		slog.Warn("fake Mattermost server started (development only)", "url", fake.URL())
		if err := signInToFake(ctx, svc, fake.URL()); err != nil {
			return fmt.Errorf("fake server sign-in: %w", err)
		}
		dev = append(dev, desktop.DevAction{Label: "Fake: mention from bob", Run: func() {
			fake.PostAs("c-offtopic", "bob", "@alice ping "+time.Now().Format("15:04:05"))
		}})
	}

	return desktop.Run(ctx, desktop.Options{
		FrontendFS:     frontendFS(),
		Service:        svc,
		Emitter:        em,
		IconPNG:        appfiles.IconPNG,
		IconUnreadPNG:  appfiles.IconUnreadPNG,
		IconMentionPNG: appfiles.IconMentionPNG,
		DevActions:     dev,
	})
}

// signInToFake adds the in-process fake server and signs alice in, so a dev
// desktop run (use a fresh SPK_MATTERMOST_HOME) shows a working chat at once.
func signInToFake(ctx context.Context, svc *api.Service, url string) error {
	srv, err := svc.AddServer(ctx, url)
	if err != nil {
		return err
	}
	_, err = svc.LoginWithPassword(ctx, srv.ID, "alice", "secret")
	return err
}
```

- [ ] **Step 5: Сборки и проверки**

Run: `go test -race ./... && go vet -tags "wails gtk3" ./... && golangci-lint run --build-tags "wails gtk3" && make build-desktop && make cross-check`
Expected: всё проходит.

- [ ] **Step 6: Проверка десктопа на этой машине (dev-режим с фейком)**

Не трогать официальный клиент и живой dev-клиент пользователя (PID в `/tmp/spkmm-live.pid`); у тестового экземпляра — свой `SPK_MATTERMOST_HOME` (свой single-instance ID, Task S4-решение):
```bash
H=$(mktemp -d); SPK_MATTERMOST_HOME=$H build/bin/spk-mattermost-desktop --mm-fake --mm-fake-channels 100 > /tmp/spkmm-t15.log 2>&1 & echo $! > /tmp/spkmm-t15.pid
```
1. Дождаться окна (`wmctrl -l | grep spk-mattermost`), скриншот окна (`import -window <id> /tmp/spkmm-t15-window.png`) и посмотреть: сайдбар фейка, открыт Town Square, лента с постами. Это проверяет Wails-биндинги всех новых методов — опечатка в имени метода ломает только десктоп.
2. Свернуть окно в трей (закрыть крестиком), в меню трея — «Fake: mention from bob» (или вызвать тот же `fake.PostAs` иначе — меню трея можно дёрнуть через `dbus-send` к `com.canonical.dbusmenu` пункта; если не выходит — кликнуть `xdotool`). Ожидается: всплывающее уведомление «Off-Topic / bob: @alice ping …» (перехват: `dbus-monitor --session "interface='org.freedesktop.Notifications',member='Notify'"` в фоне до действия — в выводе заголовок, тело и `replaces_id`), подсказка трея «… — 1 упоминание» (прочитать `busctl --user get-property <имя StatusNotifierItem> /StatusNotifierItem org.kde.StatusNotifierItem ToolTip`), иконка с красной точкой (скриншот области трея).
3. Второе упоминание в течение 10 с — `replaces_id` второго `Notify` равен id первого (одно уведомление на канал).
4. Клик по уведомлению: кликнуть всплывающее уведомление (`xdotool` по координатам из скриншота) либо послать `ActionInvoked` (`dbus-send --session --type=signal /org/freedesktop/Notifications org.freedesktop.Notifications.ActionInvoked uint32:<id> string:default` — сработает, если Wails не фильтрует отправителя). Ожидается: окно показано, открыт Off-Topic, упоминание прочитано, точка в трее пропала. Если ни один способ не срабатывает — зафиксировать и проверить в Task 16 вживую вместе с пользователем.
5. Память: `scripts/pss.sh $(cat /tmp/spkmm-t15.pid)` после предзагрузки 100 каналов — Private_Dirty ≤ 150 МБ; записать в `docs/spikes/…` S4 («Этап 2»).
6. Остановить только свой экземпляр: `kill $(cat /tmp/spkmm-t15.pid)`.

- [ ] **Step 7: Коммит**

```bash
git add internal/desktop internal/appfiles scripts/gen-icon.go cmd/spk-mattermost docs/spikes
git commit -m "desktop: chat notifications open their channel, tray badge (tooltip + dot icons), dev desktop mode with the fake server"
```

---

### Task 16: E2E, живая проверка, память, документация

**Files:**
- Create: `tests/e2e/helpers.ts`, `tests/e2e/chat.spec.ts`, `tests/e2e/reconnect.spec.ts`
- Modify: `tests/e2e/login.spec.ts` (хелперы из `helpers.ts`; после входа теперь открывается чат, а не «Вы вошли как …»)
- Modify: `docs/specs/2026-09-24-spk-mattermost-design.md` (статус, этапы: уведомления и трей — в этапе 2), `AGENTS.md`, `README.md`, `docs/spikes/2026-09-24-stage1-spikes.md` (S4 «Этап 2»), `docs/backlog.md`

**Interfaces:**
- Consumes: всё из Tasks 11–15; test-API `/api/_test/*` (Task 11); фейк: alice/bob/carol, `c-town` (150 постов «Message #N»), `c-offtopic`, `c-secret`, `c-dm-bob`, `c-gm`.
- Produces: e2e-покрытие пользовательских сценариев этапа 2; записанные замеры памяти; документация, совпадающая с кодом.

Как устроены e2e: один экземпляр приложения и один фейк на прогон (`workers: 1`), состояние фейка копится между тестами. Каждый тест добавляет сервер заново и удаляет его в конце, поэтому проверки опираются на собственные сообщения теста (уникальный текст), а не на счётчики «с нуля».

- [ ] **Step 1: Хелперы и сценарии**

`tests/e2e/helpers.ts`:
```ts
import { expect, type Page } from '@playwright/test'

export async function apiToken(page: Page) {
  return (await page.locator('meta[name="spk-mattermost-api-token"]').getAttribute('content'))!
}

async function headers(page: Page) {
  return { Authorization: `Bearer ${await apiToken(page)}`, Origin: new URL(page.url()).origin }
}

export async function testPost(page: Page, path: string, data: unknown = {}) {
  const r = await page.request.post(`/api/_test/${path}`, { headers: await headers(page), data })
  expect(r.ok(), `${path}: ${await r.text()}`).toBeTruthy()
  return r.json()
}

export async function testGet(page: Page, path: string) {
  const r = await page.request.get(`/api/_test/${path}`, { headers: await headers(page) })
  expect(r.ok()).toBeTruthy()
  return r.json()
}

export async function apiCall(page: Page, method: string, data: unknown = {}) {
  const r = await page.request.post(`/api/${method}`, { headers: await headers(page), data })
  expect(r.ok(), `${method}: ${await r.text()}`).toBeTruthy()
  return r.json()
}

export async function fakeURL(page: Page) {
  return ((await testGet(page, 'fake-url')) as { url: string }).url
}

export async function addFakeServer(page: Page) {
  await page.goto('/')
  await page.getByLabel('Server address').fill(await fakeURL(page))
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Fake MM' })).toBeVisible()
}

export async function signInAlice(page: Page) {
  await addFakeServer(page)
  await page.getByLabel('Login or email').fill('alice')
  await page.getByLabel('Password').fill('secret')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
}

export async function serverId(page: Page): Promise<number> {
  const list = (await apiCall(page, 'ListServers')) as { id: number }[]
  return list[list.length - 1].id
}

export function channel(page: Page, name: string | RegExp) {
  return page.getByRole('complementary', { name: 'Server channels' }).getByRole('button', { name })
}

export async function removeServerFromMenu(page: Page) {
  page.once('dialog', (d) => d.accept())
  await page.getByRole('button', { name: 'Server menu' }).click()
  await page.getByRole('menuitem', { name: 'Remove server' }).click()
  await expect(page.getByRole('heading', { name: 'Add a Mattermost server' })).toBeVisible()
}

export const feed = (page: Page) => page.getByRole('log', { name: 'Messages' })
export const unique = (label: string) => `${label} ${Date.now().toString(36)}`
```

`tests/e2e/login.spec.ts`: локальные `apiToken`/`fakeURL`/`addFakeServer` заменить импортом из `./helpers`; в «password sign-in and sign-out» после входа ждать `page.getByRole('heading', { name: /Town Square/ })`, выходить через меню: `page.getByRole('button', { name: 'Server menu' })` → `menuitem 'Sign out'` → `expect(page.getByText('Not signed in')).toBeVisible()`; в «GitLab SSO…» вместо `Signed in as alice` ждать заголовок Town Square; удаление сервера после входа — `removeServerFromMenu`, до входа — старый `removeServer` (кнопка на панели).

`tests/e2e/chat.spec.ts`:
```ts
import { expect, test } from '@playwright/test'
import { apiCall, channel, feed, removeServerFromMenu, serverId, signInAlice, testGet, testPost, unique } from './helpers'

test('sidebar, feed and sending once', async ({ page }) => {
  await signInAlice(page)
  // ^@…$: the group "alice, bob, carol" also contains "bob"
  for (const name of [/Town Square/, /Off-Topic/, /Secret/, /^@\s*bob$/]) await expect(channel(page, name)).toBeVisible()
  await expect(feed(page).getByText('Message #150', { exact: true })).toBeVisible()

  const text = unique('hello e2e')
  await page.getByRole('textbox', { name: 'Message' }).fill(text)
  await page.keyboard.press('Enter')
  await expect(feed(page).getByText(text)).toBeVisible()
  await expect(feed(page).getByText('Sending…')).toHaveCount(0)
  await expect(feed(page).getByText(text)).toHaveCount(1) // REST reply and WS echo → one post
  await removeServerFromMenu(page)
})

test('a mention from someone else: badges, notification, reading clears it', async ({ page }) => {
  await signInAlice(page)
  const id = await serverId(page)
  const text = unique('@alice look')
  await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: text })
  await expect(page.getByRole('navigation').getByLabel('Mentions: 1')).toBeVisible()
  await expect(channel(page, /Off-Topic/).getByLabel('Mentions: 1')).toBeVisible()
  await expect
    .poll(async () => ((await testGet(page, 'notifications')) as { body: string }[]).map((n) => n.body))
    .toContain(`bob: ${text}`)
  const notes = (await testGet(page, 'notifications')) as { title: string; server_id: number; channel_id: string; body: string }[]
  expect(notes.find((n) => n.body === `bob: ${text}`)).toMatchObject({ title: 'Off-Topic', server_id: id, channel_id: 'c-offtopic' })

  await channel(page, /Off-Topic/).click()
  await expect(feed(page).getByText(text)).toBeVisible()
  await expect(page.getByRole('navigation').getByLabel('Mentions: 1')).toHaveCount(0)
  await expect(channel(page, /Off-Topic/).getByLabel('Mentions: 1')).toHaveCount(0)
  await removeServerFromMenu(page)
})

test('notification click opens its channel', async ({ page }) => {
  await signInAlice(page)
  await testPost(page, 'notification-click', { server_id: await serverId(page), channel_id: 'c-dm-bob' })
  await expect(page.getByRole('heading', { name: /bob/ })).toBeVisible()
  await expect(feed(page).getByText('Hi Alice, this is Bob')).toBeVisible()
  await removeServerFromMenu(page)
})

test('edit with arrow-up, delete with confirmation', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  const box = page.getByRole('textbox', { name: 'Message' })
  const text = unique('to edit')
  await box.fill(text)
  await page.keyboard.press('Enter')
  await expect(feed(page).getByText(text)).toBeVisible()
  await expect(feed(page).getByText('Sending…')).toHaveCount(0)

  await box.press('ArrowUp')
  const edit = page.getByRole('textbox', { name: 'Edit message' })
  await edit.fill(`${text} v2`)
  await edit.press('Enter')
  await expect(feed(page).getByText(`${text} v2`)).toBeVisible()
  await expect(feed(page).locator('article', { hasText: `${text} v2` }).getByText('(edited)')).toBeVisible()

  const post = feed(page).locator('article', { hasText: `${text} v2` })
  await post.hover()
  page.once('dialog', (d) => d.accept())
  await post.getByRole('button', { name: 'Delete' }).click()
  await expect(feed(page).getByText(`${text} v2`)).toHaveCount(0)
  await removeServerFromMenu(page)
})

test('mark as unread keeps the channel unread while it is open', async ({ page }) => {
  await signInAlice(page)
  const unread = async () => ((await apiCall(page, 'ListServers')) as { unread: boolean }[]).at(-1)?.unread
  await expect.poll(unread).toBe(false) // baseline: nothing unread, so the dot below comes from this action
  const post = feed(page).locator('article', { hasText: 'Message #140' })
  await post.hover()
  await post.getByRole('button', { name: 'Mark as unread' }).click()
  await expect(page.getByRole('navigation').getByLabel('Unread messages')).toBeVisible()
  await page.waitForTimeout(1000) // the open, focused channel must NOT auto-read it again
  await expect(page.getByRole('navigation').getByLabel('Unread messages')).toBeVisible()
  expect(await unread()).toBe(true)
  await removeServerFromMenu(page)
})

test('history loads up to the first message', async ({ page }) => {
  await signInAlice(page)
  await expect
    .poll(
      async () => {
        await feed(page).evaluate((el) => el.scrollTo({ top: 0 }))
        return feed(page).getByText('Message #1', { exact: true }).count()
      },
      { timeout: 20_000 },
    )
    .toBeGreaterThan(0)
  await removeServerFromMenu(page)
})

test('expired session: cached chat stays readable, sign in again restores it', async ({ page }) => {
  await signInAlice(page)
  await testPost(page, 'fake/revoke')
  await expect(page.getByText('Session expired — showing saved messages.')).toBeVisible({ timeout: 15_000 })
  await expect(feed(page).getByText('Message #150', { exact: true })).toBeVisible()
  await page.getByRole('status').getByRole('button', { name: 'Sign in again' }).first().click()
  await page.getByLabel('Login or email').fill('alice')
  await page.getByLabel('Password').fill('secret')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await expect(page.getByText('Session expired — showing saved messages.')).toHaveCount(0)
  expect(((await apiCall(page, 'ListServers')) as { state: string }[]).at(-1)?.state).not.toBe('needs_reauth')
  await removeServerFromMenu(page)
})
```
(`waitForTimeout(1000)` здесь — проверка **отсутствия** события за окно времени, условия ждать нечего; это единственная пауза в сценариях.)

`tests/e2e/reconnect.spec.ts`:
```ts
import { expect, test } from '@playwright/test'
import { channel, feed, removeServerFromMenu, signInAlice, testPost, unique } from './helpers'

for (const lose of [false, true]) {
  test(`reconnect ${lose ? 'after the server lost our events: resync' : 'without loss: replay'} brings the missed post`, async ({ page }) => {
    await signInAlice(page)
    await channel(page, /Off-Topic/).click()
    await testPost(page, 'fake/drop', { lose })
    const text = unique(lose ? 'missed during resync' : 'missed during resume')
    await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: text })
    await expect(feed(page).getByText(text)).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Offline — reconnecting…')).toHaveCount(0)
    await removeServerFromMenu(page)
  })
}
```

- [ ] **Step 2: Прогон e2e**

Run: `make test-e2e`
Expected: все спеки (login, chat, reconnect) — PASS. Упавший сценарий разбирать по trace (`pnpm exec playwright show-trace`), не подбирать таймауты вслепую.

- [ ] **Step 3: Полный набор ворот**

Run: `make lint && golangci-lint run --build-tags "wails gtk3" && make test-go && make test-front && make build-desktop && make cross-check`
Expected: всё зелёное.

- [ ] **Step 4: Память**

1. Браузерный режим, 100 каналов: `SPK_MATTERMOST_HOME=$(mktemp -d) build/bin/spk-mattermost --browser --port <свободный> --mm-fake --mm-fake-channels 100 --test-api`, войти alice через API (`AddServer`, `LoginWithPassword` через `curl` с токеном из `<meta>`), дождаться окончания предзагрузки (все `GetChannel` → `loaded && !syncing`), `scripts/pss.sh <pid>` сразу и через 10 минут с инъекцией поста раз в секунду в разные каналы (`fake/post`) — роста нет.
2. Десктоп dev-режим с фейком (как в Task 15 Step 6) — Private_Dirty всех процессов ≤ 150 МБ.
3. Результаты — в `docs/spikes/2026-09-24-stage1-spikes.md`, S4, подраздел «Этап 2» (что мерили, цифры, вывод).

- [ ] **Step 5: Живая проверка на mm.citeck.ru вместе с пользователем**

Собрать `make build-desktop` и попросить пользователя перезапустить его dev-клиент (PID в `/tmp/spkmm-live.pid`; самим не убивать, официальный клиент `/opt/Mattermost` не трогать). Сценарий для пользователя и что смотреть в логе (`slog` в stderr) и памяти:
- сайдбар и бейджи совпадают с официальным веб-клиентом; открытие канала мгновенное;
- отправка, правка, удаление, «отметить непрочитанным»; ссылки открываются в браузере;
- уведомление о DM/упоминании, клик открывает канал; подсказка трея с числом упоминаний;
- сон ноутбука / выключение Wi-Fi на минуту → «Нет связи — переподключаюсь…», затем догонка без дублей;
- через час работы — `scripts/pss.sh <pid>` (Private_Dirty ≤ 150 МБ) и отсутствие роста.
Найденные проблемы — чинить в рамках этапа (TDD, отдельные коммиты), мелочи вне этапа — в `docs/backlog.md`.

- [ ] **Step 6: Документация**

- `docs/specs/…`: статус «Этап 2 реализован и проверен (дата)», в «Этапы» — уведомления и трей в этапе 2 (решение пользователя 2026-09-24), этап 3 — без них; раздел «Прочтение» — фокус учитывается только для активного сервера.
- `AGENTS.md`, «Правила»/«Things that bite»: хуки воркера не блокируют, всё тяжёлое — в горутинах, UI-события схлопываются (`events.Coalescer`, 100 мс); `servers_changed` частое — UI не сбрасывает по нему ошибки и выбор; фокус — только активному серверу; действия записи при `needs_reauth` отказывают сразу; test-API фейка (`/api/_test/fake/*`, `notifications`, `notification-click`) и `--mm-fake-channels`; десктоп `--mm-fake` (только dev) с отдельным `SPK_MATTERMOST_HOME`; jsdom-техника для теста виртуализированной ленты (`offsetHeight`/`scrollTo`).
- `README.md`: что умеет клиент после этапа 2, флаги `--mm-fake-channels` и десктопный `--mm-fake`.
- `docs/backlog.md`: оставшиеся мелочи из живой проверки; удалить пункты, которые этап закрыл.

- [ ] **Step 7: Коммит и пуш**

```bash
git add tests/e2e docs AGENTS.md README.md
git commit -m "e2e: chat and reconnect scenarios; docs: stage 2 status, memory numbers, agent rules"
git push origin main
```
Готово, когда: все ворота зелёные, e2e проходят, пользователь подтвердил работу на mm.citeck.ru, замеры памяти записаны, `git status` чистый и `main` запушен.
