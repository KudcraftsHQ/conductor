package detect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProjectInfo contains detected project type information
type ProjectInfo struct {
	Framework      string `json:"framework"`
	Language       string `json:"language"`
	PackageManager string `json:"packageManager"`
	TestFramework  string `json:"testFramework,omitempty"`
	WebEligible    bool   `json:"webEligible"`
	UIType         string `json:"uiType"` // "browser", "mobile", "cli", "library", "none"
}

// DetectProject analyzes a project directory and returns type information
func DetectProject(projectPath string) (*ProjectInfo, error) {
	info := &ProjectInfo{
		Framework: "unknown",
		Language:  "unknown",
		UIType:    "none",
	}

	// Check for package.json (JavaScript/TypeScript ecosystem)
	if pkgJSON, err := readPackageJSON(projectPath); err == nil {
		detectFromPackageJSON(info, pkgJSON)
		detectPackageManager(info, projectPath)
		detectJSTestFramework(info, projectPath, pkgJSON)

		// If root is generic node/node-backend but has workspaces, check workspace apps for web frameworks
		if !info.WebEligible && len(pkgJSON.Workspaces) > 0 {
			detectFromMonorepo(info, projectPath, pkgJSON.Workspaces)
		}

		return info, nil
	}

	// Check for Go project
	if fileExists(projectPath, "go.mod") {
		info.Framework = "go"
		info.Language = "go"
		info.PackageManager = "go"
		info.UIType = "none"
		info.WebEligible = false
		detectGoTestFramework(info, projectPath)
		return info, nil
	}

	// Check for Swift/iOS project
	if fileExists(projectPath, "Package.swift") || hasGlob(projectPath, "*.xcodeproj") {
		info.Framework = "swift"
		info.Language = "swift"
		info.PackageManager = "spm"
		info.UIType = "mobile"
		info.WebEligible = false
		info.TestFramework = "xctest"
		return info, nil
	}

	// Check for Android project
	if fileExists(projectPath, "build.gradle") || fileExists(projectPath, "build.gradle.kts") {
		info.Framework = "android"
		info.Language = "kotlin"
		info.PackageManager = "gradle"
		info.UIType = "mobile"
		info.WebEligible = false
		return info, nil
	}

	// Check for Flutter project
	if fileExists(projectPath, "pubspec.yaml") {
		content, err := os.ReadFile(filepath.Join(projectPath, "pubspec.yaml"))
		if err == nil && strings.Contains(string(content), "flutter") {
			info.Framework = "flutter"
			info.Language = "dart"
			info.PackageManager = "pub"
			info.UIType = "mobile"
			info.WebEligible = false
			return info, nil
		}
	}

	// Check for Rust project
	if fileExists(projectPath, "Cargo.toml") {
		info.Framework = "rust"
		info.Language = "rust"
		info.PackageManager = "cargo"
		info.UIType = "none"
		info.WebEligible = false
		return info, nil
	}

	// Check for Python project
	if fileExists(projectPath, "requirements.txt") || fileExists(projectPath, "pyproject.toml") || fileExists(projectPath, "setup.py") {
		detectPythonProject(info, projectPath)
		return info, nil
	}

	// Check for plain HTML project
	if fileExists(projectPath, "index.html") {
		info.Framework = "html"
		info.Language = "html"
		info.UIType = "browser"
		info.WebEligible = true
		return info, nil
	}

	return info, nil
}

type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Workspaces      workspaces        `json:"workspaces"`
}

// workspaces handles both string array and object formats
type workspaces []string

func (w *workspaces) UnmarshalJSON(data []byte) error {
	// Try array first: ["apps/*", "packages/*"]
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*w = arr
		return nil
	}
	// Try object: {"packages": ["apps/*", "packages/*"]}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		*w = obj.Packages
		return nil
	}
	return nil
}

func readPackageJSON(projectPath string) (*packageJSON, error) {
	data, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func (p *packageJSON) hasDep(name string) bool {
	if _, ok := p.Dependencies[name]; ok {
		return true
	}
	if _, ok := p.DevDependencies[name]; ok {
		return true
	}
	return false
}

func (p *packageJSON) hasAnyDep(names ...string) bool {
	for _, name := range names {
		if p.hasDep(name) {
			return true
		}
	}
	return false
}

func detectFromPackageJSON(info *ProjectInfo, pkg *packageJSON) {
	// Detect language
	if pkg.hasDep("typescript") {
		info.Language = "typescript"
	} else {
		info.Language = "javascript"
	}

	// React Native (check before React since RN also has react dep)
	if pkg.hasDep("react-native") {
		info.Framework = "react-native"
		info.UIType = "mobile"
		info.WebEligible = false
		return
	}

	// Next.js
	if pkg.hasDep("next") {
		info.Framework = "nextjs"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// Nuxt
	if pkg.hasDep("nuxt") {
		info.Framework = "nuxt"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// Remix
	if pkg.hasAnyDep("@remix-run/node", "@remix-run/react", "remix") {
		info.Framework = "remix"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// Astro
	if pkg.hasDep("astro") {
		info.Framework = "astro"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// SvelteKit
	if pkg.hasDep("@sveltejs/kit") {
		info.Framework = "sveltekit"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// Vite (check after meta-frameworks that use vite under the hood)
	if pkg.hasDep("vite") {
		info.Framework = "vite"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// Angular
	if pkg.hasDep("@angular/core") {
		info.Framework = "angular"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// Svelte (standalone, not SvelteKit)
	if pkg.hasDep("svelte") {
		info.Framework = "svelte"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// Vue (standalone, not Nuxt)
	if pkg.hasDep("vue") {
		info.Framework = "vue"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// Electron
	if pkg.hasDep("electron") {
		info.Framework = "electron"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// React (standalone CRA or custom setup — check after all meta-frameworks)
	if pkg.hasDep("react") {
		info.Framework = "react"
		info.UIType = "browser"
		info.WebEligible = true
		return
	}

	// Node.js backend frameworks (no browser UI)
	if pkg.hasAnyDep("express", "fastify", "hono", "koa", "@nestjs/core") {
		info.Framework = "node-backend"
		info.UIType = "none"
		info.WebEligible = false
		return
	}

	// Generic Node.js project
	info.Framework = "node"
	info.UIType = "none"
	info.WebEligible = false
}

// detectFromMonorepo scans workspace directories for web-eligible apps
func detectFromMonorepo(info *ProjectInfo, projectPath string, workspacePatterns []string) {
	for _, pattern := range workspacePatterns {
		// Resolve glob patterns like "apps/*"
		matches, err := filepath.Glob(filepath.Join(projectPath, pattern))
		if err != nil {
			continue
		}
		for _, dir := range matches {
			fi, err := os.Stat(dir)
			if err != nil || !fi.IsDir() {
				continue
			}
			pkg, err := readPackageJSON(dir)
			if err != nil {
				continue
			}
			// Check if this workspace package has a web framework
			candidate := &ProjectInfo{UIType: "none"}
			detectFromPackageJSON(candidate, pkg)
			if candidate.WebEligible {
				// Found a web-eligible workspace app — promote to monorepo framework
				info.Framework = candidate.Framework + "-monorepo"
				info.UIType = "browser"
				info.WebEligible = true
				if candidate.Language == "typescript" {
					info.Language = "typescript"
				}
				return
			}
		}
	}
}

func detectPackageManager(info *ProjectInfo, projectPath string) {
	switch {
	case fileExists(projectPath, "bun.lockb") || fileExists(projectPath, "bun.lock"):
		info.PackageManager = "bun"
	case fileExists(projectPath, "pnpm-lock.yaml"):
		info.PackageManager = "pnpm"
	case fileExists(projectPath, "yarn.lock"):
		info.PackageManager = "yarn"
	default:
		info.PackageManager = "npm"
	}
}

func detectJSTestFramework(info *ProjectInfo, projectPath string, pkg *packageJSON) {
	if pkg.hasDep("vitest") || hasGlob(projectPath, "vitest.config.*") {
		info.TestFramework = "vitest"
	} else if pkg.hasDep("jest") || hasGlob(projectPath, "jest.config.*") {
		info.TestFramework = "jest"
	} else if pkg.hasDep("playwright") || pkg.hasDep("@playwright/test") || hasGlob(projectPath, "playwright.config.*") {
		info.TestFramework = "playwright"
	} else if pkg.hasDep("cypress") || hasGlob(projectPath, "cypress.config.*") {
		info.TestFramework = "cypress"
	}
}

func detectGoTestFramework(info *ProjectInfo, projectPath string) {
	// Go always has built-in testing
	info.TestFramework = "go-test"
}

func detectPythonProject(info *ProjectInfo, projectPath string) {
	info.Language = "python"
	info.PackageManager = "pip"

	// Check for web frameworks
	files := []string{"requirements.txt", "pyproject.toml", "setup.py"}
	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(projectPath, f))
		if err != nil {
			continue
		}
		s := strings.ToLower(string(content))
		if strings.Contains(s, "django") {
			info.Framework = "django"
			info.UIType = "browser"
			info.WebEligible = true
			break
		}
		if strings.Contains(s, "flask") {
			info.Framework = "flask"
			info.UIType = "browser"
			info.WebEligible = true
			break
		}
		if strings.Contains(s, "streamlit") {
			info.Framework = "streamlit"
			info.UIType = "browser"
			info.WebEligible = true
			break
		}
		if strings.Contains(s, "fastapi") {
			info.Framework = "fastapi"
			info.UIType = "none"
			info.WebEligible = false
			break
		}
	}

	if info.Framework == "" {
		info.Framework = "python"
		info.UIType = "none"
		info.WebEligible = false
	}

	// Test framework
	if fileExists(projectPath, "pytest.ini") || fileExists(projectPath, "conftest.py") {
		info.TestFramework = "pytest"
	} else {
		// Check pyproject.toml for pytest config
		content, err := os.ReadFile(filepath.Join(projectPath, "pyproject.toml"))
		if err == nil && strings.Contains(string(content), "pytest") {
			info.TestFramework = "pytest"
		}
	}
}

// AuthInfo contains detected authentication information
type AuthInfo struct {
	Type        string // "none", "dev-bypass", "email-password", "oauth"
	LoginURL    string // detected login route
	SeedCommand string // detected seed script
	CallbackURL string // detected post-login redirect
}

// DetectAuth analyzes a project for authentication patterns
func DetectAuth(projectPath string) *AuthInfo {
	info := &AuthInfo{Type: "none"}

	pkg, _ := readPackageJSON(projectPath)

	// Check Cloudflare Access (dev mode = auto-auth bypass)
	if containsInSource(projectPath, "Cf-Access-Jwt-Assertion", "cloudflareaccess") {
		info.Type = "dev-bypass"
		return info
	}

	// Check auth libraries in package.json
	if pkg != nil {
		switch {
		case pkg.hasDep("better-auth"):
			info.Type = "email-password"
		case pkg.hasAnyDep("next-auth", "@auth/core", "@auth/nextjs"):
			info.Type = "email-password"
		case pkg.hasDep("@supabase/auth-helpers-nextjs") || pkg.hasDep("@supabase/ssr"):
			info.Type = "email-password"
		case pkg.hasDep("@clerk/nextjs") || pkg.hasDep("@clerk/clerk-react"):
			info.Type = "oauth"
		case pkg.hasDep("firebase"):
			info.Type = "email-password"
		}
	}

	// Also check monorepo workspace packages
	if info.Type == "none" && pkg != nil && len(pkg.Workspaces) > 0 {
		for _, pattern := range pkg.Workspaces {
			matches, err := filepath.Glob(filepath.Join(projectPath, pattern))
			if err != nil {
				continue
			}
			for _, dir := range matches {
				wsPkg, err := readPackageJSON(dir)
				if err != nil {
					continue
				}
				if wsPkg.hasDep("better-auth") || wsPkg.hasAnyDep("next-auth", "@auth/core") {
					info.Type = "email-password"
					break
				}
				if wsPkg.hasDep("@clerk/nextjs") || wsPkg.hasDep("@clerk/clerk-react") {
					info.Type = "oauth"
					break
				}
			}
			if info.Type != "none" {
				break
			}
		}
	}

	// Detect login URL
	if info.Type == "email-password" || info.Type == "oauth" {
		info.LoginURL = detectLoginURL(projectPath)
	}

	// Detect seed command
	info.SeedCommand = detectSeedCommand(projectPath, pkg)

	// Detect callback URL
	if info.Type == "email-password" || info.Type == "oauth" {
		info.CallbackURL = detectCallbackURL(projectPath)
	}

	return info
}

// containsInSource checks if any source file contains the given strings
func containsInSource(projectPath string, patterns ...string) bool {
	// Check common source directories
	dirs := []string{"src", "app", "lib", "pages", "internal"}
	for _, dir := range dirs {
		dirPath := filepath.Join(projectPath, dir)
		if _, err := os.Stat(dirPath); err != nil {
			continue
		}
		found := false
		_ = filepath.Walk(dirPath, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() || found {
				return nil
			}
			// Only check source files
			ext := filepath.Ext(path)
			if ext != ".ts" && ext != ".tsx" && ext != ".js" && ext != ".jsx" && ext != ".go" && ext != ".py" {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			s := string(content)
			for _, p := range patterns {
				if strings.Contains(s, p) {
					found = true
					return filepath.SkipAll
				}
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}

// detectLoginURL finds the login page path
func detectLoginURL(projectPath string) string {
	// Check common login page patterns
	patterns := []struct {
		glob string
		url  string
	}{
		{"src/routes/login.*", "/login"},
		{"src/pages/login.*", "/login"},
		{"app/login/**", "/login"},
		{"app/(auth)/login/**", "/login"},
		{"src/app/login/**", "/login"},
		{"pages/login.*", "/login"},
		{"src/routes/auth/login.*", "/auth/login"},
		{"pages/auth/signin.*", "/auth/signin"},
		{"app/auth/signin/**", "/auth/signin"},
		{"src/pages/auth/signin.*", "/auth/signin"},
		{"pages/signin.*", "/signin"},
		{"app/signin/**", "/signin"},
	}

	for _, p := range patterns {
		if hasGlob(projectPath, p.glob) {
			return p.url
		}
	}

	// Check monorepo apps
	for _, appDir := range []string{"apps/web", "apps/frontend", "apps/client"} {
		appPath := filepath.Join(projectPath, appDir)
		if _, err := os.Stat(appPath); err != nil {
			continue
		}
		for _, p := range patterns {
			if hasGlob(appPath, p.glob) {
				return p.url
			}
		}
	}

	return "/login" // sensible default
}

// detectSeedCommand finds the test user seed script
func detectSeedCommand(projectPath string, pkg *packageJSON) string {
	// Check for seed scripts
	seedPatterns := []struct {
		file string
		cmd  string
	}{
		{"scripts/seed-user.ts", "bun run scripts/seed-user.ts"},
		{"scripts/seed-user.js", "node scripts/seed-user.js"},
		{"scripts/seed.ts", "bun run scripts/seed.ts"},
		{"scripts/seed.js", "node scripts/seed.js"},
		{"prisma/seed.ts", "bunx prisma db seed"},
		{"prisma/seed.js", "npx prisma db seed"},
	}

	for _, p := range seedPatterns {
		if fileExists(projectPath, p.file) {
			return p.cmd
		}
	}

	// Check monorepo apps
	for _, appDir := range []string{"apps/api", "apps/web", "apps/server", "apps/backend"} {
		appPath := filepath.Join(projectPath, appDir)
		for _, p := range seedPatterns {
			if fileExists(appPath, p.file) {
				// Prefix with cd for monorepo
				return fmt.Sprintf("cd %s && %s", appDir, p.cmd)
			}
		}
	}

	// Check package.json scripts
	if pkg != nil {
		scripts := make(map[string]string)
		// Re-read to get scripts field
		data, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
		if err == nil {
			var full struct {
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(data, &full) == nil {
				scripts = full.Scripts
			}
		}
		for name := range scripts {
			if strings.Contains(name, "seed") {
				pm := "npm"
				if fileExists(projectPath, "bun.lockb") || fileExists(projectPath, "bun.lock") {
					pm = "bun"
				}
				return fmt.Sprintf("%s run %s", pm, name)
			}
		}
	}

	return ""
}

// detectCallbackURL finds the post-login redirect URL
func detectCallbackURL(projectPath string) string {
	// Check common app entry points
	appPatterns := []struct {
		glob string
		url  string
	}{
		{"app/(app)/**", "/app"},
		{"src/routes/(app)/**", "/app"},
		{"app/dashboard/**", "/dashboard"},
		{"src/pages/dashboard.*", "/dashboard"},
		{"pages/dashboard.*", "/dashboard"},
		{"app/(protected)/**", "/"},
	}

	for _, p := range appPatterns {
		if hasGlob(projectPath, p.glob) {
			return p.url
		}
	}

	// Check monorepo
	for _, appDir := range []string{"apps/web", "apps/frontend"} {
		appPath := filepath.Join(projectPath, appDir)
		if _, err := os.Stat(appPath); err != nil {
			continue
		}
		for _, p := range appPatterns {
			if hasGlob(appPath, p.glob) {
				return p.url
			}
		}
	}

	return "/"
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func hasGlob(dir, pattern string) bool {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	return err == nil && len(matches) > 0
}
