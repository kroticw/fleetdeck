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

Then re-run the download block above with the new version, update the digests here, and check that the bundle still exports `Terminal`:

```bash
node --eval 'const m = { exports: {} };
  new Function("module", "exports", require("node:fs").readFileSync("web/vendor/xterm.js", "utf8"))(m, m.exports);
  console.log(Object.keys(m.exports))'
```

It prints `[ 'Terminal' ]`. A plain `require("./web/vendor/xterm.js")` is not a check: `web/package.json` declares `"type": "module"`, so node loads the file as an ES module, the UMD wrapper finds no `module` to export into, and `require` returns an empty object — `[]`, printed without an error.

## @xterm/addon-fit 0.11.0

Sizes the terminal to the element it is drawn in (`web/js/liveterminal.js`).

| File | Source |
| --- | --- |
| `addon-fit.js` | `https://cdn.jsdelivr.net/npm/@xterm/addon-fit@0.11.0/lib/addon-fit.js` |
| `LICENSE.addon-fit` | `https://cdn.jsdelivr.net/npm/@xterm/addon-fit@0.11.0/LICENSE` |

Licence: MIT (`LICENSE.addon-fit`), vendored alongside for the same reason as xterm's.

The version is pinned to xterm's, not chosen on its own. The addon reads xterm's private render service (`_core._renderService`) to measure a cell, so it is only known to work against the xterm build it was released with. `0.11.0` and xterm `6.0.0` were published from the same commit (`f447274f430fd22513f6adbf9862d19524471c04`, the `commit` field of both `package.json` files on the npm registry), within the same minute on 2025-12-22. Bump the two together or not at all.

`lib/addon-fit.js` is the UMD build: loaded with a plain `<script>` tag it assigns `FitAddon` onto the global object, and the class is `FitAddon.FitAddon`.

### Reproducing this exact set

```bash
version=0.11.0
curl --silent --show-error --location --fail \
  --output web/vendor/addon-fit.js      "https://cdn.jsdelivr.net/npm/@xterm/addon-fit@${version}/lib/addon-fit.js"
curl --silent --show-error --location --fail \
  --output web/vendor/LICENSE.addon-fit "https://cdn.jsdelivr.net/npm/@xterm/addon-fit@${version}/LICENSE"
shasum --algorithm 256 web/vendor/addon-fit.js web/vendor/LICENSE.addon-fit
```

Expected digests. They match the files inside the registry tarball (`addon-fit-0.11.0.tgz`, SHA-1 `ba4778b69fcc9044a060c2176bbe077657d7b37e` as the registry lists it), not only what the CDN served:

```text
ba3ea256ce0620a0992a197d6c9baea64823fc93d8da07a9e366ca9943c18527  web/vendor/addon-fit.js
e256f01188af527e4d06d21d06fbf785ae9c50d4b328bf03cbe0ba7f0aa4228f  web/vendor/LICENSE.addon-fit
```

Check the export the same way as xterm's; it prints `[ 'FitAddon' ]`:

```bash
node --eval 'const m = { exports: {} };
  new Function("module", "exports", require("node:fs").readFileSync("web/vendor/addon-fit.js", "utf8"))(m, m.exports);
  console.log(Object.keys(m.exports))'
```

## Inter 4.1 and JetBrains Mono 2.304

The panel's type: Inter for the interface, JetBrains Mono for card numbers, session ids, code and the terminals. The page loads nothing from the network (`default-src 'self'`), so the faces are served from the binary like everything else.

| File | Source |
| --- | --- |
| `fonts/InterVariable.woff2` | `web/InterVariable.woff2` in `https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip` |
| `fonts/LICENSE.inter` | `LICENSE.txt` in the same archive |
| `fonts/JetBrainsMono-{Regular,Medium,SemiBold,Bold}.woff2` | `fonts/webfonts/` in `https://github.com/JetBrains/JetBrainsMono/releases/download/v2.304/JetBrainsMono-2.304.zip` |
| `fonts/LICENSE.jetbrains-mono` | `OFL.txt` in the same archive |

Licence: SIL Open Font License 1.1, both. Inter is the variable face: the interface uses weights between the named ones (550, 650), which only a variable font draws as asked.

### Reproducing this exact set

```bash
tmp=$(mktemp -d)
curl --silent --show-error --location --fail --output "$tmp/inter.zip" https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip
curl --silent --show-error --location --fail --output "$tmp/jbm.zip" https://github.com/JetBrains/JetBrainsMono/releases/download/v2.304/JetBrainsMono-2.304.zip
unzip -q -j "$tmp/inter.zip" web/InterVariable.woff2 LICENSE.txt -d "$tmp/inter"
unzip -q -j "$tmp/jbm.zip" fonts/webfonts/JetBrainsMono-Regular.woff2 fonts/webfonts/JetBrainsMono-Medium.woff2 \
  fonts/webfonts/JetBrainsMono-SemiBold.woff2 fonts/webfonts/JetBrainsMono-Bold.woff2 OFL.txt -d "$tmp/jbm"
cp "$tmp/inter/InterVariable.woff2" "$tmp"/jbm/*.woff2 web/vendor/fonts/
cp "$tmp/inter/LICENSE.txt" web/vendor/fonts/LICENSE.inter
cp "$tmp/jbm/OFL.txt" web/vendor/fonts/LICENSE.jetbrains-mono
shasum --algorithm 256 web/vendor/fonts/*
```

Expected digests:

```text
693b77d4f32ee9b8bfc995589b5fad5e99adf2832738661f5402f9978429a8e3  web/vendor/fonts/InterVariable.woff2
c503cc5ec5f8b2c7666b7ecda1adf44bd45f2e6579b2eba0fc292150416588a2  web/vendor/fonts/JetBrainsMono-Bold.woff2
086c48dfbea9ddaff1320f7e09399b8e2924e88ce67453721255db3bdbb5a353  web/vendor/fonts/JetBrainsMono-Medium.woff2
a9cb1cd82332b23a47e3a1239d25d13c86d16c4220695e34b243effa999f45f2  web/vendor/fonts/JetBrainsMono-Regular.woff2
918edad542a1da608fd2ba8daebaff9ac802309103fe760eed465b8b4e47faf1  web/vendor/fonts/JetBrainsMono-SemiBold.woff2
262481e844521b326f5ecd053e59b98c8b2da78c8ee1bdbb6e8174305e54935a  web/vendor/fonts/LICENSE.inter
30f0c136e3c88e422d0791acd97238870f9054a9729bc34cf2ff0d4ed8cac4ad  web/vendor/fonts/LICENSE.jetbrains-mono
```

### Updating

`v4.1` and `v2.304` were the latest releases of `rsms/inter` and `JetBrains/JetBrainsMono` when this was vendored (2026-11-10). Check the current ones with `gh api repos/rsms/inter/releases/latest --jq .tag_name` and the same for `JetBrains/JetBrainsMono`, re-run the block above with the new versions and update the digests here.
