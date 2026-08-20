package tools

import (
	"context"
	"fmt"
	"sync"
)

// Registry manages the set of available tools
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

var defaultRegistry *Registry
var registryOnce sync.Once

// GetRegistry returns the global tool registry
func GetRegistry() *Registry {
	registryOnce.Do(func() {
		defaultRegistry = &Registry{
			tools: make(map[string]Tool),
		}
		// Register built-in tools
		defaultRegistry.Register(&DateTimeTool{})
		defaultRegistry.Register(&WebSearchTool{})
		defaultRegistry.Register(&HTTPClientTool{})
		defaultRegistry.Register(&BashTool{})
	})
	return defaultRegistry
}

// Register adds a tool to the registry
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get finds a tool by name
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// ListAll returns all registered tools
func (r *Registry) ListAll() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []Tool
	for _, t := range r.tools {
		list = append(list, t)
	}
	return list
}

// ListAllowed returns tools filtered by allowed names (or all if map is nil or empty)
func (r *Registry) ListAllowed(allowedMap map[string]bool) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []Tool
	for name, t := range r.tools {
		if allowedMap != nil {
			if allowed, ok := allowedMap[name]; ok && !allowed {
				continue // explicitly disallowed
			}
		}
		list = append(list, t)
	}
	return list
}

// Execute runs a tool by name with arguments
func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	tool, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("tool '%s' tidak ditemukan", name)
	}
	return tool.Execute(ctx, args)
}
