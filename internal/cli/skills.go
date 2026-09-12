package cli

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/joshduffy/readback/internal/output"
	"github.com/spf13/cobra"
)

//go:embed skills/SKILL.md
var embeddedSkill []byte

type skillsExit int

func (e skillsExit) Error() string { return "" }
func (e skillsExit) ExitCode() int { return int(e) }

func installSkillsCmd(w func() *output.Writer) *cobra.Command {
	var agent, dir string
	var force bool
	cmd := &cobra.Command{
		Use: "install-skills", Short: "Install the embedded readback agent skill", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			written := []string{}
			fail := func(code int, err error) error {
				return skillsExit(w().Emit(output.Result{Command: "install-skills", Exit: code, Data: written, Error: err.Error()}, nil))
			}
			agents := []string{agent}
			switch agent {
			case "all":
				agents = []string{"claude", "codex", "cursor"}
			case "claude", "codex", "cursor":
			default:
				return fail(64, fmt.Errorf("unknown agent %q", agent))
			}
			var paths []string
			if cmd.Flags().Changed("dir") && cmd.Flags().Changed("agent") {
				return fail(64, fmt.Errorf("--dir and --agent are mutually exclusive"))
			}
			if cmd.Flags().Changed("dir") {
				if dir == "" {
					return fail(64, fmt.Errorf("dir must not be empty"))
				}
				paths = []string{filepath.Join(dir, "SKILL.md")}
			} else {
				home, err := os.UserHomeDir()
				if err != nil {
					return fail(2, err)
				}
				for _, a := range agents {
					paths = append(paths, filepath.Join(home, "."+a, "skills", "readback-cli-usage", "SKILL.md"))
				}
			}
			for _, path := range paths {
				if err := writeSkill(path, force); err != nil {
					return fail(2, err)
				}
				written = append(written, path)
			}
			w().Emit(output.Result{Command: "install-skills", OK: true, Data: written}, func(out io.Writer) {
				for _, path := range written {
					fmt.Fprintln(out, path)
				}
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "all", "claude, codex, cursor, or all")
	cmd.Flags().StringVar(&dir, "dir", "", "directory receiving SKILL.md instead of agent paths")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing skill that differs from the embedded one")
	return cmd
}

func writeSkill(path string, force bool) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular skill file %s", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		normalized := bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
		if bytes.Equal(normalized, embeddedSkill) {
			return nil // identical: leave the file and its mtime alone
		}
		if !force && len(bytes.TrimSpace(normalized)) > 0 {
			first, _, _ := bytes.Cut(normalized, []byte("\n"))
			ours, _, _ := bytes.Cut(embeddedSkill, []byte("\n"))
			if bytes.Equal(first, ours) {
				return fmt.Errorf("skill at %s was edited locally; use --force to replace", path)
			}
			return fmt.Errorf("foreign skill at %s; use --force to replace", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".readback-skill-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(embeddedSkill); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
