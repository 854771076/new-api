package main

import (
	"errors"
	"os"

	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/commands"
	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/output"
)

func main() {
	err := commands.NewRootCmd().Execute()
	if err == nil {
		return
	}
	var cmdErr *commands.CommandError
	if errors.As(err, &cmdErr) {
		output.Error(os.Stderr, cmdErr.Message, cmdErr.Code)
		os.Exit(cmdErr.ExitCode)
	}
	output.Error(os.Stderr, err.Error(), "usage_error")
	os.Exit(commands.ExitUsage)
}
