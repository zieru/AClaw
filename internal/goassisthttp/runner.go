package goassisthttp

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// RunnerResult menyimpan hasil eksekusi binary CLI
type RunnerResult struct {
	Binary  string `json:"binary"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

// ExecuteDynamicCommand mengeksekusi binary dengan sub-command dan flags dinamis
func ExecuteDynamicCommand(ctx context.Context, binaryName string, baseCommand string, flags map[string]string, timeout time.Duration) (*RunnerResult, error) {
	if binaryName == "" {
		binaryName = "g3a"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Susun argumen CLI
	args := []string{}
	if strings.TrimSpace(baseCommand) != "" {
		// Pecah argumen dasar jika terdiri dari beberapa kata (misal: "sub1 sub2")
		parts := strings.Fields(baseCommand)
		args = append(args, parts...)
	}

	// Urutkan flags agar pemanggilan konsisten & deterministik
	keys := make([]string, 0, len(flags))
	for k := range flags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		val := flags[k]
		if val == "" {
			continue
		}
		// Format flag menjadi --key=value
		args = append(args, fmt.Sprintf("--%s=%s", k, val))
	}

	fullCommand := fmt.Sprintf("%s %s", binaryName, strings.Join(args, " "))

	// Eksekusi binary
	cmd := exec.CommandContext(execCtx, binaryName, args...)
	outBytes, err := cmd.CombinedOutput()
	outputStr := string(outBytes)

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return &RunnerResult{
				Binary:  binaryName,
				Command: fullCommand,
				Output:  outputStr,
			}, fmt.Errorf("eksekusi timeout (melebihi %v): %w", timeout, err)
		}
		return &RunnerResult{
			Binary:  binaryName,
			Command: fullCommand,
			Output:  outputStr,
		}, fmt.Errorf("eksekusi gagal: %w (output: %s)", err, strings.TrimSpace(outputStr))
	}

	return &RunnerResult{
		Binary:  binaryName,
		Command: fullCommand,
		Output:  outputStr,
	}, nil
}
