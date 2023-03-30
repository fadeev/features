package main

import (
	"fmt"
	"os"

	"github.com/fadeev/features/cmd/module"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "myapp",
	Short: "A simple CLI application with multiple commands",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize()

	rootCmd.AddCommand(module.Cmd())
}
