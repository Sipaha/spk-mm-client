#!/usr/bin/env bash
# Registers a dev build as the mmauth:// handler for the current user.
# Usage: scripts/install-dev-linux.sh /abs/path/to/spk-mm-client-desktop
set -euo pipefail
bin="${1:?path to desktop binary}"
if [[ "$bin" != /* || ! -f "$bin" || ! -x "$bin" ]]; then
    echo "Expected an existing executable at an absolute path: $bin" >&2
    exit 1
fi
desktop_id=spk-mm-client-dev.desktop
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
icon_escaped=$(printf '%s' "$root/internal/appfiles/icons/icon.png" | sed 's/\\/\\\\/g')
apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$apps"
# Exec quoting is applied after Desktop Entry string unescaping: quote the
# argument first, then double every backslash for the outer string layer.
bin_escaped=$(printf '%s' "$bin" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/`/\\`/g' -e 's/\$/\\$/g' | sed 's/\\/\\\\/g')
# A development checkout must not override the packaged release's desktop ID.
cat > "$apps/$desktop_id" <<EOF
[Desktop Entry]
Type=Application
Name=SPK MM Client (development)
Exec="$bin_escaped" %u
Icon=$icon_escaped
Terminal=false
Categories=Network;Chat;
MimeType=x-scheme-handler/mmauth;
EOF
# Existing user launchers may be customized; leave them for explicit migration.
if [[ -e "$apps/spk-mm-client.desktop" || -e "$apps/spk-mattermost.desktop" ]]; then
    echo "Legacy user launcher found in $apps; remove it explicitly if it points to an obsolete dev build. It may shadow the installed release." >&2
fi
update-desktop-database "$apps" 2>/dev/null || true
xdg-mime default "$desktop_id" x-scheme-handler/mmauth
echo "mmauth:// registered to $desktop_id"
