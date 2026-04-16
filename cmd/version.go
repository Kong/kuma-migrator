package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the kuma-migrator version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("kuma-migrator", rootCmd.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
