package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The panel scans every fleet's board on every collect cycle, so what a scan
// costs is paid a couple of seconds apart for as long as the panel runs. These
// two benchmarks are the pair Cache exists for — run them before changing how
// a board is read.
//
//	go test ./internal/board -run XXX -bench BenchmarkScan -benchmem
//
// Reading a card is the cheap part: most of both numbers is the regular
// expressions ParseCard runs over the body.
func BenchmarkScan(b *testing.B) {
	for _, cards := range []int{8, 40, 160} {
		dir := boardOf(b, cards)
		b.Run(fmt.Sprintf("cards_%d", cards), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Scan(dir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkScanCached is the same board through Cache with nothing changed
// since the last pass: a directory listing and one stat per card, no reads and
// no parsing.
func BenchmarkScanCached(b *testing.B) {
	for _, cards := range []int{8, 40, 160} {
		dir := boardOf(b, cards)
		c := NewCache()
		if _, err := c.Scan(dir); err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("cards_%d", cards), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.Scan(dir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// boardOf builds a board of n cards of a size a real one reaches: a working
// card grows a log, and it is the body the parse walks.
func boardOf(tb testing.TB, n int) string {
	tb.Helper()
	dir := tb.TempDir()
	cards := CardsDir(dir)
	if err := os.MkdirAll(cards, 0o700); err != nil {
		tb.Fatal(err)
	}
	log := strings.Repeat("- 2026-09-23 12:00 — очередная строка лога, ссылка [[другая-карточка]] и `код`\n", 80)
	for i := range n {
		body := fmt.Sprintf(`---
id: T-%03d
zone: planned
stage: active
progress: 20
session: abc%05d
repo: work/thing
created: 2026-09-23
---

# Карточка %d

Тело с ссылкой [[другая-карточка]], фрагментом кода и логом.

`+"```"+`
фрагмент кода, который stripFencedCode вырезает
`+"```"+`

## Лог

%s`, i, i, i, log)
		if err := os.WriteFile(filepath.Join(cards, fmt.Sprintf("T-%03d-card.md", i)), []byte(body), 0o600); err != nil {
			tb.Fatal(err)
		}
	}
	return dir
}
