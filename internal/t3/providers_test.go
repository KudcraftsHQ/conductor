package t3

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeAgent mirrors the shape of a ready, signed-in Claude instance as the
// running T3 build reports it.
func claudeAgent() ProviderInstance {
	return ProviderInstance{
		InstanceID: "claudeAgent",
		Driver:     "claudeAgent",
		Enabled:    true,
		Installed:  true,
		Status:     "ready",
		Models: []ProviderModel{
			{Slug: "claude-fable-5"},
			{Slug: "claude-opus-5"},
		},
	}
}

// disabledCodex mirrors an instance the user has switched off. Most projects on
// this machine still name it as their default, which is why a project default
// can never be trusted without validation.
func disabledCodex() ProviderInstance {
	return ProviderInstance{
		InstanceID: "codex",
		Driver:     "codex",
		Enabled:    false,
		Installed:  false,
		Status:     "disabled",
	}
}

// The bug this whole file exists for: conductor hardcoded an instance id
// ('claude-code') that no T3 build has. thread.create accepted it, so the
// thread looked healthy and only failed when its first turn tried to start,
// where nothing was reading the error. Selection must reject it outright.
func TestValidateModelSelectionRejectsUnknownInstance(t *testing.T) {
	instances := []ProviderInstance{claudeAgent(), disabledCodex()}

	err := ValidateModelSelection(instances, ModelSelection{
		InstanceID: "claude-code", Model: "claude-opus-5",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured in this T3 build")
	// The message must name what is available, or the next person debugging
	// this is back to guessing.
	assert.Contains(t, err.Error(), "claudeAgent")
}

func TestValidateModelSelectionRejectsDisabledInstance(t *testing.T) {
	err := ValidateModelSelection([]ProviderInstance{claudeAgent(), disabledCodex()},
		ModelSelection{InstanceID: "codex", Model: "gpt-5.6-sol"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not usable")
}

func TestValidateModelSelectionRejectsUnknownModel(t *testing.T) {
	err := ValidateModelSelection([]ProviderInstance{claudeAgent()},
		ModelSelection{InstanceID: "claudeAgent", Model: "gpt-4"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not offer model")
}

func TestValidateModelSelectionAcceptsUsable(t *testing.T) {
	assert.NoError(t, ValidateModelSelection([]ProviderInstance{claudeAgent()},
		ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5"}))
}

// An instance reporting no models is uninformative, not contradictory: a build
// whose registry omits them must not be treated as offering nothing.
func TestHasModelAcceptsAnythingWhenNoModelsReported(t *testing.T) {
	instance := ProviderInstance{
		InstanceID: "custom", Driver: "custom", Enabled: true, Installed: true, Status: "ready",
	}
	assert.True(t, instance.HasModel("whatever"))
}

// availability is optional and older producers omit it. Absent must read as
// available, or conductor would refuse every instance on such a build.
func TestUsableTreatsAbsentAvailabilityAsAvailable(t *testing.T) {
	assert.True(t, claudeAgent().Usable())

	unavailable := claudeAgent()
	unavailable.Availability = "unavailable"
	assert.False(t, unavailable.Usable())
}

func TestSelectModelPrefersFirstValidCandidate(t *testing.T) {
	instances := []ProviderInstance{claudeAgent(), disabledCodex()}

	selection, err := SelectModel(instances, []ModelSelection{
		{InstanceID: "codex", Model: "gpt-5.6-sol"},         // disabled, skipped
		{InstanceID: "claudeAgent", Model: "claude-opus-5"}, // valid, wins
		{InstanceID: "claudeAgent", Model: "claude-fable-5"},
	})
	require.NoError(t, err)
	assert.Equal(t, ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5"}, selection)
}

// A candidate whose instance is usable but whose model is unknown keeps the
// instance: switching provider under the user is the bigger surprise.
func TestSelectModelRepairsUnknownModelOnUsableInstance(t *testing.T) {
	selection, err := SelectModel([]ProviderInstance{claudeAgent()}, []ModelSelection{
		{InstanceID: "claudeAgent", Model: "claude-opus-9000"},
	})
	require.NoError(t, err)
	assert.Equal(t, "claudeAgent", selection.InstanceID)
	assert.Equal(t, "claude-fable-5", selection.Model)
}

func TestSelectModelFallsBackToPreferredUsableInstance(t *testing.T) {
	grok := ProviderInstance{
		InstanceID: "grok", Driver: "grok", Enabled: true, Installed: true, Status: "ready",
		Models: []ProviderModel{{Slug: "grok-build"}},
	}
	// No candidate is usable, so the preferred driver order decides. claudeAgent
	// outranks grok.
	selection, err := SelectModel([]ProviderInstance{grok, claudeAgent()}, []ModelSelection{
		{InstanceID: "codex", Model: "gpt-5.6-sol"},
	})
	require.NoError(t, err)
	assert.Equal(t, "claudeAgent", selection.InstanceID)
}

// Better to refuse than to create a thread that cannot possibly run.
func TestSelectModelFailsWhenNothingIsUsable(t *testing.T) {
	_, err := SelectModel([]ProviderInstance{disabledCodex()}, []ModelSelection{
		{InstanceID: "codex", Model: "gpt-5.6-sol"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no T3 provider instance is usable")
}

func TestModelSelectionFromEnv(t *testing.T) {
	t.Setenv("CONDUCTOR_T3_MODEL", "claudeAgent/claude-opus-5")
	selection, ok := ModelSelectionFromEnv()
	require.True(t, ok)
	assert.Equal(t, ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5"}, selection)

	// Without the "instance/model" separator there is nothing to route on.
	t.Setenv("CONDUCTOR_T3_MODEL", "claude-opus-5")
	_, ok = ModelSelectionFromEnv()
	assert.False(t, ok)

	t.Setenv("CONDUCTOR_T3_MODEL", "")
	_, ok = ModelSelectionFromEnv()
	assert.False(t, ok)
}

// The override has to be able to name the effort, not just the model: effort is
// what the reasoning costs, and without it T3 applies the descriptor default,
// which is "high" on every current Claude model.
func TestModelSelectionFromEnvParsesOptions(t *testing.T) {
	t.Setenv("CONDUCTOR_T3_MODEL", "claudeAgent/claude-opus-5?effort=medium&contextWindow=200k&fastMode=true")
	selection, ok := ModelSelectionFromEnv()
	require.True(t, ok)
	assert.Equal(t, ModelSelection{
		InstanceID: "claudeAgent",
		Model:      "claude-opus-5",
		Options: []ModelOption{
			{ID: "effort", Value: "medium"},
			{ID: "contextWindow", Value: "200k"},
			{ID: "fastMode", Value: true}, // typed: T3's schema rejects "true"
		},
	}, selection)

	// A trailing '?' with nothing after it is the same as no options at all.
	t.Setenv("CONDUCTOR_T3_MODEL", "claudeAgent/claude-opus-5?")
	selection, ok = ModelSelectionFromEnv()
	require.True(t, ok)
	assert.Nil(t, selection.Options)

	// Junk in one pair costs that pair, not the whole override.
	t.Setenv("CONDUCTOR_T3_MODEL", "claudeAgent/claude-opus-5?effort=medium&garbage&empty=")
	selection, ok = ModelSelectionFromEnv()
	require.True(t, ok)
	assert.Equal(t, []ModelOption{{ID: "effort", Value: "medium"}}, selection.Options)
}

// Options ride along on thread.create, or the thread runs at the model's
// default effort no matter what was chosen upstream.
func TestModelSelectionMarshalsOptions(t *testing.T) {
	encoded, err := json.Marshal(ModelSelection{
		InstanceID: "claudeAgent",
		Model:      "claude-opus-5",
		Options:    []ModelOption{{ID: "effort", Value: "medium"}},
	})
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"instanceId":"claudeAgent","model":"claude-opus-5",`+
			`"options":[{"id":"effort","value":"medium"}]}`,
		string(encoded))

	// Absent rather than null when there are none: T3 distinguishes "no options
	// given" (use the descriptor defaults) from an empty selection list.
	encoded, err = json.Marshal(ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"instanceId":"claudeAgent","model":"claude-opus-5"}`, string(encoded))
}

// A project default carries its options, and they must survive the trip into
// conductor rather than being dropped on decode — that silent drop is what made
// every conductor thread reason at "high".
func TestModelSelectionDecodesOptions(t *testing.T) {
	var selection ModelSelection
	require.NoError(t, json.Unmarshal([]byte(
		`{"instanceId":"claudeAgent","model":"claude-opus-5",`+
			`"options":[{"id":"effort","value":"medium"},{"id":"fastMode","value":false}]}`), &selection))
	assert.Equal(t, []ModelOption{
		{ID: "effort", Value: "medium"},
		{ID: "fastMode", Value: false},
	}, selection.Options)
	assert.Equal(t, "claudeAgent/claude-opus-5?effort=medium&fastMode=false", selection.String())
}

// Options belong to the model they were chosen for. When SelectModel keeps a
// usable instance but has to substitute the model, they cannot come with it.
func TestSelectModelDropsOptionsWhenSubstitutingModel(t *testing.T) {
	selection, err := SelectModel([]ProviderInstance{claudeAgent()}, []ModelSelection{
		{InstanceID: "claudeAgent", Model: "claude-opus-9000",
			Options: []ModelOption{{ID: "effort", Value: "xhigh"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-fable-5", selection.Model)
	assert.Nil(t, selection.Options)
}

// A candidate that validates is passed through whole, options included.
func TestSelectModelKeepsOptionsOfValidCandidate(t *testing.T) {
	candidate := ModelSelection{
		InstanceID: "claudeAgent",
		Model:      "claude-opus-5",
		Options:    []ModelOption{{ID: "effort", Value: "medium"}},
	}
	selection, err := SelectModel([]ProviderInstance{claudeAgent()}, []ModelSelection{candidate})
	require.NoError(t, err)
	assert.Equal(t, candidate, selection)
}

// Effort is part of what a live thread proves works, so two threads on the same
// model at different efforts are counted separately. Keying on the model alone
// let whichever was seen first dictate the effort for both.
func TestSelectionsInUseCountsOptionsSeparately(t *testing.T) {
	turn := json.RawMessage(`{"id":"t"}`)
	medium := ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5",
		Options: []ModelOption{{ID: "effort", Value: "medium"}}}
	high := ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5",
		Options: []ModelOption{{ID: "effort", Value: "high"}}}

	snapshot := &ShellSnapshot{Threads: []Thread{
		{ID: "a", ModelSelection: high, LatestTurn: turn},
		{ID: "b", ModelSelection: medium, LatestTurn: turn},
		{ID: "c", ModelSelection: medium, LatestTurn: turn},
	}}

	inUse := snapshot.SelectionsInUse()
	require.Len(t, inUse, 2)
	assert.Equal(t, medium, inUse[0]) // two threads beat one
	assert.Equal(t, high, inUse[1])
}

// Threads that have run are evidence of what works in this build, which is
// stronger than anything configuration can say. Threads that never started are
// not evidence — counting them would have recycled the broken 'claude-code'
// selection forever.
func TestSelectionsInUseOnlyCountsStartedThreads(t *testing.T) {
	snapshot := &ShellSnapshot{Threads: []Thread{
		{ID: "broken", ModelSelection: ModelSelection{InstanceID: "claude-code", Model: "claude-opus-5"}},
		{ID: "ran-1", ModelSelection: ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5"},
			LatestTurn: json.RawMessage(`{"id":"t1"}`)},
		{ID: "ran-2", ModelSelection: ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5"},
			LatestTurn: json.RawMessage(`{"id":"t2"}`)},
		{ID: "archived", ModelSelection: ModelSelection{InstanceID: "grok", Model: "grok-build"},
			LatestTurn: json.RawMessage(`{"id":"t3"}`), ArchivedAt: strptr("2026-01-01T00:00:00Z")},
	}}

	inUse := snapshot.SelectionsInUse()
	require.Len(t, inUse, 1)
	assert.Equal(t, ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5"}, inUse[0])
}

// A literal JSON null is what T3 sends for a thread with no turn; it must not
// be mistaken for a turn just because the field is present.
func TestStartedIgnoresNullLatestTurn(t *testing.T) {
	assert.False(t, Thread{LatestTurn: json.RawMessage(`null`)}.Started())
	assert.False(t, Thread{}.Started())
	assert.True(t, Thread{LatestTurn: json.RawMessage(`{"id":"t1"}`)}.Started())

	// An active turn id counts even before latestTurn is projected.
	turnID := "turn-1"
	assert.True(t, Thread{Session: &ThreadSession{ActiveTurnID: &turnID}}.Started())
}

// The failure that hid for so long: session.status "error" with the reason in
// lastError, and no error anywhere on the dispatch that caused it.
func TestFailedReportsSessionError(t *testing.T) {
	reason := "Thread 'x' references unknown provider instance 'claude-code'."
	got, failed := Thread{Session: &ThreadSession{Status: "error", LastError: &reason}}.Failed()
	require.True(t, failed)
	assert.Equal(t, reason, got)

	_, failed = Thread{Session: &ThreadSession{Status: "ready"}}.Failed()
	assert.False(t, failed)

	_, failed = Thread{}.Failed()
	assert.False(t, failed)
}

// An errored session with no reason still has to fail, not pass silently.
func TestFailedReportsErrorWithoutReason(t *testing.T) {
	got, failed := Thread{Session: &ThreadSession{Status: "error"}}.Failed()
	require.True(t, failed)
	assert.NotEmpty(t, got)
}

// Decoding must survive the real payload, including the fields conductor does
// not model.
func TestProviderInstanceDecodesServerPayload(t *testing.T) {
	payload := []byte(`{
	  "instanceId": "claudeAgent",
	  "driver": "claudeAgent",
	  "enabled": true,
	  "installed": true,
	  "version": null,
	  "status": "ready",
	  "auth": {"status": "authenticated"},
	  "checkedAt": "2026-08-11T04:00:00.000Z",
	  "models": [{"slug": "claude-fable-5", "name": "Claude Fable 5", "isCustom": false,
	              "capabilities": {"optionDescriptors": []}}],
	  "slashCommands": [], "skills": []
	}`)

	var instance ProviderInstance
	require.NoError(t, json.Unmarshal(payload, &instance))
	assert.Equal(t, "claudeAgent", instance.InstanceID)
	assert.Equal(t, "authenticated", instance.Auth.Status)
	assert.True(t, instance.Usable())
	assert.True(t, instance.HasModel("claude-fable-5"))
	assert.Equal(t, "claude-fable-5", instance.DefaultModel())
}
