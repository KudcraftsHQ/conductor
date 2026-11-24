package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// UnsettledOptions control OnThreadUnsettledWith.
type UnsettledOptions struct {
	DryRun bool
	// Reprovision is passed to Revive (see ReviveOptions.Reprovision).
	Reprovision func(ctx context.Context, rec *Record) (string, error)
	Output      io.Writer
}

// OnThreadUnsettled is the T3 watcher hook (internal/t3watch, `conductor t3
// watch`): called when a thread bound to worktreePath unsettles, BEFORE the
// watcher starts that worktree's dev server. A retired path (record exists,
// directory gone) → Revive; a slimmed one (lockfile present, node_modules
// missing) → Hydrate. Must be fast and silent when there is nothing to do,
// and must not change the watcher's behaviour for worktrees the lifecycle
// does not know.
func OnThreadUnsettled(ctx context.Context, env *Env, worktreePath string) error {
	_, err := OnThreadUnsettledWith(ctx, env, worktreePath, UnsettledOptions{})
	return err
}

// OnThreadUnsettledWith is OnThreadUnsettled with options; it returns one
// human line describing what it did ("" when nothing).
func OnThreadUnsettledWith(ctx context.Context, env *Env, worktreePath string, o UnsettledOptions) (string, error) {
	kind, rec := planRestore(env, worktreePath)
	switch kind {
	case "":
		return "", nil // nothing the lifecycle knows about: not ours
	case "revive":
		res, err := Revive(ctx, env, rec.ID, ReviveOptions{
			Exec: ExecOptions{DryRun: o.DryRun}, Automatic: true,
			Reprovision: o.Reprovision, Output: o.Output,
		})
		if err != nil {
			return "", fmt.Errorf("revive %s: %w", worktreePath, err)
		}
		line := "revived " + worktreePath + " from " + rec.ID
		if res.DryRun {
			line = "would revive " + worktreePath + " from " + rec.ID
		}
		if res.Checkout != "" {
			line += " (" + res.Checkout + ")"
		}
		if res.SnapshotApplied {
			line += ", snapshot restored"
		}
		if res.Installed {
			line += ", dependencies installed"
		}
		if len(res.Notes) > 0 {
			line += "; " + strings.Join(res.Notes, "; ")
		}
		return line, nil
	}
	h, err := Hydrate(ctx, env, worktreePath, HydrateOptions{
		Exec: ExecOptions{DryRun: o.DryRun}, Quiet: true, RequireSlimmed: true, Output: o.Output,
	})
	if err != nil {
		if errors.Is(err, ErrPaused) {
			return "", nil
		}
		return "", fmt.Errorf("hydrate %s: %w", worktreePath, err)
	}
	switch {
	case h.Installed:
		return fmt.Sprintf("reinstalled dependencies in %s (%s)", h.Path, h.Command), nil
	case h.Command != "" && o.DryRun:
		return fmt.Sprintf("would reinstall dependencies in %s (%s)", h.Path, h.Command), nil
	}
	return "", nil
}

// NeedsRestore is the watcher's cheap pre-check (stats and small reads, no
// subprocess): true when OnThreadUnsettled would revive or hydrate path, so
// the watcher holds the path back only when there is real work.
func NeedsRestore(env *Env, path string) bool {
	kind, _ := planRestore(env, path)
	return kind != ""
}

// planRestore decides what an unsettling thread's path needs:
//
//	"revive"  the directory is gone and an unrevived record names it; or it
//	          is back (T3 recreates a pruned worktree when a turn is sent)
//	          while the inventory still says retired
//	"hydrate" a slimmed worktree whose dependencies are missing
//	""        nothing the lifecycle knows about
func planRestore(env *Env, path string) (string, *Record) {
	if path == "" {
		return "", nil
	}
	if paused, _ := env.Paused(); paused {
		return "", nil
	}
	if !exists(path) {
		rec, revived, err := findRecord(env, realpathMissing(path))
		if err != nil || revived {
			return "", nil
		}
		return "revive", rec
	}
	top, ok := WorktreeTop(path)
	if !ok {
		return "", nil
	}
	inv, err := env.LoadInventory()
	if err != nil {
		return "", nil
	}
	en := inv.Get(top)
	if en != nil && en.RecordID != "" && en.RetiredAt.After(en.RevivedAt) {
		if rec, err := env.ReadRecord(en.RecordID); err == nil && rec.RevivedAt.IsZero() {
			return "revive", rec
		}
	}
	if hasAnyLockfile(top) && en.Slimmed() && depsMissing(top, en) {
		return "hydrate", nil
	}
	return "", nil
}

// sessionHookWait is how long the SessionStart hook waits for an install
// before leaving it running in the background. Under the hook's own timeout
// (sessionHookTimeout) so Claude Code never kills it.
const (
	sessionHookWait    = 40 * time.Second
	sessionHookTimeout = 60 // seconds, written into settings.json
)

// SessionStartHook is `conductor wt hydrate --hook`, wired as a Claude Code
// SessionStart hook in ~/.claude/settings.json: runs in the session's cwd,
// silent and fast when nothing to do, never fails the session.
//
// It acts only inside a linked worktree the inventory marks slimmed (never a
// repo root, never a fresh worktree whose setup may still be installing). The
// install runs as a detached `conductor wt hydrate <top>`: the hook waits up
// to sessionHookWait, then leaves it running and says so. Whatever it prints
// goes into the session's context, so it prints only when it acted.
func SessionStartHook(ctx context.Context, env *Env, cwd string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = nil
		}
	}()
	top, ok := WorktreeTop(cwd)
	if !ok || !hasAnyLockfile(top) {
		return nil
	}
	if paused, _ := env.Paused(); paused {
		return nil
	}
	inv, err := env.LoadInventory()
	if err != nil {
		return nil
	}
	en := inv.Get(top)
	if en == nil || !en.Slimmed() || !depsMissing(top, en) {
		return nil
	}
	if hydrateInFlight(top) {
		fmt.Printf("conductor: dependencies are being reinstalled in %s by another process; `conductor wt hydrate` waits for it.\n", top)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	logDir := filepath.Join(env.StateDir, "hydrate")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil
	}
	logPath := filepath.Join(logDir, hydrateLockName(top)+".log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	defer func() { _ = logf.Close() }()
	cmd := exec.Command(exe, "wt", "hydrate", top)
	cmd.Dir = top
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case werr := <-done:
		if werr == nil {
			fmt.Printf("conductor: reinstalled dependencies in %s (it had been slimmed while idle).\n", top)
		} else {
			fmt.Printf("conductor: reinstalling dependencies in %s failed — see %s, or run `conductor wt hydrate`.\n", top, logPath)
		}
	case <-time.After(sessionHookWait):
		fmt.Printf("conductor: dependencies in %s are still being reinstalled in the background (it had been slimmed while idle); "+
			"run `conductor wt hydrate` to wait for them before building. Log: %s\n", top, logPath)
	case <-ctx.Done():
	}
	return nil
}

// --- Claude Code settings.json: install / uninstall / status ---

// hookMarker identifies conductor's SessionStart hook among the others.
const hookMarker = "wt hydrate --hook"

// ClaudeSettingsPath is where the hook is installed: $CONDUCTOR_CLAUDE_SETTINGS
// (for trying it on a copy), else $CLAUDE_CONFIG_DIR/settings.json, else
// ~/.claude/settings.json.
func (e *Env) ClaudeSettingsPath() string {
	if p := os.Getenv("CONDUCTOR_CLAUDE_SETTINGS"); p != "" {
		return p
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	return filepath.Join(e.Home, ".claude", "settings.json")
}

// HookCommand is the command line written into settings.json: plain
// `conductor …` when the conductor on PATH is this binary, else this binary's
// absolute path.
func HookCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return "conductor " + hookMarker
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if onPath, err := exec.LookPath("conductor"); err == nil {
		if r, err := filepath.EvalSymlinks(onPath); err == nil && r == exe {
			return "conductor " + hookMarker
		}
	}
	return strconv.Quote(exe) + " " + hookMarker
}

// HookStatus is what `wt hooks status` reports.
type HookStatus struct {
	SettingsPath string `json:"settingsPath"`
	Installed    bool   `json:"installed"`
	Command      string `json:"command,omitempty"`
	Backup       string `json:"backup,omitempty"` // written by install/uninstall
	Changed      bool   `json:"changed"`
}

// SessionHookStatus reads settings.json and reports whether the hook is in it.
func SessionHookStatus(path string) (*HookStatus, error) {
	st := &HookStatus{SettingsPath: path}
	top, err := readSettings(path)
	if err != nil {
		return st, err
	}
	groups, err := sessionStartGroups(top)
	if err != nil {
		return st, err
	}
	for _, g := range groups {
		for _, c := range hookCommands(g) {
			if strings.Contains(c, hookMarker) {
				st.Installed, st.Command = true, c
			}
		}
	}
	return st, nil
}

// InstallSessionHook adds (or updates) conductor's SessionStart hook,
// preserving every other key, and backs the file up first.
func InstallSessionHook(path, command string, dryRun bool) (*HookStatus, error) {
	return editSessionHooks(path, dryRun, func(groups []json.RawMessage) ([]json.RawMessage, bool) {
		groups, _ = removeOurs(groups)
		ours, _ := json.Marshal(map[string]any{
			"matcher": "startup|resume",
			"hooks": []map[string]any{{
				"type": "command", "command": command, "timeout": sessionHookTimeout,
			}},
		})
		return append(groups, ours), true
	}, command)
}

// UninstallSessionHook removes conductor's SessionStart hook, preserving
// everything else, and backs the file up first.
func UninstallSessionHook(path string, dryRun bool) (*HookStatus, error) {
	return editSessionHooks(path, dryRun, func(groups []json.RawMessage) ([]json.RawMessage, bool) {
		out, removed := removeOurs(groups)
		return out, removed
	}, "")
}

func editSessionHooks(path string, dryRun bool, edit func([]json.RawMessage) ([]json.RawMessage, bool), command string) (*HookStatus, error) {
	before, err := SessionHookStatus(path)
	if err != nil {
		return before, err
	}
	if command != "" && before.Installed && before.Command == command {
		return before, nil // already exactly as wanted
	}
	if command == "" && !before.Installed {
		return before, nil
	}
	top, err := readSettings(path)
	if err != nil {
		return before, err
	}
	groups, err := sessionStartGroups(top)
	if err != nil {
		return before, err
	}
	groups, changed := edit(groups)
	st := &HookStatus{SettingsPath: path, Installed: command != "", Command: command, Changed: changed}
	if !changed || dryRun {
		return st, nil
	}

	hooks, _ := objGet(top, "hooks")
	hooksObj, err := decodeObject(hooks)
	if err != nil {
		return st, fmt.Errorf("%s: \"hooks\" is not an object: %w", path, err)
	}
	if len(groups) == 0 {
		hooksObj = objDel(hooksObj, "SessionStart")
	} else {
		b, _ := json.Marshal(groups)
		hooksObj = objSet(hooksObj, "SessionStart", b)
	}
	if len(hooksObj) == 0 {
		top = objDel(top, "hooks")
	} else {
		top = objSet(top, "hooks", encodeObject(hooksObj))
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, encodeObject(top), "", "  "); err != nil {
		return st, err
	}
	pretty.WriteByte('\n')

	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		old, err := os.ReadFile(path)
		if err != nil {
			return st, err
		}
		st.Backup = path + ".conductor-backup-" + time.Now().UTC().Format("20060102-150405")
		if err := writeFileSync(st.Backup, old, mode); err != nil {
			return st, fmt.Errorf("back up %s: %w", path, err)
		}
	}
	if err := writeFileSync(path, pretty.Bytes(), mode); err != nil {
		return st, err
	}
	return st, nil
}

// removeOurs drops conductor's hook from every SessionStart group, and any
// group it leaves empty.
func removeOurs(groups []json.RawMessage) ([]json.RawMessage, bool) {
	var out []json.RawMessage
	removed := false
	for _, g := range groups {
		obj, err := decodeObject(g)
		if err != nil {
			out = append(out, g)
			continue
		}
		raw, ok := objGet(obj, "hooks")
		var hooks []json.RawMessage
		if !ok || json.Unmarshal(raw, &hooks) != nil {
			out = append(out, g)
			continue
		}
		var keep []json.RawMessage
		for _, h := range hooks {
			var c struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(h, &c) == nil && strings.Contains(c.Command, hookMarker) {
				removed = true
				continue
			}
			keep = append(keep, h)
		}
		if len(keep) == len(hooks) {
			out = append(out, g)
			continue
		}
		if len(keep) == 0 {
			continue
		}
		b, _ := json.Marshal(keep)
		out = append(out, encodeObject(objSet(obj, "hooks", b)))
	}
	return out, removed
}

func hookCommands(group json.RawMessage) []string {
	var g struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(group, &g) != nil {
		return nil
	}
	var out []string
	for _, h := range g.Hooks {
		out = append(out, h.Command)
	}
	return out
}

func readSettings(path string) ([]kv, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	obj, err := decodeObject(b)
	if err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", path, err)
	}
	return obj, nil
}

func sessionStartGroups(top []kv) ([]json.RawMessage, error) {
	hooks, ok := objGet(top, "hooks")
	if !ok {
		return nil, nil
	}
	obj, err := decodeObject(hooks)
	if err != nil {
		return nil, fmt.Errorf("\"hooks\" is not an object: %w", err)
	}
	raw, ok := objGet(obj, "SessionStart")
	if !ok {
		return nil, nil
	}
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("\"hooks.SessionStart\" is not an array: %w", err)
	}
	return groups, nil
}

// kv is one member of a JSON object, kept in file order so a rewrite of
// settings.json changes only what it must.
type kv struct {
	Key string
	Val json.RawMessage
}

func decodeObject(b []byte) ([]kv, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected an object")
	}
	var out []kv
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected a key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, kv{key, raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

func encodeObject(obj []kv) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range obj {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(m.Key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.Val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func objGet(obj []kv, key string) (json.RawMessage, bool) {
	for _, m := range obj {
		if m.Key == key {
			return m.Val, true
		}
	}
	return nil, false
}

func objSet(obj []kv, key string, val json.RawMessage) []kv {
	for i := range obj {
		if obj[i].Key == key {
			obj[i].Val = val
			return obj
		}
	}
	return append(obj, kv{key, val})
}

func objDel(obj []kv, key string) []kv {
	out := obj[:0:0]
	for _, m := range obj {
		if m.Key != key {
			out = append(out, m)
		}
	}
	return out
}
