package cli

// cli_config_show.go — `config list` and `config get` handlers. Both
// are read-only against the merged eng.Config and route through
// sanitizeConfigValue so API keys / tokens never appear unless the
// caller explicitly passes --raw.

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"gopkg.in/yaml.v3"
)

func runConfigList(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("config list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	raw := fs.Bool("raw", false, "show sensitive values")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfgMap, err := configToMap(eng.Config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config list error: %v\n", err)
		return 1
	}
	out := sanitizeConfigValue(cfgMap, "", !*raw)
	if jsonMode {
		mustPrintJSON(out)
		return 0
	}
	data, err := yaml.Marshal(out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config list error: %v\n", err)
		return 1
	}
	fmt.Print(string(data))
	return 0
}

func runConfigGet(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("config get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	raw := fs.Bool("raw", false, "show sensitive values")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) < 1 {
		fmt.Fprintln(os.Stderr, "usage: dfmc config get [--raw] <path>")
		return 2
	}
	keyPath := strings.TrimSpace(fs.Args()[0])
	cfgMap, err := configToMap(eng.Config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config get error: %v\n", err)
		return 1
	}
	value, ok := getConfigPath(cfgMap, keyPath)
	if !ok {
		fmt.Fprintf(os.Stderr, "config path not found: %s\n", keyPath)
		return 1
	}
	out := sanitizeConfigValue(value, keyPath, !*raw)
	if jsonMode {
		_ = printJSON(map[string]any{
			"path":  keyPath,
			"value": out,
		})
		return 0
	}
	switch v := out.(type) {
	case string:
		fmt.Println(v)
	default:
		data, err := yaml.Marshal(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config get error: %v\n", err)
			return 1
		}
		fmt.Print(string(data))
	}
	return 0
}
