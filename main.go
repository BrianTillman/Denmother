package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/BrianTillman/Denmother/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		exitCode := 1
		var exitCoder interface{ ExitCode() int }
		if errors.As(err, &exitCoder) {
			exitCode = exitCoder.ExitCode()
		}
		if err.Error() != "" {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(exitCode)
	}
}
