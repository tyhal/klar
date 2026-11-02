// Package cli wires the klar library into a cobra command.
package cli

import (
	"charm.land/log/v2"
	"github.com/spf13/cobra"
	"github.com/tyhal/klar/pkg/klar"
)

var levels = []log.Level{log.DebugLevel, log.InfoLevel, log.WarnLevel, log.ErrorLevel, log.FatalLevel}

// Command returns the root command for the klar CLI
func Command() *cobra.Command {
	var levelFlag, timeFlag string

	cmd := &cobra.Command{
		Use:   "klar",
		Short: "structured json → clear output",
		Example: `  kubectl logs my-pod | klar
  my-app | klar --level warn --time absolute`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			level, err := log.ParseLevel(levelFlag)
			if err != nil {
				return err
			}
			timeMode, err := klar.ParseTimeMode(timeFlag)
			if err != nil {
				return err
			}
			return klar.New(cmd.OutOrStdout(), klar.Options{Level: level, Time: timeMode}).
				Decode(cmd.Context(), cmd.InOrStdin())
		},
	}

	cmd.Flags().StringVarP(&levelFlag, "level", "l", log.DebugLevel.String(), "minimum log level to show (debug|info|warn|error|fatal)")
	cmd.Flags().StringVarP(&timeFlag, "time", "t", string(klar.TimeRelative), "timestamp style (relative|absolute|none)")

	_ = cmd.RegisterFlagCompletionFunc("level", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		out := make([]string, len(levels))
		for i, l := range levels {
			out[i] = l.String()
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("time", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		out := make([]string, len(klar.TimeModes))
		for i, m := range klar.TimeModes {
			out[i] = string(m)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
