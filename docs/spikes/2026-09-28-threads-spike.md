# Спайк: треды (ответы, панель треда, CRT) — отчёт

Копия отчёта `.superpowers/sdd/threads-spike-report.md` (Step 0 плана `docs/plans/2026-09-28-threads.md`).

Дата: 2026-09-28. Только чтение; ни коммитов, ни запусков приложения, ни обращений к реальным серверам MM.

## 0. Источники

- Mattermost **release-10.11, commit `60ecc27`** — тот же, что в `docs/research/2026-09-24-mattermost-api-facts.md` (строка 3).
  - Всё цитируемое — blobless sparse-клон этого коммита в
    `/home/spk/.spk/sawe/ss/Mattermost/.agents/tmp/mm-10.11` (расширяется через `git sparse-checkout add <path>`):
    сервер (`server/channels/{api4,app,store/sqlstore}`, `server/public/model`), `webapp/channels/src/{actions,
    packages/mattermost-redux/src/{actions,reducers,selectors}}` и компоненты webapp (`components/post`,
    `components/threading`, `components/rhs_thread`, `post_view`, `sidebar`, `i18n/en.json`). Остальные пути
    (`webapp/platform/client`, `utils`) — `git sparse-checkout add` по мере надобности.
- Пути ниже — от корня репозитория Mattermost (`server/…`, `webapp/…`); у клиента — от корня `spk-mattermost`.
- Проект: `AGENTS.md`, спека (`docs/specs/2026-09-24-spk-mattermost-design.md`: 43, 193, 213–214, 262–263, 486, 497),
  бэклог (`docs/backlog.md:22–23` — T7 «pending-ответы CRT в ленте канала; счётчик ответов не уменьшается
  при удалении; `SetWindow` затирает более новый локальный счётчик ответов»; `:70` — «треды (CRT, панель) — позже,
  после согласования»).

---

## 1. Модель данных и CRT

### 1.1 Поля

| Что | Где | Суть |
|---|---|---|
| `root_id` | `server/public/model/post.go:98` | `""` у корня; у ответа — id корня. Ответ на ответ запрещён: сервер отвергает `root_id`, указывающий на пост с непустым `root_id` (400 `api.post.create_post.root_id.app_error`, `server/channels/app/post.go:303–306`); корень должен быть в том же канале (`:299–301`). |
| `reply_count`, `last_reply_at`, `participants []*User`, `is_following *bool` | `post.go:118–121` | `is_following` — «только для корней в режиме CRT» (коммент у поля). В CRT-выдаче ленты/треда берутся из таблицы `Threads`/`ThreadMemberships` (`server/channels/store/sqlstore/post_store.go:584–589`, `1271–1274`); без CRT `reply_count` считается подзапросом `count(*) … RootId=… AND DeleteAt=0` (`post_store.go:724`, `749`). |
| Новый ответ несёт `reply_count` треда | `post_store.go:276–292` (`populateReplyCount` для ответов) | Событие `posted` ответа содержит актуальное число ответов треда — webapp просто присваивает его корню (`webapp/…/mattermost-redux/src/reducers/entities/posts.ts:418–431`, `99–112`). |
| Корень «обновляется» каждым ответом | `post_store.go:270–274` (`UPDATE Posts SET UpdateAt=? WHERE Id=rootId`) | Поэтому `posts?since=` с `collapsedThreads=true` приносит корень со свежим `reply_count`. |
| Счётчики канала | `post_store.go:~250–258` | `TotalMsgCount += все`, `TotalMsgCountRoot += только корни`; `LastRootPostAt` — только корни. |
| `Thread` | `server/public/model/thread.go:11–35` | `id`(=root), `channel_id`, `reply_count`, `last_reply_at`, `participants` (id, по старшинству; автор корня — только если ответил), `delete_at`, `team_id` (`""` у DM/GM). Запись появляется **только после первого ответа**. |
| `ThreadResponse` (то, что отдают `/threads` и `thread_updated`) | `thread.go:37–48` | `id, reply_count, last_reply_at, last_viewed_at, participants []*User, post, unread_replies, unread_mentions, is_urgent, delete_at`. Поля `is_following` **нет** (в списке — только подписанные). |
| `Threads` (список) | `thread.go:50–56` | `total, total_unread_threads, total_unread_mentions, total_unread_urgent_mentions, threads[]`. |
| `ThreadMembership` | `thread.go:101–132` | `following`, `last_view_at`, `unread_mentions`, `last_update_at`; создаётся при подписке (автоподписка: автор, упомянутые, автор корня — `server/channels/app/notification.go:227–320`, `app/post.go:438–446`). |

### 1.2 CollapsedThreads: конфиг, preference, решение

- Конфиг `ServiceSettings.CollapsedThreads`: `disabled | default_on | default_off | always_on`
  (`server/public/model/config.go:94–97`, поле `:448`); **дефолт при отсутствии — `always_on`** (`:924–925`).
  Валидация: не `disabled` требует `ThreadAutoFollow=true` (`:4554`). Отдаётся в полном клиентском конфиге
  `props["CollapsedThreads"]` (`server/config/client.go:144`, функция `GenerateClientConfig` — нужна сессия).
- Preference `display_settings / collapsed_reply_threads` = `on|off`
  (`webapp/…/mattermost-redux/src/constants/preferences.ts:15–18`).
- Webapp (`…/selectors/entities/preferences.ts:243–284`): дефолт preference `on` при `default_on|always_on`, иначе `off`;
  `isCollapsedThreadsEnabled = allowed(≠disabled) && (pref=="on" || config=="always_on")`.
- Сервер (`server/channels/app/channel.go:2883–2897`, `IsCRTEnabledForUser`): `disabled`→нет, `always_on`→да,
  иначе дефолт по конфигу, перекрытый preference. Совпадает с клиентом `internal/state/server.go:250–263` (`crtLocked`) ✔.

### 1.3 Лента канала: CRT on vs off

**CRT on** (официальный клиент):
- Лента грузится с `collapsedThreads=true` → только корни (`post_store.go:1265–1296`, `Where RootId=""`).
- Живой `posted` с `root_id` **не добавляется в ленту канала** (`reducers/entities/posts.ts:511–513`, `581–583`), только
  обновляет корень (`reply_count`, `participants` — `:418–431`).
- Под корнем — `ThreadFooter` (`webapp/channels/src/components/post/post_component.tsx:478`;
  `components/threading/channel_threads/thread_footer/thread_footer.tsx:38–150`): точка непрочитанного (если подписан и
  `unread_replies>0`), аватары участников, кнопка «N replies» (`i18n threading.numReplies`), Follow/Following,
  «Last reply …». Клик → `selectPost` → RHS.
- Иконка «Reply» в тулбаре при наведении (`components/post/post_options.tsx:123–125`).

**CRT off**:
- Лента с `collapsedThreads=false` — ответы лежат в ленте по времени, как обычные посты.
- Ответ, перед которым стоит пост другого треда, — `isFirstReply` (`components/post/index.tsx:50–62`) и получает строку
  «Commented on {name}'s message: <текст корня>» (`CommentedOn`, `post_component.tsx:406–413`; `i18n post_body.commentedOn`);
  последующие ответы того же треда — `same--root` (`:219–226`), CSS `post--comment` (`:296`).
- У корня с ответами — иконка с числом ответов (`post_options.tsx:123–125`, `hasReplies`). Клик — тот же RHS.

---

## 2. REST

| Эндпоинт | Где | Параметры / поведение |
|---|---|---|
| `GET /api/v4/posts/{id}/thread` | route `server/channels/api4/post.go:30`; handler `:760–880`; store `post_store.go:575–712` (CRT), `:714–897` (без CRT) | `perPage` (≤200; **без него — весь тред целиком**, коммент `:766–769`), `fromCreateAt`, `fromPost` (**требует `fromCreateAt`**, `:789–793`), `fromUpdateAt` (несовместим с `fromCreateAt`), `updatesOnly` (требует `fromUpdateAt`, не с `up`), `direction=up|down` (`up`=DESC, `down`=ASC), `skipFetchThreads`, `collapsedThreads`, `collapsedThreadsExtended`. Ответ `PostList{order, posts, has_next}`; **первым в `order` всегда запрошенный пост**, затем страница ответов (`post_store.go:733–734`, `879–893`). `has_next` — через `LIMIT perPage+1`. С `collapsedThreads=true` `id` обязан быть корнем (запрос `RootId = id`, `:613–619`); корень приходит с `reply_count/participants/is_following`. Удалённые ответы не возвращаются (`DeleteAt=0`), так что `updatesOnly` удалений не покажет. ETag поддержан (`post.go:871`). Права: `GetPostIfAuthorized` — чтение канала (`:865–869`). |
| как зовёт webapp | `…/actions/posts.ts:595–672`, `client4.ts:2242–2263` | `direction=down`, `perPage=60`, и **докачивает весь тред** циклом по `has_next` (`fromCreateAt/fromPost` последнего). Досинк после переподключения — `updatesOnly+fromUpdateAt` (`posts.ts:649–652`). |
| `GET /api/v4/users/{uid}/teams/{tid}/threads` | routes `api4/api.go:194–195`, `api4/user.go:103–110`; handler `user.go:3492–3567`; store `thread_store.go:284–370` | `per_page`(дефолт 60), `before`/`after` (id треда, взаимоисключающие), `since`, `unread`, `extended` (участники-профили), `deleted`, `totalsOnly`/`threadsOnly` (взаимоисключ.), `excludeDirect`. Только **подписанные** (`Following=true`, `:311–313`), DM/GM-треды (team `""`) входят в каждую команду, если не `excludeDirect` (`:327–338`). Порядок — `LastReplyAt DESC`. Права: сам пользователь + `view_team`. |
| `GET …/threads/{id}` | `user.go:3450–3490` | Один `ThreadResponse`; 404, если нет membership. |
| `PUT …/threads/{id}/read/{ts}` | `user.go:3569–3606` → `app/user.go:2947–3003` | Требует membership (иначе 404, `:2953–2957`); пересчитывает `unread_mentions`, ставит `last_view_at=ts`, шлёт `thread_read_changed`. `team_id` используется только для адресации события (права — чтение поста). |
| `PUT …/threads/read` | `app/user.go:2826–2834` | Всё прочитано в команде → `thread_read_changed` без `thread_id`. |
| `PUT/DELETE …/threads/{id}/following` | `user.go:3688–3716` → `app/user.go:2836–2860` | Подписка; событие `thread_follow_changed {thread_id, state, reply_count}`. Подписка ставит `last_view_at=now`. |
| `POST …/threads/{id}/set_unread/{post_id}` | `user.go:110` | «Непрочитано отсюда» для треда (webapp — в RHS при CRT: `webapp/channels/src/actions/post_actions.ts:411–416`). |
| `POST /api/v4/posts` с `root_id` | `api4/post.go:69–96`, `app/post.go:242–306` | Права — те же, что для поста в канал (`create_post`, `userCreatePostPermissionCheckWithContext`); приоритет поста у ответов запрещён (`postPriorityCheck…(…, post.RootId)`, `:93`). Автор ответа автоподписывается (`app/post.go:438–446`). **Свой CRT-ответ не помечает канал просмотренным** (`app/post.go:67–77`). |
| `GET /channels/{id}/posts?collapsedThreads=true` | `post_store.go:1265–1296` | Только корни + поля треда. |
| `GET /users/me/teams/unread?include_collapsed_threads=true` | `api4/team.go:587`, `app/team.go:1669–1725` | Даёт на команду `thread_count`, `thread_mention_count`, `thread_urgent_mention_count` (без DM/GM-тредов — фильтр `ThreadTeamId IN teamIDs`, `thread_store.go:414–460`). |
| `POST /channels/members/me/view` | `app/channel.go:3222–3278` | При `collapsed_threads_supported=true` и CRT **треды канала не помечаются прочитанными** (`updateThreads := ThreadAutoFollow && (!supported || !CRT)`, `:3240`). |
| `POST /users/me/posts/{pid}/set_unread` | `app/channel.go:2929–2957` | С `collapsed_threads_supported` и CRT — канальная логика по посту; без поддержки — ветка, помечающая тред (`:2959…`, `:3020–3055`). |

Лимиты страниц: `PerPageDefault=60`, `PerPageMaximum=200` (`server/channels/web/params.go:19–20`).

---

## 3. WebSocket и непрочитанное

- `posted` для ответа рассылается **всем участникам канала** как обычно (`app/post.go:861–882`); CRT-специфичного флага
  в `data` нет. Ключи: `channel_type, channel_display_name, channel_name, sender_name, team_id, set_online`, хуки
  `mentions` и `followers` (`notification.go:662–692`); `followers` — подписчики треда, которым положено
  десктоп-уведомление по `desktop_threads` (`notification.go:690–692`, `CRTNotifiers`).
- `thread_updated` — адресно каждому подписчику с CRT, на каждый ответ (`notification.go:721–850`), а также при
  «вас добавили в канал»/set_unread (`app/user.go:2862–2934`, `app/channel.go:3052`). `data.thread` — JSON-строка
  `ThreadResponse` (с постом-корнем, участниками, `unread_replies`, `unread_mentions`), плюс `previous_unread_replies`,
  `previous_unread_mentions` (для дельт счётчиков). `broadcast.team_id` — команда канала или `""` у DM (`app/post.go:564–581`).
  Автору ответа сервер сразу обнуляет непрочитанное (`notification.go:801–823`).
- `thread_read_changed`: с `thread_id` — `{thread_id, timestamp, unread_mentions, unread_replies, previous_*, channel_id}`
  (`app/user.go:2994–3002`); без `thread_id`, но с `broadcast.channel_id` — все треды канала прочитаны
  (`app/channel.go:3269–3276`, только при `updateThreads`); без обоих — все треды команды (`app/user.go:2831`).
- `thread_follow_changed`: `{thread_id, state, reply_count}` (`app/user.go:2856–2859`).
- `post_edited` — как для любого поста. `post_deleted` корня — **одно событие только для корня**, ответы удаляются
  молча (`post_store.go:972–1007`: `WHERE Id=? OR RootId=?`; `app/post.go:2824–2850`) — клиент каскадирует сам
  (webapp: `reducers/entities/posts.ts:~249`, `~812`). Удаление ответа уменьшает счётчики треда на сервере
  (`updateThreadAfterReplyDeletion`), отдельного события про корень нет (webapp уменьшает `nextPostsReplies`, `posts.ts:138–147`).
- Webapp: обработчики `websocket_actions.jsx:591–598`, `1704–1794`; `thread_updated` при открытом треде и фокусе сразу
  шлёт `read/{now}` (`:1759–1779`). Новый пост (`actions/new_post.ts:50–106`): при CRT свой ответ не трогает прочитанность
  канала (`:85–91`); чужой — `actionsToMarkChannelAsUnread(…, isRoot=post.root_id==='')` (`:149`), т.е. `msg_count_root`/
  `mention_count_root` растут **только от корней** (`mattermost-redux/src/actions/channels.ts:1238–1282`; сервер —
  `IncrementMentionCount(…, isRoot=post.RootId=="")`, `notification.go:325`, `channel_store.go:2889–2905`).
- **Непрочитанное при CRT**: канал — `total_msg_count_root − msg_count_root`, упоминания — `mention_count_root`
  (уже в facts §5). **Упоминания в ответах живут в тредах**: webapp добавляет к бейджам/командам
  `total_unread_mentions`/`total_unread_threads` из счётчиков тредов (`selectors/entities/channels.ts:666–685`, `769–795`);
  счётчики берутся `GET …/threads?totalsOnly=true` (`actions/threads.ts:94–96`) и правятся событиями.
- Боковая панель «Threads» (`components/threading/global_threads*`): список подписанных тредов команды (+DM), сортировка
  по последнему ответу, фильтр «Unread», точка/число упоминаний, «Mark all as read»; сама ссылка в сайдбаре с точкой и
  числом упоминаний (`global_threads_link`).

---

## 4. Что делает клиент сейчас

Клиент **уже CRT-осведомлён** для ленты и счётчиков, но тредов как сущности нет.

- CRT-решение — `internal/state/server.go:250–263` (как webapp). Конфиг — `internal/mmsync/worker.go:727`.
- **Окно канала**: `keep()` (`internal/state/posts.go:48–51`) — при CRT в окно попадают только корни; все запросы ленты
  передают `collapsedThreads=crt` (`internal/mmsync/fetch.go:112,149`, `actions.go:73`, `internal/mm/rest/chat.go:95–125`).
  Без CRT ответы лежат в окне inline.
- **Живой ответ при CRT** (`posts.go:494–518`): не вставляется; у корня в окне `ReplyCount++`, `LastReplyAt=create_at`,
  если `create_at > LastReplyAt` (защита от двойного учёта с REST-значением). `upsertLocked` сохраняет старый
  `ReplyCount`, если пришёл 0 (`posts.go:80–85`).
- **Лента (UI)**: при CRT под корнем некликабельный текст «Ответов: N» (`frontend/src/components/PostItem.tsx:198`,
  i18n `post.replies`). Без CRT ответ выглядит как обычный пост — **ни «Commented on…», ни связи с корнем**;
  `feedRows.ts` группирует по автору/времени, про `root_id` не знает. Ни кнопки «Ответить», ни панели.
- **Счётчики**: `unreadLocked` (`server.go:313–321`) — при CRT `*_root`, иначе обычные ✔. `applyNewPostLocked`
  (`posts.go:525–539`): `TotalMsgCount++` всегда, `TotalMsgCountRoot++` только у корня; упоминание — `MentionCount++`,
  `MentionCountRoot++` только у корня ✔ (как webapp/сервер). Двойного счёта при CRT нет.
- **Прочтение**: `ViewChannel` всегда с `collapsed_threads_supported:true` (`rest/write.go:45–48`); при CRT авто-view на
  чужой ответ подавлен (`internal/state/events.go:218`). `thread_*` события **не обрабатываются** (нет в `ApplyEvent`,
  `events.go:57–177`); треды read/follow не вызываются нигде.
- **Уведомления**: `internal/notify/decide.go:65–82` — CRT-ответ фильтруется по `followers`; «тред открыт» не учитывается
  (коммент «Thread panels arrive in stage 3»).
- **Отправка**: `Pending.RootID` и `CreatePost(RootID)` уже есть (`posts.go:12–22,336–347`, `mmsync/actions.go:132`), но
  `Worker.Send` всегда передаёт `""` (`actions.go:95`), `api.SendPost` без root (`internal/api/chat.go:121`).
- **Вложения/черновики**: трей вложений — ключ `(srv, channel)` (`internal/attach/attach.go:136–139`), черновик —
  `s.drafts[channelID]` (`posts.go:646–660`), `Composer` keyed by channel (`frontend/src/components/Composer.tsx:21–22`).
- **Фейк**: нет `/posts/{id}/thread`, `/users/{uid}/teams/{tid}/threads*`, `thread_*` событий
  (`internal/mmfake`, маршруты); `CreatePost` с `root_id` и `ReplyAs` есть (`mmfake/chat.go:417–441, 641–644`).

### 4.1 Что ломается/неточно уже сейчас (при CRT on — дефолт сервера 10.11)

1. **Ответы других людей невидимы**: при CRT в клиенте их нельзя прочесть вообще — только число. Уведомление о
   CRT-ответе (followers) приходит, клик открывает канал, где ответа нет.
2. **Бейдж недосчитывает упоминания в ответах**: `mention_count_root` их не содержит, а счётчики тредов не читаются
   (ни `teams/unread?include_collapsed_threads`, ни `/threads?totalsOnly`). Официальный клиент их показывает.
3. **Треды никогда не отмечаются прочитанными** нашим клиентом (view канала при CRT их не трогает, §2) — в
   официальных клиентах (веб/мобильный) у пользователя копятся непрочитанные треды.
4. **Свой CRT-ответ локально помечает канал прочитанным** (`posts.go:531–533` ставит `MsgCountRoot`,
   `LastViewedAt`), а сервер — нет (`app/post.go:75–77`): при непрочитанных корнях канал «прочитается» только у нас до
   следующего bootstrap. Надо: для своего CRT-ответа не трогать member-счётчики корней/`LastViewedAt`.
5. **Смена CRT мимо preference-события не сбрасывает окна**: `ResetWindows` только на `preferences_changed`
   (`events.go:129–141`, `worker.go:826–827`); `Bootstrap` (`server.go:120–178`) новый `cfg`/prefs принимает без
   сравнения `crtLocked()` до/после. Снимок, сохранённый в одном режиме и поднятый в другом (админ поменял
   `CollapsedThreads`, preference изменена офлайн), держит окна «не того вида» (ответы inline при CRT или без ответов
   без CRT) до полной перезагрузки окна. `config_changed` WS не обрабатывается.
6. Бэклог T7 подтверждается: `ChannelView` добавляет **все** pending канала, включая `RootID≠""` (`view.go:113–120`) —
   при CRT pending-ответ мелькнёт в ленте; удаление ответа не уменьшает `ReplyCount` корня (post_deleted ответа не
   найдёт его в окне — `removeLocked` ищет только сам id/детей); `SetWindow` берёт `ReplyCount` страницы, не
   сравнивая с более новым локальным. Лекарство для счётчика: брать `reply_count` из самого `posted`-ответа (сервер
   кладёт туда актуальное число, §1.1), как webapp, вместо `++`.
7. `NeedsView` (`posts.go:707–716`) при CRT учитывает и не-корневые счётчики — лишний `view` после чужого ответа
   (безвредно, но лишний запрос).

Двойного счёта при CRT нет (корневые/некорневые счётчики разведены верно). При CRT off всё корректно, кроме
отсутствия контекста ответа в ленте.

---

## 5. Рекомендация

### 5.1 Варианты

**A. Правая панель треда (RHS), состояние в Go — рекомендую.**
Как в официальном клиенте: лента канала остаётся, справа панель «Тред» (корень + ответы + свой композер).
- Go: `state.Thread` — один «открытый» тред + LRU на 2–3 недавних (только память, в снимок не пишется).
  Загрузка — `GET /posts/{root}/thread?perPage=60&direction=up&collapsedThreads=<crt>&skipFetchThreads=false` (новейшие
  60 + корень), прокрутка вверх — `fromCreateAt/fromPost` старейшего; кап окна треда ~200 постов, старшее отбрасывается
  при закрытии. Никогда не звать без `perPage` (вернёт тред целиком).
- События маршрутизируются в кэш: `posted` с `root_id` = открытый/закэшированный тред, `post_edited`, `post_deleted`
  (корень удалён — панель закрывается с пометкой; ответ — удалить, корню `reply_count--`), реакции, pending/confirm.
  После разрыва потока — перечитать последнюю страницу открытого треда (дёшево; `updatesOnly` удалений не видит).
- DTO `ThreadView` той же формы, что `ChannelView` (posts, has_more, new_since, me_id, crt) → переиспользуются `Feed`,
  `PostItem`, `buildRows`; событие `thread_changed {server_id, root_id}` через тот же коалесер.
- Композер: `Composer` получает `rootId`; `SendPost(srv, channel, rootId, msg, att)`; `Pending.RootID` уже есть. Трей
  вложений и черновик — ключ `(channel, rootId)` (загрузка всё равно с `channel_id` канала). Черновик треда в MVP —
  в памяти.
- Плюсы: привычный UX, контекст канала виден, кэш маленький (~1–2 КБ/пост в Go → ≤ ~1 МБ при 3×200), вписывается в
  архитектуру «состояние в Go, UI рисует DTO». Минусы: второй виртуализированный список, ширина окна (при узком окне
  панель должна перекрывать ленту).

**B. Разворачивание ответов прямо в ленте (inline expand).**
Клик «N ответов» раскрывает ответы под корнем в той же ленте, композер ответа — там же.
- Плюсы: нет второй панели, минимум лейаута. Минусы: ломает скролл-якорь/виртуализацию и «новые сообщения», длинные
  треды раздувают ленту, композер в середине ленты, не похоже на официальный клиент, живые ответы сдвигают ленту.
  Не рекомендую.

**C. Тред вместо ленты (полноэкранный режим с «← назад в канал»).**
- Плюсы: проще всего (одна панель, `Feed`+`Composer` без изменений раскладки), минимум памяти в UI. Минусы: теряется
  контекст канала, нельзя смотреть канал и тред одновременно. Хорош как **режим A для узкого окна**, не как основной.

Альтернатива по данным (для A): держать тред только во фронте (Go лишь проксирует REST) — меньше Go-кода, но живые
события пришлось бы дублировать во фронт, pending/вложения живут в Go — рассинхрон. Не рекомендую.

### 5.2 Рендеринг ленты канала

- **CRT on**: под корнем кликабельная строка «N ответов · последний ответ <время>» (+ аватары участников — позже),
  точка непрочитанного — позже (нужны данные тредов). В тулбаре наведения — «Ответить» (↩). Pending-ответы в ленте
  канала не показывать (T7). Счётчик ответов — из `posted.reply_count`.
- **CRT off**: ответы inline как сейчас + строка контекста «Ответ на сообщение <автор>: <фрагмент корня>» у первого
  ответа серии (логика `isFirstReply`, `post/index.tsx:50–62`), клик — открыть тред; у корня с ответами — «N ответов».
  Фрагмент корня — из окна/кэша; если корня нет — «Ответ в треде» (без доп. запроса, или ленивый `GET /posts/{root}`).
  Pending-ответ показывается и в ленте, и в панели.

### 5.3 Прочитанность, уведомления, Threads-вид

- **CRT on, MVP**: панель открыта и окно в фокусе → `PUT /users/me/teams/{tid}/threads/{root}/read/{now}` при открытии и
  на каждый новый ответ (как webapp, `thread_viewer.tsx:149–172`, `websocket_actions.jsx:1759–1779`); 404 (нет
  membership — не подписан) — молча игнорировать. `tid` — команда канала, у DM — текущая команда. `notify.Decide`:
  CRT-ответ в открытом треде при фокусе — не уведомлять; клик по уведомлению о CRT-ответе → канал + панель треда.
- **CRT off**: отдельного чтения тредов нет — view канала делает всё на сервере.
- **Бейдж упоминаний в ответах (CRT)** — рекомендую сразу за MVP (это корректность «замены официального клиента»):
  на bootstrap `GET /users/me/teams/unread?include_collapsed_threads=true` (`thread_mention_count` по командам) +
  `GET …/threads?totalsOnly=true` одной командой для DM-тредов (или `excludeDirect`-разница), дальше дельты из
  `thread_updated` (`unread_mentions − previous_unread_mentions`) и `thread_read_changed`. Суммировать в бейдж сервера/
  команды как webapp (`channels.ts:666–685`).
- **Threads-вид в сайдбаре** (список подписанных тредов, фильтр «непрочитанные», «прочитать все»,
  follow/unfollow, «непрочитано отсюда» для треда) — **позже**, отдельным этапом; до него точка непрочитанного у
  корня и Follow-кнопка тоже позже.

### 5.4 MVP vs позже

MVP (этап «Треды, ч.1»):
1. `rest.PostThread` (+ фейк: `/posts/{id}/thread` с `perPage/direction/fromCreateAt/fromPost/collapsedThreads`,
   `PUT …/threads/{id}/read/{ts}`).
2. `state.Thread` (открытый + LRU 2–3, кап 200, без снимка), маршрутизация `posted/edited/deleted/reaction` и pending.
3. RHS-панель (`Feed`+`PostItem` переиспользуются, `ThreadView` DTO, `thread_changed`), закрытие по Esc; узкое окно —
   панель поверх ленты (вариант C как режим).
4. Композер ответа с вложениями (`rootId`, трей/черновик по `(channel, root)`).
5. Лента: «N ответов» кликабельно + «Ответить» в тулбаре; CRT off — строка контекста у ответа.
6. CRT read при открытой панели в фокусе; `notify` учитывает открытый тред; клик по уведомлению открывает тред.
7. Исправления из §4.1: п.4 (свой CRT-ответ не читает канал), п.5 (сравнение `crtLocked()` в `Bootstrap` →
   `ResetWindows`), п.6 (pending-ответы вне ленты при CRT; `reply_count` из `posted`; `--` на удаление ответа).

Сразу после MVP (ч.1б): счётчики упоминаний тредов в бейдже (§5.3), обработка `thread_updated`/
`thread_read_changed`/`thread_follow_changed`, точка непрочитанного у корня, Follow/Unfollow.

Позже: Threads-вид в сайдбаре; «непрочитано отсюда» для треда; аватары участников; персистентные черновики тредов;
ETag на `GET thread`; permalink на ответ (прыжок в тред); `config_changed`.

### 5.5 Память

- Go: только открытый тред + 2–3 в LRU, ≤200 постов каждый, без SQLite; закрытие панели/смена сервера чистит «старшие»
  страницы; ничего не растёт со временем (кап LRU и кап окна). Оценка ≤ ~1 МБ кучи.
- UI: одна дополнительная виртуализированная лента (правила `useFrames`, пустеющий при размонтировании скролл-контейнер
  и eager-картинки из AGENTS.md применимы как есть); панель размонтируется при закрытии.

## 6. Замеры (Task 7, 2026-09-28/29)

### 6.1 Soak с ответами и панелями тредов

**Как.** Desktop dev-сборка, эквивалентная `e435241` (собрана из того же кода до коммита; отличия
коммита от сборки — только тесты), headless: `xvfb-run -a`, `TMPDIR` и `SPK_MM_CLIENT_HOME` в scratch,
`DBUS_SESSION_BUS_ADDRESS` на несуществующий сокет (нет трея в сессии пользователя),
`SPK_MM_CLIENT_SOAK_PROFILES` — профили кучи каждую минуту:

```
soak-desktop --mm-fake --mm-fake-servers 2 --mm-fake-channels 50 --mm-fake-churn 200ms
```

76 минут (≈ 12,7 ч нагрузки при `2s`), `scripts/pss.sh <pid>` раз в минуту, строка `go memory` раз в
минуту. Нагрузка churn после Task 7: 5 постов/с, каждый третий — ответ в один из 5 новейших корней
канала; переключение канала раз в секунду, каждое пятое — через уведомление с `root_id` (канал +
панель треда, раз в 5 с); фейки с CRT (`always_on`), `KeepPosts` режет и ответы, и записи тредов
обрезанных корней (`TestKeepPostsDropsTheThreadsOfTrimmedRoots`). Та же форма нагрузки, что у soak-ов
этапа 3 ч.1б (2 × (5 + 50) каналов, `200ms`), — для сравнения. Хост был занят (load average 5–14,
другие агенты; первые ~15 минут параллельно шли e2e) — это влияет на CPU, не на Private_Dirty.

**Private_Dirty, МБ** (по 15-минутным отрезкам; Web — мин–макс, медиана, «низ» — среднее трёх
наименьших замеров отрезка):

| минуты | main (медиана) | Web мин–макс | Web медиана | Web низ | TOTAL медиана |
|---|---|---|---|---|---|
| 0–14 | 64.6 (51.0 → 66.4, прогрев) | 54.0–156.7 | 104.9 | 82.5 | 176.8 |
| 15–29 | 67.7 | 105.9–168.8 | 128.8 | 109.8 | 204.5 |
| 30–44 | 68.4 | 111.3–192.4 | 140.5 | 117.3 | 216.6 |
| 45–59 | 68.4 | 120.4–197.5 | 148.9 | 127.8 | 224.7 |
| 60–75 | 68.5 | 118.8–190.8 | 145.2 | 121.4 | 221.6 |

WebKitNetworkProcess — 7.4 весь прогон.

**Main — плато с 15-й минуты** (67.7–68.5 по медиане, отдельные 66.2–71.6). Куча Go: `next_gc`
19 → 23 к 30-й минуте, дальше 23 до конца; `heap_alloc` колеблется 13–20 без тренда; `num_gc`
60 → 1118 (сборки идут ~14 в минуту — цифры не «застывшие»). Разница профилей кучи
(`go tool pprof -base`): минуты 30 → 76 — −0.05 МБ, 45 → 76 — −0.5 МБ; рост 15 → 30 (+2.6 МБ) — фейк
(`mmfake.createPostLocked`: посты churn длиннее засеянных, плюс ответы) и декодирование этих же
постов в окна клиента, до установившегося режима. Ничего, что росло бы со временем.

**Web — выход на плато к ~45-й минуте**, дальше медиана 145–149 и низ 121–128 без тренда; разброс
соседних замеров ±40 МБ, как и раньше (крупные временные выделения). Сравнение с soak-ом ч.1б без
тредов (180 мин, `200ms`): main 66 → 68.5 (+2–3 МБ: CRT, кэш тредов, итоги упоминаний в тредах,
треды в фейке), Web-плато по медиане ~130 → ~145–149 (+15–20 МБ: вторая виртуализированная лента —
панель треда — монтируется каждые 5 с). В пределах решения пользователя 2026-09-28 (+10–20 МБ —
нормально); роста со временем нет. Не проверено этим прогоном: `PUT read` треда требует фокуса окна,
а в Xvfb без оконного менеджера фокус не гарантирован (фейк в режиме soak журнал чтений не ведёт).

### 6.2 30 тредов по 200 ответов (не в CI)

`SPK_MM_CLIENT_MEMCHECK=1 go test ./internal/api -run TestThreadCacheHeapReturnsToBaseline -count=1 -v`:
сервис с фейком (CRT) в одном процессе, 30 тредов по 200 ответов засеяны **до** замера базы; каждый
открывается, прокручивается до потолка (`LoadOlderReplies` страницами по 60 → 201 пост с корнем) и
закрывается; куча Go — после трёх `runtime.GC()` и `debug.FreeOSMemory()`.

| прогон | база | один открытый тред на потолке | после 30 открытий/закрытий |
|---|---|---|---|
| 1 | 7.74 | 7.83 (+0.09) | 7.83 (+0.09) |
| 2 | 7.36 | 7.45 (+0.09) | 7.48 (+0.12) |

≈ 0.45 КБ кучи на ответ в кэше; открытый тред на потолке — ~0.1 МБ, граница кэша (3 треда, закрытые
обрезаны до 60) — ≤ ~0.2 МБ на сервер, заметно меньше оценки плана (≤ ~1 МБ). Мутация «LRU без
предела» (`ThreadCacheSize` = 100, закрытые всё ещё обрезаются до 60) даёт +0.71–0.74 МБ — поэтому
допуск теста ±0.5 МБ, а не ±1 МБ плана (±1 МБ такую утечку не заметил бы).

### 6.3 Повторный soak (fix round ревью Task 7, 2026-09-29)

**Зачем.** Первый прогон (§6.1) шёл 76 минут, меньше ~80, за которые Web выходит на плато; его сборка
была до ограниченной автоподгрузки истории в `Feed.tsx`, и ответы без CRT он не гонял (ревью, M6).
Пользователь одобрил повтор.

**Как.** Desktop dev-сборка из чистого worktree на `403ba93` (включает ограниченную автоподгрузку
`3ba1099`), headless, тот же запуск (`--mm-fake --mm-fake-servers 2 --mm-fake-channels 50
--mm-fake-churn 200ms`), **96 минут** (≈ 16 ч нагрузки при `2s`). Фейки теперь чередуют CRT: первый с CRT
(панель, чтение тредов, бейджи тредов), второй без (ответы в ленте со строкой «Ответ …»). Прогон шёл
**один**: скрипт дождался 3 минут без чужих прототипов (PDF-спайк) и проверял их каждую минуту — ни
одного за прогон; своих e2e параллельно не было.

**Private_Dirty, МБ, и куча Go** (медианы по отрезкам; Web «низ» — среднее трёх наименьших):

| минуты | main | Web мин | Web медиана | Web низ | TOTAL | `heap_alloc` | `next_gc` | `num_gc` |
|---|---|---|---|---|---|---|---|---|
| 0–14 | 64.5 | 77.5 | 111.9 | 89.0 | 184.0 | 16 | 20 | 59–283 |
| 15–29 | 67.8 | 120.9 | 137.4 | 122.1 | 211.8 | 16 | 22 | 299–518 |
| 30–44 | 68.0 | 123.5 | 144.9 | 131.2 | 220.0 | 18 | 23 | 534–749 |
| 45–59 | 68.4 | 121.2 | 147.0 | 129.6 | 222.6 | 18 | 23 | 763–975 |
| 60–74 | 68.9 | 145.7 | 153.5 | 146.6 | 229.3 | 19 | 23 | 990–1196 |
| 75–95 | 68.9 | 126.7 | 149.0 | 137.2 | 225.1 | 17 | 23 | 1211–1510 |

**Наклоны** (МНК по поминутным замерам, МБ/ч):

| окно | main | Web | TOTAL | `heap_alloc` | `next_gc` |
|---|---|---|---|---|---|
| 30–95 | +0.70 | +9.9 | +10.6 | −0.37 | +0.28 |
| 45–95 | +0.26 | +10.3 | +10.6 | −0.56 | +0.10 |
| 60–95 | −0.28 | −10.3 | −10.6 | −1.82 | 0.00 |

**Вывод.**
- **Main** — плато с 15-й минуты (67.8 → 68.9, наклон после 45-й минуты +0.3 МБ/ч, после 60-й −0.3).
- **Куча Go** — `next_gc` 23 с 30-й минуты до конца, `heap_alloc` без тренда. По профилям (каждые
  5 минут, `-focus`): куча клиента (`internal/state`) качается 2.75–4.5 МБ без тренда; ползёт только
  фейк в том же процессе (5.6 → ~7 МБ за час: посты churn длиннее засеянных вытесняют их), это
  инструмент, не клиент.
- **Web** — выход на плато к ~60-й минуте: медиана 153.5 (60–74) и 149.0 (75–95), наклон за последние
  35 минут отрицательный; положительный наклон окон, начинающихся с 30-й и 45-й минуты, — прогрев
  (AGENTS: ~80 минут при `200ms`) плюс всплеск 60–74 и соседние замеры ±40 МБ. Плато Web подтверждено
  на уровне медианы ~150 МБ (до тредов ~130: вторая лента — панель треда — и ответы без CRT в ленте),
  низ 137–147.
- Итого — роста со временем нет; TOTAL на плато ~225 МБ Private_Dirty при 2 серверах × 105 каналов и
  churn в 10× быстрее `2s`.
