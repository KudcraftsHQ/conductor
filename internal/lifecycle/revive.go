package lifecycle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ReviveOptions control Revive.
type ReviveOptions struct {
	Exec ExecOptions
	// NoInstall skips the dependency install after the worktree is back. The
	// entry is then marked slim, so the SessionStart hook or the watcher
	// installs on first use.
	NoInstall bool
	// Automatic: called by the watcher rather than a person. Automatic
	// revives stop while the lifecycle is paused; a manual one goes ahead,
	// since bringing work back can only add, never remove.
	Automatic bool
	// Reprovision re-provisions ports/DB through conductor's adopt path for
	// a record that was conductor-registered. It lives outside this package
	// (conductor.json is written only through internal/store); nil skips it.
	// It returns one human line for the result.
	Reprovision func(ctx context.Context, rec *Record) (string, error)
	// Output receives the installer's output; nil discards it.
	Output io.Writer
}

// ReviveResult says what came back.
type ReviveResult struct {
	Record    *Record  `json:"record"`
	Path      string   `json:"path"`
	Restored  []string `json:"restored,omitempty"` // ignored files restored from the archive
	Installed bool     `json:"installed"`

	// AlreadyPresent: the worktree was already on disk (revived earlier, or
	// T3 recreated it). Nothing was checked out.
	AlreadyPresent bool `json:"alreadyPresent,omitempty"`
	// NoOp: nothing needed doing (revived before, or never retired).
	NoOp bool `json:"noOp,omitempty"`
	// Checkout is how it came back: "branch <b>", "recreated branch <b> at
	// <sha>", "detached at <sha>".
	Checkout        string   `json:"checkout,omitempty"`
	SnapshotApplied bool     `json:"snapshotApplied,omitempty"`
	Provisioned     string   `json:"provisioned,omitempty"`
	Notes           []string `json:"notes,omitempty"`
	DryRun          bool     `json:"dryRun,omitempty"`
}

// ErrNoRecord: nothing retired matches the target.
var ErrNoRecord = errors.New("no retired worktree matches")

// FindRecord resolves a revive target — thread id, branch, path or record id —
// to the newest matching record not yet revived. When every match was already
// revived it returns the newest of them with revived=true.
func FindRecord(ctx context.Context, env *Env, target string) (*Record, error) {
	rec, _, err := findRecord(env, target)
	return rec, err
}

func findRecord(env *Env, target string) (rec *Record, revived bool, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, false, fmt.Errorf("%w: empty target", ErrNoRecord)
	}
	all, err := env.ListRecords()
	if err != nil {
		return nil, false, err
	}
	asPath := ""
	if strings.ContainsRune(target, '/') || strings.HasPrefix(target, ".") || strings.HasPrefix(target, "~") {
		p := target
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(env.Home, p[2:])
		}
		asPath = realpathMissing(p)
	}
	var revivedMatch *Record
	for _, r := range all {
		match := r.ID == target || r.Branch == target
		for _, t := range r.T3Threads {
			match = match || t == target
		}
		if asPath != "" && (r.Path == asPath || realpathMissing(r.Path) == asPath) {
			match = true
		}
		if !match {
			continue
		}
		if r.RevivedAt.IsZero() {
			return r, false, nil
		}
		if revivedMatch == nil {
			revivedMatch = r
		}
	}
	if revivedMatch != nil {
		return revivedMatch, true, nil
	}
	return nil, false, fmt.Errorf("%w %q (conductor wt ls --state retired)", ErrNoRecord, target)
}

// realpathMissing is realpath for a path that may not exist: the deepest
// existing ancestor is resolved and the rest appended, so a retired path still
// compares equal to the realpath it was recorded under.
func realpathMissing(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	var tail []string
	dir := p
	for {
		parent := filepath.Dir(dir)
		tail = append([]string{filepath.Base(dir)}, tail...)
		if parent == dir {
			return p
		}
		if r, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(append([]string{r}, tail...)...)
		}
		dir = parent
	}
}

// Revive recreates a retired worktree at the SAME path (so its T3 thread
// continues unchanged): `git worktree add <path> <branch>` (the recorded HEAD
// on a recreated branch if the branch is gone); for a snapshot, the snapshot's
// changes are then restored as uncommitted changes, untracked files included;
// the ignored archive is restored; Hydrate unless NoInstall; Reprovision when
// given; the record and inventory entry are marked revived; logged.
//
// Idempotent: a worktree already present is not checked out again. If it was
// present but never marked revived (T3 recreates a pruned worktree on its own
// when a turn is sent), the missing ignored files are restored and, on a clean
// tree at the recorded HEAD, the snapshot is applied.
func Revive(ctx context.Context, env *Env, target string, o ReviveOptions) (*ReviveResult, error) {
	rec, revived, err := findRecord(env, target)
	if errors.Is(err, ErrNoRecord) {
		// A live worktree named by path is already present: say so plainly.
		p := target
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(env.Home, p[2:])
		}
		if top, ok := WorktreeTop(p); ok && isDir(p) {
			return &ReviveResult{Path: top, AlreadyPresent: true, NoOp: true,
				Notes: []string{"present and never retired"}}, nil
		}
	}
	if err != nil {
		return nil, err
	}
	res := &ReviveResult{Record: rec, Path: rec.Path, DryRun: o.Exec.DryRun}
	if o.Automatic {
		if paused, _ := env.Paused(); paused {
			return res, ErrPaused
		}
	}
	logEv := func(result, reason string, err error) {
		ev := Event{Action: ActionRevive, Path: rec.Path, Result: result, Reason: reason, RecordID: rec.ID,
			SHA: firstNonEmpty(rec.SnapshotSHA, rec.HeadSHA)}
		if err != nil {
			ev.Error = err.Error()
		}
		_ = env.LogEvent(ev)
	}

	path := rec.Path
	present := isDir(path) && exists(filepath.Join(path, ".git"))
	if present && revived {
		res.AlreadyPresent, res.NoOp = true, true
		res.Notes = append(res.Notes, fmt.Sprintf("already revived %s ago", Human(env.Now().Sub(rec.RevivedAt))))
		return res, nil
	}
	if !present && exists(path) {
		if ents, err := os.ReadDir(path); err != nil || len(ents) > 0 {
			err := fmt.Errorf("%s exists and is not a git worktree; move it away first", path)
			logEv("failed", "path occupied", err)
			return res, err
		}
	}
	if !isDir(rec.RepoRoot) {
		err := fmt.Errorf("repository %s is gone", rec.RepoRoot)
		logEv("failed", "repo missing", err)
		return res, err
	}

	if present {
		res.AlreadyPresent = true
		res.Notes = append(res.Notes, "worktree already on disk (recreated outside the lifecycle)")
		if o.Exec.DryRun {
			return res, nil
		}
		if rec.SnapshotSHA != "" {
			head, _ := fxGit(ctx, path, "rev-parse", "HEAD")
			status, serr := fxGit(ctx, path, "status", "--porcelain", "--untracked-files=normal")
			if serr == nil && status == "" && head == rec.HeadSHA {
				if err := applySnapshot(ctx, path, rec); err != nil {
					res.Notes = append(res.Notes, err.Error())
				} else {
					res.SnapshotApplied = true
				}
			} else {
				res.Notes = append(res.Notes, fmt.Sprintf("snapshot not applied (tree is not clean at the recorded HEAD); it is kept at %s — %s",
					rec.GraveyardRef, snapshotHint(rec)))
			}
		}
	} else {
		// A stale registration of the path makes `worktree add` refuse.
		_, _ = fxGit(ctx, rec.RepoRoot, "worktree", "prune")
		args, checkout, err := checkoutArgs(ctx, rec)
		if err != nil {
			logEv("failed", "no commit to check out", err)
			return res, err
		}
		res.Checkout = checkout
		if o.Exec.DryRun {
			res.Notes = append(res.Notes, "git "+strings.Join(args, " "))
			if rec.SnapshotSHA != "" {
				res.Notes = append(res.Notes, "then restore snapshot "+short(rec.SnapshotSHA)+" as uncommitted changes")
			}
			logEv("dry-run", checkout, nil)
			return res, nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return res, err
		}
		if _, err := runGit(ctx, rec.RepoRoot, nil, args...); err != nil {
			logEv("failed", checkout, err)
			return res, err
		}
		if rec.SnapshotSHA != "" {
			if err := applySnapshot(ctx, path, rec); err != nil {
				res.Notes = append(res.Notes, err.Error())
			} else {
				res.SnapshotApplied = true
			}
		}
	}

	restored, err := restoreIgnored(env, rec, path)
	res.Restored = restored
	if err != nil {
		res.Notes = append(res.Notes, "ignored archive: "+err.Error())
	}

	var hydrateErr error
	if !o.NoInstall {
		h, err := Hydrate(ctx, env, path, HydrateOptions{Output: o.Output})
		if err != nil {
			hydrateErr = err
			res.Notes = append(res.Notes, "install failed: "+err.Error())
		} else if h != nil {
			res.Installed = h.Installed
			if !h.Installed && h.Skipped != "" {
				res.Notes = append(res.Notes, "install: "+h.Skipped)
			}
		}
	}

	if o.Reprovision != nil && rec.Conductor != nil {
		line, err := o.Reprovision(ctx, rec)
		if err != nil {
			res.Notes = append(res.Notes, "re-provision failed: "+err.Error())
		}
		res.Provisioned = line
	}

	now := env.Now().UTC()
	rec.RevivedAt = now
	if err := env.WriteRecord(rec); err != nil {
		res.Notes = append(res.Notes, "could not mark the record revived: "+err.Error())
	}
	lockTracked := false
	if _, argv, err := installerFor(ctx, path); err == nil && argv != nil {
		lockTracked = true
	}
	_ = env.UpdateInventory(func(inv *Inventory) error {
		e := inv.Ensure(path, now)
		e.RepoRoot, e.Remote = rec.RepoRoot, rec.Remote
		if e.Branch == "" {
			e.Branch = rec.Branch
		}
		e.State, e.Reason = StateActive, "revived from "+rec.ID
		e.RevivedAt, e.RecordID = now, rec.ID
		e.LastAction, e.LastActionAt, e.LastError = ActionRevive, now, ""
		if hydrateErr != nil {
			e.LastError = hydrateErr.Error()
		}
		// A fresh start: an old needs-you announcement or snapshot must not
		// retire the revived tree on the old date.
		e.NeedsYouNotifiedAt, e.RetireAt = time.Time{}, time.Time{}
		e.SnapshotSHA, e.SnapshotRef, e.SnapshotHead, e.SnapshotAt = "", "", "", time.Time{}
		if !res.Installed && lockTracked && !isDir(filepath.Join(path, "node_modules")) {
			// Back without its dependencies: exactly a slimmed tree, so the
			// SessionStart hook and the watcher install them on first use.
			e.SlimmedAt, e.SlimmedPaths = now, []string{"node_modules"}
		}
		return nil
	})
	logEv("ok", strings.Join(append([]string{firstNonEmpty(res.Checkout, "already on disk")}, res.Notes...), "; "), nil)
	return res, nil
}

// checkoutArgs builds the `git worktree add` for a record: its branch when it
// still exists, the branch recreated at the recorded HEAD when it is gone, or
// a detached HEAD.
func checkoutArgs(ctx context.Context, rec *Record) ([]string, string, error) {
	head := rec.HeadSHA
	if head == "" || !commitExists(ctx, rec.RepoRoot, head) {
		// Fall back to the graveyard ref: HEAD itself, or the snapshot's parent.
		id := strings.TrimPrefix(rec.GraveyardRef, GraveyardRefPrefix)
		if id == "" {
			id = rec.ID
		}
		sha, ok := ReadGraveyardRef(ctx, rec.RepoRoot, id)
		if !ok {
			return nil, "", fmt.Errorf("neither HEAD %s nor %s exists in %s", short(rec.HeadSHA), GraveyardRef(id), rec.RepoRoot)
		}
		if rec.SnapshotSHA != "" {
			if p, err := fxGit(ctx, rec.RepoRoot, "rev-parse", sha+"^1"); err == nil {
				sha = p
			}
		}
		head = sha
	}
	if rec.Branch == "" {
		return []string{"worktree", "add", "--detach", rec.Path, head}, "detached at " + short(head), nil
	}
	if tip, err := fxGit(ctx, rec.RepoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+rec.Branch); err == nil && tip != "" {
		how := "branch " + rec.Branch
		if tip != head {
			how += fmt.Sprintf(" (moved since retire: %s → %s)", short(head), short(tip))
		}
		return []string{"worktree", "add", rec.Path, rec.Branch}, how, nil
	}
	return []string{"worktree", "add", "-b", rec.Branch, rec.Path, head},
		fmt.Sprintf("recreated branch %s at %s", rec.Branch, short(head)), nil
}

// applySnapshot restores a snapshot commit's changes (relative to HEAD at
// retire) as uncommitted changes: tracked edits and deletions in the working
// tree, new files as untracked files. The index is left at HEAD.
func applySnapshot(ctx context.Context, path string, rec *Record) error {
	if rec.HeadSHA == "" {
		return fmt.Errorf("snapshot %s has no recorded HEAD; %s", short(rec.SnapshotSHA), snapshotHint(rec))
	}
	diff, err := runGit(ctx, path, nil, "diff", "--binary", "--no-color", "--no-ext-diff", rec.HeadSHA, rec.SnapshotSHA)
	if err != nil {
		return fmt.Errorf("read snapshot: %v; %s", err, snapshotHint(rec))
	}
	if len(bytes.TrimSpace(diff)) == 0 {
		return nil
	}
	if _, err := runGit(ctx, path, diff, "apply", "--whitespace=nowarn"); err != nil {
		return fmt.Errorf("snapshot did not apply cleanly (%v); it is kept at %s — %s", err, rec.GraveyardRef, snapshotHint(rec))
	}
	return nil
}

func snapshotHint(rec *Record) string {
	return fmt.Sprintf("apply by hand: git diff --binary %s %s | git apply", short(rec.HeadSHA), short(rec.SnapshotSHA))
}

// restoreIgnored extracts the record's ignored-files archive into path. An
// entry that already exists is left alone, and nothing outside path is
// written.
func restoreIgnored(env *Env, rec *Record, path string) ([]string, error) {
	name := rec.IgnoredArchive
	if name == "" {
		name = IgnoredArchiveName
	}
	f, err := os.Open(filepath.Join(env.RecordDir(rec.ID), filepath.Base(name)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	root := filepath.Clean(path)
	var restored []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return restored, err
		}
		rel := filepath.Clean(strings.TrimPrefix(h.Name, "./"))
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || rel == ".git" || strings.HasPrefix(rel, ".git/") {
			continue
		}
		dst := filepath.Join(root, rel)
		if !inside(dst, root) {
			continue
		}
		mode := os.FileMode(h.Mode).Perm()
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, mode|0o700); err != nil {
				return restored, err
			}
		case tar.TypeReg:
			if _, err := os.Lstat(dst); err == nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return restored, err
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return restored, err
			}
			_, cerr := io.Copy(out, tr)
			if err := out.Close(); cerr == nil {
				cerr = err
			}
			if cerr != nil {
				return restored, cerr
			}
			_ = os.Chtimes(dst, h.ModTime, h.ModTime)
			restored = append(restored, rel)
		case tar.TypeSymlink:
			if _, err := os.Lstat(dst); err == nil {
				continue
			}
			if filepath.IsAbs(h.Linkname) || !inside(filepath.Join(filepath.Dir(dst), h.Linkname), root) {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return restored, err
			}
			if err := os.Symlink(h.Linkname, dst); err == nil {
				restored = append(restored, rel)
			}
		}
	}
	return restored, nil
}

// runGit runs a git command that may write (unlike fxGit, optional locks are
// allowed), with optional stdin, and returns raw stdout.
func runGit(ctx context.Context, dir string, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), fmt.Errorf("git %s: %s", args[0], firstLine(msg))
	}
	return out.Bytes(), nil
}

func commitExists(ctx context.Context, repo, sha string) bool {
	_, err := fxGit(ctx, repo, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}
