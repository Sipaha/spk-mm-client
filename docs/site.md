# Product website and About

The product website follows SPK Ocular's static site layout while retaining MM Client's existing two-message application icon. Product screenshots must be captured from the actual application with an isolated demonstration Mattermost server; they must contain no personal chat/server data.

The application About uses a visible button and a modal that retains the chat, draft, thread, selected server and scroll position. It shows actual build information, project links, Pavel Simonov/Sipaha and the localized author profile. Employment/biography belong on the personal website and must not be duplicated into the application.

The owner enabled GitHub Actions Pages and permitted the independent `pages` branch on 2026-10-07. The product website is published at https://sipaha.github.io/spk-mm-client/. The website must not invent downloadable packages, supported native installers or unverified installer support. The user selected Apache 2.0; LICENSE and NOTICE declare it for this project. Release download links are populated from verified public GitHub release assets.

## Publication

The source is an independent orphan `pages` worktree (`.site`), never merged into application `main`. GitHub Actions Pages is enabled, and the `github-pages` environment permits branch `pages`. The pinned workflow builds and verifies the site before uploading and deploying its static artifact. The later authorized release phase adds application packaging and first v1.0.0 publication; see [releases.md](releases.md).

The first successful publication is commit `5e5b999`, workflow https://github.com/Sipaha/spk-mm-client/actions/runs/37644575853: check and deploy both succeeded. All eight live HTTPS language routes, canonical URLs, actual images, light/dark themes, mobile layout, keyboard gallery, author destinations and truthful no-release downloads were checked in a real browser; screenshots were inspected. Nine public icon/media/font-license assets matched source SHA-256 hashes.

The earlier deployment https://github.com/Sipaha/spk-mm-client/actions/runs/37630809142 passed checks but GitHub rejected it because `pages` was not allowed by the environment branch rule. The owner explicitly resolved that rule; the workflow retained environment protection and did not merge website source into application `main`.

## Verification artifacts

All demonstration profiles, browser outputs and logs belong to the containing Solution `.tmp/mm-site`. E2E screenshots use `E2E_SHOTS` (or the runner's temporary directory) rather than a path to a different checkout; Playwright results use its isolated profile directory. Native verification uses a separate fake-server profile, a dead D-Bus socket, and checks the live window PID before capturing it. Existing user applications and the shared display are preserved.

The added 40px product header changes the chat viewport height. The picture-wheel fixture reserves those 40px to preserve its original feed geometry; its assertions for a pending shift, no script writes during the gesture, no torn frames and no jumps remain strict. Three repeated targeted runs passed. No production scrolling implementation was changed for this fixture.

The two short-sidebar overflow fixtures likewise add the product header height while preserving their original list viewport. Their visibility, mention/read-state and held-channel assertions remain unchanged; all four scenarios passed in three repeated runs.

## Verified phase, 2026-10-07

- Application: Go race tests, Go/frontend linters, 1125 frontend tests (69 files), Windows desktop compile/vet and clean browser/GTK builds passed. The full browser suite passed all 107 scenarios after reserving the product-header height in geometry-specific fixtures; no checks were skipped or weakened.
- About: RU/EN visible-button discovery, actual version and Apache 2.0, localized profile/product destinations, keyboard isolation/trap/restore, mounted chat/thread identity, both drafts and scroll retention passed. A separate GTK fake-server instance was captured, its live window PID verified, and its author URL intercepted at `xdg-open`; it was stopped through its own profile flag.
- Website: all six unit tests, production Astro build, 48 combinations of eight languages × two themes × three widths, real-image loading, keyboard gallery, WCAG AA axe audits, no-release/API-failure/valid-release fixtures, denied storage, reduced motion, language navigation and no-JavaScript fallback passed. Actual full-page/mobile screenshots were inspected. Lighthouse mobile: 99/100/96/100; desktop: 91/100/96/100 (performance/accessibility/best practices/SEO), LCP below 1.81s and CLS below 0.002.
- The website was created as independent root commit `508e653`; the original published website source was `fdfe8df` (CI implementation `70b1b07`). The initial workflow ran preview in foreground outside an agent environment and timed out. Explicit `--background` plus cleanup fixed it; run https://github.com/Sipaha/spk-mm-client/actions/runs/37628456675 succeeded. CI now uses the available Chrome, installing Playwright Chromium only when needed, and separates replaceable verification jobs from serialized deployments. Before the owner enabled Pages, run https://github.com/Sipaha/spk-mm-client/actions/runs/37629844104 passed checks and skipped deployment. After Pages was enabled, run https://github.com/Sipaha/spk-mm-client/actions/runs/37630809142 passed checks but deployment was rejected by the environment branch rule described above. There are no application releases or tags from this phase.

Final logs and inspected screenshots are retained in the Solution `.tmp/mm-site`; redundant profiles, caches and diagnostic worktrees are removed. The first exploratory full run used legacy hard-coded screenshot paths in another Solution before those paths were corrected; those outside files were not deleted or modified further.

The committed application implementation is `65e82d9`. Both browser/GTK binaries were rebuilt with that actual version; RU/EN About e2e passed again, and an owned GTK window was rechecked for version/license, retained draft/feed/scroll, Close focus, focus restoration and the actual author destination. Final native screenshots and a JSON verification report remain in `.tmp/mm-site`. Own previews/native helpers were stopped, temporary profiles/package caches/diagnostic worktrees and duplicate captures removed; final reports are retained.

As of the final check, both application and website sources are pushed with Pavel Simonov <sipahabk@gmail.com> as author and committer. GitHub recognizes Apache-2.0, and the public LICENSE is readable. The owner has enabled GitHub Actions Pages. The owner has also allowed `pages` in the deployment environment; publication and live verification succeeded. This phase is complete: the final current-source workflow https://github.com/Sipaha/spk-mm-client/actions/runs/37645549589 (`fdfe8df`) passed both check and deploy jobs, and live verification passed. No independent phase is started automatically.

## Live verification after publication

The owner resolved the environment rule, and the site is live. Eight unmocked browser visits returned HTTPS 200 with correct canonical language URLs and real no-release data from GitHub; gallery keyboard control, loaded images, localized author links, both themes and mobile layout passed. The same strict website verifier then passed all 48 language/theme/width combinations against the public site, including WCAG AA axe audits, plus API unavailable/valid-release fixtures, denied storage, reduced motion, language switching and the no-JavaScript fallback. Actual published-site screenshots were inspected. One earlier proxied image wait timed out; the complete direct run passed without changing any assertion or timeout.

Live evidence is retained in `.tmp/mm-site/live/report.json`, `live-check.log`, `live-matrix-final.log`, `published-assets.json`, and the inspected live screenshots. The screenshots contain only the previously verified fictional demonstration data. Native About and application tests from this same phase remain valid; publication changed documentation/hosting only, not application code. No user application was restarted and no application release/tag was created.

## Support links

Commit `308e7b8` added visible localized support links in the desktop/mobile navigation, author section and footer. Website CI https://github.com/Sipaha/spk-mm-client/actions/runs/37651655031 succeeded. All eight live pages contained the four approved links, and real browser clicks reached each localized author page’s visible `#support` section. No wallet addresses are duplicated. Live desktop/mobile screenshots and logs are in the containing Solution `.tmp/mm-release/live-support`.

The application release [v1.0.0](https://github.com/Sipaha/spk-mm-client/releases/tag/v1.0.0) is public. All eight live languages display the 14 native package links and matching SHA-256 sidecars. Download links, localized support destinations, actual images and WCAG AA checks pass across both themes and three viewport widths. The English README and source-build anchor are published.
