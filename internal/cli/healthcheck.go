package cli

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// healthcheckCommand is the container HEALTHCHECK: images have no curl. It
// asks the server at the configured listen address whether it is ready.
func healthcheckCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "_healthcheck", Hidden: true, Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		l, err := loadSettings(cmd)
		if err != nil {
			return err
		}
		host, port, err := net.SplitHostPort(l.Server.Listen)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
			host = "127.0.0.1"
		}
		client := http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/readyz")
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("/readyz: %s", resp.Status)
		}
		return nil
	}
	return cmd
}
