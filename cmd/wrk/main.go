package main

import (
	"os"

	"github.com/BryanMcGahan/wrktree/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
