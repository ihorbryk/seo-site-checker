# Tailwind + daisyUI built by the standalone CLI, committed and embedded, no Node

The web UI is styled with Tailwind CSS v4 and daisyUI. The CSS is built by the standalone `tailwindcss` binary, pinned as a mise tool, with daisyUI's standalone `daisyui.mjs` / `daisyui-theme.mjs` plugin files committed next to the source stylesheet. The output, `internal/web/static/app.css`, is committed and `go:embed`ded like `htmx.min.js`, and CI rebuilds it and fails on drift, the same way it checks sqlc output. This keeps the repo Go-only, keeps the web binary self-contained with no runtime network dependency, and treats generated CSS like generated SQL code.

## Considered Options

- **CDN at runtime (daisyUI CSS from jsDelivr + `@tailwindcss/browser`)**: no build step, but the UI needs the network, CSS is compiled in the browser on every page load, and a third party sees every page view.
- **npm (`package.json` with `tailwindcss` and `daisyui`)**: the conventional install, but it brings Node, a lockfile and `node_modules` into a repo that has none, just to produce one CSS file.

## Consequences

- Any template edit that introduces a new utility or component class needs `mise run css` (or the `css` watch daemon) before committing; `lint:css` catches it otherwise.
- Upgrading daisyUI means re-downloading the plugin files with `mise run css:daisyui <version>`; upgrading Tailwind means bumping the mise pin.
- CSS classes in templates are both styling and, for a few semantic names (`.fetched`, `#body-diff`, …), systemtest hooks. The hooks are kept separate from the daisyUI classes.
