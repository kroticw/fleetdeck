# Vendored third-party assets

## xterm.js 6.0.0

The terminal emulator the session panel's screen tab draws into.

| File             | Source                                                              |
| ---------------- | ------------------------------------------------------------------- |
| `xterm.js`       | `https://cdn.jsdelivr.net/npm/@xterm/xterm@6.0.0/lib/xterm.js`       |
| `xterm.css`      | `https://cdn.jsdelivr.net/npm/@xterm/xterm@6.0.0/css/xterm.css`      |
| `LICENSE.xterm`  | `https://cdn.jsdelivr.net/npm/@xterm/xterm@6.0.0/LICENSE`            |

Licence: MIT (`LICENSE.xterm`). It is vendored alongside the code it covers
because a third-party file in a public repository without its licence is not
distributable.

`lib/xterm.js` is the UMD build, not the ES module (`lib/xterm.mjs`): loaded with a
plain `<script>` tag it assigns its single export, `Terminal`, onto the global
object, which is what `web/js/session.js` reads.

### Reproducing this exact set

```bash
version=6.0.0
curl --silent --show-error --location --fail \
  --output web/vendor/xterm.js      "https://cdn.jsdelivr.net/npm/@xterm/xterm@${version}/lib/xterm.js"
curl --silent --show-error --location --fail \
  --output web/vendor/xterm.css     "https://cdn.jsdelivr.net/npm/@xterm/xterm@${version}/css/xterm.css"
curl --silent --show-error --location --fail \
  --output web/vendor/LICENSE.xterm "https://cdn.jsdelivr.net/npm/@xterm/xterm@${version}/LICENSE"
shasum --algorithm 256 web/vendor/xterm.js web/vendor/xterm.css web/vendor/LICENSE.xterm
```

Expected digests — a download that is truncated but still writes a file is the
failure this table exists to catch:

```text
14903579ff54664cd72f8e8699e6961a6272c21863ec1c3b118cdc8af5d4a972  web/vendor/xterm.js
854a7c0fb70e8b1a083c16797ab827299fb18744f5ad34f227b48337e33293c6  web/vendor/xterm.css
b569f629d00f2626a8100df2a1798210535621e42164dfd426a6fe5aac7b0ccd  web/vendor/LICENSE.xterm
```

### Updating

`6.0.0` was the `dist-tags.latest` of `@xterm/xterm` on the npm registry when this
was vendored (published 2025-12-22), not a version copied from an older plan.
Check the current one before bumping:

```bash
curl --silent https://registry.npmjs.org/@xterm/xterm | \
  python3 -c 'import json,sys; print(json.load(sys.stdin)["dist-tags"]["latest"])'
```

Then re-run the download block above with the new version, update the digests
here, and check that the bundle still exports `Terminal`:

```bash
node --input-type=commonjs --eval \
  'console.log(Object.keys(require("./web/vendor/xterm.js")))'
```
