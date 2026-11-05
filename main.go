package main

import (
	"os"

	"github.com/lvdousha26/minigit/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
