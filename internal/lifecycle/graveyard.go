package lifecycle

import (
	"context"
	"fmt"
	"strings"
)

// GraveyardRefPrefix is where retired worktrees keep their commits alive in
// the main repo. Local only: nothing is ever pushed.
const GraveyardRefPrefix = "refs/graveyard/"

// GraveyardRef is refs/graveyard/<id>.
func GraveyardRef(id string) string { return GraveyardRefPrefix + id }

// CreateGraveyardRef points refs/graveyard/<id> in repoRoot at sha. It refuses
// to move an existing ref (update-ref with an empty old value), so a record id
// can never silently re-point at different commits.
func CreateGraveyardRef(ctx context.Context, repoRoot, id, sha string) error {
	if sha == "" {
		return fmt.Errorf("graveyard ref %s: empty sha", id)
	}
	ref := GraveyardRef(id)
	if _, err := fxGit(ctx, repoRoot, "check-ref-format", ref); err != nil {
		return fmt.Errorf("invalid graveyard ref %s: %w", ref, err)
	}
	if _, err := fxGit(ctx, repoRoot, "update-ref", "-m", "conductor lifecycle: retire", ref, sha, ""); err != nil {
		return fmt.Errorf("create %s: %w", ref, err)
	}
	return nil
}

// ReadGraveyardRef returns the sha refs/graveyard/<id> points at, or "" and false.
func ReadGraveyardRef(ctx context.Context, repoRoot, id string) (string, bool) {
	out, err := fxGit(ctx, repoRoot, "rev-parse", "--verify", "--quiet", GraveyardRef(id)+"^{commit}")
	if err != nil || out == "" {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// ListGraveyardRefs returns id → sha for every graveyard ref in repoRoot.
func ListGraveyardRefs(ctx context.Context, repoRoot string) (map[string]string, error) {
	out, err := fxGit(ctx, repoRoot, "for-each-ref", "--format=%(refname) %(objectname)", GraveyardRefPrefix)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		name, sha, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			refs[strings.TrimPrefix(name, GraveyardRefPrefix)] = sha
		}
	}
	return refs, nil
}
