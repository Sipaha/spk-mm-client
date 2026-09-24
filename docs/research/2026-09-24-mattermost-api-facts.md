# Mattermost 10.11 — проверенные факты протокола и API

Источник: исходники github.com/mattermost/mattermost, ветка `release-10.11`
(commit `60ecc27b9c0b`), сервер `server/…` и веб-клиент `webapp/…`. Собрано
2026-09-24 для плана этапа 2. «(по коду)» — выведено из кода, не наблюдалось
вживую. Пути — относительно корня репозитория Mattermost.

## 1. WebSocket

**Авторизация — заголовком `Authorization: Bearer <token>` на upgrade-запросе,
без `authentication_challenge`.** Resume возможен только если сессия известна
при upgrade (заголовок/cookie/`access_token`); с challenge сервер всегда
выдаёт новый `connection_id`. Challenge при уже заданном токене сервер
молча игнорирует (без `seq_reply`). Неверный токен в заголовке не даёт 401:
upgrade проходит без сессии, и через 5 с сервер закрывает сокет («did not
authenticate»). Поэтому валидность токена проверяем REST-запросом
`users/me` перед подключением (401 → «нужен вход»).
(`server/channels/api4/websocket.go`, `app/authentication.go`
`ParseAuthTokenFromRequest`, `app/platform/websocket_router.go`.)

**URL:** `wss://host/api/v4/websocket?connection_id=<id>&sequence_number=<N>`.
`sequence_number` — **следующий ожидаемый** seq (веб-клиент:
`serverSequence = msg.seq + 1`). `connection_id` без `sequence_number` или
неверный `connection_id` → сервер закрывает сокет. Первое подключение — без
параметров.

**Исходы переподключения** (`app/platform/web_conn.go` `writePump`,
`web_hub.go`):
- A. Соединение ещё в хабе (≈до 5 мин, reaper `inactiveConnReaperInterval`),
  seq в dead queue (128 событий) → сервер досылает пропущенное по порядку,
  **hello не шлёт**.
- B. Ничего не пропущено → ни досылки, ни hello.
- C. Соединение есть, seq вытеснен из очереди → новый `connection_id`,
  `hello` с `seq: 0`.
- D. Соединения нет (reaper, рестарт сервера, другой узел, переполнение
  очереди отправки 256) → новый `connection_id`, `hello` с `seq: 0`.

Клиент распознаёт потерю так: пришёл `hello`, и его `data.connection_id`
отличается от прежнего → полный ресинк; после hello следующий seq = 1
(hello.seq + 1). Если пришло событие с `seq` ≠ ожидаемому — закрыть сокет
(веб-клиент: код 4001) и переподключиться с resume. Ответы на запросы
клиента несут `seq_reply` и в проверке seq не участвуют.

**Keepalive:** сервер шлёт протокольный Ping каждые 60 с, read deadline 100 с
продлевается только Pong'ом (coder/websocket отвечает на Ping сам при
активном чтении). Клиент пингует сам (веб-клиент — `{action:"ping"}` раз в
30 с). Кадры клиент→сервер ≤ 8 КБ; сервер→клиент без лимита (ставим
read limit с запасом). При заполненной на 50% очереди сервер молча
выкидывает `typing`, `status_change`, `multiple_channels_viewed`.

**Конверт события:** `{"event","data":{…},"broadcast":{"user_id","channel_id","team_id","omit_users",…},"seq"}`.

## 2. События (data / broadcast)

| Событие | data | broadcast |
|---|---|---|
| `hello` | `server_version`, `connection_id`, `server_hostname` | user_id |
| `posted` | `post` (**JSON-строка**), `channel_type`, `channel_display_name`, `channel_name`, `sender_name`, `team_id`, `set_online`; `mentions` / `followers` — JSON-строка-массив, содержит **только id получателя**, если он упомянут/подписчик; `should_ack` только при `posted_ack=true` | channel_id |
| `post_edited` | `post` (JSON-строка) | channel_id |
| `post_deleted` | `post` (JSON-строка) | channel_id |
| `channel_viewed` | **в 10.11 не отправляется** | — |
| `multiple_channels_viewed` | `channel_times` {channel_id: last_viewed_at} | user_id |
| `post_unread` | `msg_count`, `msg_count_root`, `mention_count`, `mention_count_root`, `urgent_mention_count`, `last_viewed_at`, `post_id` | team_id, channel_id, user_id |
| `reaction_added/removed` | `reaction` (JSON-строка `{user_id,post_id,emoji_name,create_at}`) | channel_id |
| `channel_updated` | `channel` (JSON-строка) | channel_id |
| `channel_created` | `channel_id`, `team_id` (только создателю) | user_id |
| `channel_deleted` | `channel_id`, `delete_at` | team_id / channel_id |
| `channel_restored`, `channel_converted` | `channel_id` | |
| `user_added` | `user_id`, `team_id` | channel_id (+ отдельно самому пользователю) |
| `user_removed` | в канал: `user_id`, `remover_id`; удалённому: `channel_id`, `remover_id` | channel_id / user_id |
| `direct_added` | `creator_id`, `teammate_id` | channel_id |
| `group_added` | `teammate_ids` (JSON-строка) | channel_id + user_id |
| `channel_member_updated` | `channelMember` (JSON-строка) | user_id |
| `sidebar_category_created/deleted` | `category_id` | team_id, user_id |
| `sidebar_category_updated` | `updatedCategories` (JSON-строка); после сохранения preferences — **без data и team_id** | team_id?, user_id |
| `sidebar_category_order_updated` | `order` (массив id) | team_id, user_id |
| `preferences_changed/deleted` | `preferences` (JSON-строка `[]Preference`) | user_id |
| `user_updated` | `user` (**объект**, не строка) | все / user_id |
| `thread_updated` | `thread` (JSON-строка), `previous_unread_*` | team_id, user_id |
| `thread_read_changed` | `thread_id`, `timestamp`, `unread_mentions`, `unread_replies`, `channel_id` | team_id, user_id |
| `status_change` | `status`, `user_id` — **только самому пользователю** | user_id |

Чужие статусы по WS не приходят — веб-клиент опрашивает
`POST /users/status/ids` раз в ~60 с.

## 3. REST

- `GET /users/me/teams` → `[]Team{id,name,display_name,delete_at,…}`.
- `GET /users/me/channels` → все каналы всех команд + DM/GM одним массивом
  (сервер сам листает). Channel: `id, team_id ("" у DM/GM), type (O/P/D/G),
  display_name ("" у DM), name (DM: "idA__idB"), header, purpose, create_at,
  update_at, delete_at, last_post_at, last_root_post_at, total_msg_count,
  total_msg_count_root`. **Правка поста тоже двигает `last_post_at`.**
  (Командный вариант `users/{id}/teams/{tid}/channels` на пустом результате
  отдаёт 404.)
- `GET /users/me/channel_members?page=N&per_page=200` → массив
  ChannelMember (с `page=-1` — NDJSON-поток всех). Поля: `channel_id,
  user_id, last_viewed_at, msg_count, msg_count_root, mention_count,
  mention_count_root, urgent_mention_count, last_update_at, notify_props`.
  notify_props по умолчанию: `desktop:"default", mark_unread:"all",
  ignore_channel_mentions:"default", push:"default", email:"default"`.
  `mark_unread:"mention"` = канал приглушён.
- `GET /users/me/teams/{tid}/channels/categories` →
  `{categories:[{id,team_id,type,display_name,sorting,sort_order,muted,collapsed,channel_ids}],order:[id…]}`.
  type: `favorites|channels|direct_messages|custom`; sorting: `""|manual|recent|alpha`.
  DM/GM, не лежащие в других категориях, сервер кладёт в `direct_messages`
  **каждой** команды.
- `GET /users/me/preferences` → `[]{user_id,category,name,value}`.
- `GET /users/me/status` → `{user_id,status(online|away|dnd|offline|ooo),manual,last_activity_at,dnd_end_time(секунды!)}`.
- `POST /users/ids` (тело — массив id; `?since=ms` → только изменённые) →
  `[]User{id,username,first_name,last_name,nickname,delete_at,update_at,is_bot,notify_props(только свой),locale}`.
- `GET /channels/{id}/posts` — `per_page` (≤200, по умолч. 60), `before`,
  `after`, `since`, `page`, `collapsedThreads`, `collapsedThreadsExtended`,
  `skipFetchThreads`. Приоритет: since > after > before. Ответ
  `{order[],posts{id:Post},next_post_id,prev_post_id}`; конец истории —
  `prev_post_id == ""` на странице `before`/первой странице.
  Post: `id, create_at, update_at, edit_at, delete_at, is_pinned, user_id,
  channel_id, root_id, original_id, message, type, props, file_ids,
  pending_post_id, reply_count, last_reply_at, metadata{files,reactions,embeds,…}`.
- `POST /posts` → **201** + Post. Повтор с тем же `pending_post_id` в течение
  30 с возвращает уже созданный пост (идемпотентность), а пока первый ещё
  сохраняется — 500. WS-эхо `posted` несёт тот же `pending_post_id` (по коду).
- `PUT /posts/{id}/patch` `{message}`; `DELETE /posts/{id}` (удаляет и ответы).
- `POST /channels/members/me/view` `{channel_id, prev_channel_id:"", collapsed_threads_supported:true}`
  → `{status, last_viewed_at_times}`.
- `POST /users/me/posts/{pid}/set_unread` `{collapsed_threads_supported:true}` →
  `{team_id,channel_id,msg_count,msg_count_root,mention_count,mention_count_root,urgent_mention_count,last_viewed_at}`.
- Лимиты (по умолчанию выключены): 10 req/s, burst 100; 429 + `Retry-After`.

## 4. `since` — ловушки

- Сравнивает `update_at > since` (мс), **не больше 1000 строк**, порядок
  внутри лимита не гарантирован → результат ≥ 1000 = «перезагрузить окно
  последней страницей».
- Возвращает **удалённые** посты (`delete_at > 0`) и **строки истории
  правок** (`original_id != ""`) — последние отбрасывать.
- С `collapsedThreads=true` — только корневые посты (с `reply_count`,
  `last_reply_at`), без ответов.
- `update_at` поста двигают: правка, удаление (и ответов), реакции, новый
  ответ (у корня).
- Веб-клиент после каждого переподключения: `users/me/channels`,
  `channel_members?page=-1`, категории, `since` по открытому каналу,
  `teams/unread`, `users/ids?since=lastDisconnectAt`.

## 5. Непрочитанное (веб-клиент, `mattermost-redux/src/utils/channel_utils.ts`)

```ts
crt:     messages = channel.total_msg_count_root - member.msg_count_root; mentions = member.mention_count_root
non-crt: messages = channel.total_msg_count      - member.msg_count;      mentions = member.mention_count
showUnread = mentions > 0 || (!muted && messages > 0)      // muted: notify_props.mark_unread === "mention"
```
Суммы по команде/серверу пропускают приглушённые, архивные каналы и DM с
деактивированным пользователем. На `posted`: `total_msg_count(+_root для
корня)++`; упомянут (id в `mentions`) → `mention_count(+_root)++`. В DM
сервер считает каждое сообщение упоминанием получателя.

**CRT включён**, если `config.CollapsedThreads` ∉ {`disabled`, пусто} и
(preference `display_settings/collapsed_reply_threads` == `on` или конфиг
`always_on`); preference по умолчанию `on` при `default_on`/`always_on`.
Серверный дефолт — `always_on`.

**Имена:** preference `display_settings/name_format` (если не заблокировано
`LockTeammateNameDisplay`), иначе `config.TeammateNameDisplay`, иначе
`username`. Значения: `username`, `nickname_full_name`, `full_name`.

**Видимость DM/GM** (`selectors/entities/channel_categories.ts`): DM/GM
виден, если непрочитан, или открыт сейчас, или preference
`direct_channel_show/<otherUserId>` (GM: `group_channel_show/<channelId>`)
существует и ≠ `"false"`. В категории `direct_messages` — сортировка:
текущий, непрочитанные, затем по `max(member.last_viewed_at,
channel_approximate_view_time/<id>, channel_open_time/<id>)` убыв.; обрезка до
`max(limit, число непрочитанных)`, limit = preference
`sidebar_settings/limit_visible_dms_gms` (по умолчанию **40**).
Сортировка категорий: `recent` — по `max(crt ? last_root_post_at||last_post_at : last_post_at, create_at)`
убыв.; `alpha` и `""` — приглушённые в конце, затем по имени
(`localeCompare`, numeric); `manual` — порядок `channel_ids`.

## 6. Уведомления (`webapp/channels/src/actions/notification_actions.tsx`)

По порядку:
1. Свой пост (и не `props.from_webhook == "true"`) → нет.
2. Системный пост (`type` начинается с `system_`) → нет, кроме «вас добавили в канал».
3. `props.force_notification` → да (пропустить проверки ниже).
4. Канал приглушён (`mark_unread == "mention"`) → нет.
5. Статус `dnd` или `ooo` → нет.
6. `mentions ∪ followers` из события. `channelProp = member.notify_props.desktop || "default"`;
   `level = channelProp == "default" ? (user.notify_props.desktop || "all") : channelProp`;
   GM + default + пользовательский `mention` → `all`.
7. `level == none` → нет. GM + `mention` → клиентский поиск упоминаний в
   тексте (ключи упоминаний пользователя; `@all/@here/@channel` игнорируются,
   если `ignore_channel_mentions == on` или (`default` и
   `user.notify_props.channel == "false"`)). Иначе `level == mention` и id
   нет в mentions и канал не DM → нет. CRT-ответ при `level == all` и нас
   нет в followers → нет.
8. Окно в фокусе и открыт этот канал (для CRT-ответа — этот тред) → нет.

`desktop_threads` применяет сервер при заполнении `followers`.
User notify_props по умолчанию: `desktop:"mention", channel:"true",
mention_keys:"", first_name:"false", desktop_threads:"all", desktop_sound:"true"`.
