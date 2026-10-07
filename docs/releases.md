# Release builds

The user selected **v1.0.0** for the first SPK MM Client release on 2026-10-07. Build and publication automation is part of this phase; automatic application updates and AppImage distribution are separate work.

Native Linux amd64/arm64: DEB, RPM and portable tar.gz. Native Windows amd64/arm64: per-user MSI and ZIP, with the mmauth protocol handler. Native macOS amd64/arm64: app bundle, tar.gz and DMG, with the mmauth URL scheme and an ad-hoc signature. Ad-hoc signing is not Apple notarization. Windows packages are not claimed to be Authenticode signed. Linux builds require glibc 2.39+, GTK3 and WebKitGTK 4.1. Windows requires WebView2; macOS target is 12+.

Every platform also has a separately named browser-mode developer archive, excluded from the product website's native download list. Release version comes from the validated Git tag. Archives include BUILD-INFO (version, source commit, target), Apache LICENSE, NOTICE, full upstream third-party notices and documentation. Checksums cover every archive/installer, with per-file sidecars and SHA256SUMS.

CI must run Go race, linters, frontend, full browser tests, vulnerability checks and meaningful packaging tests before native packaging. Native jobs verify binary architecture, actual executable version, exact archive contents, installer install/remove where supported, and a real isolated production window with screenshots. No user application/profile is used or restarted.

The publication job first creates a draft, uploads the exact verified complete asset set, verifies its names/sizes, then publishes. Already published releases are immutable; use a new tag/version for corrections. Missing assets or checksums must fail publication. Source website remains on orphan pages and is never merged into main.

Website support links use the approved author profile's localized #support section. No wallets are duplicated and no new funding policy is introduced.

NATIVE-VERIFICATION.zip contains the six actual isolated production screenshots and version/source reports (no app logs or credentials). Its checksum is part of the exact release manifest. Publishing requires these reports to match the same tagged source commit and version.

Local pre-publication verification: 1125 frontend tests, Go race/lint, 32 packaging tests, Windows cross-build/vet, vulnerability scan with Go 1.26.8 and x/image 0.45.0, native Linux DEB/RPM/archive inspection and an actual isolated production GTK window. Live support destinations were opened and checked for all eight website languages. Generated notices include Open Sans, emoji-datasource 6.1.1 and the Apache-licensed Mattermost webapp shortname data. All 107 browser integration tests also passed (4.5 minutes), without proxying loopback traffic. Six-platform CI and first publication must still pass before the release is considered complete.

Publication preparation: GitHub temporarily rejected SSH writes with `Internal Server Error`, including a ref pointing to an existing commit. The writes recovered and main reached `f45a271` on 2026-10-08 (Asia/Novosibirsk). The English README is published. Six-platform CI is now required before creating the first release tag; no v1.0.0 tag or release has been created yet.

Main CI runs are serialized without cancelling the running commit: an older queued push was observed starting late and cancelling a newer run. Pull-request checks remain replaceable. The published English README was compared byte-for-byte with the source and inspected as rendered on GitHub.

Both the full validation job and every native build reject a regenerated third-party inventory that differs from the committed source, so rebuilding cannot silently hide stale notices or platform-specific discrepancies.

Native Windows/macOS CI exposed module names that differed only by case (`ImageZoom.tsx` / `imageZoom.ts`, `Splitter.tsx` / `splitter.ts`). Pure utility modules and their tests were renamed to `imageZoomMath` and `splitterGeometry`, with no UI behavior change. The frontend build checks module paths for case-insensitive resolution collisions before TypeScript compilation.

The corrected CI uses a fresh concurrency group during migration, so obsolete runs from the earlier pipeline cannot hold the fixed build in their queue. The full test-before-packaging gate is unchanged.

Windows license generation initially sorted nested Wails notices before its root LICENSE because pathlib compares Windows paths without case. The collector now sorts explicit POSIX relative names, and a regression test reproduces Windows path comparison on Linux. The committed inventory and all upstream legal wording remain unchanged. Production-tag vulnerability scans for Linux and Windows amd64/arm64 found no vulnerabilities.

Native build/smoke failures also emit escaped GitHub annotations with compiler or owned-window details; these can be inspected through the public checks API when anonymous job-log downloads are unavailable. No application log or user profile is published.

Static frontend legal buckets also use explicit filename order. The CI source comparison reports a bounded, escaped diff through public annotations instead of only an exit code; the inventory still must match the committed source exactly.

Windows package/license verification and MSI install/remove passed after canonical ordering. Native smoke cleanup exposed a WebView2 descendant holding the isolated profile after parent-only termination. Windows cleanup now terminates only the live owned Popen process tree, retries profile removal briefly, and reports any original UI failure before cleanup. User processes/profiles are never selected.

Validation stages are separate CI steps and run through a wrapper that preserves the original exit code, stores logs in workspace scratch, and emits only a bounded failure tail as an escaped public annotation. This makes browser/Go failures reviewable without authenticated log downloads; no validation command was removed.

Windows UI Automation found an About-named text element with no Invoke pattern. The smoke now selects the visible enabled Button control by both name and control type before invoking it, retaining the actual version/Apache text and owned screenshot requirements.

A CI browser failure was traced to the multiline-edit test pressing ArrowUp while its new post was still optimistic/pending. The product intentionally edits only confirmed own posts, so the shortcut selected an earlier one-line message. The test now waits for server confirmation and verifies the editor contains the exact seven-line post before retaining all height/growth assertions.

The multiline-edit scenario also uses a unique per-run message prefix so repeated checks cannot match earlier posts retained by the shared demonstration server. Ten consecutive real browser repetitions passed with exact text and unchanged height/growth requirements.

Windows WebView2 exposes the semantic About Button but lacks the UIA Invoke provider. The native test uses Invoke when supported, otherwise a real pointer click after checking the owned Popen window is foreground and the button center lies inside that window. Actual About version/license and screenshot checks remain unchanged. Both Linux and both macOS native jobs passed in CI 37676680110.
