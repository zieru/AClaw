package channel

import (
	"context"
)

// Channel defines the interface for chat channels
type Channel interface {
	ID() string
	Type() string
	Name() string
	Start(ctx context.Context) error
	Stop() error
	SendMessage(targetID, text string) error
}
