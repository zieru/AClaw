package whatsapp

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"goassistant/internal/storage"
)

type WizardStep int

const (
	StepNone WizardStep = iota
	StepWaitingNIK
	StepWaitingRole
)

type UserWizardSession struct {
	JID         string
	PhoneNumber string
	Name        string
	Step        WizardStep
	NIKSIAD     string
	UpdatedAt   time.Time
}

// GrapariAuthWizard handles Zero-LLM multi-step registration for GraPARI channels
type GrapariAuthWizard struct {
	mu       sync.RWMutex
	sessions map[string]*UserWizardSession // JID -> session
}

type GrapariAuthNotifier func(channelID, channelName string, user *storage.GrapariUser) error

var (
	globalWizard     *GrapariAuthWizard
	globalWizardOnce sync.Once
	gAuthNotifier    GrapariAuthNotifier
	gAuthLock        sync.RWMutex
)

// SetGrapariAuthNotifier sets the global notification callback when a user finishes registration
func SetGrapariAuthNotifier(notifier GrapariAuthNotifier) {
	gAuthLock.Lock()
	defer gAuthLock.Unlock()
	gAuthNotifier = notifier
}

func GetGrapariAuthWizard() *GrapariAuthWizard {
	globalWizardOnce.Do(func() {
		globalWizard = &GrapariAuthWizard{
			sessions: make(map[string]*UserWizardSession),
		}
	})
	return globalWizard
}

// Available roles for GraPARI operators
var validRoles = []string{
	"TL",
	"FOS",
	"CS",
	"Admin CC",
	"Admin Region",
	"Admin Area",
	"Lainnya",
}

// ParseRole resolves user input (number 1-7 or string) to a canonical role name
func ParseRole(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", false
	}

	switch trimmed {
	case "1":
		return "TL", true
	case "2":
		return "FOS", true
	case "3":
		return "CS", true
	case "4":
		return "Admin CC", true
	case "5":
		return "Admin Region", true
	case "6":
		return "Admin Area", true
	case "7":
		return "Lainnya", true
	}

	for _, r := range validRoles {
		if strings.EqualFold(trimmed, r) {
			return r, true
		}
	}

	return "", false
}

// FormatRolePrompt returns the text menu for role selection
func FormatRolePrompt() string {
	var sb strings.Builder
	sb.WriteString("📋 *PILIH ROLE ANDA:*\n\n")
	for i, r := range validRoles {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
	}
	sb.WriteString("\n_Silakan balas dengan angka 1-7 atau ketik nama role di atas._")
	return sb.String()
}

// HandleIncomingMessage evaluates and steps through the registration wizard
// Returns: (handled bool, replyText string, completedUser *storage.GrapariUser)
func (w *GrapariAuthWizard) HandleIncomingMessage(
	userJID, phoneNumber, userName, messageText string,
	existingUser *storage.GrapariUser,
) (bool, string, *storage.GrapariUser) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 1. If user is already AUTHORIZED, do not handle via wizard (proceed to normal AI)
	if existingUser != nil && existingUser.Status == storage.GrapariStatusAuthorized {
		return false, "", nil
	}

	// 2. If user is already PENDING, notify waiting status without LLM
	if existingUser != nil && existingUser.Status == storage.GrapariStatusPending {
		sess, inSess := w.sessions[userJID]
		// Allow re-register only if user explicitly typed /daftar or /ulang
		lower := strings.ToLower(strings.TrimSpace(messageText))
		if inSess || (lower != "/daftar" && lower != "/ulang" && lower != "!daftar") {
			reply := "⏳ *AKUN MENUNGGU VERIFIKASI*\n\n" +
				fmt.Sprintf("Halo *%s*, data pendaftaran Anda (NIK: `%s`, Role: `%s`) saat ini masih menunggu verifikasi.\n\n", existingUser.Name, existingUser.NIKSIAD, existingUser.Role) +
				"Mohon tunggu beberapa saat hingga otorisasi disetujui.\n_Ketik /ulang jika ingin memperbarui data pendaftaran._"
			if inSess && sess.Step != StepNone {
				// Continue session below if they started re-registering
			} else {
				return true, reply, nil
			}
		}
	}

	// 3. If user is REJECTED, notify rejection
	if existingUser != nil && existingUser.Status == storage.GrapariStatusRejected {
		lower := strings.ToLower(strings.TrimSpace(messageText))
		if lower != "/daftar" && lower != "/ulang" && lower != "!daftar" {
			reply := "⛔ *AKSES DITOLAK*\n\n" +
				fmt.Sprintf("Mohon maaf, permohonan akses untuk NIK `%s` (%s) belum disetujui oleh supervisor.\n\n", existingUser.NIKSIAD, existingUser.Name) +
				"Silakan hubungi Team Leader atau Admin Anda untuk info lebih lanjut.\n_Ketik /daftar untuk mengajukan permohonan ulang._"
			return true, reply, nil
		}
	}

	sess, ok := w.sessions[userJID]
	if !ok || sess == nil {
		sess = &UserWizardSession{
			JID:         userJID,
			PhoneNumber: phoneNumber,
			Name:        userName,
			Step:        StepNone,
			UpdatedAt:   time.Now(),
		}
		w.sessions[userJID] = sess
	}

	cleanText := strings.TrimSpace(messageText)

	switch sess.Step {
	case StepNone:
		// First interaction or restarted wizard
		sess.Step = StepWaitingNIK
		sess.UpdatedAt = time.Now()
		displayName := userName
		if displayName == "" {
			displayName = "Rekan GraPARI"
		}
		reply := fmt.Sprintf("👋 *SELAMAT DATANG DI ASISTEN LAYANAN GRAPARI*\n\n"+
			"Halo *%s*, untuk mengakses panduan SOP & operasional GraPARI, silakan lengkapi otorisasi identitas berikut:\n\n"+
			"👉 *Silakan kirimkan NIK atau SIAD Anda:*", displayName)
		return true, reply, nil

	case StepWaitingNIK:
		if len(cleanText) < 3 {
			return true, "⚠️ *NIK / SIAD tidak valid.*\nMohon kirimkan NIK atau SIAD Anda yang benar:", nil
		}
		sess.NIKSIAD = cleanText
		sess.Step = StepWaitingRole
		sess.UpdatedAt = time.Now()

		reply := fmt.Sprintf("✅ NIK/SIAD tercatat: `%s`\n\n%s", sess.NIKSIAD, FormatRolePrompt())
		return true, reply, nil

	case StepWaitingRole:
		role, valid := ParseRole(cleanText)
		if !valid {
			return true, fmt.Sprintf("⚠️ *Pilihan role tidak dikenali.*\n\n%s", FormatRolePrompt()), nil
		}

		delete(w.sessions, userJID)

		completedUser := &storage.GrapariUser{
			UserJID:     sess.JID,
			PhoneNumber: sess.PhoneNumber,
			Name:        sess.Name,
			NIKSIAD:     sess.NIKSIAD,
			Role:        role,
			Status:      storage.GrapariStatusPending,
			RequestedAt: time.Now(),
		}

		reply := fmt.Sprintf("🎉 *DATA REGISTRASI DITERIMA*\n\n"+
			"• *Nama:* %s\n"+
			"• *NIK / SIAD:* `%s`\n"+
			"• *Role:* %s\n"+
			"• *WhatsApp:* +%s\n\n"+
			"Permohonan akses Anda telah dikirimkan.\n"+
			"Anda akan menerima notifikasi di sini begitu akun telah diotorisasi. Terima kasih!",
			completedUser.Name, completedUser.NIKSIAD, completedUser.Role, completedUser.PhoneNumber)

		return true, reply, completedUser
	}

	return false, "", nil
}
