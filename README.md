# SPK MM Client website

Static Astro product site, intended URL: https://sipaha.github.io/spk-mm-client/. Source lives only on the independent orphan `pages` branch; never merge it into application `main`.

Eight website locales (Russian root, English, Simplified Chinese, Spanish, German, French, Brazilian Portuguese, Japanese), light/dark themes and real application screenshots. The application itself currently has Russian/English UI. Screenshots use a local fake Mattermost with fictional conversations. No user chats, accounts or telemetry.

The current project has no packaged GitHub release. Downloads display that fact and link to source-build instructions. Future stable release files are accepted only from this repository's GitHub release URLs. Browser-mode developer binaries and foreign URLs are excluded. GitHub API failure and JavaScript-disabled pages retain the source-build/release links.

The visual shell and language-routing helpers follow SPK Ocular. Inter fonts retain their bundled upstream license. Existing two-message MM Client icon is unchanged. Design: variance4, motion2, density4; restrained native CSS/Astro, no decorative animations or synthetic screenshots.

Pages is currently disabled. Enable repository Settings → Pages → GitHub Actions, then publish the independent `pages` branch. The pinned workflow verifies before uploading/deploying and never creates application releases.

Run `pnpm install --frozen-lockfile`, `pnpm test`, `pnpm build`, `pnpm verify`. Verification covers all eight locales, both themes, mobile/desktop, keyboard gallery, language precedence, unavailable/no-release responses and reduced motion. Run Lighthouse and inspect captures before deployment. Scratch belongs in the containing Solution `.tmp/mm-site`.

Use `TMPDIR=<Solution>/.tmp/mm-site/tmp` and `SITE_SCRATCH=<Solution>/.tmp/mm-site/site-verify`. Start `pnpm preview --host 127.0.0.1 --port 53982` before `pnpm verify`. `GENERATE_SOCIAL_CARDS=1 pnpm verify` additionally captures the real site for its social cards. `node scripts/lighthouse.mjs` checks mobile/desktop performance, accessibility, best practices, SEO and layout stability.
