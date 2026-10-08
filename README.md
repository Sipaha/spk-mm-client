# SPK MM Client website

Published static Astro product site: https://sipaha.github.io/spk-mm-client/. Source lives only on the independent orphan `pages` branch; never merge it into application `main`.

Eight website locales (Russian root, English, Simplified Chinese, Spanish, German, French, Brazilian Portuguese, Japanese), light/dark themes and real application screenshots. The application itself currently has Russian/English UI. Screenshots use a local fake Mattermost with fictional conversations. No user chats, accounts or telemetry.

The site resolves the latest stable GitHub release at runtime. Release v1.0.0 has 14 native packages and matching SHA-256 sidecars. Header and hero buttons choose a verified installer for the detected desktop OS and architecture: Linux DEB (RPM/archive fallback), Windows MSI (ZIP fallback), macOS DMG (archive fallback). Clicking starts the download; visiting the page does not start one. Browser-mode developer binaries, stale versions, incompatible formats and foreign URLs are excluded. OS and architecture selectors allow a different target. A Mac user agent reporting Intel does not establish the processor: explicit browser hints are used, otherwise the user chooses. Mobile and unknown systems retain package selection. GitHub API failure, missing releases and JavaScript-disabled pages retain source-build/release links.

The visual shell and language-routing helpers follow SPK Ocular. Inter fonts retain their bundled upstream license. Existing two-message MM Client icon is unchanged. Design: variance4, motion2, density4; restrained native CSS/Astro, no decorative animations or synthetic screenshots.

Publication uses GitHub Actions Pages from the independent `pages` branch. Repository Settings → Pages → Source must be GitHub Actions, and the `github-pages` environment must permit branch `pages` under Deployment branches and tags. The pinned workflow verifies before uploading/deploying and never creates application releases.

Run `pnpm install --frozen-lockfile`, `pnpm test`, `pnpm build`, `pnpm verify`. Verification covers all eight locales, both themes, mobile/desktop, keyboard gallery, language precedence, unavailable/no-release responses and reduced motion. Run Lighthouse and inspect captures before deployment. Scratch belongs in the containing Solution `.tmp/mm-site`.

Use `TMPDIR=<Solution>/.tmp/mm-site/tmp` and `SITE_SCRATCH=<Solution>/.tmp/mm-site/site-verify`. Start `pnpm preview --background --host 127.0.0.1 --port 53982` before `pnpm verify`. `GENERATE_SOCIAL_CARDS=1 pnpm verify` additionally captures the real site for its social cards. `node scripts/lighthouse.mjs` checks mobile/desktop performance, accessibility, best practices, SEO and layout stability. Keep local TMPDIR short and canonical (for example `<Solution>/.tmp/m`) so Chromium's Unix socket paths fit the platform limit.

CI starts Astro preview with explicit `--background` and stops its own server with an EXIT trap. Astro 7 only auto-backgrounds agent sessions; plain CI runs otherwise stay in foreground before verification can start.
CI uses `/usr/bin/google-chrome` when present, matching the local verification runner. GitHub's Ubuntu 24.04 image includes Chrome (https://github.com/actions/runner-images/blob/main/images/ubuntu/Ubuntu2404-Readme.md). If absent, the workflow installs Playwright Chromium and its dependencies. Both paths execute the same complete browser/axe matrix; browser installation is not a substitute for verification.
Verification jobs replace superseded checks; deployments have a separate serialized group and are not cancelled by a newer check. A stalled dependency setup must not keep the current source waiting behind an obsolete verification run.

Publication was verified on 2026-10-07: the Website check and deploy jobs passed, all eight live HTTPS routes loaded with their canonical language URLs, real images, both themes, working keyboard gallery and truthful no-release downloads. Public icon/media/font-license bytes matched the source SHA-256 hashes. Application release/tag publication is separate and was not performed.

Support links in desktop/mobile navigation, the author section and footer point to the approved localized author profile #support section. Wallets and funding rules are maintained on the author page; the product site does not duplicate or invent them.

The source-build fallback links to the English application README’s `#build-from-source` section.


## Platform-aware downloads, 2026-10-08

The owner requested the same primary download behavior as SPK Ocular. Its established OS/architecture hints, installer preference and manual fallback are adapted for MM Client's verified native release names. No application code, releases or tags are changed.

`pnpm verify` includes a second 48-case matrix with a populated release (eight locales, two themes, three widths), plus actual browser download events for all six OS/architecture pairs, Mac ambiguity and manual selector changes, and mobile fallback. Accessibility audits wait for entrance animations to finish rather than auditing temporarily faded text; contrast assertions remain strict. The original no-release/API-failure/no-JavaScript matrix remains. Pure logic tests verify installer ordering, version/format filtering and architecture detection.

Verification for this update: 10 pure-logic tests, clean Astro check/build, both 48-case browser matrices, six download-event scenarios, manual/unknown-architecture/mobile fallbacks and published installer HEAD requests passed. All six preferred native installers returned HTTP 200 with sizes matching release metadata. Inspected desktop/mobile captures in both themes. Lighthouse: mobile 99/100/100/100, desktop 91/100/100/100; CLS below 0.001. Logs and captures are under the containing Solution `.tmp/mm-site-auto-download`.
