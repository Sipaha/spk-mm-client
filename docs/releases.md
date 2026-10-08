# Release builds

SPK MM Client uses GitHub Actions to test, build and publish native releases. The first published version is [v1.0.0](https://github.com/Sipaha/spk-mm-client/releases/tag/v1.0.0). The application source and release workflows are on `main`; the website remains an independent orphan `pages` branch and is never merged into `main`.

## Packages and runtime requirements

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
