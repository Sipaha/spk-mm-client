# Release builds

SPK MM Client uses GitHub Actions to test, build and publish native releases. The first published version is [v1.0.0](https://github.com/Sipaha/spk-mm-client/releases/tag/v1.0.0). The application source and release workflows are on `main`; the website remains an independent orphan `pages` branch and is never merged into `main`.

## Unpublished 1.0.1 tag

The annotated `v1.0.1` tag points to
`a5c6ac452db558f1f235d73a1d86f18988f34db7`. Main CI
[38359485145](https://github.com/Sipaha/spk-mm-client/actions/runs/38359485145)
passed, but tagged Release run
[38363399939](https://github.com/Sipaha/spk-mm-client/actions/runs/38363399939)
failed in `TestSendReplyUploadProgressTellsBothChannelAndThread` before
packaging or publication. No v1.0.1 release was published. The tag is preserved.
The fixture can consume its injected upload failure before `SendReply`, which
intentionally retries uploads failed in the composer; the test then waits for
a failure after a successful retry. A deterministic fixture correction holds
that first upload until `SendReply` returns. Publishing corrected source requires
a new version; corrected source was released as v1.0.2.
The original ordering was reproduced deterministically by waiting for the
composer upload to fail before sending. The corrected fixture passed 100 race
repetitions (63.191s) and the complete API race suite (51.137s); channel/thread
event assertions and production behavior are unchanged.

## Published 1.0.2

[v1.0.2](https://github.com/Sipaha/spk-mm-client/releases/tag/v1.0.2) was
published on 2026-10-13 from
`0b02d53a36077654d72b77dc08d54536899d3324`. Candidate main CI
[38365077462](https://github.com/Sipaha/spk-mm-client/actions/runs/38365077462)
passed its common job and all six native targets. Tagged Release workflow
[38367478835](https://github.com/Sipaha/spk-mm-client/actions/runs/38367478835)
passed validation, the full common suite, all six native jobs and publication.

The actual 43 public assets were downloaded and verified: 21 payload SHA-256
entries and sidecars, 12 archives, version/source/architecture, Go 1.26.9 build
info, tagged legal and documentation contents, DEB/RPM metadata and payloads,
extracted MSI binaries/notices and DMG app contents. Production Linux CLI
`version` and `licenses` matched the tagged source. All six production PNGs
matched their source/version/hash reports and were visually reviewed.

The live website returned v1.0.2 from GitHub `releases/latest`. All 48
language/theme/width combinations passed: eight languages, light/dark themes,
375/768/1440px widths, 14 native packages and matching checksum links,
loaded images, support/source destinations, no horizontal overflow or page
errors, and WCAG AA checks. The first page used the live latest endpoint;
remaining layout audits reused the downloaded actual release metadata to
avoid exhausting anonymous GitHub quota. OS/architecture filters were set to
all when checking the complete package list. Website screenshots were reviewed.

This patch release pins Go 1.26.9 with its standard-library security fixes and
includes reconnect recovery, generation-ordered sign-in and
navigation, account-scoped media, joined service/session-owned downloads, and
native Copy Image in the image viewer. [English notes](../changelog/1.0.2/en.md)
and [Russian notes](../changelog/1.0.2/ru.md) describe user-visible changes.

Long-history refreshes use acknowledged-baseline channel deltas. The retained
history has no new cap; initial/reset responses remain full, and Go traversal
remains linear. A 50,000-post projection cache retains about 19.4 MiB until
navigation replaces it, in exchange for much smaller recurring IPC. Benchmarks
and correctness evidence are in [audit fixes](audits/2026-10-12/fixes.md).

Local validation: all Go race tests; 1135 frontend tests; 109 complete browser
integration tests; lint and Windows cross-build/vet; clean browser/Linux desktop
builds; a 90-second isolated native smoke with screenshot inspection. Copy Image
was additionally tested using actual production-tag WebKit native input and a
PNG clipboard read. Publication is gated by the tagged CI and all six native jobs.

## Packages and runtime requirements

### Linux menu launcher shadowing

A pre-existing user launcher at
`~/.local/share/applications/spk-mm-client.desktop` takes precedence over the
package's `/usr/share/applications/spk-mm-client.desktop`. The legacy dev
registration can have no `Icon` and an `Exec` pointing to a deleted checkout;
then the menu shows a generic icon and cannot start the installed release.
On the inspected installation, `/usr/bin/spk-mm-client` reports 1.0.2 and its
SHA-256 matches the public release binary, but the user launcher pointed to a
missing old dev build. The user explicitly authorized removal of that stale
override, and it was removed after preserving a local backup. GIO now resolves
`spk-mm-client.desktop` to `/usr/share/applications/spk-mm-client.desktop`,
with the correct name, command and icon. Future package upgrades update that
system entry. Copying it back into the user applications directory would create
another override, so removing a known-obsolete user entry is preferable.
The installed production binary was also started on its own Xvfb with a fresh
scratch profile: its real window rendered correctly, OCR passed and the PNG was
visually checked. The proposed packaged launcher passes
`desktop-file-validate`; the installed icon file exists.

`make install-dev-linux` now registers `spk-mm-client-dev.desktop`, named
**SPK MM Client (development)**, with the checkout's icon. It requires an
existing executable at an absolute path and intentionally makes the dev entry
the `mmauth` handler. Existing user release/legacy entries are preserved and
reported for explicit migration. The dev desktop ID cannot shadow the packaged
release ID. Exec escaping applies both Desktop Entry string escaping and quoted
argument escaping, as required by the
[Desktop Entry specification](https://specifications.freedesktop.org/desktop-entry/latest/exec-variables.html).
Scratch-only registration checks validate the file, preserve existing entries,
verify GIO's actual default-handler selection and URI delivery, refuse missing
binaries, and launch an executable whose path includes quotes, spaces, dollar,
backtick and backslash characters.

| Platform | Architectures | Native formats | Requirements |
| --- | --- | --- | --- |
| Linux | amd64, arm64 | DEB, RPM, tar.gz | glibc 2.39+, GTK 3, WebKitGTK 4.1 |
| Windows | amd64, arm64 | Per-user MSI, ZIP | Microsoft Edge WebView2 Runtime |
| macOS | amd64, arm64 | App bundle in DMG or tar.gz | macOS 12+ |

Windows packages are unsigned. macOS app bundles have an ad-hoc signature and are not notarized. Windows and macOS packages register the `mmauth` protocol. Each target also has a separately named browser-mode developer archive, excluded from the website's native download list. Automatic updates and AppImage distribution are separate work.

Archives include BUILD-INFO with version, source commit and platform; Apache LICENSE and NOTICE; full upstream third-party notices; README and release documentation. The CLI commands `version`, `--version` and `licenses` work without opening a window. Production builds reject development fake-server flags.

## Validation and publication

Main CI isolates concurrency by commit SHA so older pushes cannot cancel or block newer commits. Pull-request checks can replace earlier checks for the same PR. Main CI runs linters, all Go race tests, frontend tests, packaging tests, workflow validation, vulnerability scans, Windows cross-build/vet, committed license-inventory checks and the full browser integration suite before native packaging. Native builds run on all six OS/architecture targets. They verify executable architecture and actual version, archive contents, installer install/remove where supported, and a real production window in an isolated profile. User applications and profiles are never used or restarted.

License collection uses explicit POSIX filename ordering on every platform and fails if regenerated notices differ from committed source. The frontend build rejects module paths that collide under case-insensitive resolution. Compiler, smoke and early Go test failures emit bounded escaped GitHub annotations, preserving failed-test names even when later logs overwrite the tail, while private application logs and profiles are excluded from public assets.

The native smoke verifies window ownership before capture. Windows checks the real About dialog for version and Apache license text through UI Automation; if WebView2 has no Invoke provider, it uses the framework-provided `Accessibility.IAccessible` button default action inside the verified owned HWND subtree. Pointer fallback still requires foreground ownership and button bounds. On disposable Windows runners, a foreground-lock retry uses a real ALT press/release as documented by Microsoft; it changes no persistent settings and still requires verified foreground ownership before clicking. Cleanup targets only the process tree created by the test. Linux and macOS verify actual rendered text with OCR.

An annotated `vX.Y.Z` tag starts the release workflow. The tag supplies the package version. All tests and six native jobs run again before publication. `NATIVE-VERIFICATION.zip` contains six actual production PNGs, matching source/version reports and a manifest, with no app logs or credentials. The publication gate requires all reports to match the tagged source and version.

A release has exactly **43 assets**: 20 package archives/installers, their 20 SHA-256 sidecars, the native-evidence ZIP and its sidecar, and SHA256SUMS containing 21 payload checksums. Publication creates a draft, uploads the complete verified set, checks remote names and sizes, then makes the release public. Missing or corrupt assets fail publication. Published releases and tags are immutable; corrections require a new version.

Public verification downloads the actual assets, checks sizes/digests and sidecars, compares version/source/architecture/legal payloads with tagged source, inspects MSI and DMG contents, runs the Linux executable and visually reviews all six production screenshots. The live website is checked across eight languages, both themes and three widths, including native package/checksum links, images, support destinations and WCAG AA audits.

## Reaction ordering and test fixtures

Reaction requests record their expected WebSocket echoes before sending. Delayed echoes of older toggles cannot retire the newest intent while later requests are still outstanding, including when unsent opposite clicks coalesce. Definitively refused requests remove their expected echo; existing retry behavior and the 30-second unpinned intent lifetime remain. Deterministic state regressions and 300 repeated concurrent-click scenarios verify immediate convergence without relaxing the assertion.

Geometry fixtures use local noon so five-hour-old posts cannot unexpectedly add a date-separator row around midnight. Browser tests wait for confirmed posts before editing. The gap-anchor setup waits for stable scrolling beyond the 150ms ScrollShift idle flush and retries only positioning before the measured event. Exact pixel-position assertions after a live post and the held history response remain unchanged. Reaction in-flight tests hold a real received HTTP request instead of relying on a 300ms delay: optimistic state alone does not prove sending has started, and unsent opposite clicks may legitimately coalesce. The tests retain refusal/rollback checks and verify exact POST/DELETE counts. All three corrected scenarios passed 100 repetitions each under the race detector. Verification included 1125 frontend tests, 33 packaging tests, all 107 browser integration tests and 20 consecutive gap-anchor repetitions.

Website support links use the approved author's localized `#support` page. Wallets are not duplicated and this release introduces no new funding policy.

## Deterministic reply pending-state verification

Release CI exposed a timing assumption in `TestReplyIsCreatedWithRootID`: a
fast create response or WebSocket echo can confirm the post after the change
notification but before the test reads the pending view. The fixture now holds
the actual create request until pending state, root ID and CRT feed exclusion
have been inspected, then releases it and verifies confirmation and root count.
Assertions are retained; no production delay or timeout relaxation was added.
