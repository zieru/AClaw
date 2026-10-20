package tools

import (
	"sync"
	"time"
)

// SudoSession holds temporary in-memory sudo credentials with expiration
type SudoSession struct {
	Password  string
	ExpiresAt time.Time
}

var (
	sudoSessions   sync.Map // key: string (chatID or userID) -> SudoSession
	sessionTimeout = 5 * time.Minute
)

// SetSudoSession stores a sudo password in memory for 5 minutes
func SetSudoSession(key string, password string) {
	if key == "" || password == "" {
		return
	}
	sudoSessions.Store(key, SudoSession{
		Password:  password,
		ExpiresAt: time.Now().Add(sessionTimeout),
	})
}

// GetSudoSession retrieves valid (non-expired) sudo password from memory
func GetSudoSession(key string) string {
	if key == "" {
		return ""
	}
	val, ok := sudoSessions.Load(key)
	if !ok {
		return ""
	}
	sess, ok := val.(SudoSession)
	if !ok {
		return ""
	}
	if time.Now().After(sess.ExpiresAt) {
		sudoSessions.Delete(key)
		return ""
	}
	// Refresh expiration upon active use (similar to standard sudo grace period)
	sess.ExpiresAt = time.Now().Add(sessionTimeout)
	sudoSessions.Store(key, sess)
	return sess.Password
}

// ClearSudoSession removes stored sudo password immediately
func ClearSudoSession(key string) {
	if key != "" {
		sudoSessions.Delete(key)
	}
}
