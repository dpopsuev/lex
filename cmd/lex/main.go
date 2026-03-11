package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dpopsuev/lex/internal/lexicon"
	lexmcp "github.com/dpopsuev/lex/internal/mcp"
	"github.com/dpopsuev/lex/internal/protocol"
	"github.com/dpopsuev/lex/internal/registry"
)

var Version = "dev"

func newService() *protocol.Service {
	return protocol.New(registry.New(envOr("LEX_ROOT", registry.DefaultRoot())), nil)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

var rootCmd = &cobra.Command{
	Use:   "lex",
	Short: "Lexicon resolver for AI agents",
	Long: `Lex reads .cursor/ rules and skills with zero configuration, supports remote
lexicon repositories, and cascading lexicon resolution -- via CLI or MCP server.`,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run:   func(cmd *cobra.Command, args []string) { fmt.Printf("lex %s\n", Version) },
}

var serveFlags struct {
	workspaces []string
	transport  string
	addr       string
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Lex MCP server (stdio or HTTP)",
	Long: `Start an MCP server that exposes lexicon tools.

  stdio (default): reads/writes JSON-RPC over stdin/stdout.
  http:            starts a Streamable HTTP server on --addr.

Tools: resolve_lexicon, inspect_lexicon, manage_lexicons, get_config, set_config.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		initLogger()
		roots := serveFlags.workspaces
		if len(roots) == 0 {
			cwd, _ := os.Getwd()
			roots = []string{cwd}
		}
		reg := registry.New(registry.DefaultRoot())
		srv := lexmcp.NewServer(reg, roots, Version)
		if serveFlags.transport == "http" {
			handler := sdkmcp.NewStreamableHTTPHandler(
				func(r *http.Request) *sdkmcp.Server { return srv },
				nil,
			)
			slog.Info("lex server starting", "transport", "http", "addr", serveFlags.addr)
			return http.ListenAndServe(serveFlags.addr, handler)
		}
		slog.Info("lex server starting", "transport", "stdio")
		return srv.Run(context.Background(), &sdkmcp.StdioTransport{})
	},
}

var addFlags struct {
	ref      string
	priority int
}

var addCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Register a remote lexicon repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		src, err := svc.AddLexicon(cmd.Context(), args[0], addFlags.ref, addFlags.priority)
		if err != nil {
			return err
		}
		return printJSON(src)
	},
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Re-fetch all registered lexicon repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		n, err := svc.SyncLexicons(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Printf("Synced %d lexicon(s)\n", n)
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered lexicon sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		sources, err := svc.ListSources(cmd.Context())
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			fmt.Println("No lexicon sources registered.")
			return nil
		}
		return printJSON(sources)
	},
}

var resolveFlags struct {
	path       string
	filter     string
	labels     []string
	format     string
	activeFile string
	context    []string
}

var resolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Resolve effective rules and skills from local workspace and remote lexicons",
	Long: `Merge local .cursor/ rules and skills with registered remote lexicons.
Higher priority wins on name conflicts. Use --filter and --labels to narrow results.

Smart routing: pass --file to get only rules whose globs match the active file,
and --context to match domain keywords against artifact labels.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		opts := lexicon.ResolveOpts{
			PathFilter: resolveFlags.filter,
			Labels:     resolveFlags.labels,
			ActiveFile: resolveFlags.activeFile,
			Context:    resolveFlags.context,
		}
		res, err := svc.Resolve(cmd.Context(), resolveFlags.path, opts)
		if err != nil {
			return err
		}
		if resolveFlags.format == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(res)
		}
		if len(res.Rules) > 0 {
			fmt.Printf("Rules (%d):\n", len(res.Rules))
			for _, r := range res.Rules {
				extra := formatRuleMeta(r.Labels, r.Globs, r.AlwaysApply)
				fmt.Printf("  %-30s  pri:%-3d  src:%s%s\n", r.Name, r.Priority, r.Source, extra)
			}
		}
		if len(res.Skills) > 0 {
			fmt.Printf("Skills (%d):\n", len(res.Skills))
			for _, s := range res.Skills {
				labels := ""
				if len(s.Labels) > 0 {
					labels = "  labels:[" + strings.Join(s.Labels, ",") + "]"
				}
				fmt.Printf("  %-30s  pri:%-3d  src:%s%s\n", s.Name, s.Priority, s.Source, labels)
			}
		}
		if len(res.Rules) == 0 && len(res.Skills) == 0 {
			fmt.Println("No rules or skills resolved.")
		}
		return nil
	},
}

var removeCmd = &cobra.Command{
	Use:   "remove <url>",
	Short: "Remove a registered lexicon source by URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		if err := svc.RemoveLexicon(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Removed lexicon: %s\n", args[0])
		return nil
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or set global config",
	Long:  `Show global config from ~/.lex/config.yaml, or use 'config set KEY VALUE' to update.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		cfg, err := svc.GetConfig(cmd.Context())
		if err != nil {
			return err
		}
		return printJSON(cfg)
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set [key] [value]",
	Short: "Set a config value",
	Long:  `Set a global config value. Keys: default_priority, cache_dir, enabled, labels (comma-separated).`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		if err := svc.SetConfig(cmd.Context(), args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("Set %s = %s\n", args[0], args[1])
		return nil
	},
}

var enableCmd = &cobra.Command{
	Use:   "enable <url>",
	Short: "Enable a disabled lexicon source",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		if err := svc.EnableSource(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Enabled: %s\n", args[0])
		return nil
	},
}

var disableCmd = &cobra.Command{
	Use:   "disable <url>",
	Short: "Disable a lexicon source without removing it",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		if err := svc.DisableSource(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Disabled: %s\n", args[0])
		return nil
	},
}

var initCmd = &cobra.Command{
	Use:   "init [path]",
	Short: "Scaffold a new lexicon repository",
	Long: `Create a starter lexicon.yaml and standard directory layout (rules/, skills/, templates/).
If no path is given, uses the current directory.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}
		return scaffoldLexicon(dir)
	},
}

func scaffoldLexicon(root string) error {
	for _, d := range []string{"rules", "skills", "templates"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return err
		}
	}

	yamlPath := filepath.Join(root, "lexicon.yaml")
	if _, err := os.Stat(yamlPath); err == nil {
		fmt.Printf("lexicon.yaml already exists in %s, skipping\n", root)
	} else {
		content := `name: my-lexicon
description: Organization-wide rules, skills, and templates
version: "0.1.0"

defaults:
  priority: 25

routing: []
`
		if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
			return err
		}
	}

	fmt.Printf("Lexicon scaffolded in %s\n", root)
	fmt.Println("  rules/       - place rule .md files here")
	fmt.Println("  skills/      - place skill directories here (each with SKILL.md)")
	fmt.Println("  templates/   - place template .md files here")
	fmt.Println("  lexicon.yaml - edit to configure routing and metadata")
	return nil
}

var bridgeFlags struct {
	global bool
}

var bridgeCmd = &cobra.Command{
	Use:   "cursor-bridge-rule [path]",
	Short: "Install the lex-bridge.mdc Cursor rule",
	Long: `Write the lex-bridge.mdc rule that triggers resolve_lexicon at session start.

  --global: install to ~/.cursor/rules/ (applies to all workspaces)
  [path]:   install to <path>/.cursor/rules/ (defaults to cwd)`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService()
		path := ""
		if len(args) > 0 {
			path = args[0]
		}
		result, err := svc.InstallBridgeRule(cmd.Context(), path, bridgeFlags.global)
		if err != nil {
			return err
		}
		if result.Created {
			fmt.Printf("Created %s\n", result.Path)
		} else {
			fmt.Printf("Already exists: %s\n", result.Path)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd, serveCmd, addCmd, syncCmd, listCmd, removeCmd, resolveCmd, initCmd, bridgeCmd, configCmd, enableCmd, disableCmd)
	configCmd.AddCommand(configSetCmd)
	bridgeCmd.Flags().BoolVar(&bridgeFlags.global, "global", false, "Install to ~/.cursor/rules/ (all workspaces)")

	serveCmd.Flags().StringArrayVar(&serveFlags.workspaces, "workspace", nil, "Workspace root paths (repeatable; defaults to cwd)")
	serveCmd.Flags().StringVar(&serveFlags.transport, "transport", envOr("LEX_TRANSPORT", "stdio"), "Transport type: stdio, http ($LEX_TRANSPORT)")
	serveCmd.Flags().StringVar(&serveFlags.addr, "addr", envOr("LEX_ADDR", ":8082"), "Listen address for http transport ($LEX_ADDR)")
	addCmd.Flags().StringVar(&addFlags.ref, "ref", "", "Branch or tag to clone")
	addCmd.Flags().IntVar(&addFlags.priority, "priority", 25, "Priority (higher wins on conflict)")
	resolveCmd.Flags().StringVar(&resolveFlags.path, "path", "", "Workspace root path (defaults to cwd)")
	resolveCmd.Flags().StringVar(&resolveFlags.filter, "filter", "", "Path/name substring filter")
	resolveCmd.Flags().StringSliceVar(&resolveFlags.labels, "labels", nil, "Label filter (comma-separated)")
	resolveCmd.Flags().StringVar(&resolveFlags.format, "format", "text", "Output format (text, json)")
	resolveCmd.Flags().StringVar(&resolveFlags.activeFile, "file", "", "Active file path for smart glob routing")
	resolveCmd.Flags().StringSliceVar(&resolveFlags.context, "context", nil, "Context keywords for smart label routing (comma-separated)")
}

func formatRuleMeta(labels, globs []string, alwaysApply bool) string {
	var parts []string
	if len(labels) > 0 {
		parts = append(parts, "labels:["+strings.Join(labels, ",")+"]")
	}
	if alwaysApply {
		parts = append(parts, "always")
	}
	if len(globs) > 0 {
		parts = append(parts, "globs:["+strings.Join(globs, ",")+"]")
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, " ")
}

func printJSON(v any) error {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
	return nil
}

func initLogger() {
	level := slog.LevelInfo
	if v := os.Getenv("LEX_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
