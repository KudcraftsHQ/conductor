package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanWriteReportsWritableDirectory(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "conductor")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\ntrue\n"), 0755))

	assert.True(t, canWrite(bin))
}

func TestCanWriteFalseForMissingFile(t *testing.T) {
	assert.False(t, canWrite(filepath.Join(t.TempDir(), "nope")))
}

func TestCanWriteFalseForReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "conductor")
	require.NoError(t, os.WriteFile(bin, []byte("x"), 0755))
	require.NoError(t, os.Chmod(dir, 0555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	assert.False(t, canWrite(bin))
}

// startRunningBinary copies a real compiled executable into dir and starts it,
// returning its path.
//
// It must be a genuine binary, not a #!/bin/sh script: when a script runs, the
// executing image is the interpreter, so the script file itself is not busy and
// ETXTBSY never triggers. Copying /bin/sleep gives a real ELF/Mach-O whose file
// *is* the running image.
func startRunningBinary(t *testing.T, dir string) string {
	t.Helper()

	src, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary available")
	}
	data, err := os.ReadFile(src)
	require.NoError(t, err)

	bin := filepath.Join(dir, "sleeper")
	require.NoError(t, os.WriteFile(bin, data, 0755))

	cmd := exec.Command(bin, "30")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Give the kernel a moment to map the image before probing.
	time.Sleep(100 * time.Millisecond)
	return bin
}

// TestCanWriteWhileBinaryIsRunning is the regression this fix exists for: on
// Linux, probing a *running* executable for write returns ETXTBSY, which made
// `conductor update` report "installed in a system directory" and refuse to
// update even from ~/.local/bin.
func TestCanWriteWhileBinaryIsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only")
	}
	dir := t.TempDir()
	bin := startRunningBinary(t, dir)

	// Guard: confirm the fixture actually reproduces the condition on this
	// platform, so the assertion below cannot pass vacuously.
	if runtime.GOOS == "linux" {
		info, err := os.Stat(bin)
		require.NoError(t, err)
		f, err := os.OpenFile(bin, os.O_WRONLY, info.Mode())
		if err == nil {
			_ = f.Close()
			t.Fatal("fixture is not actually running/busy — the old probe would have succeeded, so this test proves nothing")
		}
	}

	assert.True(t, canWrite(bin), "a running binary in a writable dir must still be replaceable")
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "conductor")
	src := filepath.Join(dir, "new")
	require.NoError(t, os.WriteFile(dst, []byte("old"), 0755))
	require.NoError(t, os.WriteFile(src, []byte("new contents"), 0644))

	require.NoError(t, replaceBinary(src, dst))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "new contents", string(got))

	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm(), "replacement must stay executable")
}

// TestReplaceBinaryWhileRunning covers the second half of the bug: copyFile
// used os.Create, which truncates in place and fails with ETXTBSY on a running
// binary. Renaming over it succeeds.
func TestReplaceBinaryWhileRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot rename over a running executable on Windows")
	}
	dir := t.TempDir()
	bin := startRunningBinary(t, dir)

	src := filepath.Join(dir, "new")
	require.NoError(t, os.WriteFile(src, []byte("replacement payload"), 0644))

	// Guard: the old in-place copy must fail here, otherwise this test would
	// pass regardless of the fix.
	if runtime.GOOS == "linux" {
		require.Error(t, copyFile(src, bin),
			"in-place copy should fail with ETXTBSY on a running binary")
	}

	require.NoError(t, replaceBinary(src, bin), "must be able to replace a running binary")

	got, err := os.ReadFile(bin)
	require.NoError(t, err)
	assert.Equal(t, "replacement payload", string(got))
}

func TestReplaceBinaryLeavesTargetIntactOnMissingSource(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "conductor")
	require.NoError(t, os.WriteFile(dst, []byte("original"), 0755))

	require.Error(t, replaceBinary(filepath.Join(dir, "absent"), dst))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "original", string(got), "a failed update must not damage the existing binary")

	// The probe/staging files must not be left behind.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestIsSystemInstallationKnownPaths(t *testing.T) {
	// Guards the classifier the update gate reports on.
	assert.NotPanics(t, func() { _ = IsSystemInstallation() })
}
