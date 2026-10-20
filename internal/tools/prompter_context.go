package tools

import "context"

// PasswordPrompter defines the interface for prompting passwords interactively
// out-of-band (e.g. via Telegram ForceReply dialog) without leaking to AI.
type PasswordPrompter interface {
	PromptPassword(ctx context.Context, title, description string) (password string, finish func(), err error)
}

type passwordPrompterKey struct{}

// WithPasswordPrompter attaches a PasswordPrompter to the context
func WithPasswordPrompter(ctx context.Context, p PasswordPrompter) context.Context {
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, passwordPrompterKey{}, p)
}

// GetPasswordPrompter extracts a PasswordPrompter from the context
func GetPasswordPrompter(ctx context.Context) PasswordPrompter {
	if ctx == nil {
		return nil
	}
	if p, ok := ctx.Value(passwordPrompterKey{}).(PasswordPrompter); ok {
		return p
	}
	return nil
}
