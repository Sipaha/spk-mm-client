# SPK MM Client 1.0.2

- Build with Go 1.26.9, including its standard-library security fixes.
- Recover promptly after the network returns or the device wakes, including interrupted sign-in validation and synchronization.
- Prevent late GitLab/password sign-in responses from restoring a session after sign-out, server removal or a newer login.
- Isolate cached media between accounts, including WebView image URLs, while keeping the signed-in account's cache available offline.
- Cancel and finish active downloads on shutdown or account changes. Canceling one caller leaves a shared download available to other callers.
- Ignore outdated server lists and notification navigation so they cannot undo newer choices.
- Send incremental channel updates for long histories, preserving continuous scrolling, edits, reactions and history gaps.
- Right-click an image in the viewer to copy it through the native WebView menu.

Native packages for Linux, Windows and macOS (amd64/arm64), SHA-256 checksums and production-window verification remain included. Package signing and runtime requirements are unchanged from 1.0.0.
