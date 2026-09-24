# spk-mattermost

Лёгкий десктопный клиент Mattermost (Linux, Windows, macOS) на Wails v3 + React.
Спецификация — `docs/specs/2026-09-24-spk-mattermost-design.md`.
Результаты спайков этапа 1 — `docs/spikes/2026-09-24-stage1-spikes.md`.

## Разработка

    make build          # фронт + бинарь (browser-режим)
    make run-browser    # http://127.0.0.1:5180 с фейковым сервером
    make run            # desktop
    make test           # go + фронт + e2e
    make install-dev-linux  # зарегистрировать dev-сборку для mmauth:// (Linux)
    make pss PID=<pid>  # сумма PSS процесса и его потомков (Linux)

Данные: `~/.spk/spk-mattermost/` (переопределяется `SPK_MATTERMOST_HOME`).
