package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type BashTool struct{}

func (t *BashTool) Name() string {
	return "bash_exec"
}

func (t *BashTool) Description() string {
	return "Menjalankan perintah terminal/shell (bash di Linux atau powershell/cmd di Windows). Hanya gunakan jika diizinkan."
}

func (t *BashTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"command": {
				Type:        "string",
				Description: "Perintah shell/terminal yang akan dieksekusi.",
			},
		},
		Required: []string{"command"},
	}
}

func (t *BashTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	cmdStr, ok := args["command"].(string)
	if !ok || strings.TrimSpace(cmdStr) == "" {
		return "", fmt.Errorf("parameter 'command' wajib diisi")
	}

	// Set timeout of 30 seconds for command execution
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(execCtx, "sh", "-c", cmdStr)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	outStr := stdout.String()
	errStr := stderr.String()

	var sb strings.Builder
	if len(outStr) > 0 {
		sb.WriteString("STDOUT:\n")
		if len(outStr) > 4000 {
			sb.WriteString(outStr[:4000] + "\n...[truncated]")
		} else {
			sb.WriteString(outStr)
		}
	}
	if len(errStr) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("STDERR:\n")
		if len(errStr) > 2000 {
			sb.WriteString(errStr[:2000] + "\n...[truncated]")
		} else {
			sb.WriteString(errStr)
		}
	}

	if err != nil {
		if sb.Len() == 0 {
			return fmt.Sprintf("Error eksekusi: %v", err), nil
		}
		sb.WriteString(fmt.Sprintf("\n(Exit Code Error: %v)", err))
	}

	if sb.Len() == 0 {
		return "(Command selesai tanpa output)", nil
	}

	return sb.String(), nil
}
