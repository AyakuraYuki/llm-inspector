// 大语言模型可用性评测工具的 CLI 入口。
//
// 用法:
//
//	go run ./main.go run --config=config.yml
//	go run ./main.go list
//	go run ./main.go version
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := cli.Execute(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		if ee, ok := errors.AsType[*cli.ExitError](err); ok {
			os.Exit(ee.Code)
		}
		os.Exit(1)
	}
}
