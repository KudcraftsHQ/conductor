package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/stretchr/testify/require"
)

// fill sets every field reachable from v to a non-zero value, so a copy that
// forgets a field — or shares a pointer, slice or map — cannot pass.
func fill(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(7)
	case reflect.String:
		v.SetString(path)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Date(2026, 10, 3, 7, 46, 26, 0, time.UTC)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			fill(t, v.Field(i), path+"."+v.Type().Field(i).Name)
		}
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fill(t, p.Elem(), path)
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(t, s.Index(0), path+"[0]")
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		fill(t, k, path+".key")
		e := reflect.New(v.Type().Elem()).Elem()
		fill(t, e, path+"[key]")
		m.SetMapIndex(k, e)
		v.Set(m)
	default:
		t.Fatalf("fill: unhandled kind %s at %s", v.Kind(), path)
	}
}

// assertUnshared fails if any pointer, slice or map in a is the same memory as
// in b: a snapshot that aliases the store's state is not a snapshot.
func assertUnshared(t *testing.T, a, b reflect.Value, path string) {
	t.Helper()
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() {
			return
		}
		require.NotEqual(t, a.Pointer(), b.Pointer(), "%s is shared with the store", path)
		assertUnshared(t, a.Elem(), b.Elem(), path)
	case reflect.Slice:
		if a.Len() == 0 {
			return
		}
		require.NotEqual(t, a.Pointer(), b.Pointer(), "%s is shared with the store", path)
		for i := 0; i < a.Len(); i++ {
			assertUnshared(t, a.Index(i), b.Index(i), path)
		}
	case reflect.Map:
		if a.Len() == 0 {
			return
		}
		require.NotEqual(t, a.Pointer(), b.Pointer(), "%s is shared with the store", path)
		for _, k := range a.MapKeys() {
			assertUnshared(t, a.MapIndex(k), b.MapIndex(k), path+"[k]")
		}
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if a.Type().Field(i).IsExported() {
				assertUnshared(t, a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name)
			}
		}
	}
}

// GetConfigSnapshot used to copy worktrees field by field, and every field
// added after that list was written — Hibernated, T3Threads, SetupPID — came
// back zero. The T3 watcher reads state through it, so a hibernated worktree
// looked awake and was hibernated again on every tick.
func TestConfigSnapshotCopiesEveryField(t *testing.T) {
	var cfg config.Config
	fill(t, reflect.ValueOf(&cfg).Elem(), "Config")
	s := New(&cfg, WithDisableSave())
	defer func() { _, _ = s.Close() }()

	snapshot := s.GetConfigSnapshot()

	require.Equal(t, &cfg, snapshot)
	assertUnshared(t, reflect.ValueOf(snapshot).Elem(), reflect.ValueOf(&cfg).Elem(), "Config")
}

func TestConfigSnapshotSeesHibernation(t *testing.T) {
	s := newTestStore()
	defer func() { _, _ = s.Close() }()
	require.NoError(t, s.AddProject("kudtrading", &config.Project{Path: "/repo", Worktrees: map[string]*config.Worktree{}}))
	require.NoError(t, s.AddWorktree("kudtrading", "chicago-4", &config.Worktree{
		Path: "/wt", Ports: []int{3000}, T3Threads: []string{"mcp:a"},
	}))

	require.NoError(t, s.HibernateWorktree("kudtrading", "chicago-4"))

	wt := s.GetConfigSnapshot().Projects["kudtrading"].Worktrees["chicago-4"]
	require.True(t, wt.Hibernated)
	require.False(t, wt.HibernatedAt.IsZero())
	require.Equal(t, []string{"mcp:a"}, wt.T3Threads)
}
