package detect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	}
	return dir
}

func TestDetectNextJS(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"next":"14.0.0","react":"18.0.0"},"devDependencies":{"typescript":"5.0.0"}}`,
		"bun.lockb":    "",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "nextjs", info.Framework)
	assert.Equal(t, "typescript", info.Language)
	assert.Equal(t, "bun", info.PackageManager)
	assert.Equal(t, "browser", info.UIType)
	assert.True(t, info.WebEligible)
}

func TestDetectVite(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json":     `{"dependencies":{"react":"18.0.0"},"devDependencies":{"vite":"5.0.0","vitest":"1.0.0"}}`,
		"pnpm-lock.yaml":   "",
		"vitest.config.ts": "",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "vite", info.Framework)
	assert.Equal(t, "browser", info.UIType)
	assert.True(t, info.WebEligible)
	assert.Equal(t, "pnpm", info.PackageManager)
	assert.Equal(t, "vitest", info.TestFramework)
}

func TestDetectReactNative(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"react":"18.0.0","react-native":"0.72.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "react-native", info.Framework)
	assert.Equal(t, "mobile", info.UIType)
	assert.False(t, info.WebEligible)
}

func TestDetectAngular(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"@angular/core":"17.0.0"},"devDependencies":{"typescript":"5.0.0"}}`,
		"yarn.lock":    "",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "angular", info.Framework)
	assert.Equal(t, "browser", info.UIType)
	assert.True(t, info.WebEligible)
	assert.Equal(t, "yarn", info.PackageManager)
}

func TestDetectVue(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"vue":"3.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "vue", info.Framework)
	assert.Equal(t, "browser", info.UIType)
	assert.True(t, info.WebEligible)
}

func TestDetectNuxt(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"nuxt":"3.0.0","vue":"3.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "nuxt", info.Framework)
	assert.True(t, info.WebEligible)
}

func TestDetectRemix(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"@remix-run/node":"2.0.0","@remix-run/react":"2.0.0","react":"18.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "remix", info.Framework)
	assert.True(t, info.WebEligible)
}

func TestDetectAstro(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"astro":"4.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "astro", info.Framework)
	assert.True(t, info.WebEligible)
}

func TestDetectSvelteKit(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"svelte":"4.0.0"},"devDependencies":{"@sveltejs/kit":"2.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "sveltekit", info.Framework)
	assert.True(t, info.WebEligible)
}

func TestDetectElectron(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"electron":"28.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "electron", info.Framework)
	assert.Equal(t, "browser", info.UIType)
	assert.True(t, info.WebEligible)
}

func TestDetectNodeBackend(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"express":"4.0.0"},"devDependencies":{"jest":"29.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "node-backend", info.Framework)
	assert.Equal(t, "none", info.UIType)
	assert.False(t, info.WebEligible)
	assert.Equal(t, "jest", info.TestFramework)
}

func TestDetectGo(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"go.mod": "module example.com/myapp\n\ngo 1.21",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "go", info.Framework)
	assert.Equal(t, "go", info.Language)
	assert.Equal(t, "go", info.PackageManager)
	assert.Equal(t, "none", info.UIType)
	assert.False(t, info.WebEligible)
	assert.Equal(t, "go-test", info.TestFramework)
}

func TestDetectSwift(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"Package.swift": "// swift-tools-version: 5.9",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "swift", info.Framework)
	assert.Equal(t, "swift", info.Language)
	assert.Equal(t, "mobile", info.UIType)
	assert.False(t, info.WebEligible)
	assert.Equal(t, "xctest", info.TestFramework)
}

func TestDetectAndroid(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"build.gradle.kts": "plugins { id(\"com.android.application\") }",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "android", info.Framework)
	assert.Equal(t, "mobile", info.UIType)
	assert.False(t, info.WebEligible)
}

func TestDetectFlutter(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"pubspec.yaml": "name: my_app\ndependencies:\n  flutter:\n    sdk: flutter",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "flutter", info.Framework)
	assert.Equal(t, "dart", info.Language)
	assert.Equal(t, "mobile", info.UIType)
	assert.False(t, info.WebEligible)
}

func TestDetectRust(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"Cargo.toml": "[package]\nname = \"my-app\"",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "rust", info.Framework)
	assert.Equal(t, "rust", info.Language)
	assert.Equal(t, "none", info.UIType)
	assert.False(t, info.WebEligible)
}

func TestDetectDjango(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"requirements.txt": "django==4.2\ncelery==5.3",
		"conftest.py":      "",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "django", info.Framework)
	assert.Equal(t, "python", info.Language)
	assert.Equal(t, "browser", info.UIType)
	assert.True(t, info.WebEligible)
	assert.Equal(t, "pytest", info.TestFramework)
}

func TestDetectFlask(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"requirements.txt": "flask==3.0\ngunicorn",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "flask", info.Framework)
	assert.True(t, info.WebEligible)
}

func TestDetectPythonBackend(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"requirements.txt": "fastapi==0.100\nuvicorn",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "fastapi", info.Framework)
	assert.Equal(t, "none", info.UIType)
	assert.False(t, info.WebEligible)
}

func TestDetectPlainHTML(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"index.html": "<html><body>Hello</body></html>",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "html", info.Framework)
	assert.Equal(t, "browser", info.UIType)
	assert.True(t, info.WebEligible)
}

func TestDetectUnknown(t *testing.T) {
	dir := t.TempDir()

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "unknown", info.Framework)
	assert.Equal(t, "none", info.UIType)
	assert.False(t, info.WebEligible)
}

func TestDetectReactCRA(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json": `{"dependencies":{"react":"18.0.0","react-dom":"18.0.0","react-scripts":"5.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "react", info.Framework)
	assert.Equal(t, "browser", info.UIType)
	assert.True(t, info.WebEligible)
}

func TestDetectMonorepoWithWebApp(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json":             `{"workspaces":["apps/*","packages/*"],"devDependencies":{"turbo":"2.0.0","typescript":"5.0.0"}}`,
		"apps/web/package.json":    `{"dependencies":{"vite":"5.0.0","react":"18.0.0"}}`,
		"apps/api/package.json":    `{"dependencies":{"hono":"4.0.0"}}`,
		"packages/db/package.json": `{"dependencies":{"prisma":"5.0.0"}}`,
		"bun.lockb":                "",
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "vite-monorepo", info.Framework)
	assert.Equal(t, "typescript", info.Language)
	assert.Equal(t, "browser", info.UIType)
	assert.True(t, info.WebEligible)
	assert.Equal(t, "bun", info.PackageManager)
}

func TestDetectMonorepoNextJS(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json":          `{"workspaces":["apps/*"],"devDependencies":{"turbo":"2.0.0"}}`,
		"apps/web/package.json": `{"dependencies":{"next":"14.0.0","react":"18.0.0"},"devDependencies":{"typescript":"5.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "nextjs-monorepo", info.Framework)
	assert.True(t, info.WebEligible)
}

func TestDetectMonorepoNoWebApp(t *testing.T) {
	dir := setupProject(t, map[string]string{
		"package.json":          `{"workspaces":["apps/*"],"devDependencies":{"turbo":"2.0.0"}}`,
		"apps/api/package.json": `{"dependencies":{"express":"4.0.0"}}`,
	})

	info, err := DetectProject(dir)
	require.NoError(t, err)
	assert.False(t, info.WebEligible)
	assert.Equal(t, "none", info.UIType)
}

func TestPackageManagerDetection(t *testing.T) {
	tests := []struct {
		name     string
		lockfile string
		expected string
	}{
		{"bun lockb", "bun.lockb", "bun"},
		{"bun lock", "bun.lock", "bun"},
		{"pnpm", "pnpm-lock.yaml", "pnpm"},
		{"yarn", "yarn.lock", "yarn"},
		{"npm", "package-lock.json", "npm"},
		{"default npm", "", "npm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{
				"package.json": `{"dependencies":{"next":"14.0.0"}}`,
			}
			if tt.lockfile != "" {
				files[tt.lockfile] = ""
			}
			dir := setupProject(t, files)

			info, err := DetectProject(dir)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, info.PackageManager)
		})
	}
}
