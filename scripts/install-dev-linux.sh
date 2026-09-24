#!/usr/bin/env bash
# Registers a dev build as the mmauth:// handler for the current user.
# Usage: scripts/install-dev-linux.sh /abs/path/to/spk-mattermost-desktop
set -euo pipefail
bin="${1:?path to desktop binary}"
apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$apps"
# Desktop Entry spec: inside a double-quoted Exec argument, backslash, double
# quote, backtick and dollar-sign must be backslash-escaped (backslash first,
# so the escaping added for the other three isn't itself re-escaped).
bin_escaped=$(printf '%s' "$bin" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/`/\\`/g' -e 's/\$/\\$/g')
cat > "$apps/spk-mattermost.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=spk-mattermost
Exec="$bin_escaped" %u
Terminal=false
Categories=Network;Chat;
MimeType=x-scheme-handler/mmauth;
EOF
update-desktop-database "$apps" 2>/dev/null || true
xdg-mime default spk-mattermost.desktop x-scheme-handler/mmauth
echo "mmauth:// -> $(xdg-mime query default x-scheme-handler/mmauth)"
