package cmd

import (
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	kubeContext string
	colorMode   string
)

var rootCmd = &cobra.Command{
	Use:   "kt",
	Short: "Kubernetes toolkit",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&kubeContext, "context", "", "kubectl context to use (default: current context)")
	rootCmd.PersistentFlags().StringVar(&colorMode, "color", "always", "color output: always, auto, none")
	cobra.OnInitialize(applyColorMode)
}

func applyColorMode() {
	switch colorMode {
	case "always":
		color.NoColor = false
	case "none":
		color.NoColor = true
	case "auto":
		// let fatih/color decide based on TTY detection
	default:
		fmt.Fprintf(os.Stderr, "invalid --color value %q: must be always, auto, or none\n", colorMode)
		os.Exit(1)
	}
}

// Execute is the entry point called from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
