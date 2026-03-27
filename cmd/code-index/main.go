// Copyright (C) 2026 by Posit Software, PBC
package main

import (
	"log"
	"os"

	commands "github.com/posit-dev/code-index/cmd/code-index/cmd"
)

func main() {
	log.SetOutput(os.Stderr)
	err := commands.RootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
