# Product website and About

The product website follows SPK Ocular's static site layout while retaining MM Client's existing two-message application icon. Product screenshots must be captured from the actual application with an isolated demonstration Mattermost server; they must contain no personal chat/server data.

The application About uses a visible button and a modal that retains the chat, draft, thread, selected server and scroll position. It shows actual build information, project links, Pavel Simonov/Sipaha and the localized author profile. Employment/biography belong on the personal website and must not be duplicated into the application.

Current repository state: no published GitHub release. The owner enabled GitHub Actions Pages on 2026-10-07; publication is awaiting permission for the independent `pages` branch in the `github-pages` environment. The website must not invent downloadable packages, supported native installers or unverified installer support. The user selected Apache 2.0; LICENSE and NOTICE declare it for this project. Hosting setup is not changed until a complete verified website is available.

## Hosting prerequisite

The source is an independent orphan `pages` worktree (`.site`), never merged into application `main`. The Pages workflow builds and verifies the site before uploading the static artifact, and deploys only when Pages is configured for GitHub Actions. Application release/tag creation is outside this phase.

GitHub Actions Pages is enabled. Website workflow https://github.com/Sipaha/spk-mm-client/actions/runs/37630809142 passed the complete check job, but GitHub rejected deployment before its steps ran: `Branch "pages" is not allowed to deploy to github-pages due to environment protection rules.` Evidence: https://github.com/Sipaha/spk-mm-client/actions/runs/37630809142/job/112825349515.

The repository owner must allow branch `pages` in **Settings → Environments → github-pages → Deployment branches and tags**, retaining the existing restrictions. There is no authenticated administration capability in this session; the workflow does not bypass environment protection, merge the orphan website branch into `main`, or create application releases. After the owner changes the rule, rerun/push the website workflow, verify deployment and the live eight-language site. Until then the product URL is the intended destination, not a verified published site.

## Verification artifacts

All demonstration profiles, browser outputs and logs belong to the containing Solution `.tmp/mm-site`. E2E screenshots use `E2E_SHOTS` (or the runner's temporary directory) rather than a path to a different checkout; Playwright results use its isolated profile directory. Native verification uses a separate fake-server profile, a dead D-Bus socket, and checks the live window PID before capturing it. Existing user applications and the shared display are preserved.

The added 40px product header changes the chat viewport height. The picture-wheel fixture reserves those 40px to preserve its original feed geometry; its assertions for a pending shift, no script writes during the gesture, no torn frames and no jumps remain strict. Three repeated targeted runs passed. No production scrolling implementation was changed for this fixture.

The two short-sidebar overflow fixtures likewise add the product header height while preserving their original list viewport. Their visibility, mention/read-state and held-channel assertions remain unchanged; all four scenarios passed in three repeated runs.

## Verified phase, 2026-10-07

- Application: Go race tests, Go/frontend linters, 1125 frontend tests (69 files), Windows desktop compile/vet and clean browser/GTK builds passed. The full browser suite passed all 107 scenarios after reserving the product-header height in geometry-specific fixtures; no checks were skipped or weakened.
- About: RU/EN visible-button discovery, actual version and Apache 2.0, localized profile/product destinations, keyboard isolation/trap/restore, mounted chat/thread identity, both drafts and scroll retention passed. A separate GTK fake-server instance was captured, its live window PID verified, and its author URL intercepted at `xdg-open`; it was stopped through its own profile flag.
- Website: all six unit tests, production Astro build, 48 combinations of eight languages × two themes × three widths, real-image loading, keyboard gallery, WCAG AA axe audits, no-release/API-failure/valid-release fixtures, denied storage, reduced motion, language navigation and no-JavaScript fallback passed. Actual full-page/mobile screenshots were inspected. Lighthouse mobile: 99/100/96/100; desktop: 91/100/96/100 (performance/accessibility/best practices/SEO), LCP below 1.81s and CLS below 0.002.
- The website was created as independent root commit `508e653`; current website source is `a714829` (CI implementation `70b1b07`). The initial workflow ran preview in foreground outside an agent environment and timed out. Explicit `--background` plus cleanup fixed it; run https://github.com/Sipaha/spk-mm-client/actions/runs/37628456675 succeeded. CI now uses the available Chrome, installing Playwright Chromium only when needed, and separates replaceable verification jobs from serialized deployments. Before the owner enabled Pages, run https://github.com/Sipaha/spk-mm-client/actions/runs/37629844104 passed checks and skipped deployment. After Pages was enabled, run https://github.com/Sipaha/spk-mm-client/actions/runs/37630809142 passed checks but deployment was rejected by the environment branch rule described above. There are no application releases or tags from this phase.

Final logs and inspected screenshots are retained in the Solution `.tmp/mm-site`; redundant profiles, caches and diagnostic worktrees are removed. The first exploratory full run used legacy hard-coded screenshot paths in another Solution before those paths were corrected; those outside files were not deleted or modified further.

The committed application implementation is `65e82d9`. Both browser/GTK binaries were rebuilt with that actual version; RU/EN About e2e passed again, and an owned GTK window was rechecked for version/license, retained draft/feed/scroll, Close focus, focus restoration and the actual author destination. Final native screenshots and a JSON verification report remain in `.tmp/mm-site`. Own previews/native helpers were stopped, temporary profiles/package caches/diagnostic worktrees and duplicate captures removed; final reports are retained.

As of the final check, both application and website sources are pushed with Pavel Simonov <sipahabk@gmail.com> as author and committer. GitHub recognizes Apache-2.0, and the public LICENSE is readable. The owner has enabled GitHub Actions Pages. The remaining prerequisite is the environment rule described above. After `pages` is permitted, rerun/push the Website workflow and verify deployment and the live eight-language site before calling publication complete. No other phase is started while this hosting prerequisite remains.
