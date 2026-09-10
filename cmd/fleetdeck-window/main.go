//go:build darwin

// Command fleetdeck-window is a native window around the panel that already
// runs as a separate, independently-managed process. It never starts, stops
// or owns that process — it only opens a window pointed at the URL the panel
// already answers on, exactly the way a browser tab does today. Closing the
// window must not stop the panel; the panel keeps working, sessions keep
// working, the operator can reopen the window later and find everything as
// they left it.
//
// Why webview_go, and what the fallback is: this needed a native window
// without a second build toolchain in a project that currently has only Go.
// webview_go wraps WKWebView on macOS via cgo and nothing else — no bundled
// browser runtime, no separate frontend dependency chain. A probe binary
// built from this exact dependency comes to 2.9MB, linked only against
// system frameworks (WebKit, CoreFoundation, libSystem, libresolv, libc++,
// libobjc — see TestWindowBinaryLinksAgainstWebKit), against roughly 3-10MB
// for the same window built with Tauri and ~89MB for Electron's minimal
// macOS bundle. The one real cost: webview_go carries no tagged releases and
// its last commit predates this file by more than a year, so the dependency
// below is pinned to an exact commit, not a branch or a floating version.
// If it stops working, the fallback is Tauri (the second-cheapest path, and
// the one with an actively maintained toolchain) — and because this wrapper
// is deliberately thin and owns nothing but the window itself, that rewrite
// touches this one command, never the panel it points at.
//
// The //go:build darwin above is load-bearing, not decoration: this project's
// own CI runs a ubuntu-latest leg too, and webview_go's cgo directives ask
// for gtk+-3.0 and webkit2gtk-4.0 via pkg-config on Linux -- packages that
// CI runner does not have installed, and this project has no reason to
// install, since the panel itself is darwin-only (spec section 1). Without
// the tag, `go build ./...`/`govulncheck ./...` on that runner tried to
// compile this package anyway and failed on the missing pkg-config entries;
// with it, the package has no buildable files at all outside darwin, and the
// standard toolchain already treats that as "nothing to build here", not an
// error -- confirmed with GOOS=linux locally, not assumed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	webview "github.com/webview/webview_go"
)

// defaultURL matches cmd/fleetdeck-status's own default (see its FLEETDECK_ENDPOINT
// handling) rather than reading internal/config: the window has exactly one thing to
// know -- where the panel answers -- and pulling in the whole config package to learn
// a port would be a second way to fail the config file can never affect the panel
// itself failing to load.
const defaultURL = "http://127.0.0.1:7777/"

// reachabilityTimeout bounds the one check this command makes before deciding
// whether to open the panel directly or show the waiting page. A window that
// hangs on a slow or absent network answer before it has even appeared would
// read as the whole application being frozen, not as "the panel isn't up yet".
const reachabilityTimeout = 800 * time.Millisecond

// reachable reports whether the panel answers at url right now. Any answer at
// all -- even an HTTP error status -- means a server is listening; only a
// failure to connect at all (refused, timed out, no route) means it is not.
func reachable(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// waitingPage is shown instead of a browser's own "connection refused" when
// the panel is not answering yet. It polls on its own, in the page itself,
// and replaces itself the moment the panel responds -- there is deliberately
// no Go-side retry loop for this: once the window has ever loaded the real
// panel, the panel's own frontend (store.js) already reconnects with backoff
// and shows its own offline state if the panel later goes away, the same way
// it already does for a browser tab. This page only has to cover the one gap
// that code does not: nothing has loaded yet for it to reconnect from.
//
// url is interpolated twice: once as page text (html.EscapeString covers
// that) and once as a JS string literal inside <script> (jsStringLiteral
// covers that -- Go's %q quotes for Go syntax, not for breaking out of an
// HTML <script> block, and does nothing about a literal "</script>"
// substring inside the value ending the tag early regardless of any string
// quoting). Both forms are also what a person set on the command line
// themselves, not input the panel received from anywhere untrusted.
func waitingPage(url string) string {
	escaped := html.EscapeString(url)
	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>fleetdeck</title>
<style>
  html, body { height: 100%%; margin: 0; }
  body {
    display: flex; align-items: center; justify-content: center;
    font-family: system-ui, -apple-system, sans-serif;
    background: #14161a; color: #e7e9ec;
  }
  main { max-width: 28rem; text-align: center; padding: 2rem; }
  h1 { font-size: 1.1rem; font-weight: 600; margin: 0 0 0.75rem; }
  p { color: #9aa1ac; font-size: 0.9rem; line-height: 1.5; margin: 0; }
  code { background: #21252c; padding: 0.1em 0.4em; border-radius: 4px; }
</style>
</head>
<body>
<main>
  <h1>Панель не запущена</h1>
  <p>Окно ждёт ответ на <code>%s</code> и пока его не получает. Запустите панель — тем же способом, каким вы её обычно поднимаете (бинарём <code>fleetdeck</code> или своим скриптом) — и это окно само откроет её, как только она ответит. Ничего здесь нажимать не нужно.</p>
</main>
<script>
  const url = %s;
  const check = () => {
    fetch(url, { method: "GET", cache: "no-store" })
      .then(() => { window.location.href = url; })
      .catch(() => { setTimeout(check, 1500); });
  };
  setTimeout(check, 1500);
</script>
</body>
</html>`, escaped, jsStringLiteral(url))
}

// jsStringLiteral turns a Go string into a JSON string literal (json.Marshal
// on a string always produces one, and every JSON string literal is also a
// valid JS one) and then neutralizes "</", the one sequence a JS string
// quoting rule has no reason to escape but that ends the surrounding
// <script> tag the moment an HTML parser sees it -- before any JS ever runs.
func jsStringLiteral(s string) string {
	quoted, err := json.Marshal(s)
	if err != nil {
		// s is a plain string; json.Marshal on a string cannot fail.
		panic(err)
	}
	return strings.ReplaceAll(string(quoted), "</", "<\\/")
}

func main() {
	url := flag.String("url", defaultURL, "URL the panel answers on")
	flag.Parse()

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("fleetdeck")
	w.SetSize(1440, 900, webview.HintNone)

	ctx, cancel := context.WithTimeout(context.Background(), reachabilityTimeout)
	up := reachable(ctx, *url)
	cancel()

	if up {
		w.Navigate(*url)
	} else {
		log.Printf("fleetdeck-window: %s is not answering yet, showing the waiting page", *url)
		w.SetHtml(waitingPage(*url))
	}

	w.Run()
}
