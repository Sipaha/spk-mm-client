# Копирование изображения в просмотрщике

Запрос пользователя 2026-10-13: ПКМ → «Скопировать изображение».

Причина: используемый runtime Wails v3 beta.25 в production отменяет стандартное
contextmenu для non-editable элементов, если нет CSS opt-in. В debug меню всегда
разрешено, поэтому обычный dev smoke не проверяет этот случай.

Изменение: original и preview `<img>` в `ImageZoom` имеют класс `viewer-image`;
в `index.css` только для этого класса установлено `--default-contextmenu: show`.
При ПКМ WebView показывает штатное меню с Copy Image. Данные копирует сам
WebView из загруженного изображения; это raster, не URL и не скриншот zoom/pan.
Backdrop, keyboard zoom, wheel и drag handlers не менялись. Preview тоже можно
копировать, когда original ещё не загрузился или недоступен; после замены
копируется original. Account-scoped media URL и ограничения размеров прежние.

Проверено:

- `make release`: frontend + Linux desktop с тегом `production` — PASS;
  browser build и Windows build/vet — PASS. Это локальные сборки, не публикация.
- 1135 frontend tests / 70 files — PASS; Go/frontend lint — PASS, `0 issues`.
- Native WebKit в отдельном Xvfb, own fake profile и dead DBUS path: реальный
  XTest ПКМ, на просмотренном скриншоте есть Copy Image; реальный click этой
  строки, `xclip -selection clipboard -target image/png -o` получил PNG
  **960×540, 25891 bytes**, размер равен `naturalWidth/naturalHeight` original.
  Проверка в отдельном X display не меняла clipboard пользовательского сеанса. Процесс завершён.
- Native probe собран с `wails gtk3 production` и Go overlay только в Solution
  `.tmp`: overlay разрешает fake dev flags и открывает просмотрщик через
  DevDriver; production runtime/menu и исходный CSS сохранены. Скриншот:
  `.tmp/mm-copy-image/context-menu.png`; PNG: `.tmp/mm-copy-image/clipboard.png`;
  исходник проверки: `.tmp/mm-copy-image/native_test.py`; лог `native-test.log`.
- Targeted Playwright `media.spec.ts`: **8 passed (18.4s)**, результат в `.tmp/mm-copy-image/e2e.log`.
  Логи сборок и остальных проверок: `.tmp/mm-copy-image/checks.log`,
  `.tmp/mm-fixes/copy-image-build.log`. Все paths от корня Solution.

Установленный `/usr/bin/spk-mm-client --version` отдельно проверен: `1.0.0`.
Исходные незакоммиченные изменения, A1–A6 и reconnect сохранены; установки,
commit и публикации не выполнялись. Windows/macOS native clipboard в этом
проходе не запускался; CSS опирается на общий контракт Wails.
