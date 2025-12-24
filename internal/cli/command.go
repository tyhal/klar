package cli

import (
	"github.com/spf13/cobra"
	"github.com/tyhal/klar/pkg/klar"
)

// Command returns the root command for the klar CLI
func Command() *cobra.Command {
	return &cobra.Command{
		Use:   "klar",
		Short: "structured json → clear output",
		RunE:  stream,
	}
}

func stream(cmd *cobra.Command, _ []string) error {
	return klar.New(cmd.OutOrStdout()).Decode(cmd.Context(), cmd.InOrStdin())
}
