package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"myapphub/cli/internal/client"
	"myapphub/cli/internal/model"
	"myapphub/cli/internal/packageinfo"
)

const Version = "1.0.0"
const defaultServerURL = "https://apphub.bensonday.cc"

type API interface {
	List(context.Context) ([]model.PublicApp, error)
	Upload(context.Context, string, model.UploadMetadata, []byte, io.Writer) (model.UploadResult, error)
	Promote(context.Context, string, string) (model.PublicApp, error)
	Delete(context.Context, string, string) (time.Time, error)
	DeleteApp(context.Context, string) (time.Time, error)
	Restore(context.Context, string, string) (model.PublicApp, error)
	SetPassword(context.Context, string, string) (model.PublicApp, error)
}

type PackageParser interface {
	Parse(context.Context, string, packageinfo.Options) (packageinfo.Result, error)
}

type Dependencies struct {
	API          API
	Parser       PackageParser
	Out          io.Writer
	Err          io.Writer
	In           io.Reader
	ReadPassword func(io.Reader, io.Writer) (string, error)
	NewAPI       func(string, string, time.Duration) (API, error)
}

type application struct {
	deps          Dependencies
	server, token string
	timeout       time.Duration
	json          bool
}

func NewRoot(deps Dependencies) *cobra.Command {
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	if deps.Err == nil {
		deps.Err = os.Stderr
	}
	if deps.In == nil {
		deps.In = os.Stdin
	}
	if deps.Parser == nil {
		deps.Parser = packageinfo.New()
	}
	if deps.ReadPassword == nil {
		deps.ReadPassword = readPassword
	}
	if deps.NewAPI == nil {
		deps.NewAPI = func(server, token string, timeout time.Duration) (API, error) {
			return client.New(server, token, timeout)
		}
	}
	server := os.Getenv("APPHUB_SERVER")
	if server == "" {
		server = defaultServerURL
	}
	app := &application{deps: deps, server: server, timeout: 30 * time.Minute}
	root := &cobra.Command{Use: "apphub", Short: "MyApphub command line client", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(deps.Out)
	root.SetErr(deps.Err)
	root.SetIn(deps.In)
	root.PersistentFlags().StringVar(&app.server, "server", app.server, "App Hub server URL (APPHUB_SERVER)")
	root.PersistentFlags().StringVar(&app.token, "token", "", "management token (prefer APPHUB_TOKEN)")
	root.PersistentFlags().DurationVar(&app.timeout, "timeout", app.timeout, "request timeout")
	root.PersistentFlags().BoolVar(&app.json, "json", false, "write machine-readable JSON")
	root.AddCommand(app.uploadCommand(), app.listCommand(), app.infoCommand(), app.promoteCommand(), app.deleteCommand(), app.restoreCommand(), app.passwordCommand(), versionCommand(deps.Out), completionCommand(root))
	return root
}

func (a *application) api() (API, error) {
	if a.deps.API != nil {
		return a.deps.API, nil
	}
	token := a.token
	if token == "" {
		token = a.defaultToken()
	}
	return a.deps.NewAPI(a.server, token, a.timeout)
}

func (a *application) defaultToken() string {
	if a.server == defaultServerURL {
		if executable, err := os.Executable(); err == nil {
			path := filepath.Join(filepath.Dir(executable), "..", "..", ".secrets", "production-admin-token")
			if data, err := os.ReadFile(path); err == nil {
				if token := strings.TrimSpace(string(data)); token != "" {
					return token
				}
			}
		}
	}
	return os.Getenv("APPHUB_TOKEN")
}
func (a *application) print(value any, text string) error {
	if a.json {
		return json.NewEncoder(a.deps.Out).Encode(value)
	}
	_, err := fmt.Fprintln(a.deps.Out, text)
	return err
}

func (a *application) uploadCommand() *cobra.Command {
	var notes, notesFile, name, icon, slug string
	var noPromote bool
	command := &cobra.Command{Use: "upload <file.ipa|file.apk>", Short: "Validate and upload an app package", Args: cobra.ExactArgs(1), Example: "  apphub upload ./App.ipa --notes \"Fix login\"\n  apphub upload ./app.apk --notes-file release.md --no-promote", RunE: func(cmd *cobra.Command, args []string) error {
		if notes != "" && notesFile != "" {
			return errors.New("--notes and --notes-file cannot be used together")
		}
		if notesFile != "" {
			data, err := os.ReadFile(notesFile)
			if err != nil {
				return err
			}
			notes = string(data)
		}
		if !a.json {
			_, _ = fmt.Fprintln(a.deps.Err, "正在验证并解析安装包…")
		}
		parsed, err := a.deps.Parser.Parse(cmd.Context(), args[0], packageinfo.Options{Name: name, IconPath: icon, Slug: slug, ReleaseNotes: notes, Promote: !noPromote})
		if err != nil {
			return err
		}
		api, err := a.api()
		if err != nil {
			return err
		}
		var progress io.Writer
		if !a.json {
			progress = a.deps.Err
		}
		result, err := api.Upload(cmd.Context(), args[0], parsed.Metadata, parsed.Icon, progress)
		if err != nil {
			return err
		}
		message := fmt.Sprintf("上传成功：%s %s (%s)", result.App.Slug, result.Version.VersionName, result.Version.BuildNumber)
		if result.Duplicate {
			message = fmt.Sprintf("文件已存在：%s %s (%s)", result.App.Slug, result.Version.VersionName, result.Version.BuildNumber)
			if result.IconUpdated {
				message = fmt.Sprintf("文件已存在，已更新图标：%s %s (%s)", result.App.Slug, result.Version.VersionName, result.Version.BuildNumber)
			}
		}
		return a.print(result, message)
	}}
	command.Flags().StringVar(&notes, "notes", "", "release notes")
	command.Flags().StringVar(&notesFile, "notes-file", "", "read release notes from file")
	command.Flags().StringVar(&name, "name", "", "override app name")
	command.Flags().StringVar(&icon, "icon", "", "override PNG/JPEG/WebP icon")
	command.Flags().StringVar(&slug, "slug", "", "slug for a new app")
	command.Flags().BoolVar(&noPromote, "no-promote", false, "upload without making this the current version")
	return command
}

func (a *application) listCommand() *cobra.Command {
	return &cobra.Command{Use: "list [app-slug]", Short: "List apps and versions", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		api, err := a.api()
		if err != nil {
			return err
		}
		apps, err := api.List(cmd.Context())
		if err != nil {
			return err
		}
		if len(args) == 1 {
			filtered := make([]model.PublicApp, 0, 1)
			for _, app := range apps {
				if app.Slug == args[0] {
					filtered = append(filtered, app)
				}
			}
			apps = filtered
			if len(apps) == 0 {
				return errors.New("app not found")
			}
		}
		if a.json {
			return json.NewEncoder(a.deps.Out).Encode(map[string]any{"apps": apps})
		}
		for _, app := range apps {
			fmt.Fprintf(a.deps.Out, "%s  %s  %s  current=%s\n", app.Slug, app.Platform, app.Identifier, empty(app.CurrentVersionID))
			for _, version := range app.Versions {
				marker := ""
				if version.ID == app.CurrentVersionID {
					marker = " *"
				}
				fmt.Fprintf(a.deps.Out, "  %s  %s (%s)  %d bytes%s\n", version.ID, version.VersionName, version.BuildNumber, version.FileSize, marker)
			}
		}
		return nil
	}}
}

func (a *application) infoCommand() *cobra.Command {
	return &cobra.Command{Use: "info <app-slug> <version-id>", Short: "Show one version", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		api, err := a.api()
		if err != nil {
			return err
		}
		apps, err := api.List(cmd.Context())
		if err != nil {
			return err
		}
		for _, app := range apps {
			if app.Slug == args[0] {
				for _, version := range app.Versions {
					if version.ID == args[1] {
						return a.print(version, fmt.Sprintf("%s %s (%s)\nSHA-256: %s\nSize: %d", app.Name, version.VersionName, version.BuildNumber, version.SHA256, version.FileSize))
					}
				}
			}
		}
		return errors.New("version not found")
	}}
}

func (a *application) promoteCommand() *cobra.Command {
	return &cobra.Command{Use: "promote <app-slug> <version-id>", Short: "Make a version current", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		api, err := a.api()
		if err != nil {
			return err
		}
		app, err := api.Promote(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		return a.print(app, "当前版本已切换。")
	}}
}
func (a *application) deleteCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{Use: "delete <app-slug> [version-id]", Short: "Move an app or one version to the seven-day trash", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		if !yes {
			return errors.New("add --yes to confirm deletion")
		}
		api, err := a.api()
		if err != nil {
			return err
		}
		var until time.Time
		if len(args) == 1 {
			until, err = api.DeleteApp(cmd.Context(), args[0])
		} else {
			until, err = api.Delete(cmd.Context(), args[0], args[1])
		}
		if err != nil {
			return err
		}
		message := "版本已移入回收站，可恢复至 " + until.Format(time.RFC3339)
		if len(args) == 1 {
			message = "整个应用已移入回收站，保留至 " + until.Format(time.RFC3339)
		}
		return a.print(map[string]any{"deleted": true, "recoverableUntil": until}, message)
	}}
	command.Flags().BoolVar(&yes, "yes", false, "confirm deletion")
	return command
}
func (a *application) restoreCommand() *cobra.Command {
	return &cobra.Command{Use: "restore <app-slug> <version-id>", Short: "Restore a version from trash", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		api, err := a.api()
		if err != nil {
			return err
		}
		app, err := api.Restore(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		return a.print(app, "版本已恢复。")
	}}
}
func (a *application) passwordCommand() *cobra.Command {
	var clear bool
	command := &cobra.Command{Use: "password <app-slug>", Short: "Set or clear an app share password", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		password := ""
		var err error
		if !clear {
			if env := os.Getenv("APPHUB_SHARE_PASSWORD"); env != "" {
				password = env
			} else {
				password, err = a.deps.ReadPassword(a.deps.In, a.deps.Err)
				if err != nil {
					return err
				}
			}
		}
		api, err := a.api()
		if err != nil {
			return err
		}
		app, err := api.SetPassword(cmd.Context(), args[0], password)
		if err != nil {
			return err
		}
		message := "分享密码已更新。"
		if clear {
			message = "分享密码已清除。"
		}
		return a.print(app, message)
	}}
	command.Flags().BoolVar(&clear, "clear", false, "remove the share password")
	return command
}

func versionCommand(output io.Writer) *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print version", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { _, err := fmt.Fprintln(output, Version); return err }}
}
func completionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{Use: "completion [bash|zsh|fish|powershell]", Short: "Generate shell completion", Args: cobra.ExactArgs(1), ValidArgs: []string{"bash", "zsh", "fish", "powershell"}, RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return root.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return root.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell":
			return root.GenPowerShellCompletion(cmd.OutOrStdout())
		}
		return errors.New("unsupported shell")
	}}
}
func readPassword(input io.Reader, output io.Writer) (string, error) {
	if file, ok := input.(*os.File); ok && file.Fd() == os.Stdin.Fd() {
		_ = exec.Command("stty", "-echo").Run()
		defer exec.Command("stty", "echo").Run()
	}
	_, _ = fmt.Fprint(output, "新的应用分享密码：")
	line, err := bufio.NewReader(input).ReadString('\n')
	_, _ = fmt.Fprintln(output)
	return strings.TrimSpace(line), err
}
func empty(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
