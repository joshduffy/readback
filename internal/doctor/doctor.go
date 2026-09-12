// Package doctor : report detected agent CLIs, provider auth, and hook installation state.
package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/joshduffy/readback/internal/output"
	"github.com/joshduffy/readback/internal/registry"
	"github.com/joshduffy/readback/internal/verify"
	"github.com/spf13/cobra"
)

const name = "doctor"

// Version is the readback version reported by doctor. cli.Version cannot be
// imported here without an import cycle, so cli sets this var at startup.
var Version = "dev"

var agentBins = []string{"claude", "codex", "cursor", "gemini", "kimi"}

func init() {
	registry.Register(registry.Module{
		Name:      name,
		Summary:   "Report detected agent CLIs, provider auth, and whether compiled hooks are installed and firing",
		Status:    registry.StatusBeta,
		Milestone: "v0.1",
		Keywords:  []string{"doctor", "install", "auth", "gh", "wrangler", "hooks"},
		Schema: map[string]interface{}{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"title":   "readback doctor result",
			"type":    "object",
		},
	})
}

// Probes makes every environment interaction injectable for tests.
type Probes struct {
	LookPath   func(name string) (string, error)
	RunVersion func(ctx context.Context, path string) (string, error)
	// GhAuth runs `gh auth status` and returns stdout and stderr separately.
	// gh writes its report to stderr; stdout is discarded by the caller.
	GhAuth   func(ctx context.Context) (stdout, stderr string, err error)
	CFClient func() *http.Client
	Home     func() (string, error)
	Cwd      func() (string, error)
	Env      func(key string) string
	// VersionTimeout bounds each --version probe. Zero means the default.
	VersionTimeout time.Duration
}

const defaultVersionTimeout = 3 * time.Second

func (p Probes) versionTimeout() time.Duration {
	if p.VersionTimeout > 0 {
		return p.VersionTimeout
	}
	return defaultVersionTimeout
}

// boundedCmd returns a command that is hard-bounded by ctx. On unix it runs
// in its own process group and cancellation kills the whole group, so a
// probe that spawns children still returns within the deadline; on Windows
// cancellation kills only the direct process. WaitDelay caps the time spent
// waiting on pipes inherited by grandchildren.
func boundedCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	setProcAttrs(cmd)
	cmd.Cancel = func() error {
		return killProc(cmd)
	}
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd
}

func defaultProbes() Probes {
	return Probes{
		LookPath: exec.LookPath,
		RunVersion: func(ctx context.Context, path string) (string, error) {
			cmd := boundedCmd(ctx, path, "--version")
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = io.Discard
			err := cmd.Run()
			return firstLine(out.String()), err
		},
		GhAuth: func(ctx context.Context) (string, string, error) {
			cmd := boundedCmd(ctx, "gh", "auth", "status")
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			return stdout.String(), stderr.String(), err
		},
		CFClient: func() *http.Client { return &http.Client{Timeout: 5 * time.Second} },
		Home:     os.UserHomeDir,
		Cwd:      os.Getwd,
		Env:      os.Getenv,
	}
}

type agentInfo struct {
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

type ghInfo struct {
	Found      bool   `json:"found"`
	Version    string `json:"version"`
	AuthOK     bool   `json:"auth_ok"`
	AuthDetail string `json:"auth_detail"`
	Error      string `json:"error"`
}

type cfInfo struct {
	TokenSource      string `json:"token_source"`
	AccountReachable bool   `json:"account_reachable"`
}

type assertionsInfo struct {
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	ParseOK bool   `json:"parse_ok"`
	Error   string `json:"error"`
}

type hooksInfo struct {
	ClaudeSettingsMentionsReadback bool   `json:"claude_settings_mentions_readback"`
	CodexHooksMentionsReadback     bool   `json:"codex_hooks_mentions_readback"`
	Error                          string `json:"error"`
}

type report struct {
	Version    string               `json:"version"`
	Agents     map[string]agentInfo `json:"agents"`
	Gh         ghInfo               `json:"gh"`
	Cloudflare cfInfo               `json:"cloudflare"`
	Assertions assertionsInfo       `json:"assertions"`
	Hooks      hooksInfo            `json:"hooks"`
}

// Command returns the cobra command for this module.
func Command(w func() *output.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report detected agent CLIs, provider auth, and hook installation state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			code := execute(w(), defaultProbes())
			cmd.SilenceUsage = true
			return exitError(code)
		},
	}
}

func execute(w *output.Writer, probes Probes) int {
	rep := diagnose(probes)
	code := output.ExitVerified
	errMsg := ""
	if !rep.Gh.AuthOK {
		code = output.ExitUnproven
		errMsg = "gh authentication is not working (gh.auth_ok is false); run: gh auth login"
	}
	return w.Emit(output.Result{
		Command: name,
		OK:      code == output.ExitVerified,
		Exit:    code,
		Data:    rep,
		Error:   errMsg,
	}, func(o io.Writer) { renderTable(o, rep) })
}

func diagnose(p Probes) report {
	rep := report{
		Version: Version,
		Agents:  make(map[string]agentInfo, len(agentBins)),
	}
	for _, bin := range agentBins {
		rep.Agents[bin] = probeAgent(p, bin)
	}
	rep.Gh = probeGh(p)
	rep.Cloudflare = probeCloudflare(p)
	rep.Assertions = probeAssertions(p)
	rep.Hooks = probeHooks(p)
	return rep
}

func probeAgent(p Probes, bin string) agentInfo {
	path, err := p.LookPath(bin)
	if err != nil {
		return agentInfo{Found: false}
	}
	info := agentInfo{Found: true, Path: path}
	ctx, cancel := context.WithTimeout(context.Background(), p.versionTimeout())
	defer cancel()
	if version, err := p.RunVersion(ctx, path); err == nil {
		info.Version = version
	}
	return info
}

func probeGh(p Probes) ghInfo {
	path, err := p.LookPath("gh")
	if err != nil {
		return ghInfo{Found: false, Error: "gh not found on PATH"}
	}
	info := ghInfo{Found: true}
	versionCtx, versionCancel := context.WithTimeout(context.Background(), p.versionTimeout())
	if version, err := p.RunVersion(versionCtx, path); err == nil {
		info.Version = version
	}
	versionCancel()
	authCtx, authCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer authCancel()
	_, stderr, err := p.GhAuth(authCtx)
	info.AuthOK = err == nil
	info.AuthDetail = redactGhTokens(strings.TrimSpace(stderr))
	return info
}

// probeCloudflare never surfaces the token, any prefix of it, or its length.
func probeCloudflare(p Probes) cfInfo {
	token := p.Env("CLOUDFLARE_API_TOKEN")
	source := "none"
	if token != "" {
		source = "env"
	} else if home, err := p.Home(); err == nil {
		data, err := os.ReadFile(filepath.Join(home, ".cloudflare", "api-token"))
		if err == nil && strings.TrimSpace(string(data)) != "" {
			source = "file"
			token = strings.TrimSpace(string(data))
		}
	}
	info := cfInfo{TokenSource: source}
	if token == "" {
		return info
	}
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer reqCancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://api.cloudflare.com/client/v4/accounts", nil)
	if err != nil {
		return info
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.CFClient().Do(req)
	if err != nil {
		return info
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return info
	}
	var parsed struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Success {
		info.AccountReachable = true
	}
	return info
}

func probeAssertions(p Probes) assertionsInfo {
	cwd, err := p.Cwd()
	if err != nil {
		return assertionsInfo{Found: false, Error: err.Error()}
	}
	path, ok := verify.FindAssertions(cwd)
	if !ok {
		return assertionsInfo{Found: false}
	}
	info := assertionsInfo{Found: true, Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	if _, err := verify.ParseAssertions(data); err != nil {
		info.Error = err.Error()
		return info
	}
	info.ParseOK = true
	return info
}

func probeHooks(p Probes) hooksInfo {
	home, err := p.Home()
	if err != nil {
		return hooksInfo{Error: err.Error()}
	}
	return hooksInfo{
		ClaudeSettingsMentionsReadback: fileContains(filepath.Join(home, ".claude", "settings.json"), "readback"),
		CodexHooksMentionsReadback:     fileContains(filepath.Join(home, ".codex", "hooks.json"), "readback"),
	}
}

func fileContains(path, needle string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), needle)
}

var ghTokenPattern = regexp.MustCompile(`github_pat_\S*|gh[pousr]_\S*`)

func redactGhTokens(s string) string {
	return ghTokenPattern.ReplaceAllString(s, "[redacted]")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func renderTable(o io.Writer, rep report) {
	fmt.Fprintf(o, "readback %s\n", rep.Version)
	rows := make([][]string, 0, len(agentBins))
	for _, bin := range agentBins {
		a := rep.Agents[bin]
		rows = append(rows, []string{bin, boolWord(a.Found), dash(a.Path), dash(a.Version)})
	}
	output.Table(o, []string{"AGENT", "FOUND", "PATH", "VERSION"}, rows)
	fmt.Fprintln(o)
	output.Table(o, []string{"GH", "VALUE"}, [][]string{
		{"found", boolWord(rep.Gh.Found)},
		{"version", dash(rep.Gh.Version)},
		{"auth_ok", boolWord(rep.Gh.AuthOK)},
		{"auth_detail", dash(rep.Gh.AuthDetail)},
		{"error", dash(rep.Gh.Error)},
	})
	fmt.Fprintln(o)
	output.Table(o, []string{"CLOUDFLARE", "VALUE"}, [][]string{
		{"token_source", rep.Cloudflare.TokenSource},
		{"account_reachable", boolWord(rep.Cloudflare.AccountReachable)},
	})
	fmt.Fprintln(o)
	output.Table(o, []string{"ASSERTIONS", "VALUE"}, [][]string{
		{"found", boolWord(rep.Assertions.Found)},
		{"path", dash(rep.Assertions.Path)},
		{"parse_ok", boolWord(rep.Assertions.ParseOK)},
		{"error", dash(rep.Assertions.Error)},
	})
	fmt.Fprintln(o)
	output.Table(o, []string{"HOOKS", "VALUE"}, [][]string{
		{"claude_settings_mentions_readback", boolWord(rep.Hooks.ClaudeSettingsMentionsReadback)},
		{"codex_hooks_mentions_readback", boolWord(rep.Hooks.CodexHooksMentionsReadback)},
		{"error", dash(rep.Hooks.Error)},
	})
}

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

type exitError int

func (e exitError) Error() string { return "" }
func (e exitError) ExitCode() int { return int(e) }
