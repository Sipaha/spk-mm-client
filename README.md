# SPK MM Client

A desktop client for Mattermost, built with Go, Wails v3 and React. It uses the
system WebView on Linux, Windows and macOS. The application interface is available
in English and Russian.

[Website](https://sipaha.github.io/spk-mm-client/en/) ·
[Releases](https://github.com/Sipaha/spk-mm-client/releases) ·
[Support the author](https://sipaha.github.io/about/en/#support)

## Features

- Connect to multiple Mattermost servers with password sign-in or GitLab SSO,
  when enabled by the server.
- Read and write messages, edit or delete your posts, react with emoji, save
  posts and mark messages unread.
- Open threads alongside the channel, search messages and jump to a result in
  its conversation.
- Keep separate drafts, preview Markdown and attach files through the file
  picker, drag and drop or clipboard.
- Preview images, PDFs, text, Markdown, audio and video; download attachments
  and open supported file types with system applications.
- Receive desktop notifications and track unread messages and mentions across
  servers. Cached conversations remain readable when disconnected.

The interface uses a dark gray palette and bundled Open Sans. Screenshots on the
website show the actual application connected to a demonstration server with
fictional conversations.

## Downloads

Get published packages from [GitHub Releases](https://github.com/Sipaha/spk-mm-client/releases).
The first release selected for publication is **v1.0.0**; publication is being
verified. Until it appears there, build from source using the instructions below.

| Platform | Native package formats | Requirements |
| --- | --- | --- |
| Linux amd64 / arm64 | DEB, RPM, tar.gz | glibc 2.39+, GTK 3, WebKitGTK 4.1 |
| Windows amd64 / arm64 | Per-user MSI, ZIP | Microsoft Edge WebView2 Runtime |
| macOS amd64 / arm64 | App bundle in DMG or tar.gz | macOS 12 or later |

Windows packages are unsigned. macOS packages use an ad-hoc signature and are not
notarized. Each release includes SHA-256 checksums and third-party license texts.
Browser-mode archives are separately named developer builds. Automatic updates
and AppImage packages are not available in this release.

See [release documentation](docs/releases.md) for verification and packaging details.

## Build from source

Use **Go 1.26.8 or later**, **Node.js 22** and **pnpm 10.33.0**. Native Linux builds
also need the GTK 3 and WebKitGTK 4.1 development libraries. On Ubuntu 24.04:

```sh
sudo apt-get install libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev
make build-desktop
./build/bin/spk-mm-client-desktop
```

`make run` rebuilds and starts the desktop client in the foreground. It does not
stop an existing instance. `make release VERSION=1.0.0` builds the production
Linux desktop binary; production desktop builds reject development fake-server
flags. Windows and macOS packages are built on their native CI runners.

For browser-mode development:

```sh
make build
make run-browser
```

The development UI opens at `http://127.0.0.1:5180` with an in-process demonstration
Mattermost server and a separate temporary data directory. Browser mode is bound
to loopback. For a desktop demonstration, use `--mm-fake` with an isolated
`SPK_MM_CLIENT_HOME`; never point development fake-server runs at your real profile.

## Data and configuration

The default data directory is `~/.spk/mm-client/`. Override it with
`SPK_MM_CLIENT_HOME` to use a separate profile. `SPK_MM_CLIENT_DOWNLOADS` selects
the downloads directory; otherwise the system downloads directory is used.

On Linux, `SPK_MM_CLIENT_GPU=always|ondemand|never` controls WebKitGTK hardware
acceleration. The default is `always`; use `never` if your graphics driver causes
rendering problems.

`spk-mm-client version` and `spk-mm-client --version` print the build version.
`spk-mm-client licenses` prints the bundled third-party notices. The visible
**About** button also shows the version, license and project links.

## Development and checks

```sh
make lint                 # Go vet, golangci-lint and frontend ESLint
make test-go              # Go tests with the race detector
make test-front           # Frontend tests
make test-e2e             # Browser integration tests
make check-packaging      # Packaging and publication contract tests
make cross-check          # Windows desktop cross-build and vet
```

CI runs these checks, scans Go dependencies for vulnerabilities, builds packages
on six native OS/architecture runners and verifies a real isolated production
window on each runner. A validated `vMAJOR.MINOR.PATCH` tag publishes a GitHub
Release only after the complete asset set is uploaded and checked.

Architecture, protocol findings and development notes are in
[docs](docs/), including the [design specification](docs/specs/2026-09-24-spk-mattermost-design.md)
and [known limitations](docs/backlog.md). The website source lives on the separate
`pages` branch.

## License and author

SPK MM Client is licensed under [Apache License 2.0](LICENSE).
See [NOTICE](NOTICE) and [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt) for
copyright and dependency notices.

Author: [Pavel Simonov / Sipaha](https://sipaha.github.io/about/en/).
[Support the project’s author](https://sipaha.github.io/about/en/#support).
