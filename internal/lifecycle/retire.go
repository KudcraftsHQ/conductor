package lifecycle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// Retire removes a worktree so that Revive can bring it back exactly:
//  1. re-check everything (fresh facts, fresh plan, HEAD, work, processes);
//  2. id := NewRecordID; refs/graveyard/<id> at HEAD (Snapshot when there are
//     unpushed commits — it points the ref at HEAD, keeping them alive);
//  3. archive small ignored, non-regenerable files (not on the slim
//     allowlist; cap s.IgnoredArchiveCapMB) into RecordDir(id)/IgnoredArchiveName,
//     listing ArchivedPaths and SkippedPaths;
//  4. conductor-registered: release ports/DB/tunnel through ReleaseResources
//     (hibernate) and keep the entry — it stays hibernated, which is exactly
//     what the watcher expects of a registered worktree whose directory is
//     gone, and what it wakes when a thread returns; close tmux windows inside;
//     re-check no process inside (skip if busy);
//  5. env.WriteRecord (fsynced) BEFORE anything is removed;
//  6. `git worktree remove <path>` WITHOUT --force, clear leftovers written
//     during removal (interim script's remove_worktree), `git worktree prune`.
//     Never delete the branch.
//
// A worktree with uncommitted or untracked changes is skipped, not removed:
// `git worktree remove` refuses one without --force, which the lifecycle
// never uses. Its needs-you snapshot keeps the work safe, and it stays slim.
//
// Returns the record written.
func Retire(ctx context.Context, env *Env, f *Facts, plan Plan, s Settings, o ExecOptions) (*Record, error) {
	if paused, _ := env.Paused(); paused {
		return nil, ErrPaused
	}
	fresh, freshPlan, why := recheck(ctx, env, f, ActionRetire, s, o.Force)
	if why != "" {
		return nil, skipped(env, ActionRetire, f, plan.Forced, why)
	}
	if fresh.Dirty > 0 || fresh.Untracked > 0 {
		ref := ""
		if fresh.Entry != nil && fresh.Entry.SnapshotRef != "" {
			ref = "; work is in " + fresh.Entry.SnapshotRef
		}
		return nil, skipped(env, ActionRetire, f, plan.Forced,
			GitSummary(fresh)+": git worktree remove would need --force, which the lifecycle never uses"+ref)
	}
	if fresh.RepoRoot == "" || fresh.Head == "" {
		return nil, skipped(env, ActionRetire, f, plan.Forced, "repo root or HEAD unknown")
	}
	if fresh.Conductor != nil && fresh.Conductor.IsRoot {
		return nil, skipped(env, ActionRetire, f, plan.Forced, "conductor project root")
	}

	now := env.Now()
	id := NewRecordID(fresh, now)
	rec := &Record{
		ID:        id,
		Path:      fresh.Path,
		RepoRoot:  fresh.RepoRoot,
		Remote:    fresh.Remote,
		Branch:    fresh.Branch,
		HeadSHA:   fresh.Head,
		Conductor: fresh.Conductor,
		Lockfile:  fresh.Lockfile,
		RetiredAt: now.UTC(),
		Reason:    freshPlan.Reason,
	}
	for _, t := range fresh.Threads {
		rec.T3Threads = append(rec.T3Threads, t.ID)
	}
	for _, st := range []string{"MERGED", "CLOSED"} {
		if p := prOf(fresh, st); p.Number > 0 {
			pr := p
			rec.PR = &pr
			break
		}
	}

	if o.DryRun {
		_ = env.LogEvent(Event{Action: ActionRetire, Path: f.Path, Result: "dry-run", RecordID: id, Reason: rec.Reason, Forced: plan.Forced})
		return rec, nil
	}

	fail := func(stage string, err error) (*Record, error) {
		err = fmt.Errorf("%s: %w", stage, err)
		_ = env.LogEvent(Event{Action: ActionRetire, Path: f.Path, Result: "failed", Error: err.Error(), RecordID: id, Forced: plan.Forced})
		recordOutcome(env, f.Path, ActionRetire, err.Error(), nil)
		return nil, err
	}

	// 2. graveyard ref.
	if fresh.HasUnsavedWork() {
		snap, err := Snapshot(ctx, env, fresh, id, o)
		if err != nil {
			return fail("snapshot", err)
		}
		if snap.SHA != fresh.Head {
			rec.SnapshotSHA = snap.SHA
		}
	} else if err := CreateGraveyardRef(ctx, fresh.RepoRoot, id, fresh.Head); err != nil {
		return fail("graveyard ref", err)
	}
	rec.GraveyardRef = GraveyardRef(id)
	if _, ok := ReadGraveyardRef(ctx, fresh.RepoRoot, id); !ok {
		return fail("graveyard ref", errors.New(rec.GraveyardRef+" did not read back"))
	}

	// 3. ignored, non-regenerable files.
	if err := os.MkdirAll(env.RecordDir(id), 0o700); err != nil {
		return fail("record dir", err)
	}
	archived, skippedPaths, err := archiveIgnored(ctx, fresh.Path, filepath.Join(env.RecordDir(id), IgnoredArchiveName), s)
	if err != nil {
		_ = os.RemoveAll(env.RecordDir(id))
		return fail("archive ignored files", err)
	}
	rec.ArchivedPaths, rec.SkippedPaths = archived, skippedPaths
	if len(archived) > 0 {
		rec.IgnoredArchive = IgnoredArchiveName
	}
	if n, err := OwnBytes(fresh.Path); err == nil {
		rec.FreedBytes = n
	}

	// 4. release conductor resources, close windows, re-check processes.
	if c := fresh.Conductor; c != nil && !c.Archived && !c.Hibernated {
		if err := releaseConductor(c); err != nil {
			_ = os.RemoveAll(env.RecordDir(id))
			return fail("release conductor resources", err)
		}
	}
	closeDevWindows(ctx, fresh.DevWindows)
	if busy := busyInside(ctx, fresh.Path); busy != "" {
		_ = os.RemoveAll(env.RecordDir(id))
		return nil, skipped(env, ActionRetire, f, plan.Forced, busy)
	}
	if head, err := fxGit(ctx, fresh.Path, "rev-parse", "HEAD"); err != nil || head != fresh.Head {
		_ = os.RemoveAll(env.RecordDir(id))
		return nil, skipped(env, ActionRetire, f, plan.Forced, "HEAD moved while retiring")
	}

	// 5. the record, durably, before anything is removed.
	if err := env.WriteRecord(rec); err != nil {
		_ = os.RemoveAll(env.RecordDir(id))
		return fail("write record", err)
	}

	// 6. remove.
	if err := removeWorktree(ctx, env, fresh.RepoRoot, fresh.Path); err != nil {
		if exists(filepath.Join(fresh.Path, ".git")) {
			// Nothing was removed: the record would only mislead revive.
			_ = os.Rename(env.RecordPath(id), filepath.Join(env.RecordDir(id), "record.aborted.json"))
		}
		return fail("git worktree remove", err)
	}

	_ = env.LogEvent(Event{Action: ActionRetire, Path: f.Path, Result: "ok", RecordID: id, SHA: firstNonEmpty(rec.SnapshotSHA, rec.HeadSHA),
		FreedBytes: rec.FreedBytes, Reason: rec.Reason, Forced: plan.Forced})
	recordOutcome(env, f.Path, ActionRetire, "", func(en *Entry) {
		en.State, en.Reason = StateRetired, rec.Reason
		en.RecordID, en.RetiredAt = id, rec.RetiredAt
	})
	return rec, nil
}

// releaseConductor hibernates a registered worktree: ports, database and
// tunnel go, the entry stays (what `conductor worktree hibernate` does).
func releaseConductor(c *ConductorRef) error {
	s, err := store.Load()
	if err != nil {
		return err
	}
	defer func() { _, _ = s.Close() }()
	m := workspace.NewManagerWithStore(s.GetConfigSnapshot(), s)
	return m.ReleaseResources(c.Project, c.Name)
}

// removeWorktree is the interim script's remove_worktree: `git worktree
// remove` (never --force). git has already checked the tree is clean, so if it
// took the .git link but left the directory behind, what is left was written
// during the removal (conductor's context files, a dev server's cache), and is
// cleared too. Then `git worktree prune`.
func removeWorktree(ctx context.Context, env *Env, repoRoot, path string) error {
	_, err := gitRun(ctx, repoRoot, 10*time.Minute, nil, "worktree", "remove", path)
	if err != nil {
		if info, serr := os.Stat(path); serr == nil && info.IsDir() && !exists(filepath.Join(path, ".git")) {
			left := listSome(path, 4)
			_ = os.RemoveAll(path)
			if !exists(path) {
				_ = env.LogEvent(Event{Action: ActionRetire, Path: path, Result: "ok", Reason: "cleared leftovers written during removal: " + strings.Join(left, ", ")})
				err = nil
			}
		}
	}
	_, _ = gitRun(ctx, repoRoot, time.Minute, nil, "worktree", "prune")
	if err == nil && exists(path) {
		err = errors.New("directory still present after removal")
	}
	return err
}

func listSome(dir string, n int) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		if len(out) == n {
			out = append(out, "…")
			break
		}
		out = append(out, e.Name())
	}
	return out
}

// archiveIgnored writes the worktree's gitignored files that are not
// regenerable (not on the slim allowlist) into dest as a tar.gz, up to
// s.IgnoredArchiveCapMB in total. Returns the archived and skipped paths;
// writes nothing when there is nothing to archive.
func archiveIgnored(ctx context.Context, root, dest string, s Settings) (archived, skipped []string, err error) {
	out, err := gitRun(ctx, root, 2*time.Minute, nil, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return nil, nil, err
	}
	regen := map[string]bool{".git": true}
	for _, a := range s.SlimAllowlist {
		regen[a] = true
	}
	for _, n := range DefaultSlimAllowlist {
		regen[n] = true
	}
	capBytes := int64(s.IgnoredArchiveCapMB) << 20

	var entries []string
	for _, e := range strings.Split(out, "\x00") {
		if e = strings.TrimSuffix(e, "/"); e != "" {
			entries = append(entries, e)
		}
	}
	sort.Strings(entries)
	// git lists a directory whose whole content is ignored and, sometimes,
	// entries below it as well; keep the outermost.
	var top []string
	for _, e := range entries {
		if n := len(top); n > 0 && inside(e, top[n-1]) {
			continue
		}
		top = append(top, e)
	}

	isRegen := func(rel string) bool {
		for _, part := range strings.Split(rel, "/") {
			if regen[part] {
				return true
			}
		}
		return false
	}
	var total int64
	var keep []string
	for _, rel := range top {
		if isRegen(rel) {
			skipped = append(skipped, rel+" (regenerable)")
			continue
		}
		full := filepath.Join(root, rel)
		if !safeRelOrLink(root, rel) {
			skipped = append(skipped, rel+" (outside the worktree)")
			continue
		}
		n, over := treeBytes(full, regen, capBytes)
		switch {
		case over:
			skipped = append(skipped, fmt.Sprintf("%s (over %d MB cap)", rel, s.IgnoredArchiveCapMB))
		case total+n > capBytes:
			skipped = append(skipped, fmt.Sprintf("%s (%s, archive cap reached)", rel, humanBytes(n)))
		default:
			total += n
			keep = append(keep, rel)
		}
	}
	if len(keep) == 0 {
		return nil, skipped, nil
	}
	if err := writeTarGz(root, dest, keep, regen); err != nil {
		return nil, nil, err
	}
	return keep, skipped, nil
}

// safeRelOrLink is safeRel for an archive entry, which may itself be a symlink
// (archived as a link, never followed); its parents must be real directories.
func safeRelOrLink(root, rel string) bool {
	if rel == "" || filepath.IsAbs(rel) || filepath.Clean(rel) != rel || strings.HasPrefix(rel, "..") {
		return false
	}
	parent := filepath.Dir(rel)
	return parent == "." || safeRel(root, parent)
}

// treeBytes sums regular-file sizes under path (not following symlinks,
// skipping regenerable dirs), stopping once it passes limit.
func treeBytes(path string, regen map[string]bool, limit int64) (int64, bool) {
	var n int64
	stop := errors.New("over")
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != path && regen[d.Name()] {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		if n > limit {
			return stop
		}
		return nil
	})
	return n, errors.Is(err, stop) || n > limit
}

// writeTarGz archives rels (relative to root) into dest, durably.
func writeTarGz(root, dest string, rels []string, regen map[string]bool) error {
	tmp := dest + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, rel := range rels {
		base := filepath.Join(root, rel)
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && p != base && regen[d.Name()] {
				return filepath.SkipDir
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				if link, err = os.Readlink(p); err != nil {
					return err
				}
			} else if !info.Mode().IsRegular() && !info.IsDir() {
				return nil // sockets, fifos, devices
			}
			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			name, _ := filepath.Rel(root, p)
			hdr.Name = filepath.ToSlash(name)
			if info.IsDir() {
				hdr.Name += "/"
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				src, err := os.Open(p)
				if err != nil {
					return err
				}
				_, err = io.Copy(tw, src)
				_ = src.Close()
				return err
			}
			return nil
		})
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("archive %s: %w", rel, err)
		}
	}
	if err := tw.Close(); err != nil {
		_ = file.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(dest)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
