package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/runner"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "列出全部评测层与检查项",
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Println("评测层与检查项：")
		for _, li := range runner.Catalog() {
			fmt.Printf("\n%s %s\n", li.ID, li.Name)
			for _, c := range li.Checks {
				fmt.Printf("  - %s\n", c)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(listCmd) // register this sub command
}
