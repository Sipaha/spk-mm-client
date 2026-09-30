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
| `post_deleted` | `post` (JSON-строка) — пост **до** удаления: `delete_at` 0, `update_at` его собственный (`app/post.go` `DeletePost`: `GetSingle`, затем `Store.Delete`, затем `CleanUpAfterPostDeletion` маршалит ту копию) | channel_id |
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
  `skipFetchThreads`. Приоритет: since > after > before. Без CRT
  `reply_count` приходит только с `skipFetchThreads=true` (подзапрос COUNT в
  `getRootPosts`/`getParentsPosts`/`GetPostsSince`), иначе 0, а в `posts`
  добавляются все треды страницы; клиент шлёт `skipFetchThreads=true` всегда.
  При CRT `reply_count` берётся из `Threads`. `post_edited` несёт настоящий
  `reply_count` (`GetSingle`). Ответ
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

## 7. Этап 3: аватары, статусы, файлы, реакции, эмодзи

Проверено 2026-09-25 по исходникам `release-10.11` (raw.githubusercontent.com,
ветка `release-10.11`) для плана `docs/plans/2026-09-25-stage3-avatars-previews-reactions.md`.
Ссылки — относительно `https://github.com/mattermost/mattermost/blob/release-10.11/`.

### 7.1 Аватары

- `GET /api/v4/users/{id}/image` (`server/channels/api4/user.go` `getProfileImage`):
  ETag = `strconv.FormatInt(user.LastPictureUpdate, 10)`; при совпадении
  `If-None-Match` — **304** (`c.HandleEtag`); иначе тело PNG,
  `Content-Type: image/png` всегда, `Cache-Control: max-age=86400, private`
  (5 минут, если файл не прочитался и отдан сгенерированный). 403, если
  пользователя «не видно» (`UserCanSeeOtherUser`).
- Пользователь без своей картинки всё равно получает картинку по `/image`:
  сервер генерирует её из инициалов (`GetProfileImage` → default). Отдельно
  есть `GET /users/{id}/image/default`.
- `last_picture_update` (`server/public/model/user.go`, `omitempty`) —
  версия картинки. Загрузка своей — `UpdateLastPictureUpdate` = `+now`;
  сброс на сгенерированную — `ResetLastPictureUpdate` = **`-now`
  (отрицательное)** (`server/channels/store/sqlstore/user_store.go`); смена
  username при сгенерированной картинке перегенерирует её и снова сбрасывает
  версию (`app/user.go` `UpdateUser`). Значит, версия меняется при любой смене
  картинки, может быть 0 (поле не пришло) или отрицательной.
- Веб-клиент строит URL `…/users/{id}/image?_={last_picture_update}` (параметр
  `_` только если версия ≠ 0; `webapp/platform/client/src/client4.ts`
  `getProfilePictureUrl`) — сервер параметр игнорирует, это ключ кэша браузера.
- Сайдбар веб-клиента: у DM — аватар собеседника размера `xs` с иконкой статуса
  (у ботов статуса нет) (`components/sidebar/.../sidebar_direct_channel.tsx`);
  у GM — не аватары, а квадрат с числом участников (`sidebar_group_channel.tsx`,
  число из статистики канала). Иконка статуса: online / away / dnd, всё
  остальное (offline, ooo) — «offline» (`components/status_icon.tsx`).

### 7.2 Статусы присутствия

- `POST /api/v4/users/status/ids` (`api4/status.go` `getUserStatusesByIds`):
  тело — массив id; дубли убираются; **каждый id обязан быть длиной 26**, иначе
  400 на весь запрос; пустой массив — 400. Ответ — `[]Status`
  `{user_id,status,manual,last_activity_at,dnd_end_time}`; пользователь без
  строки в таблице статусов (и несуществующий id) → `status:"offline"`
  (`app/platform/status.go` `GetUserStatusesByIds`). При
  `ServiceSettings.EnableUserStatuses=false` ответ — пустой массив.
- `status_change` публикуется с `broadcast.user_id = <чей статус>` —
  **приходит только самому пользователю** (`app/platform/status.go`
  `BroadcastStatus`), при «занятом» сервере не публикуется вовсе. Чужие статусы
  веб-клиент опрашивает: `addVisibleUsersInCurrentChannelAndSelfToStatusPoll`
  (`webapp/channels/src/actions/status_actions.ts`) — авторы видимых постов
  открытого канала, собеседники DM с `direct_channel_show=true` и сам
  пользователь; период — `config.UsersStatusAndProfileFetchingPollIntervalMilliseconds`.

### 7.3 Файлы

- Маршруты (`api4/file.go`): `GET /files/{id}` (оригинал), `/files/{id}/thumbnail`,
  `/files/{id}/preview`, `/files/{id}/info`; все требуют права читать канал
  файла (403 иначе, 404 для удалённого). `?download=1` — `Content-Disposition: attachment`.
- Миниатюра — `image/jpeg`, вписана в **120×100**; превью — `image/jpeg`
  шириной до **1920** (`app/file.go` `imageThumbnailWidth/Height`,
  `imagePreviewWidth`). Нет миниатюры/превью → **400**
  (`api.file.get_file_thumbnail.no_thumbnail.app_error` / `…preview…`).
- Отдача — `http.ServeContent` (`server/platform/shared/web/files.go`
  `WriteFileResponse`): **`Range` поддерживается** (206);
  `X-Content-Type-Options: nosniff`; «опасные» типы отдаются как `text/plain`;
  inline только для медиатипов (jpeg, png, bmp, gif, tiff, webp, …), остальное —
  attachment. При `WebserverMode=gzip` вместо `Content-Length` —
  `X-Uncompressed-Content-Length`.
- `FileInfo` (`server/public/model/file_info.go`): `id, user_id, post_id,
  channel_id, create_at, update_at, delete_at, name, extension, size, mime_type,
  width, height (omitempty), has_preview_image (omitempty), mini_preview
  (base64 крошечного JPEG), remote_id, archived`.
- `has_preview_image` (`app/file.go` `preprocessImage`): `true` для
  декодируемых растровых картинок; **`false` для SVG** (только размеры) и для
  **GIF** (анимация — веб-клиент показывает оригинал); миниатюра/превью GIF при
  этом всё равно пишутся. Картинка больше `MaxImageResolution` не загружается.
- Пост из `posted` несёт `metadata.files`: `CreatePost` готовит пост через
  `PreparePostForClient` до рассылки (`app/post.go`), событие кладёт
  `post.ToJSON()` (`publishWebsocketEventForPost`).

### 7.4 Реакции

- `POST /api/v4/reactions` тело `{user_id, post_id, emoji_name}`
  (`api4/reaction.go` `saveReaction`): `user_id` ≠ сессии → 403
  (`api.reaction.save_reaction.user_id.app_error`); нет права
  `add_reaction` в канале → 403; ответ **200** + `Reaction`. Имя проверяется:
  `^[a-zA-Z0-9\-\+_]+$`, ≤ 64 (`model/reaction.go`), и оно должно быть
  **системным** (`model.GetSystemEmojiId`, таблица `SystemEmojis` в
  `model/emoji_data.go` — 4464 имени вместе с алиасами и оттенками кожи) или
  существующим кастомным (иначе 404 из `GetEmojiByName`) (`app/reaction.go`).
  Новое имя сверх `ServiceSettings.UniqueEmojiReactionLimitPerPost` → **400**
  `app.reaction.save.save.too_many_reactions`; архивный канал → **403**
  `api.reaction.save.archived_channel.app_error`.
- `DELETE /api/v4/users/{user_id}/posts/{post_id}/reactions/{emoji_name}` →
  `{"status":"OK"}`; архивный канал → 403
  (`api.reaction.delete.archived_channel.app_error`); чужую реакцию — только с
  `remove_others_reactions`.
- События `reaction_added` / `reaction_removed`: `data.reaction` — JSON-строка
  `Reaction{user_id,post_id,emoji_name,create_at,update_at,delete_at,remote_id,channel_id}`,
  `broadcast.channel_id` = канал поста (`app/reaction.go` `sendReactionEvent`);
  приходят и автору (эхо). `update_at` поста меняется.
- Алиасы — разные имена: `+1` и `thumbsup` — обе строки есть в `SystemEmojis`
  с одним кодом `1f44d`, но реакции с ними — **разные записи**. Веб-клиент
  ставит из пикера **первое** short name набора (`getEmojiName`), то есть `+1`,
  `smile`, …; клик по чипу повторяет имя чипа.
- Недавние эмодзи (`webapp/channels/src/actions/emoji_actions.js`
  `addRecentEmojis`): preference `category="recent_emojis"`, `name=<user id>`,
  `value` — JSON `[{"name","usageCount"}]`, **не больше 27**, отсортирован по
  `usageCount` по возрастанию (повторно выбранное удаляется и дописывается в
  конец с `usageCount+1`); показывается от конца.

### 7.5 Эмодзи

- Набор веб-клиента — npm `emoji-datasource@6.1.1` + `additional_shortnames.json`
  (`webapp/channels/build/emoji/make_emojis.mjs`); тот же генератор пишет
  серверную `SystemEmojis`. Все имена из `emoji.json` этой версии + добавочные
  есть в `SystemEmojis` (проверено скриптом 2026-09-25: 2328 имён без
  оттенков, расхождений 0).
- Кастомные (`api4/emoji.go`): `GET /emoji?page&per_page(≤200)&sort=name` →
  `[]Emoji{id,creator_id,name,create_at,update_at,delete_at}`;
  `GET /emoji/name/{name}` (404, если нет); `POST /emoji/names` (массив имён);
  `GET /emoji/{id}/image` — `Content-Type: image/<тип>`,
  `Cache-Control: max-age=2592000, private`. При
  `ServiceSettings.EnableCustomEmoji=false` — **501** на всё. Флаг есть в
  `config/client?format=old` (`EnableCustomEmoji`).
- `emoji_added`: `data.emoji` — JSON-строка `Emoji`, рассылается всем
  (`app/emoji.go`).

### 7.7 Загрузка файлов

Не проверено вживую (чтение `api4/file.go`, `app/file.go`, `app/post.go`,
`model/config.go`, `model/post.go` из `release-10.11`), см. также спайк
`docs/spikes/2026-09-27-attachments-spike.md` §5.

- `POST /api/v4/files` — два режима:
  - **Простой** (`api4/file.go:77-176`, `doUploadFile`): query
    `channel_id` и `filename` обязательны, `client_id` опционален; тело —
    **сырые байты одного файла**, без JSON/base64/multipart. `Content-Length`
    обязателен: 0 (или отсутствие) → **400**. Требуется право
    `PermissionUploadFile` в канале (иначе 403).
  - **Multipart** (`api4/file.go:198-285`): `channel_id` должен идти до
    файловых частей формы, иначе сервер уходит в буферизованный legacy-путь;
    `client_ids` (если задан) должен совпадать по числу с файлами. Клиент
    этого не использует (один файл на запрос — простого режима достаточно).
  - `ServiceSettings.EnableFileAttachments=false` → **403**
    `api.file.attachments.disabled.app_error` (проверяется раньше
    прав на канал).
  - Размер тела больше `FileSettings.MaxFileSize` → **413**; лимит по
    умолчанию **100 МиБ** (`model/config.go:1820`, `MaxFileSize`).
    `FileSettings.MaxImageResolution` (умолч. **7680×4320**) отдельно
    ограничивает декодируемые растровые картинки (перекодировка/превью не
    строятся, но сам файл при этом всё равно принимается — только резолюшн
    картинки, не размер тела).
  - Ответ **201** `FileUploadResponse{file_infos:[FileInfo], client_ids:[…]}`
    — `client_ids[i]` соответствует `file_infos[i]` (для простого режима —
    один элемент); `FileInfo.user_id` — сессия, `post_id=""` (файл ещё не
    прикреплён к посту).
  - `Upload sessions` (`POST /api/v4/uploads`, `POST /api/v4/uploads/{id}`,
    `api4/upload.go`) — отдельный, возобновляемый протокол загрузки
    (используется мобильным клиентом для больших файлов); клиентом не
    используется — простого режима с одним файлом на запрос достаточно, и
    `MaxFileSize` в обоих одинаков.
- `MaxFileSize` и `EnableFileAttachments` присутствуют в
  `GET /api/v4/config/client?format=old` для залогиненных пользователей
  (`server/config/client.go:75,86`): `MaxFileSize` — **строка** с
  десятичным числом байт, `EnableFileAttachments` — строка `"true"`/`"false"`.
- Прикрепление к посту (`app/post.go` `attachFileIDsToPost`,
  вызывается из `CreatePost`): `Post.FileIDs`, для каждого id —
  молча отбрасывается, если файл не существует, принадлежит другому
  каналу, был загружен другим пользователем или уже прикреплён к посту
  (`post_id != ""`); дубликаты в списке убираются. У выживших id
  `FileInfo.PostID` проставляется на ID нового поста. `model/post.go:57,518`
  (`PostFileidsMaxRunes = 300`) ограничивает **JSON-представление** массива
  `file_ids`: 26-символьные id в кавычках с запятыми — это около **10** id.
  Итоговый набор id, реально прикреплённых, может быть меньше запрошенного
  (отброс — не ошибка всего запроса).
- Раздача (`GET /files/{id}`, `/thumbnail`, `/preview`, `/info`) — см. §7.3;
  ничего специфичного для только что загруженного файла (он неотличим от
  файла, прикреплённого раньше).

### 7.8 Не проверено вживую

Всё выше (§7.1–7.7) — чтение кода, не наблюдение на mm.citeck.ru. Отдельно
не проверено: ведёт ли `Range` себя так же за gzip-прокси сервера (код
отдачи — `ServeContent`, но ответ может быть сжат) — клиент поэтому
принимает и 200, и 206 и сам обрезает тело. Для §7.7 отдельно не
проверено: точный `app_error` id при `Content-Length: 0` и при 413
(`api.file.upload_file.read_request.app_error` /
`api.file.upload_file.too_large_detailed.app_error` — по памяти о похожих
именах в этом файле сервера, не по чтению точной версии); фейк
(`internal/mmfake`) использует эти же id для правдоподобия, но клиент не
завязывается на точный `id`, только на код ответа (400/413) и заголовок
`Retry-After` там, где он есть.

## 8. Треды (Task 1 плана `docs/plans/2026-09-28-threads.md`)

Проверено 2026-09-28 по исходникам `release-10.11` (тот же клон-коммит, что
в строке 3), пути ниже — от корня клона Mattermost.

### 8.1 `GET /api/v4/posts/{id}/thread`

`server/channels/api4/post.go:760–880` (`getPostThread`), store —
`server/channels/store/sqlstore/post_store.go:575–712` (CRT,
`getPostWithCollapsedThreads`) и `:714–897` (без CRT, `Get`).

- `perPage` — **без него сервер возвращает тред целиком** (комментарий в
  коде: "return all items unless it's set", `post.go:766–769`); `≤200`
  (`web.PerPageMaximum`), иначе 400. Наш REST-клиент (`rest.PostThread`)
  никогда не зовёт без `perPage` по конструкции: 0 → `ThreadPageDefault`
  (60), `>200` → `ThreadPageMax` (200).
- `direction=up|down` — `up`=DESC (новейшие первыми), `down`=ASC; пустой —
  без `ORDER BY` в коде сервера (де-факто неопределённый порядок). Клиент
  всегда шлёт `up`. **Фейк отклоняется от этого намеренно**: пустой/
  отсутствующий `direction` он тоже трактует как `up` (детерминированный
  порядок вместо неопределённого) — упрощение, безопасное потому что
  реальный клиент никогда не оставляет `direction` пустым.
- `fromCreateAt`/`fromPost` — курсор постранично; `fromPost` без
  `fromCreateAt` → 400 (`post.go:789–793`). `fromUpdateAt` несовместим с
  `fromCreateAt` (400) и с `direction=up` при `updatesOnly` (400);
  `updatesOnly` требует `fromUpdateAt`. Наш клиент использует только
  `fromCreateAt`/`fromPost`, никогда `fromUpdateAt`/`updatesOnly` —
  **`updatesOnly` не увидел бы удалений ответов** (сервер фильтрует
  `DeleteAt=0` до сравнения `UpdateAt`, `post_store.go:611`, `724`).
- Ответ `PostList{order, posts, has_next}`; **`order[0]` — всегда сам
  запрошенный пост** (`pl.AddPost(&post); pl.AddOrder(id)` до цикла по
  ответам, `post_store.go:733–734` (CRT) и «add post/order for the found
  root» в `Get` без CRT). `has_next` — `LIMIT perPage+1`, сервер отбрасывает
  последний элемент и выставляет `true` (`post_store.go:686–692`,
  `879–887`); **сервер выставляет `list.HasNext = &hasNext` безусловно**
  (`post_store.go:709`, `894` — присваивание вне `if opts.PerPage != 0`):
  без `perPage` `hasNext` остаётся `false` (лимита нет — значит, отдан весь
  тред), но указатель не `nil` (fix round 1: предыдущая версия этого
  раздела и фейк ошибочно утверждали, что без `perPage` `has_next`
  отсутствует вовсе). Модель — `PostList.HasNext *bool
  json:"has_next,omitempty"`; фейк теперь тоже всегда его выставляет.
- `id` ответа (не корня) — не ошибка. С `collapsedThreads=true` сервер
  отвечает 200: `order` = `[id ответа]` (сам ответ, с его `root_id`), ответов
  нет — CRT-запрос выбирает `WHERE RootId = id` (`post_store.go:575–697`),
  `has_next=false`; `app/post.go:1100–1130` (`GetPostThread`) и
  `api4/post.go:858–866` ничего не проверяют сверх наличия поста и прав.
  Без CRT (`post_store.go:714–760`) — ответ в `order[0]`, затем весь тред
  его корня (корень и ответы, `RootId = <корень>`). Клиент по `root_id`
  поста `order[0]` понимает, что открыт ответ, и открывает тред корня
  (`mmsync.fetchThread` → `state.RedirectThread`). Фейк (Task 3, fix round 2)
  с CRT отвечает как сервер; без CRT отдаёт ответ без треда корня — клиенту
  этого достаточно (смотрит только `order[0].root_id`). До fix round 2 фейк
  отвечал на это 400 — было ошибочное «намеренно строже».
- Удалённые ответы и строки истории правок (`original_id != ""`) не
  отдаются (`DeleteAt=0` в WHERE).
- Права — как у чтения поста (`GetPostIfAuthorized`, членство в канале);
  неизвестный/удалённый корень → 404; не член канала → 403.

### 8.2 `reply_count`, `last_reply_at`, счётчик ответов

- **Новый ответ несёт `reply_count` треда в себе**, не только на корне:
  `populateReplyCount` (`post_store.go:276–292`) присваивает создаваемому
  ответу текущее число (неудалённых) ответов треда, **включая этот**; веб-
  клиент просто копирует это число на корень
  (`mattermost-redux/…/reducers/entities/posts.ts:418–431`, `99–112`) вместо
  инкремента — так избегается двойной счёт REST+WS-эха своего ответа.
  Фейк (`createPostLocked`) делает то же: `root.ReplyCount++`, затем
  `p.ReplyCount = root.ReplyCount`.
- Корень получает свежий `UpdateAt` на каждый ответ
  (`post_store.go:270–274`, `UPDATE Posts SET UpdateAt=?`) — поэтому
  страница ленты, полученная после живого ответа, несёт «старый» (по
  времени относительно ответа) `UpdateAt` корня, что важно для правила
  «не затирать более новый локальный счётчик» (Task 2/3).
- Удаление одного ответа уменьшает `reply_count` треда и корня на сервере
  (`updateThreadAfterReplyDeletion`, `last_reply_at` пересчитывается по
  оставшимся ответам) без отдельного события про корень — тот же
  `post_deleted` покрывает это; `UpdateAt` корня сдвигается на время
  удаления (`post_store.go:1004–1014`). Время удаления знает только строка
  `since=` (`delete_at`); в `post_deleted` его нет (см. таблицу событий).
  Фейк: `deleteReplyEffectsLocked`.
- Удаление корня — **одно** `post_deleted` только для корня; ответы
  помечаются удалёнными молча (`post_store.go:972–1007`,
  `WHERE Id=? OR RootId=?`) — клиент каскадирует сам.
- `root_id` при создании поста — три разных исхода, не один
  (`app/post.go:280–305`, fix round 1 — предыдущая версия этого раздела
  ошибочно сводила все три к одному 400):
  - корня нет или он удалён (`Store().Post().Get(...)` возвращает
    `ErrNotFound` — удалённые не отдаются, `DeleteAt=0` в `WHERE`) → 400
    `api.post.create_post.root_id.app_error`;
  - корень **есть, но не в том канале**, что у нового поста
    (`!parentPostList.IsChannelId(post.ChannelId)`, после успешного чтения,
    т.е. без ошибки стора) → **500** `api.post.create_post.channel_root_id.app_error`
    (именно 500, не 400 — код так и делает, `http.StatusInternalServerError`);
  - корень есть, в том же канале, но сам является ответом (`rootPost.RootId
    != ""`) → 400 `api.post.create_post.root_id.app_error` (ответ на ответ
    запрещён).

### 8.3 `teams/unread` и `threads?totalsOnly`

- `GET /api/v4/users/{uid}/teams/unread?include_collapsed_threads=true`
  (`api4/team.go:587`, `app/team.go:1669–1725`) → `[]TeamUnread{team_id,
  msg_count, mention_count, msg_count_root, mention_count_root, thread_count,
  thread_mention_count, thread_urgent_mention_count}`. Тредовые поля
  считаются только когда `CollapsedThreads != disabled`, и только по
  **подписанным** тредам той команды: `Threads.ThreadTeamId IN teamIDs`
  (`thread_store.go:414–460`, `GetTeamsUnreadForUser`) — список команд не
  включает `""`, так что **DM/GM-треды никогда не попадают сюда**.
- `GET /api/v4/users/{uid}/teams/{tid}/threads?totalsOnly=true[&excludeDirect=true]`
  (`api4/user.go:3488–3567`, `app/user.go:2710–2775`) →
  `Threads{total, total_unread_threads, total_unread_mentions,
  total_unread_urgent_mentions, threads}` — с `totalsOnly` `threads` не
  заполняется (наш фейк отдаёт `[]`, не `null`, чтобы декод в
  `model.ThreadTotals` был единообразным). `totalsOnly` вместе с
  `threadsOnly` → 400 (`user.go:3536–3539`,
  `api.getThreadsForUser.bad_only_params`). Без `excludeDirect` DM/GM-треды
  (`ThreadTeamId=""`) включаются в счёт команды (`WHERE ThreadTeamId=tid OR
  ThreadTeamId=''`, иначе только `=tid`) — клиент считает вклад DM/GM
  вычитанием: `TeamsUnread` (без DM/GM) — это `teams/unread`, а разница
  `ThreadTotals(excludeDirect=false).Total − ThreadTotals(excludeDirect=true).Total`
  для команды по умолчанию (`""`/nav team) даёт DM/GM-часть.
- Только **подписанные** треды (`ThreadMemberships.Following=true`) видны в
  обоих эндпоинтах — не всякий тред, где пользователь когда-либо отвечал.

### 8.4 `PUT .../threads/{id}/read/{ts}`

`api4/user.go:3569–3606`, `app/user.go:2947–3003`
(`UpdateThreadReadForUser`).

- Требует существующее членство в треде: `GetThreadMembershipForUser` не
  находит запись → 404 `app.user.get_thread_membership_for_user.not_found`
  (`app/user.go:2795–2807`) — «нет подписки, значит нечего помечать».
  Права на сам тред отдельно не проверяются за пределами этого (обычное
  чтение поста).
- Пересчитывает `unread_mentions` (упоминания в ответах с `create_at > ts`),
  ставит `last_viewed=ts`; публикует `thread_read_changed`
  `{thread_id, timestamp, unread_mentions, unread_replies,
  previous_unread_mentions, previous_unread_replies, channel_id}` с
  broadcast `{user_id, team_id}` (`ts` — переданный `team_id` из URL, не
  обязательно команда канала треда — берётся только для адресации события).
  Ответ вызывающему — `ThreadResponse` (не «просто OK»).
- Нет `PUT .../threads/read` (без id, «все треды команды прочитаны») в
  Task 1 — фейк/клиент его не реализуют; событие `thread_read_changed` без
  `thread_id` (broadcast есть только `team_id`/`channel_id`) описано у
  сервера (`app/user.go:2826–2834`, `app/channel.go:3269–3276`) и
  декодируется (`ws.DecodeThreadReadChanged`), но кто его шлёт — задача
  позже этого плана (пока фейк отправляет `thread_read_changed` только с
  `thread_id`, для одного PUT-вызова).

### 8.5 Формы событий

- `thread_updated` (`app/notification.go:660–850`) — адресно **каждому
  подписчику с включённым CRT**, на каждый ответ, включая автора самого
  ответа (у него `last_viewed`/`unread_*` уже обнулены до сериализации,
  `notification.go:801–823`). `data.thread` — JSON-строка `ThreadResponse`
  (`id, reply_count, last_reply_at, last_viewed_at, participants, post,
  unread_replies, unread_mentions, is_urgent, delete_at`), плюс
  `previous_unread_mentions`/`previous_unread_replies` — снимок **до**
  эффекта этого ответа (для брандового подписчика — `0`/`0`).
  `broadcast.team_id` — команда канала, `""` у DM/GM.
- `posted` для ответа несёт `followers` (JSON-строка-массив) наравне с
  `mentions` — **по получателю**: массив содержит id получателя, только
  если он в списке подписчиков (`CRTNotifiers`, а не «весь список
  подписчиков всем»), и никогда не содержит самого автора ответа
  (`app/notification.go:355`, `useAddFollowersHook`). Наш фейк
  (`deliverLocked`) повторяет этот же per-recipient паттерн, уже
  используемый для `mentions`.
- `thread_read_changed` без `thread_id` — «прочитаны все треды канала»
  (есть `broadcast.channel_id`, `app/channel.go:3269–3276`, только когда
  `updateThreads` — CRT и `collapsed_threads_supported`) или «все треды
  команды» (ни `thread_id`, ни специфики — `app/user.go:2826–2834`).
  `ws.DecodeThreadReadChanged` в обоих случаях даёт `ThreadID == ""` —
  вызывающий код различает эти два случая по остальным полям
  (`ChannelID` есть/нет).
- `thread_follow_changed` (`{thread_id, state, reply_count}`,
  `app/user.go:2856–2859`) — не реализовано в Task 1 (нет
  `PUT/DELETE .../following` ни в фейке, ни в REST-клиенте); в бэклог.

### 8.6 Тест-API фейка (Task 1)

`cmd/spk-mm-client/browser.go`, за `--test-api`:

- `POST /api/_test/fake/post` — `root_id` теперь необязателен (было:
  всегда корень); с ним постит ответ (`fake.ReplyAs`).
- `POST /api/_test/fake/thread {channel_id, username, replies}` →
  `{root_id}` — создаёт корень от `username`, затем `replies` ответов
  поочерёдно от bob/carol (`fake.SeedThread`); оба должны быть членами
  `channel_id`, иначе фейк паникует (как остальные `...As`-хелперы) — во
  всех сидах c обоими подходит `c-town`/`c-gm`.
- `POST /api/_test/fake/edit {post_id, message}`, `POST
  /api/_test/fake/delete {post_id}` — тонкие обёртки над
  `fake.EditAs`/`fake.DeleteAs` (существовали как Go-методы, теперь и как
  HTTP-тест-маршруты).
- `POST /api/_test/fake/crt {mode}` — `always_on|default_on|default_off|
  disabled`, runtime-переключатель (`fake.SetCollapsedThreads`); как и
  другие runtime-переключатели фейка (`max-file-size`,
  `file-attachments-enabled`), клиент видит новое значение только после
  следующего bootstrap.
- `GET /api/_test/fake/thread-reads` → `[]ThreadRead{root_id, team_id, ts}`
  — журнал вызовов `PUT .../threads/{id}/read/{ts}`.
- `POST /api/_test/notification-click` принимает (и пока игнорирует)
  необязательный `root_id` — привязка «открыть тред» приходит в Task 5.

## 9. Поиск и посты вокруг (Task 1 плана поиска, `docs/specs/2026-09-30-search-design.md`)

Проверено 2026-09-30 по исходникам 10.11 (`.agents/tmp/mm-10.11`, тот же
`release-10.11`), «(по коду)». Пути — от корня клона Mattermost.

### 9.1 `POST /api/v4/teams/{team_id}/posts/search`

`server/channels/api4/post.go:900–998` (`searchPostsInTeam` → `searchPosts`),
`server/channels/app/post.go:1739–1796` (`SearchPostsForUser`),
`server/public/model/search_params.go` (`ParseSearchParams`),
`server/channels/store/sqlstore/post_store.go:2112–2280` (`search`) и
`:2976–3018` (`SearchPostsForUser`).

- Порядок проверок: нет права `view_team` на команду → **403**
  (`api.context.permissions.app_error`) — до чтения тела; тело не JSON → 400
  `api.post.search_posts.invalid_body.app_error`; `terms` нет или `""` →
  **400 `api.context.invalid_param.app_error`**. `terms` из одних пробелов —
  не ошибка: разбор даёт пустой список, ответ 200 пустой.
- Тело (`model.SearchParameter`, все поля — указатели): `terms`,
  `is_or_search`, `time_zone_offset` (**секунды** к востоку от UTC — веб-клиент
  шлёт `utcOffset() * 60`, `webapp/channels/src/actions/views/rhs.ts:234–236`),
  `include_deleted_channels` (действует только вместе с
  `ExperimentalViewArchivedChannels`), `page` (0), `per_page` (**60**, если
  поля нет). Наш `rest.SearchPosts`: `per_page` 0 → 20, `> 200` → 200.
- Ответ — `PostSearchResults`: `PostList` (`order`, `posts`,
  `next_post_id`/`prev_post_id` пустые) + `matches`. **У SQL-движка
  `matches` всегда `null`** — `MakePostSearchResults(posts, nil)`
  (`post_store.go:3018`, и `:2979` для `page > 0`); `matches` наполняют только
  Elasticsearch/Bleve (enterprise search layer, в клоне его нет). Подсветку
  клиент строит сам по терминам запроса.
- `order` — **новые первыми** (`posts.SortByCreateAt()`: `CreateAt` по
  убыванию, `public/model/post_list.go:156`).
- **Пагинация SQL-движка — не offset**: `page > 0` → всегда пусто («we don't
  support paging for DB search», `post_store.go:2977–2980`), а `page 0`
  возвращает **до 100 hits на группу запроса** (`Limit(100)`,
  `post_store.go:2128`) **независимо от `per_page`**; групп до двух (обычные
  слова и хэштеги — `ParseSearchParams` разбивает; результаты сливаются), то
  есть до 200. У ES/Bleve — offset `page × per_page`, и между страницами
  снимок не атомарен (новый пост сдвигает страницы → повторы: дедуп по id).
  Следствие для клиента: конец результатов — **сырая страница короче
  `per_page`** (SQL: `page 0` короче — конец; длиннее — один лишний запрос
  `page 1`, пустой). Веб-клиент 10.11 считает концом пустую страницу
  (`isEnd: posts.order.length === 0`, `mattermost-redux/src/actions/search.ts:103`),
  `per_page: 20` (`WEBAPP_SEARCH_PER_PAGE`, `:21`). Длина `page 0` у SQL не
  ограничена `per_page` — клиент не должен предполагать `≤ per_page`.
- Область: каналы, где пользователь **член**, этой команды **и** его DM/GM
  (`TeamId = team OR TeamId = ''`), неархивные (`inQuery`,
  `post_store.go:2238–2255`, `buildSearchTeamFilterClause` `:2029–2038`).
  Посты: `DeleteAt = 0` (удалённые и строки истории правок — у них тоже
  `DeleteAt ≠ 0` — не ищутся), **без системных** (`Type NOT LIKE 'system_%'`,
  `:2126`); ответы в тредах ищутся наравне с корнями, CRT не влияет.
- Синтаксис (`search_params.go`): слова через пробел; `"фраза"` — одно
  слово (с `-` перед кавычкой — исключение фразы); `имя:значение` или
  `имя: значение` для `from`, `in`/`channel`, `before`, `after`, `on`, `ext`,
  с `-` — исключающий фильтр; у прочих слов срезается пунктуация по краям
  (хвостовая `*` остаётся); слово вида `#тег` — отдельная группа хэштегов.
  Пустое после разбора (`*`, `!!!`) — пустой ответ 200 (`app/post.go:1754`,
  `sqlstore/utils.go` `removeNonAlphaNumericUnquotedTerms`).
- Сопоставление (PostgreSQL, `post_store.go:2149–2195`): символы
  `< > + - ( ) ~ :` — пробелы; `слово*` → префикс (`:*`); `"a b"` → `a<->b`
  (соседние слова в этом порядке); слова через `&` (AND), при `is_or_search`
  через `|`; исключения — `&!(x|y)`. **Только исключения** (`-foo` без
  положительных слов) дают `' &!(foo)'` — синтаксическая ошибка `to_tsquery`,
  которую `search` глотает (`:2263–2266`) → пустой ответ. Слова
  `to_tsvector` со стеммингом (`english` по умолчанию) и без учёта регистра.
- `from:user` (`@` срезается) → id по username; `in:name` (`~` срезается) →
  канал команды по `name` (slug); `in:@user` — DM с ним, `in:@a,b,c` — GM
  **ровно этих** пользователей (веб-клиент перечисляет всех участников,
  себя тоже: `getChannelNameForSearch`, `mattermost-redux/src/selectors/entities/channels.ts:246–264`)
  — `app/post.go:1528–1565`, `:1620–1630`. Неизвестное имя остаётся строкой
  и ничего не совпадает — пустой ответ, не ошибка. Несколько `from:`/`in:` —
  ИЛИ. `from:` — только члены команды (`buildSearchPostFilterClause`, `:2076`).
- Даты (`buildCreateDateFilterClause`, `:1990–2027`; `search_params.go`
  `Get*Millis`): дни в поясе `time_zone_offset`; `on:D` — весь день D
  (перекрывает прочие даты); `after:D` — с начала **следующего** дня;
  `before:D` — до конца **предыдущего**. Нераспознанная дата: `after:` —
  «сегодня», `before:`/`on:` — ничего не совпадает.
- Фейк (`internal/mmfake/search.go`) повторяет разбор и SQL-сопоставление без
  стемминга; по умолчанию листает offset-страницами (`page × per_page`, как
  поисковый движок) — чтобы тесты могли проверить дедуп и новые hits между
  страницами; `Options.SearchSQLEngine` включает точное поведение SQL-движка
  (`page > 0` пусто, до 100 на `page 0`). `matches` у фейка всегда `null`.

### 9.2 Посты канала вокруг поста: `after=` / `before=`

`server/channels/api4/post.go:222–347` (`getPostsForChannel`),
`server/channels/app/post.go:1245–1414` (`GetPostsAfterPost`,
`GetNextPostIdFromPostList`/`GetPrevPostIdFromPostList`,
`AddCursorIdsForPostList`), `post_store.go:1559–1760` (`getPostsAround`,
`getPostIdAroundTime`).

- Приоритет курсоров: `since` > `after` > `before` (цепочка `if/else if`,
  `post.go:289–315`). Невалидный id (не 26 символов) в `after`/`before` → 400.
- Выборка: `CreateAt > (SELECT CreateAt FROM Posts WHERE Id = ?)` (для
  `before` — `<`), канал, `DeleteAt = 0`, при `collapsedThreads` — только
  корни; `ORDER BY CreateAt ASC` (для `before` — `DESC`) `LIMIT per_page
  OFFSET page×per_page`, затем порядок переворачивается
  (`prepareThreadedResponse(…, reversed = !before)`) — **`order` в обоих
  случаях новые первыми**. `after=X` отдаёт `per_page` **ближайших** к X более
  новых постов.
- Курсор сравнивается по `create_at` строки из подзапроса без фильтров:
  **удалённый пост и пост другого канала годятся как курсор**;
  **неизвестный id** → подзапрос `NULL` → **200 с пустой страницей** (не 404).
- `prev_post_id`/`next_post_id` (`AddCursorIdsForPostList`, `app/post.go:1375–1414`):
  `after=X`, `page 0` → `prev_post_id = X` (даже неизвестный X);
  `next_post_id` — пусто, если страница короче `per_page`, иначе id первого
  видимого поста новее самого нового на странице (`GetPostIdAfterTime`,
  `DeleteAt = 0`, с CRT — только корни) — пусто, если таких нет (полная
  последняя страница). `before=X` зеркально: `next_post_id = X`,
  `prev_post_id` — пусто на короткой странице, иначе ближайший более старый
  или пусто. `since` → оба пусты. Без курсора — оба по соседям страницы.
- Фейк (`channelPosts`) повторяет всё перечисленное, кроме проверки формата
  id и `page > 0` (клиент всегда шлёт `page=0`).

### 9.3 `GET /api/v4/posts/{id}`

`server/channels/api4/post.go:532–585` (`getPost`), `app/post.go:1075–1098`
(`GetSinglePost`), `:2206–2229` (`GetPostIfAuthorized`).

- Неизвестный или удалённый пост (строка истории правок тоже — у неё
  `DeleteAt ≠ 0`) → **404 `app.post.get.app_error`**.
- Права: член канала — 200; **не член открытого канала своей команды — тоже
  200** (`PermissionReadPublicChannel`); приватный канал/DM/GM без членства →
  **403**. Ответ — сам `Post` (с `root_id`, `channel_id`).
- `skipFetchThreads` этот маршрут не читает — наш `rest.Post` его не шлёт.

### 9.4 Тред вниз: `direction=down`

`post_store.go:620–660` (CRT) и `:782–850` (без CRT), `api4/post.go:817–829`.

- `direction=down` — `ORDER BY CreateAt ASC, Id ASC`; с `fromCreateAt`
  (+`fromPost`) — ответы **новее** курсора: `CreateAt > T OR (CreateAt = T AND
  Id > P)`; `LIMIT perPage+1`, `has_next` — есть ли ещё **более новые**.
  `order[0]` — всё так же сам запрошенный корень, дальше ответы по
  возрастанию. `direction` не `up`/`down` → 400. `rest.ThreadQuery.Down`.
- Причуда без CRT (по коду): запрос без CRT выбирает `Id = root OR RootId =
  root` одной выборкой с тем же `LIMIT perPage+1`, так что корень занимает
  слот, когда проходит фильтр курсора — **без курсора** (или `up`, когда
  дошли до начала) страница может нести на один ответ меньше и
  `has_next=true` при отсутствии продолжения (следующий запрос вернёт
  пусто). С курсором `down` корень старше курсора — не мешает. Фейк этого не
  повторяет.
- Фейк (`internal/mmfake/threads.go`) уже умел `down` (с Task 1 тредов);
  `TestThreadDown` закрепляет поведение.

### 9.5 Тест-API фейка (поиск)

- `GET /api/_test/fake/search-calls` → `[]SearchCall{team_id, user_id,
  terms, is_or_search, page, per_page}` — журнал `POST .../posts/search`
  (`fake.SearchCalls()`; `per_page` — как применён: 60, если поля не было).
