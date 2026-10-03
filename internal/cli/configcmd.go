package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	settings "github.com/androiddrew/kokoro-run/internal/config"
)

func configCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print the effective settings and where each one came from",
		Long: "Print the settings every command would use, as YAML that can be saved as a config file.\n" +
			"Each value is resolved from the defaults, then --config (or " + settings.FileEnv + "), then\n" +
			"KOKORO_RUN_* environment variables, then flags. A setting's variable is KOKORO_RUN_ and its\n" +
			"YAML path in upper case, with _ for the dots: server.listen is KOKORO_RUN_SERVER_LISTEN.",
		Args: cobra.NoArgs,
	}
	// Every setting's flag, so `config` shows what a command given them would use.
	frontendFlags(cmd)
	synthesisFlags(cmd)
	languageFlag(cmd)
	textPrepFlags(cmd)
	serverFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		l, err := loadSettings(cmd)
		if err != nil {
			return err
		}
		out, err := annotated(l)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), out)
		return err
	}
	return cmd
}

// annotated renders l as YAML with each value's source as a line comment.
func annotated(l *settings.Loaded) (string, error) {
	var doc yaml.Node
	if err := doc.Encode(l.Config); err != nil {
		return "", err
	}
	comment(&doc, "", l.Sources)
	file := "none"
	if l.File != "" {
		file = l.File
	}
	key := "not set (auth off)"
	if l.Server.APIKey != "" {
		key = "set, from KOKORO_RUN_API_KEY"
		if l.Sources["server.apikey"] != settings.FromEnv {
			key = "set, from server.api_key_file"
		}
	}
	doc.HeadComment = "Effective kokoro-run settings. Config file: " + file + "\nAPI key: " + key
	b, err := yaml.Marshal(&doc)
	return string(b), err
}

// comment marks each leaf of a mapping node with where its value came from.
func comment(n *yaml.Node, prefix string, sources map[string]settings.Source) {
	if n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		path := prefix + key.Value
		if src, ok := sources[path]; ok {
			note := string(src)
			if src == settings.FromEnv {
				note += " " + settings.EnvName(path)
			}
			key.LineComment = note
			continue
		}
		if value.Kind == yaml.MappingNode && !strings.HasSuffix(path, "voices") && !strings.HasSuffix(path, "model_aliases") {
			comment(value, path+".", sources)
		}
	}
}
