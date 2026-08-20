package cli

import (
	"context"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/runner"
)

var rootCmd = &cobra.Command{
	Use:           programName(),
	Short:         "大语言模型可用性评测工具",
	Version:       runner.Version,
	SilenceUsage:  true, // do not print usage during runtime
	SilenceErrors: true, // print errors only in `main`
}

// Execute is the only entrypoint
func Execute(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

func programName() (name string) {
	if exe, err := os.Executable(); err == nil {
		name = filepath.Base(exe)
	}
	if name == "" {
		name = filepath.Base(os.Args[0])
	}
	return name
}

type ExitError struct {
	Err  error
	Code int
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }
