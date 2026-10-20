package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// GrapariUser represents an authorized/registered user in a GraPARI channel
type GrapariUser struct {
	UserJID     string     `json:"user_jid"`
	PhoneNumber string     `json:"phone_number"`
	Name        string     `json:"name"`
	NIKSIAD     string     `json:"nik_siad"`
	Role        string     `json:"role"`
	Status      string     `json:"status"` // PENDING, AUTHORIZED, REJECTED
	RequestedAt time.Time  `json:"requested_at"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	ApprovedBy  string     `json:"approved_by,omitempty"`
}

const (
	GrapariStatusPending    = "PENDING"
	GrapariStatusAuthorized = "AUTHORIZED"
	GrapariStatusRejected   = "REJECTED"
)

// GrapariDB wraps the isolated SQLite database for a specific GraPARI channel
type GrapariDB struct {
	dbPath    string
	channelID string
	db        *sql.DB
	mu        sync.RWMutex
}

var (
	grapariDBRegistry   = make(map[string]*GrapariDB)
	grapariRegistryLock sync.Mutex
)

// CleanChannelFileName converts channel ID and Name into a safe filename
func CleanChannelFileName(channelID, channelName string) string {
	cleanName := strings.TrimSpace(channelName)
	reg := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	cleanName = reg.ReplaceAllString(cleanName, "_")
	cleanName = strings.Trim(cleanName, "_")

	cleanID := reg.ReplaceAllString(strings.TrimSpace(channelID), "_")
	cleanID = strings.Trim(cleanID, "_")

	if cleanName == "" {
		return fmt.Sprintf("grapari_%s.db", cleanID)
	}
	return fmt.Sprintf("grapari_%s_%s.db", cleanID, cleanName)
}

// OpenGrapariDB initializes and migrates an isolated SQLite database for a GraPARI channel
func OpenGrapariDB(dataDir, channelID, channelName string) (*GrapariDB, error) {
	grapariRegistryLock.Lock()
	defer grapariRegistryLock.Unlock()

	if instance, ok := grapariDBRegistry[channelID]; ok && instance != nil {
		return instance, nil
	}

	fileName := CleanChannelFileName(channelID, channelName)
	dbPath := filepath.Join(dataDir, fileName)

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create grapari db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open grapari sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping grapari sqlite: %w", err)
	}

	migrationSQL := `
	CREATE TABLE IF NOT EXISTS grapari_users (
		user_jid TEXT PRIMARY KEY,
		phone_number TEXT NOT NULL,
		name TEXT NOT NULL,
		nik_siad TEXT NOT NULL,
		role TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'PENDING',
		requested_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		approved_at DATETIME,
		approved_by TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_grapari_users_status ON grapari_users(status);

	CREATE TABLE IF NOT EXISTS grapari_config (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`

	if _, err := db.Exec(migrationSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to migrate grapari tables: %w", err)
	}

	gdb := &GrapariDB{
		dbPath:    dbPath,
		channelID: channelID,
		db:        db,
	}

	grapariDBRegistry[channelID] = gdb
	return gdb, nil
}

// Close closes the underlying database connection and removes it from registry
func (g *GrapariDB) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	grapariRegistryLock.Lock()
	delete(grapariDBRegistry, g.channelID)
	grapariRegistryLock.Unlock()

	if g.db != nil {
		return g.db.Close()
	}
	return nil
}

// CleanJID removes device index (e.g. 628xxx:73@s.whatsapp.net -> 628xxx@s.whatsapp.net)
func CleanJID(jid string) string {
	raw := strings.TrimSpace(jid)
	colon := strings.Index(raw, ":")
	at := strings.Index(raw, "@")
	if colon != -1 && at != -1 && colon < at {
		return raw[:colon] + raw[at:]
	}
	return raw
}

// NormalizePhone extracts clean digits and ensures 62 prefix for Indonesian numbers
func NormalizePhone(s string) string {
	var digits strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	str := digits.String()
	if strings.HasPrefix(str, "08") {
		str = "628" + str[2:]
	}
	return str
}

// GetUser returns a user by their JID or phone number, matching across multi-device variants
func (g *GrapariDB) GetUser(jid string) (*GrapariUser, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.getUserUnlocked(jid)
}

func (g *GrapariDB) getUserUnlocked(identifier string) (*GrapariUser, error) {
	raw := strings.TrimSpace(identifier)
	if raw == "" {
		return nil, nil
	}

	cleanJID := CleanJID(raw)

	userPart := raw
	if at := strings.Index(userPart, "@"); at != -1 {
		userPart = userPart[:at]
	}
	if colon := strings.Index(userPart, ":"); colon != -1 {
		userPart = userPart[:colon]
	}

	normPhone := NormalizePhone(userPart)
	if normPhone == "" {
		normPhone = NormalizePhone(raw)
	}

	phoneJID := ""
	if normPhone != "" {
		phoneJID = normPhone + "@s.whatsapp.net"
	}

	patternColon := ""
	patternAt := ""
	if normPhone != "" {
		patternColon = normPhone + ":%"
		patternAt = normPhone + "@%"
	}

	query := `
	SELECT user_jid, phone_number, name, nik_siad, role, status, requested_at, approved_at, approved_by
	FROM grapari_users 
	WHERE user_jid = ? 
	   OR user_jid = ?
	   OR user_jid = ?
	   OR phone_number = ?
	   OR phone_number = ?
	   OR (user_jid LIKE ? AND ? != '')
	   OR (user_jid LIKE ? AND ? != '')
	ORDER BY 
	   CASE 
	       WHEN status = 'AUTHORIZED' THEN 1 
	       WHEN status = 'PENDING' THEN 2 
	       ELSE 3 
	   END,
	   requested_at DESC
	LIMIT 1
	`
	var u GrapariUser
	var approvedAt sql.NullTime
	var approvedBy sql.NullString

	err := g.db.QueryRow(query, raw, cleanJID, phoneJID, userPart, normPhone, patternColon, patternColon, patternAt, patternAt).Scan(
		&u.UserJID,
		&u.PhoneNumber,
		&u.Name,
		&u.NIKSIAD,
		&u.Role,
		&u.Status,
		&u.RequestedAt,
		&approvedAt,
		&approvedBy,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if approvedAt.Valid {
		u.ApprovedAt = &approvedAt.Time
	}
	if approvedBy.Valid {
		u.ApprovedBy = approvedBy.String
	}

	return &u, nil
}

// UpsertUser inserts or updates a user in the GraPARI channel database
func (g *GrapariDB) UpsertUser(u *GrapariUser) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	status := u.Status
	if status == "" {
		status = GrapariStatusPending
	}

	cleanJID := CleanJID(u.UserJID)
	normPhone := NormalizePhone(u.PhoneNumber)
	if normPhone == "" {
		normPhone = NormalizePhone(u.UserJID)
	}

	// Check if a record already exists for this JID or phone number
	existing, _ := g.getUserUnlocked(cleanJID)
	if existing == nil && normPhone != "" {
		existing, _ = g.getUserUnlocked(normPhone)
	}

	if existing != nil {
		updateQuery := `
		UPDATE grapari_users 
		SET user_jid = ?, phone_number = ?, name = ?, nik_siad = ?, role = ?, status = ?, requested_at = CURRENT_TIMESTAMP
		WHERE user_jid = ? OR phone_number = ?
		`
		_, err := g.db.Exec(updateQuery, cleanJID, normPhone, u.Name, u.NIKSIAD, u.Role, status, existing.UserJID, normPhone)
		return err
	}

	insertQuery := `
	INSERT INTO grapari_users (user_jid, phone_number, name, nik_siad, role, status, requested_at)
	VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(user_jid) DO UPDATE SET
		phone_number = excluded.phone_number,
		name = excluded.name,
		nik_siad = excluded.nik_siad,
		role = excluded.role,
		status = excluded.status,
		requested_at = CURRENT_TIMESTAMP
	`
	_, err := g.db.Exec(insertQuery, cleanJID, normPhone, u.Name, u.NIKSIAD, u.Role, status)
	return err
}

// SetUserStatus updates user status (e.g. AUTHORIZED or REJECTED) along with the approver info
func (g *GrapariDB) SetUserStatus(jid, status, approvedBy string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	cleanJID := CleanJID(jid)
	userPart := jid
	if at := strings.Index(userPart, "@"); at != -1 {
		userPart = userPart[:at]
	}
	if colon := strings.Index(userPart, ":"); colon != -1 {
		userPart = userPart[:colon]
	}
	normPhone := NormalizePhone(userPart)
	if normPhone == "" {
		normPhone = NormalizePhone(jid)
	}

	pattern := ""
	if normPhone != "" {
		pattern = normPhone + "%"
	}

	query := `
	UPDATE grapari_users
	SET status = ?, approved_at = CURRENT_TIMESTAMP, approved_by = ?
	WHERE user_jid = ? 
	   OR user_jid = ? 
	   OR phone_number = ? 
	   OR phone_number = ?
	   OR (user_jid LIKE ? AND ? != '')
	`
	res, err := g.db.Exec(query, status, approvedBy, jid, cleanJID, userPart, normPhone, pattern, pattern)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("user %s not found", jid)
	}
	return nil
}

// SetApprovalGroup saves the target Telegram group chat ID for approval notifications
func (g *GrapariDB) SetApprovalGroup(chatID int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	query := `
	INSERT INTO grapari_config (key, value, updated_at)
	VALUES ('telegram_approval_group_id', ?, CURRENT_TIMESTAMP)
	ON CONFLICT(key) DO UPDATE SET
		value = excluded.value,
		updated_at = CURRENT_TIMESTAMP
	`
	_, err := g.db.Exec(query, fmt.Sprintf("%d", chatID))
	return err
}

// GetApprovalGroup returns the target Telegram group chat ID, or 0 if not configured
func (g *GrapariDB) GetApprovalGroup() (int64, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var valStr string
	err := g.db.QueryRow("SELECT value FROM grapari_config WHERE key = 'telegram_approval_group_id'").Scan(&valStr)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var chatID int64
	_, parseErr := fmt.Sscanf(valStr, "%d", &chatID)
	return chatID, parseErr
}

// ListUsers returns all users in the GraPARI channel, optionally filtered by status
func (g *GrapariDB) ListUsers(statusFilter string) ([]*GrapariUser, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var query string
	var args []interface{}
	if statusFilter != "" {
		query = `SELECT user_jid, phone_number, name, nik_siad, role, status, requested_at, approved_at, approved_by 
		         FROM grapari_users WHERE status = ? ORDER BY requested_at DESC`
		args = append(args, statusFilter)
	} else {
		query = `SELECT user_jid, phone_number, name, nik_siad, role, status, requested_at, approved_at, approved_by 
		         FROM grapari_users ORDER BY requested_at DESC`
	}

	rows, err := g.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*GrapariUser
	for rows.Next() {
		var u GrapariUser
		var approvedAt sql.NullTime
		var approvedBy sql.NullString
		if err := rows.Scan(
			&u.UserJID,
			&u.PhoneNumber,
			&u.Name,
			&u.NIKSIAD,
			&u.Role,
			&u.Status,
			&u.RequestedAt,
			&approvedAt,
			&approvedBy,
		); err != nil {
			return nil, err
		}
		if approvedAt.Valid {
			u.ApprovedAt = &approvedAt.Time
		}
		if approvedBy.Valid {
			u.ApprovedBy = approvedBy.String
		}
		list = append(list, &u)
	}
	return list, nil
}
