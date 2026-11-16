package main

import (
	"os"
	"testing"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/stretchr/testify/assert"
)

// Under T3 orchestration V2 the adopt hook runs on every thread launch, so an
// already-registered worktree must only be provisioned when nothing else will
// and it is not already set up — provisioning drops and re-clones its database.
func TestPlanExisting(t *testing.T) {
	running := func(pid int, startedAgo time.Duration) *config.Worktree {
		return &config.Worktree{
			SetupStatus:    config.SetupStatusRunning,
			SetupPID:       pid,
			SetupStartedAt: time.Now().Add(-startedAgo),
		}
	}
	cases := []struct {
		name        string
		wt          *config.Worktree
		bind, force bool
		provision   bool
		root        bool
	}{
		{name: "set up", wt: &config.Worktree{SetupStatus: config.SetupStatusDone}},
		{name: "predates status tracking", wt: &config.Worktree{}},
		{name: "repository root", wt: &config.Worktree{IsRoot: true, SetupStatus: config.SetupStatusFailed}, root: true},
		{name: "provisioning in this very process", wt: running(os.Getpid(), time.Minute)},
		{name: "provisioning, even when forced", wt: running(os.Getpid(), time.Minute), force: true},
		{name: "hibernated is left to the watcher", wt: &config.Worktree{Hibernated: true, SetupStatus: config.SetupStatusDone}},
		{name: "failed is retried", wt: &config.Worktree{SetupStatus: config.SetupStatusFailed}, provision: true},
		{name: "failed but bind-only", wt: &config.Worktree{SetupStatus: config.SetupStatusFailed}, bind: true},
		{name: "stalled is retried", wt: running(0, 2*time.Hour), provision: true},
		{name: "forced re-provision", wt: &config.Worktree{SetupStatus: config.SetupStatusDone}, force: true, provision: true},
		{name: "vanished", wt: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planExisting(tc.wt, tc.bind, tc.force)
			assert.Equal(t, tc.provision, plan.provision, plan.reason)
			assert.Equal(t, tc.root, plan.root)
		})
	}
}
