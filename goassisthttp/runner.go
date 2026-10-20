package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RunnerResult menyimpan hasil eksekusi binary g3a
type RunnerResult struct {
	Command string
	Output  string
}

// ExecuteG3AFunneling mengeksekusi binary g3a dengan argumen flag yang dibentuk dari query param
func ExecuteG3AFunneling(ctx context.Context, selectVal, limitVal, outputVal string) (*RunnerResult, error) {
	// Buat context dengan timeout 30 detik agar eksekusi tidak menggantung
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Argumen dasar: /datafunneling/funneling
	args := []string{"/datafunneling/funneling"}

	// Tambahkan flag secara dinamis jika parameter diisi
	if selectVal != "" {
		args = append(args, fmt.Sprintf("--select=%s", selectVal))
	}
	if limitVal != "" {
		args = append(args, fmt.Sprintf("--limit=%s", limitVal))
	}
	if outputVal != "" {
		args = append(args, fmt.Sprintf("--output=%s", outputVal))
	}

	fullCommand := fmt.Sprintf("g3a %s", strings.Join(args, " "))

	// Eksekusi binary via os/exec
	cmd := exec.CommandContext(execCtx, "g3a", args...)
	outBytes, err := cmd.CombinedOutput()
	outputStr := string(outBytes)

	// Tangani error eksekusi atau timeout
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return &RunnerResult{
				Command: fullCommand,
				Output:  outputStr,
			}, fmt.Errorf("eksekusi binary timeout (melebihi 30 detik): %w", err)
		}
		return &RunnerResult{
			Command: fullCommand,
			Output:  outputStr,
		}, fmt.Errorf("eksekusi binary gagal: %w (output: %s)", err, strings.TrimSpace(outputStr))
	}

	return &RunnerResult{
		Command: fullCommand,
		Output:  outputStr,
	}, nil
}
