package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/config"
	"goassistant/internal/storage"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// NativeAdapter manages a native WhatsApp Multi-Device connection
type NativeAdapter struct {
	channelID       string
	name            string
	device          *store.Device
	client          *whatsmeow.Client
	orchestrator    *agent.Orchestrator
	db              *storage.DB
	settings        WhatsAppSettings
	mu              sync.RWMutex
	activeTasks     sync.Map // chatID -> context.CancelFunc
	lastQRBytes     []byte
	lastQRStr       string
	isPairing       bool
	qrChan          <-chan whatsmeow.QRChannelItem
	cancelQR        context.CancelFunc
	onLoginSuccess  func()
}

func NewNativeAdapter(channelID, name string, device *store.Device, settings WhatsAppSettings, orch *agent.Orchestrator, db *storage.DB) *NativeAdapter {
	store.SetOSInfo("Chrome (Windows)", [3]uint32{128, 0, 0})

	adapter := &NativeAdapter{
		channelID:    channelID,
		name:         name,
		device:       device,
		settings:     settings,
		orchestrator: orch,
		db:           db,
	}

	logger := waLog.Stdout("WA-"+channelID, "WARN", true)
	adapter.client = whatsmeow.NewClient(device, logger)
	adapter.client.AddEventHandler(adapter.handleEvent)

	return adapter
}

func (a *NativeAdapter) ID() string   { return a.channelID }
func (a *NativeAdapter) Type() string { return "whatsapp" }
func (a *NativeAdapter) Name() string { return a.name }

func (a *NativeAdapter) GetSettings() WhatsAppSettings {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.settings
}

func (a *NativeAdapter) UpdateSettings(s WhatsAppSettings) error {
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	return a.saveSettings()
}

func (a *NativeAdapter) saveSettings() error {
	a.mu.RLock()
	stBytes, _ := json.Marshal(a.settings)
	a.mu.RUnlock()

	ch, err := a.db.GetChannel(a.channelID)
	if err != nil || ch == nil {
		return err
	}
	ch.SettingsJSON = string(stBytes)
	if a.device.ID != nil {
		ch.Identifier = a.device.ID.String()
	}
	return a.db.SaveChannel(ch)
}

func (a *NativeAdapter) IsConnected() bool {
	return a.client != nil && a.client.IsConnected()
}

func (a *NativeAdapter) IsLoggedIn() bool {
	return a.client != nil && a.client.IsLoggedIn()
}

func (a *NativeAdapter) GetLastQR() ([]byte, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastQRBytes, a.lastQRStr
}

func (a *NativeAdapter) Start(ctx context.Context) error {
	if a.client == nil {
		return fmt.Errorf("client whatsmeow belum diinisialisasi")
	}

	if a.client.Store.ID == nil {
		// Device not logged in yet, start QR channel
		a.StartPairing()
	} else {
		// Connect directly
		if err := a.client.Connect(); err != nil {
			log.Printf("⚠️ [Channel-WA] Gagal menghubungkan WhatsApp '%s': %v", a.name, err)
			return err
		}
		log.Printf("🟢 [Channel-WA] WhatsApp '%s' terhubung (JID: %s)", a.name, a.client.Store.ID.String())
	}
	return nil
}

func (a *NativeAdapter) Stop() error {
	if a.cancelQR != nil {
		a.cancelQR()
	}
	if a.client != nil {
		a.client.Disconnect()
	}
	return nil
}

func (a *NativeAdapter) SetOnLoginSuccess(cb func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onLoginSuccess = cb
}

// StartPairing initiates the QR code generation loop
// StartPairing initiates the QR code generation loop
func (a *NativeAdapter) StartPairing() error {
	a.mu.Lock()
	if a.cancelQR != nil {
		a.cancelQR()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelQR = cancel
	a.isPairing = true
	a.mu.Unlock()

	// Ensure client is connected before requesting QR channel
	if !a.client.IsConnected() {
		if err := a.client.Connect(); err != nil {
			return fmt.Errorf("gagal menghubungkan ke server WhatsApp: %w", err)
		}
	}

	qrChan, err := a.client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("gagal mendapatkan QR channel: %w", err)
	}

	go func() {
		for item := range qrChan {
			if item.Event == "code" {
				// Generate QR Code PNG with good resolution & border
				pngBytes, err := qrcode.Encode(item.Code, qrcode.Medium, 400)
				a.mu.Lock()
				if err == nil {
					a.lastQRBytes = pngBytes
				}
				a.lastQRStr = item.Code
				a.mu.Unlock()
				log.Printf("📱 [Channel-WA] QR Code baru dihasilkan untuk channel '%s'", a.name)
			} else if item.Event == "success" {
				a.mu.Lock()
				a.isPairing = false
				a.lastQRBytes = nil
				a.lastQRStr = ""
				cb := a.onLoginSuccess
				a.mu.Unlock()

				log.Printf("🎉 [Channel-WA] WhatsApp '%s' BERHASIL TERHUBUNG!", a.name)
				if a.client.Store.ID != nil {
					a.mu.Lock()
					a.settings.JID = a.client.Store.ID.String()
					a.mu.Unlock()
					_ = a.saveSettings()
				}
				if cb != nil {
					go cb()
				}
				return
			} else if item.Event == "timeout" {
				log.Printf("⏳ [Channel-WA] QR Code timeout untuk channel '%s'", a.name)
			}
		}
	}()

	return nil
}

// PairPhone requests an 8-character pairing code for linking via phone number
func (a *NativeAdapter) PairPhone(phone string) (string, error) {
	if !a.client.IsConnected() {
		if err := a.client.Connect(); err != nil {
			return "", fmt.Errorf("gagal menghubungkan ke WhatsApp: %w", err)
		}
	}
	cleanPhone := strings.TrimPrefix(strings.TrimSpace(phone), "+")
	cleanPhone = strings.ReplaceAll(cleanPhone, " ", "")
	cleanPhone = strings.ReplaceAll(cleanPhone, "-", "")

	code, err := a.client.PairPhone(context.Background(), cleanPhone, true, whatsmeow.PairClientChrome, "Chrome (Windows)")
	if err != nil {
		return "", fmt.Errorf("gagal meminta pairing code: %w", err)
	}
	return code, nil
}

// Logout unlinks and deletes session
func (a *NativeAdapter) Logout(ctx context.Context) error {
	if a.client != nil && a.client.IsLoggedIn() {
		_ = a.client.Logout(ctx)
	}
	a.mu.Lock()
	a.settings.JID = ""
	a.settings.PushName = ""
	a.lastQRBytes = nil
	a.lastQRStr = ""
	a.mu.Unlock()
	return a.saveSettings()
}

// SendMessage sends an outgoing text message to a WhatsApp number/JID
func (a *NativeAdapter) SendMessage(targetID, text string) error {
	if a.client == nil || !a.client.IsConnected() {
		return fmt.Errorf("whatsapp client '%s' belum terkoneksi", a.name)
	}

	targetJID, err := parseJID(targetID)
	if err != nil {
		return fmt.Errorf("invalid WhatsApp JID %s: %w", targetID, err)
	}

	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	_, err = a.client.SendMessage(context.Background(), targetJID, msg)
	return err
}

// SendFile uploads and sends an image or document attachment to a WhatsApp number/JID
func (a *NativeAdapter) SendFile(targetID, filePath, caption string) error {
	if a.client == nil || !a.client.IsConnected() {
		return fmt.Errorf("whatsapp client '%s' belum terkoneksi", a.name)
	}

	targetJID, err := parseJID(targetID)
	if err != nil {
		return fmt.Errorf("invalid WhatsApp JID %s: %w", targetID, err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("gagal membaca file %s: %w", filePath, err)
	}

	mimeType := http.DetectContentType(data)
	ext := strings.ToLower(filepath.Ext(filePath))
	isImage := strings.HasPrefix(mimeType, "image/") || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp"

	if isImage {
		uploaded, err := a.client.Upload(context.Background(), data, whatsmeow.MediaImage)
		if err != nil {
			return fmt.Errorf("gagal upload gambar WhatsApp: %w", err)
		}
		msg := &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(mimeType),
				URL:           &uploaded.URL,
				DirectPath:    &uploaded.DirectPath,
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		}
		_, err = a.client.SendMessage(context.Background(), targetJID, msg)
		return err
	}

	uploaded, err := a.client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("gagal upload dokumen WhatsApp: %w", err)
	}
	fileName := filepath.Base(filePath)
	msg := &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{
			Caption:       proto.String(caption),
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			Mimetype:      proto.String(mimeType),
			URL:           &uploaded.URL,
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
		},
	}
	_, err = a.client.SendMessage(context.Background(), targetJID, msg)
	return err
}

func parseJID(target string) (waTypes.JID, error) {
	if strings.Contains(target, "@") {
		return waTypes.ParseJID(target)
	}
	// Default to standard user JID
	cleanNum := strings.TrimPrefix(target, "+")
	return waTypes.NewJID(cleanNum, waTypes.DefaultUserServer), nil
}

// handleEvent processes incoming events from whatsmeow
func (a *NativeAdapter) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		a.handleMessage(v)
	case *events.Connected:
		log.Printf("🟢 [Channel-WA] WhatsApp '%s' tersambung ke server.", a.name)
		if a.client.Store.ID != nil {
			a.mu.Lock()
			a.settings.JID = a.client.Store.ID.String()
			a.mu.Unlock()
			_ = a.saveSettings()
		}
	case *events.LoggedOut:
		log.Printf("🔴 [Channel-WA] WhatsApp '%s' LOGGED OUT dari perangkat.", a.name)
		a.mu.Lock()
		a.settings.JID = ""
		a.mu.Unlock()
		_ = a.saveSettings()
	}
}

// handleMessage evaluates policies and routes incoming messages to Orchestrator
func (a *NativeAdapter) handleMessage(msg *events.Message) {
	if msg.Info.IsFromMe {
		return // Ignore own messages
	}

	chatJID := msg.Info.Chat
	senderJID := msg.Info.Sender
	isGroup := msg.Info.IsGroup

	chatID := chatJID.String()
	senderID := senderJID.User
	if senderID == "" {
		senderID = senderJID.String()
	}
	senderName := msg.Info.PushName
	if senderName == "" {
		senderName = senderID
	}

	// 1. Extract Text
	text := extractMessageText(msg.Message)
	cleanText := strings.TrimSpace(text)
	lowerText := strings.ToLower(cleanText)

	log.Printf("📩 [Channel-WA] Pesan masuk dari %s (%s) [Group: %v]: '%s'", senderJID.String(), chatID, isGroup, cleanText)

	// 2. Policy Checks
	a.mu.RLock()
	st := a.settings
	a.mu.RUnlock()

	if isGroup {
		// Group Policy
		if st.GroupPolicy == GroupPolicyBlock {
			log.Printf("🛡️ [Channel-WA] Pesan grup %s diabaikan (Policy: Block Groups)", chatID)
			return // Group messages blocked
		}
		if st.GroupPolicy == GroupPolicyWhitelist {
			allowed := false
			for _, g := range st.AllowedGroups {
				cleanG := strings.TrimSpace(g)
				if strings.EqualFold(cleanG, chatID) || strings.EqualFold(cleanG, chatJID.User) || strings.Contains(chatID, cleanG) {
					allowed = true
					break
				}
			}
			if !allowed {
				log.Printf("🛡️ [Channel-WA] Pesan grup %s diabaikan karena tidak ada di Whitelist Groups", chatID)
				return // Group not in whitelist
			}
		}

		// Mention Policy
		if st.MentionPolicy == MentionPolicyRequire {
			if !a.isBotMentionedOrReplied(msg) {
				return // Silently ignore if not mentioned or replied
			}
		}
	} else {
		// DM Policy
		if st.DMPolicy == DMPolicyBlock {
			log.Printf("🛡️ [Channel-WA] Pesan DM dari %s diabaikan (Policy: Block All DM)", senderJID.String())
			return // DM blocked
		}
		if st.DMPolicy == DMPolicyTrusted {
			trusted := false
			for _, num := range st.TrustedNumbers {
				if isPhoneMatching(num, senderJID, chatJID) {
					trusted = true
					break
				}
			}
			if !trusted {
				log.Printf("🛡️ [Channel-WA] Pesan DM dari %s (%s) diabaikan karena tidak terdaftar di Trusted List (%v)", senderJID.String(), chatID, st.TrustedNumbers)
				return // Sender not in trusted list
			}
		}
	}

	// 3. Handle Interactive Commands
	if lowerText == "/stop" || lowerText == "!stop" || lowerText == "/cancel" || lowerText == "stop" || lowerText == "batal" {
		if cancelVal, loaded := a.activeTasks.LoadAndDelete(chatID); loaded {
			if cancel, ok := cancelVal.(context.CancelFunc); ok {
				cancel()
			}
			go func() {
				_ = a.SendMessage(chatID, "🛑 *PROSES DIHENTIKAN*\n\nRespon AI yang sedang diproses telah dibatalkan.")
			}()
		} else {
			go func() {
				_ = a.SendMessage(chatID, "ℹ️ Tidak ada proses respon AI yang sedang berjalan.")
			}()
		}
		return
	}

	if lowerText == "/new" || lowerText == "/reset" || lowerText == "!reset" || lowerText == "/clear" {
		if cancelVal, loaded := a.activeTasks.LoadAndDelete(chatID); loaded {
			if cancel, ok := cancelVal.(context.CancelFunc); ok {
				cancel()
			}
		}
		session, err := a.db.GetOrCreateSession(a.channelID, chatID, senderID)
		if err == nil && session != nil {
			_ = a.db.ClearSessionMessages(session.ID)
		}
		go func() {
			_ = a.SendMessage(chatID, "✨ *SESI BARU DIMULAI*\n\nRiwayat dan konteks percakapan telah direset.\nSilakan ajukan pertanyaan atau instruksi baru!")
		}()
		return
	}

	if lowerText == "/help" || lowerText == "!help" {
		go func() {
			helpText := "👋 *PANDUAN ASISTEN AI (WHATSAPP)*\n\n" +
				"Silakan kirimkan pertanyaan atau perintah langsung di chat ini.\n\n" +
				"📌 *Daftar Perintah:*\n" +
				"• */new* - Mulai sesi baru & reset riwayat percakapan\n" +
				"• */stop* - Batalkan/hentikan proses respon AI\n" +
				"• */status* - Cek status sesi & kebijakan limit\n" +
				"• */help* - Buka panduan ini"
			_ = a.SendMessage(chatID, helpText)
		}()
		return
	}

	if lowerText == "/status" || lowerText == "!status" {
		go func() {
			session, _ := a.db.GetOrCreateSession(a.channelID, chatID, senderID)
			msgCount := 0
			if session != nil {
				msgCount, _ = a.db.CountSessionMessages(session.ID)
			}
			policy := a.db.GetResolvedPolicy(a.channelID, chatID)

			statusText := fmt.Sprintf("🤖 *STATUS ASISTEN AI (WHATSAPP)*\n\n"+
				"• Channel: *%s*\n"+
				"• Chat ID: `%s`\n"+
				"• Sesi Aktif: `%d pesan` (Maks: `%d`)\n"+
				"• Token Saver: `%s`\n"+
				"• Model Override: `%s`\n\n"+
				"💡 Gunakan */stop* untuk membatalkan atau */new* untuk reset sesi.",
				a.name, chatID, msgCount, policy.MaxHistoryTurns, policy.TokenSaverMode, policy.ModelOverride)
			_ = a.SendMessage(chatID, statusText)
		}()
		return
	}

	if cleanText == "" {
		return
	}

	// 4. Process Message through Orchestrator
	go func() {
		if cancelVal, loaded := a.activeTasks.LoadAndDelete(chatID); loaded {
			if cancel, ok := cancelVal.(context.CancelFunc); ok {
				cancel()
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(config.Get().Timeouts.HandlerSeconds)*time.Second)
		a.activeTasks.Store(chatID, cancel)
		defer func() {
			a.activeTasks.Delete(chatID)
			cancel()
		}()

		resp, err := a.orchestrator.ProcessMessage(ctx, agent.UserRequest{
			ChannelType: "whatsapp",
			ChannelID:   a.channelID,
			ChannelName: a.name,
			ChatID:      chatID,
			UserID:      senderID,
			UserName:    senderName,
			UserPrompt:  cleanText,
		})

		if err != nil {
			if ctx.Err() == context.Canceled {
				return
			}
			friendlyErr := agent.FormatUserFriendlyError(err)
			_ = a.SendMessage(chatID, friendlyErr)
			return
		}

		if resp != nil {
			if resp.Text != "" {
				_ = a.SendMessage(chatID, resp.Text)
			}
			for _, mf := range resp.MediaFiles {
				if mf.FilePath != "" {
					_ = a.SendFile(chatID, mf.FilePath, mf.Caption)
				}
			}
		}
	}()
}

// isBotMentionedOrReplied checks if the bot is @mentioned or quoted in a group message
func (a *NativeAdapter) isBotMentionedOrReplied(msg *events.Message) bool {
	if a.client.Store.ID == nil {
		return false
	}
	botUser := a.client.Store.ID.User

	// Check ContextInfo for mentions and reply
	ctxInfo := getContextInfo(msg.Message)
	if ctxInfo != nil {
		for _, m := range ctxInfo.GetMentionedJID() {
			if strings.Contains(m, botUser) {
				return true
			}
		}
		if ctxInfo.GetParticipant() != "" && strings.Contains(ctxInfo.GetParticipant(), botUser) {
			return true
		}
	}

	// Check if prompt includes @botUser or name
	text := extractMessageText(msg.Message)
	if strings.Contains(text, "@"+botUser) {
		return true
	}

	return false
}

func extractMessageText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if conv := m.GetConversation(); conv != "" {
		return conv
	}
	if ext := m.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	if img := m.GetImageMessage(); img != nil {
		return img.GetCaption()
	}
	if vid := m.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}
	return ""
}

func getContextInfo(m *waE2E.Message) *waE2E.ContextInfo {
	if m == nil {
		return nil
	}
	if ext := m.GetExtendedTextMessage(); ext != nil {
		return ext.GetContextInfo()
	}
	if img := m.GetImageMessage(); img != nil {
		return img.GetContextInfo()
	}
	if vid := m.GetVideoMessage(); vid != nil {
		return vid.GetContextInfo()
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		return doc.GetContextInfo()
	}
	return nil
}

func normalizePhone(s string) string {
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

func isPhoneMatching(trustedPattern string, senderJID, chatJID waTypes.JID) bool {
	normTrusted := normalizePhone(trustedPattern)
	if normTrusted == "" {
		return false
	}

	candidates := []string{
		normalizePhone(senderJID.User),
		normalizePhone(senderJID.ToNonAD().User),
		normalizePhone(chatJID.User),
		normalizePhone(chatJID.ToNonAD().User),
	}

	for _, c := range candidates {
		if c != "" && (c == normTrusted || strings.HasSuffix(c, normTrusted) || strings.HasSuffix(normTrusted, c)) {
			return true
		}
	}

	cleanTrusted := strings.TrimPrefix(strings.TrimSpace(trustedPattern), "+")
	if strings.Contains(senderJID.String(), cleanTrusted) || strings.Contains(chatJID.String(), cleanTrusted) {
		return true
	}

	return false
}
