package cli

import (
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/tyhal/klar/pkg/klar"
)

// Command returns the root command for the klar CLI
func Command() *cobra.Command {
	return &cobra.Command{
		Use: "klar <level>",
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			levels := []log.Level{log.DebugLevel, log.InfoLevel, log.WarnLevel, log.ErrorLevel, log.FatalLevel}
			completions := make([]string, len(levels))
			for i, level := range levels {
				completions[i] = level.String()
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
		Args:  cobra.MaximumNArgs(1),
		Short: "structured json → clear output",
		RunE: func(cmd *cobra.Command, args []string) error {
			parseLevel := log.DebugLevel
			var err error
			if len(args) == 1 {
				parseLevel, err = log.ParseLevel(args[0])
				if err != nil {
					return err
				}
			}
			return klar.New(cmd.OutOrStdout(), parseLevel).Decode(cmd.Context(), cmd.InOrStdin())
		},
	}
}
