package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/binkovsky/forktrust/internal/cli"
)

// version is overridden at build time via -ldflags "-X main.version=$(git describe ...)".
var version = "dev"

func main() {
	cli.SetVersion(version)
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		var coded *cli.CodedError
		if errors.As(err, &coded) {
			os.Exit(coded.Code)
		}
		os.Exit(cli.ExitGenericError)
	}
}
