# Vendored test-only assets

Nothing here is embedded in the binary or served by the panel: `web/embed.go` embeds `web/vendor`, not this directory.

## @xterm/headless 6.0.0

xterm.js's terminal core without a renderer, so a test can replay a stream through the same parser and buffer the page draws with (`web/tests/terminalwidth.test.js`).

| File | Source |
| --- | --- |
| `xterm-headless.cjs` | `https://cdn.jsdelivr.net/npm/@xterm/headless@6.0.0/lib-headless/xterm-headless.js` |
| `LICENSE.xterm` | a copy of `web/vendor/LICENSE.xterm` |

Licence: MIT. The npm package declares it (`"license": "MIT"`) but ships no licence file of its own. It is published from the xterm.js repository, whose licence is `web/vendor/LICENSE.xterm`, so a copy of that file sits beside it.

The version is pinned to `web/vendor/xterm.js`, not chosen on its own. `@xterm/headless` 6.0.0 and `@xterm/xterm` 6.0.0 were published from the same commit, `f447274f430fd22513f6adbf9862d19524471c04` (the `gitHead` field of both on the npm registry). A test on another core would prove nothing about the one the page loads. Bump the two together.

It is renamed to `.cjs` because `web/package.json` declares `"type": "module"`: under a `.js` name node would load the CommonJS bundle as an ES module and find no exports. A test loads it with `createRequire(import.meta.url)`.

The browser bundle `web/vendor/xterm.js` cannot stand in for it: loading it in node fails before a terminal exists, because it reads `document` as it loads.

### Reproducing this exact set

```bash
version=6.0.0
curl --silent --show-error --location --fail \
  --output web/tests/vendor/xterm-headless.cjs "https://cdn.jsdelivr.net/npm/@xterm/headless@${version}/lib-headless/xterm-headless.js"
cp web/vendor/LICENSE.xterm web/tests/vendor/LICENSE.xterm
shasum --algorithm 256 web/tests/vendor/xterm-headless.cjs web/tests/vendor/LICENSE.xterm
```

Expected digests. The first matches the file inside the registry tarball, not only what the CDN served:

```text
17a90b650cf6b77cce2b98c4063884d43545e4ce177a54b76ccfc906f1aacaed  web/tests/vendor/xterm-headless.cjs
b569f629d00f2626a8100df2a1798210535621e42164dfd426a6fe5aac7b0ccd  web/tests/vendor/LICENSE.xterm
```
