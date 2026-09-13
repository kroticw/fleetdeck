// Command publish-release publishes the GitHub release for a tag with the given
// artifacts, and finishes whatever an earlier, interrupted run of it left behind. It
// is the Publish step of .github/workflows/release.yaml; see
// internal/releasepublish for why that is not `gh release create`.
//
//	GITHUB_TOKEN=... go run ./scripts/publish-release --tag v1.2.3 dist/*.tar.gz dist/*.zip dist/*.dmg
//
// The repository and API root default to GITHUB_REPOSITORY and GITHUB_API_URL, which
// GitHub Actions sets for every step.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kroticw/fleetdeck/internal/releasepublish"
)

func main() {
	api := os.Getenv("GITHUB_API_URL")
	if api == "" {
		api = "https://api.github.com"
	}
	repo := flag.String("repo", os.Getenv("GITHUB_REPOSITORY"), "repository to publish in, owner/name")
	flag.StringVar(&api, "api", api, "GitHub REST API root")
	tag := flag.String("tag", "", "tag the release is for")
	flag.Parse()

	// A cancelled run gets SIGINT: stop between requests rather than be killed in
	// the middle of one. Either way a rerun finishes the release.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	p := &releasepublish.Publisher{
		API:    api,
		Repo:   *repo,
		Token:  os.Getenv("GITHUB_TOKEN"),
		Client: &http.Client{},
		Log:    os.Stdout,
		Waits:  releasepublish.DefaultWaits,
		Settle: releasepublish.DefaultSettle,
	}
	if err := p.Publish(ctx, *tag, flag.Args()); err != nil {
		fmt.Printf("::error::%s\n", strings.ReplaceAll(err.Error(), "\n", " "))
		os.Exit(1)
	}
}
