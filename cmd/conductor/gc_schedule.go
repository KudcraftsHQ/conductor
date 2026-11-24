package main

// `conductor wt schedule`: the hourly lifecycle sweep as a systemd user timer.
//
// A timer rather than a hook in `conductor t3 watch`: the sweep runs for
// minutes (git, gh, deleting node_modules) and would stall the watcher's 5 s
// reconcile loop, the watcher only exists on T3 machines while the lifecycle
// covers every worktree, and a separate unit can be stopped, paused or
// inspected (`journalctl --user -u conductor-lifecycle`) on its own.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hammashamzah/conductor/internal/lifecycle"
	"github.com/spf13/cobra"
)

const lifecycleUnit = "conductor-lifecycle"

var wtScheduleBinary string

var wtScheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Install or remove the hourly lifecycle sweep (systemd user timer)",
	Long: `Manages ~/.config/systemd/user/conductor-lifecycle.{service,timer}, which run
'conductor wt gc' every sweepInterval (default 1h). install writes the units
and reloads systemd but does not enable them; it prints the command that does.`,
}

var wtScheduleInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Write the systemd user units (does not enable them)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		bin := wtScheduleBinary
		if bin == "" {
			if p, err := exec.LookPath("conductor"); err == nil {
				bin = p
			} else if p, err := os.Executable(); err == nil {
				bin = p
			}
		}
		if abs, err := filepath.Abs(bin); err == nil {
			bin = abs
		}
		env, err := lifecycle.NewEnv()
		if err != nil {
			return err
		}
		interval := lifecycle.ResolveSettings(env.Config, "").SweepInterval
		if interval <= 0 {
			interval = lifecycle.DefaultSweepInterval
		}
		dir := systemdUserDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		service := fmt.Sprintf(`[Unit]
Description=conductor worktree lifecycle sweep (slim, needs-you, retire)

[Service]
Type=oneshot
Environment=PATH=%%h/.local/bin:%%h/.bun/bin:%%h/go/bin:/usr/local/bin:/usr/bin:/bin
ExecStart=%s wt gc
Nice=10
IOSchedulingClass=idle
TimeoutStartSec=55min
`, bin)
		timer := fmt.Sprintf(`[Unit]
Description=Run the conductor worktree lifecycle sweep every %s

[Timer]
OnActiveSec=10min
OnUnitActiveSec=%ds
RandomizedDelaySec=2min

[Install]
WantedBy=timers.target
`, lifecycle.Human(interval), int(interval.Seconds()))
		for name, body := range map[string]string{lifecycleUnit + ".service": service, lifecycleUnit + ".timer": timer} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				return err
			}
			fmt.Println("wrote " + tildePath(filepath.Join(dir, name)))
		}
		if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			fmt.Printf("systemctl --user daemon-reload: %s\n", strings.TrimSpace(string(out)))
		}
		fmt.Printf("\nNot enabled. To start the hourly sweep:\n  systemctl --user enable --now %s.timer\n", lifecycleUnit)
		return nil
	},
}

var wtScheduleUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Disable the timer and remove the units",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_ = exec.Command("systemctl", "--user", "disable", "--now", lifecycleUnit+".timer").Run()
		dir := systemdUserDir()
		for _, name := range []string{lifecycleUnit + ".service", lifecycleUnit + ".timer"} {
			if err := os.Remove(filepath.Join(dir, name)); err == nil {
				fmt.Println("removed " + tildePath(filepath.Join(dir, name)))
			}
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		return nil
	},
}

var wtScheduleStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether the timer is installed and enabled",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := filepath.Join(systemdUserDir(), lifecycleUnit+".timer")
		if _, err := os.Stat(path); err != nil {
			fmt.Println("not installed (conductor wt schedule install)")
			return nil
		}
		enabled, _ := exec.Command("systemctl", "--user", "is-enabled", lifecycleUnit+".timer").Output()
		active, _ := exec.Command("systemctl", "--user", "is-active", lifecycleUnit+".timer").Output()
		fmt.Printf("installed: %s\nenabled: %s\nactive: %s\n", tildePath(path),
			strings.TrimSpace(string(enabled)), strings.TrimSpace(string(active)))
		return nil
	},
}

func systemdUserDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "systemd", "user")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func registerLifecycleSchedule() {
	wtScheduleInstallCmd.Flags().StringVar(&wtScheduleBinary, "binary", "", "conductor binary the unit runs (default: conductor on PATH)")
	wtScheduleCmd.AddCommand(wtScheduleInstallCmd, wtScheduleUninstallCmd, wtScheduleStatusCmd)
	worktreeCmd.AddCommand(wtScheduleCmd)
}
