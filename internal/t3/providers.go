package t3

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// A thread's model selection names a *provider instance*, and an instance id
// that the running build does not have is the single most damaging thing
// conductor can get wrong here.
//
// It fails silently in the worst possible way: thread.create validates the
// selection's shape but not its existence, so the command bus accepts it and
// the thread appears in the UI looking healthy. The instance is only resolved
// later, when the first turn tries to start, and the failure lands in
// thread.session.lastError where nothing was looking:
//
//	Thread '…' references unknown provider instance 'claude-code'.
//	The instance is not configured in this build.
//
// From outside, that is indistinguishable from a thread that ignores
// programmatic turns — the thread exists, the turn dispatches with a 2xx, and
// latestTurn simply stays null forever. Conductor previously hardcoded
// 'claude-code' as its default instance, which no T3 build has ever had (the
// Claude driver's instance id is 'claudeAgent'), so *every* thread conductor
// created was born unable to run.
//
// So instances are discovered rather than assumed, every candidate selection is
// validated against the live registry before use, and a thread is never created
// against a selection that cannot run.

// preferredDrivers orders provider drivers by conductor's preference, used when
// nothing else picks one. Anything installed is better than nothing, so this is
// a preference and not a filter.
var preferredDrivers = []string{"claudeAgent", "codex", "cursor", "grok", "opencode"}

// ProviderModel is one model offered by a provider instance.
type ProviderModel struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// ProviderInstance is one configured provider in the running T3 build.
type ProviderInstance struct {
	InstanceID   string          `json:"instanceId"`
	Driver       string          `json:"driver"`
	Enabled      bool            `json:"enabled"`
	Installed    bool            `json:"installed"`
	Status       string          `json:"status"`
	Availability string          `json:"availability"`
	Models       []ProviderModel `json:"models"`
	Auth         struct {
		Status string `json:"status"`
	} `json:"auth"`
}

// Usable reports whether a thread created against this instance can actually
// run a turn.
//
// An absent availability means "available": the field is optional and older
// producers omit it, so a missing value must not read as unavailable.
func (p ProviderInstance) Usable() bool {
	return p.Enabled && p.Installed &&
		p.Availability != "unavailable" &&
		p.Status != "disabled" && p.Status != "not-installed"
}

// HasModel reports whether the instance offers a model by slug. An instance
// that reports no models at all is not contradicted, only uninformative, so it
// accepts anything.
func (p ProviderInstance) HasModel(slug string) bool {
	if len(p.Models) == 0 {
		return true
	}
	for _, m := range p.Models {
		if m.Slug == slug {
			return true
		}
	}
	return false
}

// DefaultModel returns the instance's first model, or "" when it lists none.
func (p ProviderInstance) DefaultModel() string {
	if len(p.Models) == 0 {
		return ""
	}
	return p.Models[0].Slug
}

// serverConfigEvent is the first value of the subscribeServerConfig stream.
type serverConfigEvent struct {
	Type   string `json:"type"`
	Config struct {
		Providers []ProviderInstance `json:"providers"`
	} `json:"config"`
}

// ProviderInstances returns every provider instance the running build has
// configured, usable or not.
//
// This goes over the WebSocket because there is no HTTP route for it: the
// registry is only published on the subscribeServerConfig subscription, whose
// first frame is a full config snapshot. Reading T3's settings.json instead
// would be wrong — it records only *overrides*, so a built-in instance the user
// never touched does not appear there at all.
func (c *Client) ProviderInstances(ctx context.Context) ([]ProviderInstance, error) {
	conn, err := c.Dial(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	var event serverConfigEvent
	if err := conn.CallStream(ctx, MethodSubscribeServerConfig, map[string]any{}, &event); err != nil {
		return nil, fmt.Errorf("failed to read T3's provider registry: %w", err)
	}
	if len(event.Config.Providers) == 0 {
		return nil, fmt.Errorf("T3 reported no configured provider instances")
	}
	return event.Config.Providers, nil
}

// UsableInstances returns the instances that can run a turn, in conductor's
// preference order.
func UsableInstances(instances []ProviderInstance) []ProviderInstance {
	var usable []ProviderInstance
	for _, instance := range instances {
		if instance.Usable() {
			usable = append(usable, instance)
		}
	}
	rank := func(driver string) int {
		for i, name := range preferredDrivers {
			if name == driver {
				return i
			}
		}
		return len(preferredDrivers)
	}
	sort.SliceStable(usable, func(i, j int) bool {
		return rank(usable[i].Driver) < rank(usable[j].Driver)
	})
	return usable
}

// ValidateModelSelection reports why a selection cannot run, or nil if it can.
func ValidateModelSelection(instances []ProviderInstance, selection ModelSelection) error {
	if selection.InstanceID == "" || selection.Model == "" {
		return fmt.Errorf("incomplete model selection")
	}
	for _, instance := range instances {
		if instance.InstanceID != selection.InstanceID {
			continue
		}
		if !instance.Usable() {
			return fmt.Errorf("provider instance %q is configured but not usable (enabled=%t installed=%t status=%q)",
				instance.InstanceID, instance.Enabled, instance.Installed, instance.Status)
		}
		if !instance.HasModel(selection.Model) {
			return fmt.Errorf("provider instance %q does not offer model %q (has: %s)",
				instance.InstanceID, selection.Model, strings.Join(modelSlugs(instance), ", "))
		}
		return nil
	}
	return fmt.Errorf("provider instance %q is not configured in this T3 build (configured: %s)",
		selection.InstanceID, strings.Join(instanceIDs(instances), ", "))
}

// SelectModel picks the first candidate selection that can actually run.
//
// Candidates are ordered by the caller from most to least specific — an
// explicit request, then the project's default, then whatever the rest of T3 is
// already using. A candidate naming a usable instance but an unknown model is
// repaired rather than discarded, since the instance is the part that decides
// whether anything runs at all. When no candidate survives, the most preferred
// usable instance's default model is used, and only a build with no usable
// provider at all is an error.
func SelectModel(instances []ProviderInstance, candidates []ModelSelection) (ModelSelection, error) {
	usable := UsableInstances(instances)
	if len(usable) == 0 {
		return ModelSelection{}, fmt.Errorf(
			"no T3 provider instance is usable (configured: %s). Enable and sign in to one in T3's settings",
			strings.Join(instanceIDs(instances), ", "))
	}

	for _, candidate := range candidates {
		if candidate.InstanceID == "" {
			continue
		}
		if ValidateModelSelection(instances, candidate) == nil {
			return candidate, nil
		}
		// The instance is usable; only the model is unknown to it. Keep the
		// instance — switching provider silently would be the bigger surprise.
		for _, instance := range usable {
			if instance.InstanceID == candidate.InstanceID && instance.DefaultModel() != "" {
				return ModelSelection{InstanceID: instance.InstanceID, Model: instance.DefaultModel()}, nil
			}
		}
	}

	best := usable[0]
	if best.DefaultModel() == "" {
		return ModelSelection{}, fmt.Errorf("T3 provider instance %q reports no models", best.InstanceID)
	}
	return ModelSelection{InstanceID: best.InstanceID, Model: best.DefaultModel()}, nil
}

// ModelSelectionFromEnv reads a manual override from CONDUCTOR_T3_MODEL, in
// "instance/model" form with optional provider options appended as a query
// string. It is the escape hatch for a build whose registry conductor reads
// wrongly, and for pinning the knobs that cost money:
//
//	CONDUCTOR_T3_MODEL=claudeAgent/claude-opus-5
//	CONDUCTOR_T3_MODEL=claudeAgent/claude-opus-5?effort=medium
//	CONDUCTOR_T3_MODEL=claudeAgent/claude-opus-5?effort=medium&contextWindow=200k
//
// Option ids are the provider's own ("effort", "contextWindow", "fastMode"),
// passed through unchanged; conductor does not know their vocabulary and does
// not try to. "true"/"false" become booleans because T3's toggle options are
// typed, and a quoted "true" is rejected by its schema.
func ModelSelectionFromEnv() (ModelSelection, bool) {
	raw := strings.TrimSpace(os.Getenv("CONDUCTOR_T3_MODEL"))
	if raw == "" {
		return ModelSelection{}, false
	}
	spec, query, _ := strings.Cut(raw, "?")
	instance, model, found := strings.Cut(spec, "/")
	if !found {
		return ModelSelection{}, false
	}
	instance, model = strings.TrimSpace(instance), strings.TrimSpace(model)
	if instance == "" || model == "" {
		return ModelSelection{}, false
	}
	return ModelSelection{
		InstanceID: instance,
		Model:      model,
		Options:    parseModelOptions(query),
	}, true
}

// parseModelOptions reads "effort=medium&fastMode=true" into option
// selections. Malformed pairs are skipped rather than failing the whole
// override: losing one knob is better than falling back to a different model
// entirely, and an option T3 rejects is reported by T3 with its own vocabulary.
func parseModelOptions(query string) []ModelOption {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	var options []ModelOption
	for _, pair := range strings.Split(query, "&") {
		id, value, found := strings.Cut(pair, "=")
		id, value = strings.TrimSpace(id), strings.TrimSpace(value)
		if !found || id == "" || value == "" {
			continue
		}
		switch value {
		case "true":
			options = append(options, ModelOption{ID: id, Value: true})
		case "false":
			options = append(options, ModelOption{ID: id, Value: false})
		default:
			options = append(options, ModelOption{ID: id, Value: value})
		}
	}
	return options
}

// SelectionsInUse returns the model selections T3's own live threads are
// running, most used first.
//
// This is the strongest available evidence of what works in this build: a
// selection carried by a thread that has turns has been proven to run, which no
// amount of reading configuration can establish.
func (s *ShellSnapshot) SelectionsInUse() []ModelSelection {
	type tally struct {
		selection ModelSelection
		count     int
	}
	var order []string
	counts := make(map[string]*tally)
	for _, thread := range s.Threads {
		if thread.Archived() || !thread.Started() {
			continue
		}
		selection := thread.ModelSelection
		if selection.InstanceID == "" || selection.Model == "" {
			continue
		}
		// Options are part of the identity here: a build where most threads run
		// one model at "medium" is evidence for medium, and keying on the model
		// alone would let whichever thread happened to be seen first decide the
		// effort for all of them.
		key := selection.String()
		if counts[key] == nil {
			counts[key] = &tally{selection: selection}
			order = append(order, key)
		}
		counts[key].count++
	}
	sort.SliceStable(order, func(i, j int) bool {
		return counts[order[i]].count > counts[order[j]].count
	})
	out := make([]ModelSelection, 0, len(order))
	for _, key := range order {
		out = append(out, counts[key].selection)
	}
	return out
}

func instanceIDs(instances []ProviderInstance) []string {
	out := make([]string, 0, len(instances))
	for _, instance := range instances {
		out = append(out, instance.InstanceID)
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

func modelSlugs(instance ProviderInstance) []string {
	out := make([]string, 0, len(instance.Models))
	for _, model := range instance.Models {
		out = append(out, model.Slug)
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}
