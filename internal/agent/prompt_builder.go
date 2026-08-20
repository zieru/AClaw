package agent

import (
	"fmt"
	"strings"
	"time"
)

type PromptBuilder struct {
	mdLoader *MDLoader
}

func NewPromptBuilder(mdLoader *MDLoader) *PromptBuilder {
	return &PromptBuilder{mdLoader: mdLoader}
}

type PromptContext struct {
	AgentRole      string
	ChannelType    string
	ChannelName    string
	UserName       string
	UserID         string
	MemoryContext  string
	SessionSummary string
	ActiveModel    string
	ActiveProvider string
}

// BuildSystemPrompt builds the complete system prompt from Markdown files and context
func (pb *PromptBuilder) BuildSystemPrompt(ctx PromptContext) (string, error) {
	var sb strings.Builder

	// 1. Load IDENTITY.md
	identity, err := pb.mdLoader.GetFile("IDENTITY.md")
	if err == nil && identity != "" {
		sb.WriteString(identity)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("# Identity\nKamu adalah GoAssistant, sebuah AI assistant cerdas, tanggap, dan membantu yang berjalan di lingkungan server mandiri.\n\n")
	}

	// 2. Load SOUL.md / KNOWLEDGE.md
	soul, err := pb.mdLoader.GetFile("SOUL.md")
	if err == nil && soul != "" {
		sb.WriteString("## Core Knowledge & SOP:\n")
		sb.WriteString(soul)
		sb.WriteString("\n\n")
	}

	// 3. Load specific AGENTS.md role if requested
	if ctx.AgentRole != "" && ctx.AgentRole != "default" {
		agentsMD, err := pb.mdLoader.GetFile("AGENTS.md")
		if err == nil && agentsMD != "" {
			sb.WriteString(fmt.Sprintf("## Active Role: %s\n", ctx.AgentRole))
			sb.WriteString(agentsMD)
			sb.WriteString("\n\n")
		}
	}

	// 4. Injected Context & Memory
	sb.WriteString("## Environment & Session Context:\n")
	sb.WriteString(fmt.Sprintf("- Current Time: %s\n", time.Now().Format("Monday, 02 January 2006 15:04:05 MST")))
	sb.WriteString(fmt.Sprintf("- Channel: %s (%s)\n", ctx.ChannelName, ctx.ChannelType))
	if ctx.UserName != "" {
		sb.WriteString(fmt.Sprintf("- User: %s (ID: %s)\n", ctx.UserName, ctx.UserID))
	}
	if ctx.ActiveModel != "" {
		sb.WriteString(fmt.Sprintf("- Active AI Model: %s\n", ctx.ActiveModel))
	}
	if ctx.ActiveProvider != "" {
		sb.WriteString(fmt.Sprintf("- AI Provider Gateway: %s\n", ctx.ActiveProvider))
	}
	sb.WriteString("\n")

	if ctx.SessionSummary != "" {
		sb.WriteString("### Previous Conversation Summary:\n")
		sb.WriteString(ctx.SessionSummary)
		sb.WriteString("\n\n")
	}

	if ctx.MemoryContext != "" {
		sb.WriteString(ctx.MemoryContext)
		sb.WriteString("\n")
	}

	// 5. Tool Instructions if TOOLS.md exists
	toolsMD, err := pb.mdLoader.GetFile("TOOLS.md")
	if err == nil && toolsMD != "" {
		sb.WriteString("## Tool Usage Guidelines:\n")
		sb.WriteString(toolsMD)
		sb.WriteString("\n\n")
	}

	rawPrompt := sb.String()

	// Replace template variables
	rawPrompt = strings.ReplaceAll(rawPrompt, "{{time}}", time.Now().Format("2006-01-02 15:04:05"))
	rawPrompt = strings.ReplaceAll(rawPrompt, "{{user_name}}", ctx.UserName)
	rawPrompt = strings.ReplaceAll(rawPrompt, "{{channel}}", ctx.ChannelName)
	rawPrompt = strings.ReplaceAll(rawPrompt, "{{model}}", ctx.ActiveModel)
	rawPrompt = strings.ReplaceAll(rawPrompt, "{{provider}}", ctx.ActiveProvider)

	return rawPrompt, nil
}
