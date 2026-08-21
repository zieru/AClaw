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
	AgentRole       string
	ChannelType     string
	ChannelName     string
	UserName        string
	UserID          string
	MemoryContext   string
	SessionSummary  string
	ActiveModel     string
	ActiveProvider  string
	ThinkingEnabled bool
	StreamMode      bool
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

	// 3. Load AGENTS.md for Multi-Agent & Specialized Roles
	agentsMD, err := pb.mdLoader.GetFile("AGENTS.md")
	if err == nil && agentsMD != "" {
		sb.WriteString("## Multi-Agent Delegation & Specialized Roles:\n")
		sb.WriteString("Kamu dapat memecah masalah kompleks dan mendelegasikan sub-tugas ke sub-agen spesialis melalui tool `delegate_task` agar konteks tetap fokus dan tidak membengkak.\n")
		sb.WriteString(agentsMD)
		sb.WriteString("\n\n")
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
	if ctx.ThinkingEnabled {
		rawPrompt = strings.ReplaceAll(rawPrompt, "{{thinking_enabled}}", "true")
	} else {
		rawPrompt = strings.ReplaceAll(rawPrompt, "{{thinking_enabled}}", "false")
	}
	if ctx.StreamMode {
		rawPrompt = strings.ReplaceAll(rawPrompt, "{{stream_mode}}", "streaming")
	} else {
		rawPrompt = strings.ReplaceAll(rawPrompt, "{{stream_mode}}", "batch")
	}

	return rawPrompt, nil
}

// BuildSubagentPrompt constructs a dedicated, focused system prompt for a specialized subagent
func (pb *PromptBuilder) BuildSubagentPrompt(role string) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Specialized Sub-Agent Role: %s\n", strings.ToUpper(role)))
	sb.WriteString("Kamu adalah Sub-Agen Spesialis independen yang bertugas menyelesaikan satu sub-tugas tertentu dengan fokus penuh, cepat, dan presisi.\n\n")

	agentsMD, err := pb.mdLoader.GetFile("AGENTS.md")
	if err == nil && agentsMD != "" {
		sb.WriteString("## Spesialisasi & Aturan Peran:\n")
		sb.WriteString(agentsMD)
		sb.WriteString("\n\n")
	}

	// Role-specific enhancements
	switch strings.ToLower(role) {
	case "coder", "programmer", "developer":
		sb.WriteString("## Pedoman Khusus Coder:\n")
		sb.WriteString("- Tulis kode yang bersih, efisien, dan terdokumentasi.\n")
		sb.WriteString("- Sertakan error handling dan edge cases.\n")
		sb.WriteString("- Gunakan best practices dari bahasa pemrograman yang diminta.\n\n")
	case "analyst", "analyzer":
		sb.WriteString("## Pedoman Khusus Analyst:\n")
		sb.WriteString("- Analisis data secara sistematis dan terstruktur.\n")
		sb.WriteString("- Berikan insight yang actionable dan didukung bukti.\n")
		sb.WriteString("- Gunakan tabel atau bullet points untuk kejelasan.\n\n")
	case "researcher":
		sb.WriteString("## Pedoman Khusus Researcher:\n")
		sb.WriteString("- Lakukan riset mendalam dan komprehensif.\n")
		sb.WriteString("- Sertakan sumber dan referensi jika memungkinkan.\n")
		sb.WriteString("- Bandingkan multiple perspektif dan sudut pandang.\n\n")
	case "writer", "copywriter":
		sb.WriteString("## Pedoman Khusus Writer:\n")
		sb.WriteString("- Tulis dengan gaya yang sesuai konteks dan audiens.\n")
		sb.WriteString("- Perhatikan struktur, alur, dan kejelasan pesan.\n")
		sb.WriteString("- Gunakan bahasa yang menarik dan persuasif.\n\n")
	case "reviewer":
		sb.WriteString("## Pedoman Khusus Reviewer:\n")
		sb.WriteString("- Review secara kritis dan konstruktif.\n")
		sb.WriteString("- Identifikasi kekuatan dan kelemahan.\n")
		sb.WriteString("- Berikan saran perbaikan yang spesifik.\n\n")
	}

	sb.WriteString("## Prinsip Kerja Sub-Agen:\n")
	sb.WriteString("1. Fokus hanya pada instruksi tugas yang didelegasikan kepadamu.\n")
	sb.WriteString("2. Gunakan data/konteks terisolasi yang diberikan tanpa memerlukan seluruh riwayat percakapan.\n")
	sb.WriteString("3. Kembalikan hasil yang padat, akurat, solutif, dan terstruktur tanpa basa-basi pembuka yang berlebihan.\n")
	sb.WriteString("4. Jika tugas memerlukan tools, gunakan tools yang tersedia secara efisien.\n")

	return sb.String(), nil
}

