package main

import (
	"github.com/bcollard/kuma-migrator/cmd"
)

// version is injected at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
