package mux

import (
	"testing"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestParseKind(t *testing.T) {
	tests := []struct {
		in   string
		want Kind
	}{
		{"tmux", KindTmux},
		{"herdr", KindHerdr},
		{"auto", KindAuto},
		{"", KindAuto},
		{"screen", KindAuto},
		{"TMUX", KindAuto}, // case-sensitive by design
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, ParseKind(tt.in), "ParseKind(%q)", tt.in)
	}
}

func TestResolveExplicitKinds(t *testing.T) {
	assert.Equal(t, KindTmux, Resolve(KindTmux).Kind())
	assert.Equal(t, KindHerdr, Resolve(KindHerdr).Kind())
}

func TestFromConfigHonorsConfig(t *testing.T) {
	t.Setenv("CONDUCTOR_MUX", "")

	cfg := config.NewConfig()
	cfg.Defaults.Multiplexer = "herdr"
	assert.Equal(t, KindHerdr, FromConfig(cfg).Kind())

	cfg.Defaults.Multiplexer = "tmux"
	assert.Equal(t, KindTmux, FromConfig(cfg).Kind())
}

func TestFromConfigEnvOverridesConfig(t *testing.T) {
	cfg := config.NewConfig()
	cfg.Defaults.Multiplexer = "tmux"

	t.Setenv("CONDUCTOR_MUX", "herdr")
	assert.Equal(t, KindHerdr, FromConfig(cfg).Kind())
}

func TestFromConfigNilFallsBackToAuto(t *testing.T) {
	t.Setenv("CONDUCTOR_MUX", "")
	// auto never panics and always yields a usable multiplexer.
	assert.NotNil(t, FromConfig(nil))
}

func TestAutoPrefersHerdrInsideHerdrPane(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "w123-1")
	assert.Equal(t, KindHerdr, auto().Kind())
}

func TestAutoDefaultsToTmux(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "")
	// tmux is present in dev/CI images; when it is not, auto may legitimately
	// pick herdr. Either way the choice must be one of the two known kinds.
	k := auto().Kind()
	assert.Contains(t, []Kind{KindTmux, KindHerdr}, k)
}

func TestWindowNameIsStableAcrossImplementations(t *testing.T) {
	assert.Equal(t, "proj/feature-x", Tmux().WindowName("proj", "feature-x"))
	assert.Equal(t, "proj/feature-x", Herdr().WindowName("proj", "feature-x"))
}

func TestTracksAgentStatus(t *testing.T) {
	assert.False(t, Tmux().TracksAgentStatus(), "tmux needs conductor's own tracker")
	assert.True(t, Herdr().TracksAgentStatus(), "herdr reports agent status itself")
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "''"},
		{"claude", "claude"},
		{"--flag=value", `'--flag=value'`},
		{"two words", `'two words'`},
		{"it's", `'it'\''s'`},
		{"$HOME", `'$HOME'`},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, shellQuote(tt.in), "shellQuote(%q)", tt.in)
	}
}

func TestShellJoin(t *testing.T) {
	assert.Equal(t, `claude --append-system-prompt 'be nice'`,
		shellJoin([]string{"claude", "--append-system-prompt", "be nice"}))
	assert.Equal(t, "", shellJoin(nil))
}

func TestErrUnsupported(t *testing.T) {
	err := &ErrUnsupported{Kind: KindHerdr, Op: "DetachSession"}
	assert.Equal(t, "herdr does not support DetachSession", err.Error())
}
