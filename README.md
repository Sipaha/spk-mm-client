# SPK MM Client website

Static Astro product site, intended URL: https://sipaha.github.io/spk-mm-client/. Source lives only on the independent orphan `pages` branch; never merge it into application `main`.

Eight website locales (Russian root, English, Simplified Chinese, Spanish, German, French, Brazilian Portuguese, Japanese), light/dark themes and real application screenshots. The application itself currently has Russian/English UI. Screenshots use a local fake Mattermost with fictional conversations. No user chats, accounts or telemetry.

The current project has no packaged GitHub release. Downloads display that fact and link to source-build instructions. Future stable release files are accepted only from this repository's GitHub release URLs. Browser-mode developer binaries and foreign URLs are excluded. GitHub API failure and JavaScript-disabled pages retain the source-build/release links.

The visual shell and language-routing helpers follow SPK Ocular. Inter fonts retain their bundled upstream license. Existing two-message MM Client icon is unchanged. Design: variance4, motion2, density4; restrained native CSS/Astro, no decorative animations or synthetic screenshots.

Publication uses GitHub Actions Pages from the independent `pages` branch. Repository Settings → Pages → Source must be GitHub Actions, and the `github-pages` environment must permit branch `pages` under Deployment branches and tags. The pinned workflow verifies before uploading/deploying and never creates application releases.

Run `pnpm install --frozen-lockfile`, `pnpm test`, `pnpm build`, `pnpm verify`. Verification covers all eight locales, both themes, mobile/desktop, keyboard gallery, language precedence, unavailable/no-release responses and reduced motion. Run Lighthouse and inspect captures before deployment. Scratch belongs in the containing Solution `.tmp/mm-site`.

Use `TMPDIR=<Solution>/.tmp/mm-site/tmp` and `SITE_SCRATCH=<Solution>/.tmp/mm-site/site-verify`. Start `pnpm preview --background --host 127.0.0.1 --port 53982` before `pnpm verify`. `GENERATE_SOCIAL_CARDS=1 pnpm verify` additionally captures the real site for its social cards. `node scripts/lighthouse.mjs` checks mobile/desktop performance, accessibility, best practices, SEO and layout stability.

CI starts Astro preview with explicit `--background` and stops its own server with an EXIT trap. Astro 7 only auto-backgrounds agent sessions; plain CI runs otherwise stay in foreground before verification can start.
CI uses `/usr/bin/google-chrome` when present, matching the local verification runner. GitHub's Ubuntu 24.04 image includes Chrome (https://github.com/actions/runner-images/blob/main/images/ubuntu/Ubuntu2404-Readme.md). If absent, the workflow installs Playwright Chromium and its dependencies. Both paths execute the same complete browser/axe matrix; browser installation is not a substitute for verification.
Verification jobs replace superseded checks; deployments have a separate serialized group and are not cancelled by a newer check. A stalled dependency setup must not keep the current source waiting behind an obsolete verification run.
