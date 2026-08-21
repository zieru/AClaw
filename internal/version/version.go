package version

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

var (
	// Version is the current semantic version of GoAssistant (updated to latest tag)
	Version = "1.5.4"
	// BuildDate is the date or timestamp this binary was compiled / committed
	BuildDate = "2026-08-21"
	// GitCommit is the short commit hash
	GitCommit = ""
	// GitBranch is the active git branch
	GitBranch = "main"
	// DefaultRepo is the default GitHub repository for releases
	DefaultRepo = "zieru/AClaw"
	// IsDirty indicates uncommitted local changes
	IsDirty = false
)

func init() {
	detectBuildInfo()
	detectGitRepo()
}

// detectBuildInfo reads Go runtime buildinfo embedded into the binary
func detectBuildInfo() {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return
	}

	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		v := strings.TrimPrefix(info.Main.Version, "v")
		if v != "" {
			Version = v
		}
	}

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				GitCommit = s.Value[:7]
			} else {
				GitCommit = s.Value
			}
		case "vcs.time":
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				BuildDate = t.Format("2006-01-02")
			}
		case "vcs.modified":
			if s.Value == "true" {
				IsDirty = true
			}
		}
	}
}

// detectGitRepo inspects the local .git directory if present
func detectGitRepo() {
	gitDir := findGitDir()
	if gitDir == "" {
		return
	}

	// 1. Read HEAD to get branch and/or commit
	headPath := filepath.Join(gitDir, "HEAD")
	if headData, err := os.ReadFile(headPath); err == nil {
		headStr := strings.TrimSpace(string(headData))
		if strings.HasPrefix(headStr, "ref: refs/heads/") {
			GitBranch = strings.TrimPrefix(headStr, "ref: refs/heads/")
			refFile := filepath.Join(gitDir, "refs", "heads", filepath.FromSlash(GitBranch))
			if refData, err := os.ReadFile(refFile); err == nil {
				commit := strings.TrimSpace(string(refData))
				if len(commit) >= 7 && GitCommit == "" {
					GitCommit = commit[:7]
				}
			}
		} else if len(headStr) >= 7 && GitCommit == "" {
			GitCommit = headStr[:7]
		}
	}

	// 2. Scan tags in .git/refs/tags
	tagsDir := filepath.Join(gitDir, "refs", "tags")
	if entries, err := os.ReadDir(tagsDir); err == nil && len(entries) > 0 {
		var highestTag string
		var highestParts []int

		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			rawTag := e.Name()
			cleanTag := strings.TrimPrefix(rawTag, "v")
			cleanTag = strings.TrimPrefix(cleanTag, ".")
			parts := parseSemverParts(cleanTag)
			if len(parts) > 0 {
				if len(highestParts) == 0 || compareSemver(parts, highestParts) > 0 {
					highestParts = parts
					highestTag = cleanTag
				}
			}
		}

		if highestTag != "" {
			Version = highestTag
		}
	}
}

func findGitDir() string {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}

	for i := 0; i < 5; i++ {
		gitPath := filepath.Join(dir, ".git")
		if fi, err := os.Stat(gitPath); err == nil && fi.IsDir() {
			return gitPath
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func parseSemverParts(v string) []int {
	// e.g. "1.5.4" or "1.5.1-1"
	if idx := strings.IndexAny(v, "-+"); idx != -1 {
		v = v[:idx]
	}
	v = strings.ReplaceAll(v, "..", ".")
	segments := strings.Split(v, ".")
	var parts []int
	for _, s := range segments {
		if n, err := strconv.Atoi(s); err == nil {
			parts = append(parts, n)
		}
	}
	return parts
}

func compareSemver(a, b []int) int {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := 0; i < maxLen; i++ {
		valA, valB := 0, 0
		if i < len(a) {
			valA = a[i]
		}
		if i < len(b) {
			valB = b[i]
		}
		if valA > valB {
			return 1
		}
		if valA < valB {
			return -1
		}
	}
	return 0
}

// GetVersion returns semantic version string without 'v' prefix
func GetVersion() string {
	return Version
}

// GetGitCommit returns short git commit hash
func GetGitCommit() string {
	return GitCommit
}

// GetBuildDate returns formatted build/commit date
func GetBuildDate() string {
	return BuildDate
}

// GetFullVersion returns formatted display version string
func GetFullVersion() string {
	commitStr := ""
	if GitCommit != "" {
		commitStr = fmt.Sprintf(" (%s)", GitCommit)
	}
	return fmt.Sprintf("v%s%s • %s", Version, commitStr, BuildDate)
}
