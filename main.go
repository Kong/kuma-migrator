package main

import (
	"github.com/Kong/kuma-migrator/cmd"
)

// version is injected at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
