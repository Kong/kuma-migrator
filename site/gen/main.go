// Command gen builds the static documentation site under docs/docs/ from site/.
//
// Usage (from site/gen):
//
//	go run . -root ../..
//
// See site/README.md for the source format.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", "../..", "repository root")
	lastmod := flag.String("lastmod", "", "sitemap <lastmod> date (YYYY-MM-DD); default: latest git commit date of site/")
	release := flag.String("release", "", `release tag for the header badge; default: git describe --tags --abbrev=0; "none" hides it`)
	flag.Parse()

	cfg := Config{Root: *root, LastMod: *lastmod, Release: *release}
	n, err := Build(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("site: wrote %d pages to docs/docs/\n", n)
}
