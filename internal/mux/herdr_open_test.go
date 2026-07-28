package mux

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHerdrOpenPlanCreatesTerminalOnlyWorkspace(t *testing.T) {
	plan, err := newHerdrOpenPlan("kudtrading", "feature-x", "/worktree", HerdrOpenOptions{})
	require.NoError(t, err)

	assert.Equal(t, "kudtrading/feature-x", plan.Label)
	assert.Equal(t, []string{"workspace", "create", "--cwd", "/worktree", "--label", "kudtrading/feature-x", "--focus"}, plan.CreateArgs)
	assert.Empty(t, plan.RootCommand)
	assert.False(t, plan.NeedsDevPane)
}

func TestHerdrOpenPlanStartsInteractiveClaudeOnlyWhenRequested(t *testing.T) {
	plan, err := newHerdrOpenPlan("kudtrading", "feature-x", "/worktree", HerdrOpenOptions{Claude: true})
	require.NoError(t, err)

	assert.Equal(t, "claude", plan.RootLabel)
	assert.Contains(t, plan.RootCommand, "claude")
	assert.NotContains(t, plan.RootCommand, "--print")
	assert.False(t, plan.NeedsDevPane)
}

func TestHerdrOpenPlanStartsOneShotClaudeWithoutInteractiveMode(t *testing.T) {
	plan, err := newHerdrOpenPlan("kudtrading", "feature-x", "/worktree", HerdrOpenOptions{Prompt: "fix the totals"})
	require.NoError(t, err)

	assert.Equal(t, "claude", plan.RootLabel)
	assert.Equal(t, []string{"claude", "--print", "fix the totals"}, plan.RootCommand)
	assert.False(t, plan.NeedsDevPane)
}

func TestHerdrOpenPlanAddsConductorRunDevPane(t *testing.T) {
	plan, err := newHerdrOpenPlan("kudtrading", "feature-x", "/worktree", HerdrOpenOptions{Dev: true})
	require.NoError(t, err)

	assert.Equal(t, "terminal", plan.RootLabel)
	assert.True(t, plan.NeedsDevPane)
	assert.Equal(t, "conductor run", plan.DevCommand)
}

func TestHerdrOpenPlanRejectsInteractiveAndOneShotClaudeTogether(t *testing.T) {
	_, err := newHerdrOpenPlan("kudtrading", "feature-x", "/worktree", HerdrOpenOptions{Claude: true, Prompt: "fix it"})
	require.EqualError(t, err, "--claude and --prompt cannot be used together")
}
