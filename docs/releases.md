# Release builds

The user selected **v1.0.0** for the first SPK MM Client release on 2026-10-07. Build and publication automation is part of this phase; automatic application updates and AppImage distribution are separate work.

Native Linux amd64/arm64: DEB, RPM and portable tar.gz. Native Windows amd64/arm64: per-user MSI and ZIP, with the mmauth protocol handler. Native macOS amd64/arm64: app bundle, tar.gz and DMG, with the mmauth URL scheme and an ad-hoc signature. Ad-hoc signing is not Apple notarization. Windows packages are not claimed to be Authenticode signed. Linux builds require glibc 2.39+, GTK3 and WebKitGTK 4.1. Windows requires WebView2; macOS target is 12+.

Every platform also has a separately named browser-mode developer archive, excluded from the product website's native download list. Release version comes from the validated Git tag. Archives include BUILD-INFO (version, source commit, target), Apache LICENSE, NOTICE, full upstream third-party notices and documentation. Checksums cover every archive/installer, with per-file sidecars and SHA256SUMS.

CI must run Go race, linters, frontend, full browser tests, vulnerability checks and meaningful packaging tests before native packaging. Native jobs verify binary architecture, actual executable version, exact archive contents, installer install/remove where supported, and a real isolated production window with screenshots. No user application/profile is used or restarted.

The publication job first creates a draft, uploads the exact verified complete asset set, verifies its names/sizes, then publishes. Already published releases are immutable; use a new tag/version for corrections. Missing assets or checksums must fail publication. Source website remains on orphan pages and is never merged into main.

Website support links use the approved author profile's localized #support section. No wallets are duplicated and no new funding policy is introduced.

NATIVE-VERIFICATION.zip contains the six actual isolated production screenshots and version/source reports (no app logs or credentials). Its checksum is part of the exact release manifest. Publishing requires these reports to match the same tagged source commit and version.

Local pre-publication verification: 1125 frontend tests, Go race/lint, 28 packaging tests, Windows cross-build/vet, vulnerability scan with Go 1.26.8 and x/image 0.45.0, native Linux DEB/RPM/archive inspection and an actual isolated production GTK window. Live support destinations were opened and checked for all eight website languages. Generated notices include Open Sans, emoji-datasource 6.1.1 and the Apache-licensed Mattermost webapp shortname data. All 107 browser integration tests also passed (4.5 minutes), without proxying loopback traffic. Six-platform CI and first publication must still pass before the release is considered complete.

Publication preparation: GitHub temporarily rejected SSH writes with `Internal Server Error`, including a ref pointing to an existing commit. The writes recovered and main reached `f45a271` on 2026-10-08 (Asia/Novosibirsk). The English README is published. Six-platform CI is now required before creating the first release tag; no v1.0.0 tag or release has been created yet.

Main CI runs are serialized without cancelling the running commit: an older queued push was observed starting late and cancelling a newer run. Pull-request checks remain replaceable. The published English README was compared byte-for-byte with the source and inspected as rendered on GitHub.

Both the full validation job and every native build reject a regenerated third-party inventory that differs from the committed source, so rebuilding cannot silently hide stale notices or platform-specific discrepancies.

Native Windows/macOS CI exposed module names that differed only by case (`ImageZoom.tsx` / `imageZoom.ts`, `Splitter.tsx` / `splitter.ts`). Pure utility modules and their tests were renamed to `imageZoomMath` and `splitterGeometry`, with no UI behavior change. The frontend build checks module paths for case-insensitive resolution collisions before TypeScript compilation.
