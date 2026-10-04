package cli

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/androiddrew/kokoro-run/internal/assets"
)

func pullCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Download the Kokoro model and voices, checking their SHA-256",
		Long: "Download kokoro/kokoro-v1.0.onnx and kokoro/voices-v1.0.bin (about 354 MB) into the assets directory.\n" +
			"Files already present and verified are kept. Downloads are checked before they replace anything.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Assets directory (default: the assets setting, KOKORO_RUN_ASSETS, or ./assets)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if dir == "" {
			l, err := loadSettings(cmd)
			if err != nil {
				return err
			}
			dir = l.Assets
		}
		return assets.Pull(cmd.Context(), http.DefaultClient, dir, cmd.ErrOrStderr())
	}
	return cmd
}
