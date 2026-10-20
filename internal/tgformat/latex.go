package tgformat

import (
	"regexp"
	"strings"
)

var (
	blockMathRegex      = regexp.MustCompile(`(?s)\$\$(.*?)\$\$`)
	inlineMathRegex     = regexp.MustCompile(`\$([^\$\n]+)\$`)
	textMacroRegex      = regexp.MustCompile(`\\(?:text|mathrm|operatorname)\{([^}]+)\}`)
	boldMacroRegex      = regexp.MustCompile(`\\(?:mathbf|textbf)\{([^}]+)\}`)
	italicMacroRegex    = regexp.MustCompile(`\\(?:mathit|textit)\{([^}]+)\}`)
	underlineMacroRegex = regexp.MustCompile(`\\underline\{([^}]+)\}`)
	fracMacroRegex      = regexp.MustCompile(`\\frac\{([^}]+)\}\{([^}]+)\}`)
	sqrtMacroRegex      = regexp.MustCompile(`\\sqrt\{([^}]+)\}`)
)

// CleanLaTeXMath converts LaTeX math notation and symbols into readable text with Unicode characters.
func CleanLaTeXMath(text string) string {
	if !strings.Contains(text, `\`) && !strings.Contains(text, "$") {
		return text
	}

	// 1. Convert block math $$ ... $$
	text = blockMathRegex.ReplaceAllStringFunc(text, func(match string) string {
		sub := blockMathRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return convertMathExpression(sub[1])
	})

	// 2. Convert inline math $ ... $
	text = inlineMathRegex.ReplaceAllStringFunc(text, func(match string) string {
		sub := inlineMathRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		content := sub[1]
		// Don't convert if it's plain text without math/latex indicators (e.g. currency "$100 to $200")
		if !isLikelyMath(content) {
			return match
		}
		return convertMathExpression(content)
	})

	// 3. Convert any standalone LaTeX macros that might not be wrapped in $
	text = convertMathExpression(text)

	return text
}

func isLikelyMath(s string) bool {
	if strings.Contains(s, `\`) || strings.Contains(s, "^") || strings.Contains(s, "_") ||
		strings.Contains(s, "=") || strings.Contains(s, "×") || strings.Contains(s, "÷") ||
		strings.Contains(s, "±") || strings.Contains(s, "≈") || strings.Contains(s, "≠") ||
		strings.Contains(s, "≤") || strings.Contains(s, "≥") {
		return true
	}
	return false
}

func convertMathExpression(s string) string {
	// Nested macros: resolve text/bold/italic inside first (up to 3 passes)
	for i := 0; i < 3; i++ {
		prev := s
		s = textMacroRegex.ReplaceAllString(s, "$1")
		s = boldMacroRegex.ReplaceAllString(s, "**$1**")
		s = italicMacroRegex.ReplaceAllString(s, "*$1*")
		s = underlineMacroRegex.ReplaceAllString(s, "__$1__")
		s = fracMacroRegex.ReplaceAllString(s, "$1 / $2")
		s = sqrtMacroRegex.ReplaceAllString(s, "√($1)")
		if s == prev {
			break
		}
	}

	// Symbols mapping
	replacements := []struct {
		pattern *regexp.Regexp
		repl    string
	}{
		{regexp.MustCompile(`\\Delta\s*`), "Δ"},
		{regexp.MustCompile(`\\delta\s*`), "δ"},
		{regexp.MustCompile(`\\Omega\s*`), "Ω"},
		{regexp.MustCompile(`\\omega\s*`), "ω"},
		{regexp.MustCompile(`\\mu\s*`), "μ"},
		{regexp.MustCompile(`\\pi\s*`), "π"},
		{regexp.MustCompile(`\\Pi\s*`), "Π"},
		{regexp.MustCompile(`\\theta\s*`), "θ"},
		{regexp.MustCompile(`\\Theta\s*`), "Θ"},
		{regexp.MustCompile(`\\lambda\s*`), "λ"},
		{regexp.MustCompile(`\\Lambda\s*`), "Λ"},
		{regexp.MustCompile(`\\alpha\s*`), "α"},
		{regexp.MustCompile(`\\beta\s*`), "β"},
		{regexp.MustCompile(`\\gamma\s*`), "γ"},
		{regexp.MustCompile(`\\Gamma\s*`), "Γ"},
		{regexp.MustCompile(`\\sigma\s*`), "σ"},
		{regexp.MustCompile(`\\Sigma\s*`), "Σ"},
		{regexp.MustCompile(`\\times\b`), "×"},
		{regexp.MustCompile(`\\cdot\b`), "·"},
		{regexp.MustCompile(`\\bullet\b`), "·"},
		{regexp.MustCompile(`\\div\b`), "÷"},
		{regexp.MustCompile(`\\pm\b`), "±"},
		{regexp.MustCompile(`\\mp\b`), "∓"},
		{regexp.MustCompile(`\\approx\b`), "≈"},
		{regexp.MustCompile(`\\neq\b`), "≠"},
		{regexp.MustCompile(`\\leq\b`), "≤"},
		{regexp.MustCompile(`\\le\b`), "≤"},
		{regexp.MustCompile(`\\geq\b`), "≥"},
		{regexp.MustCompile(`\\ge\b`), "≥"},
		{regexp.MustCompile(`\\ll\b`), "≪"},
		{regexp.MustCompile(`\\gg\b`), "≫"},
		{regexp.MustCompile(`\\infty\b`), "∞"},
		{regexp.MustCompile(`\\degree\b`), "°"},
		{regexp.MustCompile(`\^\\circ\b`), "°"},
		{regexp.MustCompile(`\\circ\b`), "°"},
		{regexp.MustCompile(`\\rightarrow\b`), "→"},
		{regexp.MustCompile(`\\to\b`), "→"},
		{regexp.MustCompile(`\\leftarrow\b`), "←"},
		{regexp.MustCompile(`\\Rightarrow\b`), "⇒"},
		{regexp.MustCompile(`\\Leftarrow\b`), "⇐"},
		{regexp.MustCompile(`\\sum\b`), "∑"},
		{regexp.MustCompile(`\\prod\b`), "∏"},
		{regexp.MustCompile(`\\int\b`), "∫"},
		{regexp.MustCompile(`\\quad\b`), "  "},
		{regexp.MustCompile(`\\qquad\b`), "    "},
		{regexp.MustCompile(`\\,\b`), " "},
		{regexp.MustCompile(`\\;\b`), " "},

		// Superscripts
		{regexp.MustCompile(`\^\{\-1\}`), "⁻¹"},
		{regexp.MustCompile(`\^\{\-2\}`), "⁻²"},
		{regexp.MustCompile(`\^\{\-3\}`), "⁻³"},
		{regexp.MustCompile(`\^2\b`), "²"},
		{regexp.MustCompile(`\^3\b`), "³"},
		{regexp.MustCompile(`\^1\b`), "¹"},
		{regexp.MustCompile(`\^0\b`), "⁰"},
		{regexp.MustCompile(`\^4\b`), "⁴"},
		{regexp.MustCompile(`\^5\b`), "⁵"},
		{regexp.MustCompile(`\^6\b`), "⁶"},
		{regexp.MustCompile(`\^7\b`), "⁷"},
		{regexp.MustCompile(`\^8\b`), "⁸"},
		{regexp.MustCompile(`\^9\b`), "⁹"},
		{regexp.MustCompile(`\^\{2\}`), "²"},
		{regexp.MustCompile(`\^\{3\}`), "³"},
		{regexp.MustCompile(`\^\{1\}`), "¹"},
		{regexp.MustCompile(`\^\{0\}`), "⁰"},
		{regexp.MustCompile(`\^T\b`), "ᵀ"},

		// Subscripts
		{regexp.MustCompile(`_0\b`), "₀"},
		{regexp.MustCompile(`_1\b`), "₁"},
		{regexp.MustCompile(`_2\b`), "₂"},
		{regexp.MustCompile(`_3\b`), "₃"},
		{regexp.MustCompile(`_4\b`), "₄"},
		{regexp.MustCompile(`_5\b`), "₅"},
		{regexp.MustCompile(`_6\b`), "₆"},
		{regexp.MustCompile(`_7\b`), "₇"},
		{regexp.MustCompile(`_8\b`), "₈"},
		{regexp.MustCompile(`_9\b`), "₉"},
		{regexp.MustCompile(`_\{0\}`), "₀"},
		{regexp.MustCompile(`_\{1\}`), "₁"},
		{regexp.MustCompile(`_\{2\}`), "₂"},
		{regexp.MustCompile(`_\{3\}`), "₃"},
	}

	for _, r := range replacements {
		s = r.pattern.ReplaceAllString(s, r.repl)
	}

	return strings.TrimSpace(s)
}
