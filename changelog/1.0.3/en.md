# SPK MM Client 1.0.3

- Separate paragraphs more clearly in messages, threads and draft previews. Single line breaks and short messages stay compact.
- On Linux, retract system notifications after their messages are read, including reads from another client. This clears stale notification badges on Cinnamon's taskbar.
- Keep already-read notifications from reappearing when a delayed message burst finishes. Retry notification updates after a temporarily unresponsive desktop notification service.
- Keep the Linux development launcher separate from the installed release, with its own name and icon.
- Improve the repository README with the application icon, release badges and navigation links.

No Cinnamon extension or system configuration changes are required. The tray shows the client's unread/mention state; Cinnamon's taskbar counts retained system notifications, so these numbers can still differ while unread notifications remain.

Native packages for Linux, Windows and macOS (amd64/arm64), SHA-256 checksums and production-window verification remain included. Package signing and runtime requirements are unchanged.
