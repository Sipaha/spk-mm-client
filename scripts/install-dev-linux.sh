#!/usr/bin/env bash
# Registers a dev build as the mmauth:// handler for the current user.
# Usage: scripts/install-dev-linux.sh /abs/path/to/spk-mm-client-desktop
set -euo pipefail
bin="${1:?path to desktop binary}"
apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$apps"
# Desktop Entry spec: inside a double-quoted Exec argument, backslash, double
# quote, backtick and dollar-sign must be backslash-escaped (backslash first,
# so the escaping added for the other three isn't itself re-escaped).
bin_escaped=$(printf '%s' "$bin" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/`/\\`/g' -e 's/\$/\\$/g')
cat > "$apps/spk-mm-client.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=spk-mm-client
Exec="$bin_escaped" %u
Terminal=false
Categories=Network;Chat;
MimeType=x-scheme-handler/mmauth;
EOF
# Task 0 rename (2026-09-25): drop the stale pre-rename entry so it can't
# still claim the mmauth scheme alongside the new one.
rm -f "$apps/spk-mattermost.desktop"
update-desktop-database "$apps" 2>/dev/null || true
xdg-mime default spk-mm-client.desktop x-scheme-handler/mmauth
echo "mmauth:// -> $(xdg-mime query default x-scheme-handler/mmauth)"
