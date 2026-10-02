package admin

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"goassistant/internal/memory"
	"goassistant/internal/storage"
	tele "gopkg.in/telebot.v3"
)

type MemoryUI struct {
	db             *storage.DB
	memoryManager  *memory.Manager
	sessionManager *memory.SessionManager
}

func NewMemoryUI(db *storage.DB, mm *memory.Manager, sm *memory.SessionManager) *MemoryUI {
	return &MemoryUI{db: db, memoryManager: mm, sessionManager: sm}
}

// HandleMemory handles the top-level `/memory` and `/memories` commands
func (ui *MemoryUI) HandleMemory(c tele.Context) error {
	if ui.memoryManager == nil {
		return c.Send("⚠️ <b>Engine Memory GoAssistant belum aktif.</b>\nPastikan <code>memory.enabled: true</code> di konfigurasi.", tele.ModeHTML)
	}

	payload := ""
	if c.Message() != nil {
		payload = strings.TrimSpace(c.Message().Payload)
	}

	parts := strings.Fields(payload)
	if len(parts) == 0 || parts[0] == "status" || parts[0] == "dashboard" {
		text, menu := ui.RenderMemoryDashboard(strconv.FormatInt(c.Sender().ID, 10))
		return c.Send(text, menu, tele.ModeHTML)
	}

	subCmd := strings.ToLower(parts[0])
	switch subCmd {
	case "enable", "on":
		ui.memoryManager.SetEnabled(true)
		return c.Send("✅ <b>Engine Memory GoAssistant diaktifkan.</b>", tele.ModeHTML)

	case "disable", "off":
		ui.memoryManager.SetEnabled(false)
		return c.Send("🛑 <b>Engine Memory GoAssistant dinonaktifkan.</b>", tele.ModeHTML)

	case "autoextract", "extract":
		if len(parts) < 2 {
			cfg := ui.memoryManager.GetConfig()
			st := "Nonaktif"
			if cfg.AutoExtract {
				st = "Aktif"
			}
			return c.Send(fmt.Sprintf("ℹ️ <b>Auto-Extract Percakapan:</b> <code>%s</code>\n\nGunakan: <code>/memory autoextract on</code> atau <code>/memory autoextract off</code>", st), tele.ModeHTML)
		}
		arg := strings.ToLower(parts[1])
		if arg == "on" || arg == "true" || arg == "1" {
			ui.memoryManager.SetAutoExtract(true)
			return c.Send("✅ <b>Auto-Extract percakapan diaktifkan.</b> Bot akan secara otomatis mengekstrak fakta penting dari percakapan.", tele.ModeHTML)
		} else if arg == "off" || arg == "false" || arg == "0" {
			ui.memoryManager.SetAutoExtract(false)
			return c.Send("🛑 <b>Auto-Extract percakapan dinonaktifkan.</b>", tele.ModeHTML)
		}
		return c.Send("⚠️ Gunakan <code>/memory autoextract on</code> atau <code>/memory autoextract off</code>", tele.ModeHTML)

	case "strategy", "strat":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Strategi Memory Saat Ini:</b> <code>%s</code>\n\nPilihan: <code>recent</code>, <code>semantic</code>, <code>hybrid</code>\nUbah dengan: <code>/memory strategy &lt;pilihan&gt;</code>", ui.memoryManager.GetStrategy()), tele.ModeHTML)
		}
		newStrat := strings.ToLower(parts[1])
		if newStrat != "recent" && newStrat != "semantic" && newStrat != "hybrid" {
			return c.Send("⚠️ Pilihan strategi tidak valid. Gunakan salah satu:\n• <code>recent</code> (kronologis waktu)\n• <code>semantic</code> (vektor makna)\n• <code>hybrid</code> (BM25 + Vektor + Recency)", tele.ModeHTML)
		}
		ui.memoryManager.SetStrategy(newStrat)
		return c.Send(fmt.Sprintf("✅ <b>Strategi memory engine berhasil diubah ke:</b> <code>%s</code>", newStrat), tele.ModeHTML)

	case "model", "embedding_model", "setmodel":
		if len(parts) < 2 {
			cfg := ui.memoryManager.GetConfig()
			emb := cfg.GetEmbeddingConfig()
			currModel := emb.Model
			if currModel == "" {
				currModel = "(belum disetel)"
			}
			return c.Send(fmt.Sprintf("ℹ️ <b>Model Embedding Saat Ini:</b> <code>%s</code> (Provider: <code>%s</code>)\n\n"+
				"Ubah dengan: <code>/memory model &lt;nama_model&gt;</code>\n\n"+
				"Contoh populer:\n"+
				"• <code>/memory model text-embedding-3-small</code> (OpenAI)\n"+
				"• <code>/memory model text-embedding-004</code> (Gemini)\n"+
				"• <code>/memory model nomic-embed-text</code> (Ollama)\n"+
				"• <code>/memory model mxbai-embed-large</code> (Ollama)",
				html.EscapeString(currModel), html.EscapeString(emb.Provider)), tele.ModeHTML)
		}
		newModel := strings.TrimSpace(parts[1])
		ui.memoryManager.SetEmbeddingModel(newModel)
		return c.Send(fmt.Sprintf("✅ <b>Model embedding berhasil diubah ke:</b> <code>%s</code>\nStatus Remote Embedding kini <b>Aktif</b>.", html.EscapeString(newModel)), tele.ModeHTML)

	case "provider", "prov":
		if len(parts) < 2 {
			cfg := ui.memoryManager.GetConfig()
			emb := cfg.GetEmbeddingConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Provider Embedding Saat Ini:</b> <code>%s</code>\n\nPilihan: <code>openai</code>, <code>gemini</code>, <code>ollama</code>, <code>custom</code>\nUbah dengan: <code>/memory provider &lt;nama_provider&gt;</code>", html.EscapeString(emb.Provider)), tele.ModeHTML)
		}
		newProv := strings.ToLower(strings.TrimSpace(parts[1]))
		if newProv != "openai" && newProv != "gemini" && newProv != "ollama" && newProv != "custom" {
			return c.Send("⚠️ Provider tidak valid. Gunakan: <code>openai</code>, <code>gemini</code>, <code>ollama</code>, atau <code>custom</code>.", tele.ModeHTML)
		}
		ui.memoryManager.SetEmbeddingProvider(newProv)
		return c.Send(fmt.Sprintf("✅ <b>Provider embedding berhasil diubah ke:</b> <code>%s</code>", html.EscapeString(newProv)), tele.ModeHTML)

	case "embed", "embedding":
		if len(parts) < 2 {
			cfg := ui.memoryManager.GetConfig()
			emb := cfg.GetEmbeddingConfig()
			st := "Nonaktif"
			if emb.Enabled {
				st = "Aktif"
			}
			return c.Send(fmt.Sprintf("ℹ️ <b>Status Vector Embedding:</b> <code>%s</code>\n\nGunakan: <code>/memory embed on</code> atau <code>/memory embed off</code>", st), tele.ModeHTML)
		}
		toggle := strings.ToLower(parts[1])
		if toggle == "on" || toggle == "enable" || toggle == "1" || toggle == "true" {
			ui.memoryManager.SetEmbeddingEnabled(true)
			return c.Send("✅ <b>Vector Embedding berhasil diaktifkan.</b>", tele.ModeHTML)
		} else if toggle == "off" || toggle == "disable" || toggle == "0" || toggle == "false" {
			ui.memoryManager.SetEmbeddingEnabled(false)
			return c.Send("✅ <b>Vector Embedding dinonaktifkan</b> (Fallback ke FTS5 BM25).", tele.ModeHTML)
		} else {
			return c.Send("⚠️ Gunakan <code>/memory embed on</code> atau <code>/memory embed off</code>", tele.ModeHTML)
		}

	case "baseurl", "url":
		if len(parts) < 2 {
			cfg := ui.memoryManager.GetConfig()
			emb := cfg.GetEmbeddingConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Embedding Base URL Saat Ini:</b> <code>%s</code>\n\nUbah dengan: <code>/memory baseurl &lt;url&gt;</code>\nContoh untuk Ollama: <code>/memory baseurl http://localhost:11434/v1</code>", html.EscapeString(emb.BaseURL)), tele.ModeHTML)
		}
		newURL := strings.TrimSpace(parts[1])
		if newURL == "default" || newURL == "none" || newURL == "-" {
			newURL = ""
		}
		ui.memoryManager.SetEmbeddingBaseURL(newURL)
		return c.Send(fmt.Sprintf("✅ <b>Base URL embedding berhasil diatur ke:</b> <code>%s</code>", html.EscapeString(newURL)), tele.ModeHTML)

	case "key", "apikey":
		if len(parts) < 2 {
			return c.Send("Ubah API key embedding dengan: <code>/memory key &lt;api_key&gt;</code>", tele.ModeHTML)
		}
		newKey := strings.TrimSpace(parts[1])
		ui.memoryManager.SetEmbeddingAPIKey(newKey)
		return c.Send("✅ <b>API Key untuk embedding berhasil diperbarui.</b>", tele.ModeHTML)

	case "dimensions", "dims":
		if len(parts) < 2 {
			cfg := ui.memoryManager.GetConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Dimensi Vektor Saat Ini:</b> <code>%d</code>\n\nUbah dengan: <code>/memory dimensions &lt;jumlah&gt;</code> (contoh: <code>/memory dimensions 1536</code>)", cfg.Embedding.Dimensions), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 64 || val > 16384 {
			return c.Send("⚠️ Nilai dimensi vektor harus antara 64 sampai 16384.", tele.ModeHTML)
		}
		ui.memoryManager.SetEmbeddingDimensions(val)
		return c.Send(fmt.Sprintf("✅ <b>Dimensi vektor embedding berhasil diatur ke:</b> <code>%d</code>", val), tele.ModeHTML)

	case "similarity", "threshold", "sim":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Ambang Batas Similarity Saat Ini:</b> <code>%.2f</code>\n\nUbah dengan: <code>/memory similarity &lt;0.10 - 1.00&gt;</code> (contoh: <code>/memory similarity 0.60</code>)", ui.memoryManager.GetSimilarityThreshold()), tele.ModeHTML)
		}
		val, err := strconv.ParseFloat(parts[1], 64)
		if err != nil || val <= 0.0 || val > 1.0 {
			return c.Send("⚠️ Nilai similarity threshold harus berupa desimal antara 0.01 sampai 1.00 (misal: 0.60).", tele.ModeHTML)
		}
		ui.memoryManager.SetSimilarityThreshold(val)
		return c.Send(fmt.Sprintf("✅ <b>Similarity threshold berhasil diatur ke:</b> <code>%.2f</code>", val), tele.ModeHTML)

	case "tokens", "maxtokens", "token":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Anggaran Token Saat Ini:</b> <code>%d tokens</code>\n\nUbah dengan: <code>/memory tokens &lt;jumlah&gt;</code> (contoh: <code>/memory tokens 2000</code>)", ui.memoryManager.GetMaxTokens()), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 100 || val > 32000 {
			return c.Send("⚠️ Nilai token harus berupa angka antara 100 dan 32000.", tele.ModeHTML)
		}
		ui.memoryManager.SetMaxTokens(val)
		return c.Send(fmt.Sprintf("✅ <b>Anggaran token injeksi memori berhasil diatur ke:</b> <code>%d tokens</code>", val), tele.ModeHTML)

	case "maxitems", "items":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Batas Maksimal Item Memori Saat Ini:</b> <code>%d item</code>\n\nUbah dengan: <code>/memory maxitems &lt;jumlah&gt;</code> (contoh: <code>/memory maxitems 20</code>)", ui.memoryManager.GetMaxContextItems()), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 1 || val > 100 {
			return c.Send("⚠️ Nilai max items harus antara 1 sampai 100.", tele.ModeHTML)
		}
		ui.memoryManager.SetMaxContextItems(val)
		return c.Send(fmt.Sprintf("✅ <b>Batas item memori injeksi prompt berhasil diatur ke:</b> <code>%d item</code>", val), tele.ModeHTML)

	case "retention", "retention_days", "days":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Masa Retensi Saat Ini:</b> <code>%d hari</code>\n\nUbah dengan: <code>/memory retention &lt;hari&gt;</code> (contoh: <code>/memory retention 30</code>)", ui.memoryManager.GetRetentionDays()), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 1 || val > 365 {
			return c.Send("⚠️ Nilai retensi harus antara 1 sampai 365 hari.", tele.ModeHTML)
		}
		ui.memoryManager.SetRetentionDays(val)
		return c.Send(fmt.Sprintf("✅ <b>Masa retensi memori berhasil diatur ke:</b> <code>%d hari</code>", val), tele.ModeHTML)

	case "promotion", "promo", "promotion_threshold":
		if len(parts) < 2 {
			return c.Send(fmt.Sprintf("ℹ️ <b>Ambang Promosi Saat Ini:</b> <code>≥ %dx akses</code>\n\nUbah dengan: <code>/memory promotion &lt;kali&gt;</code> (contoh: <code>/memory promotion 3</code>)", ui.memoryManager.GetPromotionThreshold()), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 1 || val > 50 {
			return c.Send("⚠️ Nilai ambang promosi harus antara 1 sampai 50.", tele.ModeHTML)
		}
		ui.memoryManager.SetPromotionThreshold(val)
		return c.Send(fmt.Sprintf("✅ <b>Ambang promosi memori berhasil diatur ke:</b> <code>≥ %dx akses</code>", val), tele.ModeHTML)

	case "compaction", "autocompact":
		if len(parts) < 2 {
			cfg := ui.memoryManager.GetConfig()
			st := "Nonaktif"
			if cfg.AutoCompaction {
				st = "Aktif"
			}
			return c.Send(fmt.Sprintf("ℹ️ <b>Auto-Compaction:</b> <code>%s</code>\n\nGunakan: <code>/memory compaction on</code> atau <code>/memory compaction off</code>", st), tele.ModeHTML)
		}
		arg := strings.ToLower(parts[1])
		if arg == "on" || arg == "true" || arg == "1" {
			ui.memoryManager.SetAutoCompaction(true)
			return c.Send("✅ <b>Auto-Compaction diaktifkan.</b>", tele.ModeHTML)
		} else if arg == "off" || arg == "false" || arg == "0" {
			ui.memoryManager.SetAutoCompaction(false)
			return c.Send("🛑 <b>Auto-Compaction dinonaktifkan.</b>", tele.ModeHTML)
		}
		return c.Send("⚠️ Gunakan <code>/memory compaction on</code> atau <code>/memory compaction off</code>", tele.ModeHTML)

	case "compactinterval", "interval":
		if len(parts) < 2 {
			cfg := ui.memoryManager.GetConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Interval Compaction Saat Ini:</b> <code>%d jam</code>\n\nUbah dengan: <code>/memory compactinterval &lt;jam&gt;</code> (contoh: <code>/memory compactinterval 24</code>)", cfg.CompactionIntervalHours), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 1 || val > 720 {
			return c.Send("⚠️ Nilai interval harus antara 1 sampai 720 jam.", tele.ModeHTML)
		}
		ui.memoryManager.SetCompactionIntervalHours(val)
		return c.Send(fmt.Sprintf("✅ <b>Interval auto-compaction berhasil diatur ke:</b> <code>%d jam</code>", val), tele.ModeHTML)

	case "compactthreshold", "cthreshold":
		if len(parts) < 2 {
			cfg := ui.memoryManager.GetConfig()
			return c.Send(fmt.Sprintf("ℹ️ <b>Ambang Pemicu Compaction Saat Ini:</b> <code>%d item</code>\n\nUbah dengan: <code>/memory compactthreshold &lt;jumlah&gt;</code> (contoh: <code>/memory compactthreshold 100</code>)", cfg.CompactionThreshold), tele.ModeHTML)
		}
		val, err := strconv.Atoi(parts[1])
		if err != nil || val < 10 || val > 10000 {
			return c.Send("⚠️ Nilai ambang pemicu harus antara 10 sampai 10000 item.", tele.ModeHTML)
		}
		ui.memoryManager.SetCompactionThreshold(val)
		return c.Send(fmt.Sprintf("✅ <b>Ambang pemicu auto-compaction berhasil diatur ke:</b> <code>%d item</code>", val), tele.ModeHTML)

	case "compact", "prune":
		userID := strconv.FormatInt(c.Sender().ID, 10)
		rep, err := ui.memoryManager.Compact(context.Background(), "user", userID)
		if err != nil {
			return c.Send(fmt.Sprintf("❌ Gagal menjalankan compaction: %v", err), tele.ModeHTML)
		}
		msg := fmt.Sprintf("🧹 <b>HASIL AUTO-COMPACTION MEMORI</b>\n\n"+
			"• <b>Memori Usang Dihapus:</b> <code>%d catatan</code>\n"+
			"• <b>Indeks SQLite FTS5:</b> <code>Dioptimalkan (OK)</code>\n"+
			"• <b>Total Memori Aktif Anda:</b> <code>%d catatan</code>\n\n"+
			"Database memori Anda kini bersih dan optimal!", rep.PrunedExpired, rep.TotalActive)
		return c.Send(msg, tele.ModeHTML)

	case "list":
		userID := strconv.FormatInt(c.Sender().ID, 10)
		items, err := ui.db.ListMemoriesByScope("user", userID, "", 15)
		if err != nil || len(items) == 0 {
			return c.Send("📭 <i>Belum ada catatan memori yang tersimpan untuk akun Anda.</i>", tele.ModeHTML)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📋 <b>DAFTAR MEMORI ANDA (%d item terbaru):</b>\n\n", len(items)))
		for i, it := range items {
			promTag := ""
			if it.IsPromoted {
				promTag = " ⭐️[PERMANEN]"
			}
			contentExcerpt := it.Content
			if len([]rune(contentExcerpt)) > 70 {
				contentExcerpt = string([]rune(contentExcerpt)[:70]) + "..."
			}
			sb.WriteString(fmt.Sprintf("<b>%d.</b> <code>[%s]</code>%s\n   %s\n\n",
				i+1, html.EscapeString(it.Key), promTag, html.EscapeString(contentExcerpt)))
		}
		return c.Send(sb.String(), tele.ModeHTML)

	case "search":
		if len(parts) < 2 {
			return c.Send("Gunakan format: <code>/memory search &lt;kata_kunci&gt;</code>", tele.ModeHTML)
		}
		query := strings.Join(parts[1:], " ")
		userID := strconv.FormatInt(c.Sender().ID, 10)
		results, err := ui.memoryManager.SearchMemoriesAdvanced("user", userID, query, "", 5)
		if err != nil || len(results) == 0 {
			return c.Send(fmt.Sprintf("🔍 <i>Tidak ditemukan memori yang cocok dengan: %s</i>", html.EscapeString(query)), tele.ModeHTML)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🔍 <b>HASIL PENCARIAN MEMORI (%d ditemukan):</b>\n\n", len(results)))
		for i, it := range results {
			sb.WriteString(fmt.Sprintf("<b>%d.</b> <code>[%s]</code> (Score: <code>%.2f</code>)\n   %s\n\n",
				i+1, html.EscapeString(it.Key), it.Score, html.EscapeString(it.Content)))
		}
		return c.Send(sb.String(), tele.ModeHTML)

	case "clear":
		userID := strconv.FormatInt(c.Sender().ID, 10)
		if err := ui.memoryManager.ClearUserMemory(userID); err != nil {
			return c.Send(fmt.Sprintf("❌ Gagal menghapus memori: %v", err), tele.ModeHTML)
		}
		return c.Send("🗑️ <b>Semua catatan memori pribadi Anda berhasil dihapus.</b>", tele.ModeHTML)

	case "seed", "import":
		csvPath := ""
		if len(parts) > 1 {
			csvPath = strings.Join(parts[1:], " ")
		}
		userID := strconv.FormatInt(c.Sender().ID, 10)
		res, err := ui.memoryManager.ImportSeedCSV(context.Background(), csvPath, userID)
		if err != nil {
			return c.Send(fmt.Sprintf("❌ <b>Gagal mengimpor seed CSV:</b> %v", err), tele.ModeHTML)
		}
		return c.Send(fmt.Sprintf("📥 <b>HASIL IMPOR SEED CSV MEMORY</b>\n\n"+
			"• <b>Total Diproses:</b> <code>%d item</code>\n"+
			"• <b>Berhasil Diimpor:</b> <code>%d item</code>\n"+
			"• <b>Error / Dilewati:</b> <code>%d</code>\n\n"+
			"Catatan memori kini telah siap digunakan dalam pencarian!",
			res.TotalProcessed, res.Inserted, len(res.Errors)), tele.ModeHTML)

	case "test", "testembed", "checkembed":
		_ = c.Send("🧪 <i>Menguji koneksi vektor embedding ke provider... Mohon tunggu.</i>", tele.ModeHTML)
		res, err := ui.memoryManager.TestEmbedding(context.Background())
		return c.Send(formatAdminEmbeddingDiagnosticReport(res, err), tele.ModeHTML)

	case "reset":
		ui.memoryManager.ResetToDefaults()
		return c.Send("🔄 <b>Seluruh pengaturan memory engine telah di-reset ke nilai default pabrik.</b>", tele.ModeHTML)

	default:
		return c.Send("Perintah tidak dikenal. Ketik <code>/memory</code> untuk membuka dashboard lengkap.", tele.ModeHTML)
	}
}

// RenderMemoryDashboard renders top-level hub dashboard
func (ui *MemoryUI) RenderMemoryDashboard(userID string) (string, *tele.ReplyMarkup) {
	cfg := ui.memoryManager.GetConfig()
	activeStrategy := ui.memoryManager.GetStrategy()
	maxTokens := ui.memoryManager.GetMaxTokens()
	maxItems := ui.memoryManager.GetMaxContextItems()
	retentionDays := ui.memoryManager.GetRetentionDays()
	promotionThreshold := ui.memoryManager.GetPromotionThreshold()
	activeCount, _ := ui.db.CountMemories("user", userID)

	engineStatus := "🟢 Aktif"
	if !cfg.Enabled {
		engineStatus = "🔴 Nonaktif"
	}
	extractStatus := "🟢 Aktif"
	if !cfg.AutoExtract {
		extractStatus = "🔴 Nonaktif"
	}

	embCfg := cfg.GetEmbeddingConfig()
	embStatus := "🔴 Nonaktif (BM25 FTS5 Cepat)"
	if embCfg.Enabled && embCfg.Model != "" {
		embStatus = fmt.Sprintf("🟢 Aktif (<code>%s</code> - <code>%s</code>)", html.EscapeString(embCfg.Provider), html.EscapeString(embCfg.Model))
	}

	stratDesc := "Hybrid (BM25 + Vektor + Recency)"
	if activeStrategy == "recent" {
		stratDesc = "Recent (Kronologis Waktu Terbaru)"
	} else if activeStrategy == "semantic" {
		stratDesc = "Semantic (Vektor Kemiripan Makna)"
	}

	var sb strings.Builder
	sb.WriteString("🧠 <b>PUSAT KONTROL & PENGATURAN MEMORY ENGINE</b>\n\n")
	sb.WriteString(fmt.Sprintf("• <b>Status Engine:</b> %s | <b>Auto-Extract:</b> %s\n", engineStatus, extractStatus))
	sb.WriteString(fmt.Sprintf("• <b>Strategi Aktif:</b> <code>%s</code> (%s)\n", activeStrategy, stratDesc))
	sb.WriteString(fmt.Sprintf("• <b>Anggaran Token Prompt:</b> <code>%d tokens</code> (Maks: <code>%d item</code>)\n", maxTokens, maxItems))
	sb.WriteString(fmt.Sprintf("• <b>Masa Retensi:</b> <code>%d hari</code> (Auto-promosi &ge; <code>%dx</code> pakai)\n", retentionDays, promotionThreshold))
	sb.WriteString(fmt.Sprintf("• <b>Auto-Compaction:</b> <code>Aktif</code> (Tiap %d jam, ambang: %d)\n", cfg.CompactionIntervalHours, cfg.CompactionThreshold))
	sb.WriteString(fmt.Sprintf("• <b>Similarity Threshold:</b> <code>%.2f</code>\n", cfg.SimilarityThreshold))
	sb.WriteString(fmt.Sprintf("• <b>Remote Embedding:</b> %s\n", embStatus))
	sb.WriteString(fmt.Sprintf("• <b>Memori Pribadi Anda:</b> <code>%d catatan</code>\n\n", activeCount))
	sb.WriteString("⚙️ <i>Pilih sub-menu di bawah untuk mengatur parameter secara detail:</i>")

	menu := &tele.ReplyMarkup{}
	btnGeneral := menu.Data("⚙️ Umum & Ekstrak", "mem_menu_general")
	btnStrategy := menu.Data("🎯 Strategi & Token", "mem_menu_strategy")
	btnRetention := menu.Data("⏳ Retensi & Promosi", "mem_menu_retention")
	btnCompaction := menu.Data("🧹 Auto-Compaction", "mem_menu_compaction")
	btnEmbedding := menu.Data("🔌 Remote Embedding", "mem_menu_embedding")
	btnData := menu.Data("📋 Kelola Catatan", "mem_menu_data")
	btnRefresh := menu.Data("🔄 Segarkan Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnGeneral, btnStrategy),
		menu.Row(btnRetention, btnCompaction),
		menu.Row(btnEmbedding, btnData),
		menu.Row(btnRefresh),
	)

	return sb.String(), menu
}

// RenderMemoryGeneralMenu renders General & Auto-Extract sub-menu
func (ui *MemoryUI) RenderMemoryGeneralMenu(userID string) (string, *tele.ReplyMarkup) {
	cfg := ui.memoryManager.GetConfig()

	engTag := "🟢 Engine: AKTIF"
	if !cfg.Enabled {
		engTag = "🔴 Engine: NONAKTIF"
	}
	extTag := "🟢 Auto-Extract: AKTIF"
	if !cfg.AutoExtract {
		extTag = "🔴 Auto-Extract: NONAKTIF"
	}

	text := "⚙️ <b>PENGATURAN UMUM & EKSTRAKSI MEMORY</b>\n\n" +
		"• <b>Master Engine:</b> " + engTag + "\n" +
		"  <i>Mengaktifkan penyimpanan & injeksi konteks memori ke sistem prompt.</i>\n\n" +
		"• <b>Auto-Extract:</b> " + extTag + "\n" +
		"  <i>Mengekstrak otomatis fakta profil, SOP, dan konsep dari chat secara background.</i>\n\n" +
		"• <b>Seed CSV Path:</b> <code>" + html.EscapeString(cfg.SeedCSVPath) + "</code>\n\n" +
		"<i>Klik tombol di bawah untuk toggle atau reset:</i>"

	menu := &tele.ReplyMarkup{}
	btnToggleEng := menu.Data(engTag, "mem_toggle_engine")
	btnToggleExt := menu.Data(extTag, "mem_toggle_autoextract")
	btnReset := menu.Data("🔄 Reset ke Default", "mem_reset_defaults")
	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnToggleEng),
		menu.Row(btnToggleExt),
		menu.Row(btnReset),
		menu.Row(btnBack),
	)

	return text, menu
}

// RenderMemoryStrategyMenu renders Retrieval Strategy & Token Budget sub-menu
func (ui *MemoryUI) RenderMemoryStrategyMenu(userID string) (string, *tele.ReplyMarkup) {
	activeStrat := ui.memoryManager.GetStrategy()
	maxTokens := ui.memoryManager.GetMaxTokens()
	maxItems := ui.memoryManager.GetMaxContextItems()

	text := fmt.Sprintf("🎯 <b>PENGATURAN STRATEGI & BUDGET TOKEN</b>\n\n"+
		"• <b>Strategi Saat Ini:</b> <code>%s</code>\n"+
		"  - <b>Hybrid:</b> FTS5 BM25 + Semantic Vector + Recency weighting (Direkomendasikan)\n"+
		"  - <b>Recent:</b> Urutkan berdasarkan waktu pembaruan terbaru\n"+
		"  - <b>Semantic:</b> Berdasarkan kemiripan vektor makna (Cosine Similarity)\n\n"+
		"• <b>Anggaran Token Prompt:</b> <code>%d tokens</code>\n"+
		"• <b>Batas Maksimal Item:</b> <code>%d item</code>\n\n"+
		"<i>Pilih strategi atau preset anggaran token / batas item:</i>",
		activeStrat, maxTokens, maxItems)

	menu := &tele.ReplyMarkup{}
	btnHybrid := menu.Data("🔘 Hybrid", "mem_strat_hybrid")
	if activeStrat == "hybrid" {
		btnHybrid = menu.Data("✅ Hybrid", "mem_strat_hybrid")
	}
	btnRecent := menu.Data("⚡ Recent", "mem_strat_recent")
	if activeStrat == "recent" {
		btnRecent = menu.Data("✅ Recent", "mem_strat_recent")
	}
	btnSemantic := menu.Data("🧠 Semantic", "mem_strat_semantic")
	if activeStrat == "semantic" {
		btnSemantic = menu.Data("✅ Semantic", "mem_strat_semantic")
	}

	btnTok500 := menu.Data(fmt.Sprintf("%s 500 Tok", checkMemMark(maxTokens == 500)), "mem_tok_500")
	btnTok1000 := menu.Data(fmt.Sprintf("%s 1000 Tok", checkMemMark(maxTokens == 1000)), "mem_tok_1000")
	btnTok2000 := menu.Data(fmt.Sprintf("%s 2000 Tok", checkMemMark(maxTokens == 2000)), "mem_tok_2000")
	btnTok4000 := menu.Data(fmt.Sprintf("%s 4000 Tok", checkMemMark(maxTokens == 4000)), "mem_tok_4000")

	btnItem10 := menu.Data(fmt.Sprintf("%s 10 Item", checkMemMark(maxItems == 10)), "mem_item_10")
	btnItem20 := menu.Data(fmt.Sprintf("%s 20 Item", checkMemMark(maxItems == 20)), "mem_item_20")
	btnItem50 := menu.Data(fmt.Sprintf("%s 50 Item", checkMemMark(maxItems == 50)), "mem_item_50")

	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnHybrid, btnRecent, btnSemantic),
		menu.Row(btnTok500, btnTok1000, btnTok2000, btnTok4000),
		menu.Row(btnItem10, btnItem20, btnItem50),
		menu.Row(btnBack),
	)

	return text, menu
}

// RenderMemoryRetentionMenu renders Retention & Promotion Threshold sub-menu
func (ui *MemoryUI) RenderMemoryRetentionMenu(userID string) (string, *tele.ReplyMarkup) {
	retDays := ui.memoryManager.GetRetentionDays()
	promThresh := ui.memoryManager.GetPromotionThreshold()

	text := fmt.Sprintf("⏳ <b>PENGATURAN RETENSI & PROMOSI MEMORI</b>\n\n"+
		"• <b>Masa Retensi:</b> <code>%d hari</code>\n"+
		"  <i>Batas usia memori sebelum kedaluwarsa jika tidak digunakan kembali.</i>\n\n"+
		"• <b>Ambang Promosi Permanen:</b> <code>&ge; %dx akses</code>\n"+
		"  <i>Memori yang sering diakses (&ge; ambang) otomatis dipromosikan menjadi permanen (long-term).</i>\n\n"+
		"<i>Pilih preset masa retensi atau ambang promosi:</i>",
		retDays, promThresh)

	menu := &tele.ReplyMarkup{}
	btnRet7 := menu.Data(fmt.Sprintf("%s 7h", checkMemMark(retDays == 7)), "mem_ret_7")
	btnRet14 := menu.Data(fmt.Sprintf("%s 14h", checkMemMark(retDays == 14)), "mem_ret_14")
	btnRet30 := menu.Data(fmt.Sprintf("%s 30h", checkMemMark(retDays == 30)), "mem_ret_30")
	btnRet60 := menu.Data(fmt.Sprintf("%s 60h", checkMemMark(retDays == 60)), "mem_ret_60")
	btnRet365 := menu.Data(fmt.Sprintf("%s 365h", checkMemMark(retDays == 365)), "mem_ret_365")

	btnProm1 := menu.Data(fmt.Sprintf("%s 1x", checkMemMark(promThresh == 1)), "mem_prom_1")
	btnProm2 := menu.Data(fmt.Sprintf("%s 2x", checkMemMark(promThresh == 2)), "mem_prom_2")
	btnProm3 := menu.Data(fmt.Sprintf("%s 3x", checkMemMark(promThresh == 3)), "mem_prom_3")
	btnProm5 := menu.Data(fmt.Sprintf("%s 5x", checkMemMark(promThresh == 5)), "mem_prom_5")

	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnRet7, btnRet14, btnRet30, btnRet60, btnRet365),
		menu.Row(btnProm1, btnProm2, btnProm3, btnProm5),
		menu.Row(btnBack),
	)

	return text, menu
}

// RenderMemoryCompactionMenu renders Auto-Compaction sub-menu
func (ui *MemoryUI) RenderMemoryCompactionMenu(userID string) (string, *tele.ReplyMarkup) {
	cfg := ui.memoryManager.GetConfig()

	compTag := "🟢 Auto-Compaction: AKTIF"
	if !cfg.AutoCompaction {
		compTag = "🔴 Auto-Compaction: NONAKTIF"
	}

	text := fmt.Sprintf("🧹 <b>PENGATURAN AUTO-COMPACTION & PEMBERSIHAN</b>\n\n"+
		"• <b>Status:</b> %s\n"+
		"• <b>Interval Eksekusi:</b> <code>Tiap %d jam</code>\n"+
		"• <b>Ambang Batas Jumlah:</b> <code>%d item</code> (Pemicu kompresi)\n\n"+
		"<i>Compaction merampingkan memori, menghapus catatan usang/expired, dan mengoptimalkan indeks SQLite FTS5.</i>",
		compTag, cfg.CompactionIntervalHours, cfg.CompactionThreshold)

	menu := &tele.ReplyMarkup{}
	btnToggleComp := menu.Data(compTag, "mem_toggle_compaction")

	btnCint6 := menu.Data(fmt.Sprintf("%s 6 Jam", checkMemMark(cfg.CompactionIntervalHours == 6)), "mem_cint_6")
	btnCint12 := menu.Data(fmt.Sprintf("%s 12 Jam", checkMemMark(cfg.CompactionIntervalHours == 12)), "mem_cint_12")
	btnCint24 := menu.Data(fmt.Sprintf("%s 24 Jam", checkMemMark(cfg.CompactionIntervalHours == 24)), "mem_cint_24")
	btnCint48 := menu.Data(fmt.Sprintf("%s 48 Jam", checkMemMark(cfg.CompactionIntervalHours == 48)), "mem_cint_48")

	btnCthr50 := menu.Data(fmt.Sprintf("%s 50 Item", checkMemMark(cfg.CompactionThreshold == 50)), "mem_cthr_50")
	btnCthr100 := menu.Data(fmt.Sprintf("%s 100 Item", checkMemMark(cfg.CompactionThreshold == 100)), "mem_cthr_100")
	btnCthr200 := menu.Data(fmt.Sprintf("%s 200 Item", checkMemMark(cfg.CompactionThreshold == 200)), "mem_cthr_200")

	btnPruneNow := menu.Data("🧹 Bersihkan & Prune Sekarang", "mem_compact_now")
	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnToggleComp),
		menu.Row(btnCint6, btnCint12, btnCint24, btnCint48),
		menu.Row(btnCthr50, btnCthr100, btnCthr200),
		menu.Row(btnPruneNow),
		menu.Row(btnBack),
	)

	return text, menu
}

// RenderMemoryEmbeddingMenu renders Remote Vector Embedding sub-menu
func (ui *MemoryUI) RenderMemoryEmbeddingMenu(userID string) (string, *tele.ReplyMarkup) {
	cfg := ui.memoryManager.GetConfig()
	emb := cfg.GetEmbeddingConfig()

	embTag := "🟢 Vector Embedding: AKTIF"
	if !emb.Enabled {
		embTag = "🔴 Vector Embedding: NONAKTIF"
	}

	modelStr := emb.Model
	if modelStr == "" {
		modelStr = "(belum ditentukan)"
	}
	baseStr := emb.BaseURL
	if baseStr == "" {
		baseStr = "(default endpoint)"
	}

	keyInfo := "Tidak terdeteksi"
	if ui.memoryManager.GetEmbedder() != nil {
		if resolvedKey, keySource := ui.memoryManager.GetEmbedder().ResolveAPIKey(); resolvedKey != "" {
			masked := resolvedKey
			if len(masked) > 8 {
				masked = masked[:4] + "..." + masked[len(masked)-4:]
			}
			keyInfo = fmt.Sprintf("<code>%s</code> (%s)", html.EscapeString(masked), html.EscapeString(keySource))
		}
	}

	text := fmt.Sprintf("🔌 <b>PENGATURAN REMOTE VECTOR EMBEDDING</b>\n\n"+
		"• <b>Status:</b> %s\n"+
		"• <b>Provider:</b> <code>%s</code>\n"+
		"• <b>Model:</b> <code>%s</code>\n"+
		"• <b>Dimensi Vektor:</b> <code>%d dims</code>\n"+
		"• <b>Similarity Threshold:</b> <code>%.2f</code> (Cosine)\n"+
		"• <b>Base URL:</b> <code>%s</code>\n"+
		"• <b>Sumber API Key:</b> %s\n\n"+
		"<i>Vektor embedding digunakan untuk strategi pencarian Semantic dan Hybrid.</i>",
		embTag, html.EscapeString(emb.Provider), html.EscapeString(modelStr),
		emb.Dimensions, cfg.SimilarityThreshold, html.EscapeString(baseStr), keyInfo)

	menu := &tele.ReplyMarkup{}
	btnToggle := menu.Data(embTag, "mem_toggle_embed")
	btnPickModel := menu.Data("🤖 Pilih Model Preset", "mem_menu_models")

	btnOAI := menu.Data(fmt.Sprintf("%s OpenAI", checkMemMark(emb.Provider == "openai")), "mem_prov_openai")
	btnGem := menu.Data(fmt.Sprintf("%s Gemini", checkMemMark(emb.Provider == "gemini")), "mem_prov_gemini")
	btnOll := menu.Data(fmt.Sprintf("%s Ollama", checkMemMark(emb.Provider == "ollama")), "mem_prov_ollama")
	btnCust := menu.Data(fmt.Sprintf("%s Custom", checkMemMark(emb.Provider == "custom")), "mem_prov_custom")

	btnDim768 := menu.Data(fmt.Sprintf("%s 768 Dims", checkMemMark(emb.Dimensions == 768)), "mem_dims_768")
	btnDim1536 := menu.Data(fmt.Sprintf("%s 1536 Dims", checkMemMark(emb.Dimensions == 1536)), "mem_dims_1536")
	btnDim3072 := menu.Data(fmt.Sprintf("%s 3072 Dims", checkMemMark(emb.Dimensions == 3072)), "mem_dims_3072")

	btnSim50 := menu.Data(fmt.Sprintf("%s 0.50", checkMemMark(cfg.SimilarityThreshold == 0.50)), "mem_sim_50")
	btnSim60 := menu.Data(fmt.Sprintf("%s 0.60", checkMemMark(cfg.SimilarityThreshold == 0.60)), "mem_sim_60")
	btnSim70 := menu.Data(fmt.Sprintf("%s 0.70", checkMemMark(cfg.SimilarityThreshold == 0.70)), "mem_sim_70")
	btnSim80 := menu.Data(fmt.Sprintf("%s 0.80", checkMemMark(cfg.SimilarityThreshold == 0.80)), "mem_sim_80")

	btnTestEmbed := menu.Data("🧪 Uji Koneksi Embedding", "mem_test_embed")
	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnToggle, btnPickModel),
		menu.Row(btnOAI, btnGem, btnOll, btnCust),
		menu.Row(btnDim768, btnDim1536, btnDim3072),
		menu.Row(btnSim50, btnSim60, btnSim70, btnSim80),
		menu.Row(btnTestEmbed),
		menu.Row(btnBack),
	)

	return text, menu
}

// RenderMemoryDataMenu renders Data management sub-menu
func (ui *MemoryUI) RenderMemoryDataMenu(userID string) (string, *tele.ReplyMarkup) {
	cfg := ui.memoryManager.GetConfig()
	activeCount, _ := ui.db.CountMemories("user", userID)

	text := fmt.Sprintf("📋 <b>KELOLA DATA & CATATAN MEMORI</b>\n\n"+
		"• <b>Total Catatan Memori Anda:</b> <code>%d item</code>\n"+
		"• <b>Lokasi File Seed CSV:</b>\n  <code>%s</code>\n\n"+
		"<i>Pilih tindakan di bawah untuk melihat, mengimpor, atau menghapus catatan:</i>",
		activeCount, html.EscapeString(cfg.SeedCSVPath))

	menu := &tele.ReplyMarkup{}
	btnList := menu.Data("📋 Lihat 15 Memori Terbaru", "mem_list_now")
	btnImport := menu.Data("📥 Impor Seed CSV OmniRoute", "mem_import_seed")
	btnClear := menu.Data("🗑️ Hapus Semua Memori Saya", "mem_clear_confirm")
	btnBack := menu.Data("🔙 Kembali ke Dashboard", "mem_refresh")

	menu.Inline(
		menu.Row(btnList),
		menu.Row(btnImport),
		menu.Row(btnClear),
		menu.Row(btnBack),
	)

	return text, menu
}

func (ui *MemoryUI) RenderModelPicker() (string, *tele.ReplyMarkup) {
	text := "🤖 <b>PILIH PRESET MODEL EMBEDDING REMOTE</b>\n\n" +
		"Pilih salah satu model embedding yang didukung:\n\n" +
		"1. <b>OpenAI text-embedding-3-small</b> (1536 dimensi, akurat & cepat)\n" +
		"2. <b>Google Gemini text-embedding-004</b> (768 dimensi)\n" +
		"3. <b>Ollama nomic-embed-text</b> (768 dimensi, lokal :11434)\n" +
		"4. <b>Ollama mxbai-embed-large</b> (1024 dimensi, lokal :11434)\n\n" +
		"<i>Atau gunakan perintah teks:</i>\n" +
		"<code>/memory model &lt;nama_model&gt;</code>\n" +
		"<code>/memory provider &lt;provider&gt;</code>\n" +
		"<code>/memory baseurl &lt;url&gt;</code>"

	menu := &tele.ReplyMarkup{}
	btnOAI := menu.Data("OpenAI text-embedding-3-small", "mem_set_model_openai_text-embedding-3-small")
	btnGemini := menu.Data("Gemini text-embedding-004", "mem_set_model_gemini_text-embedding-004")
	btnNomic := menu.Data("Ollama nomic-embed-text", "mem_set_model_ollama_nomic-embed-text")
	btnMxbai := menu.Data("Ollama mxbai-embed-large", "mem_set_model_ollama_mxbai-embed-large")
	btnBack := menu.Data("🔙 Kembali ke Menu Embedding", "mem_menu_embedding")

	menu.Inline(
		menu.Row(btnOAI),
		menu.Row(btnGemini),
		menu.Row(btnNomic),
		menu.Row(btnMxbai),
		menu.Row(btnBack),
	)

	return text, menu
}

func checkMemMark(active bool) string {
	if active {
		return "✅"
	}
	return "🔘"
}

func formatAdminEmbeddingDiagnosticReport(res *memory.EmbedTestResult, err error) string {
	if res == nil {
		errStr := "Terjadi kesalahan internal"
		if err != nil {
			errStr = err.Error()
		}
		return fmt.Sprintf("❌ <b>Uji Koneksi Gagal!</b>\n\n<b>Error:</b> <code>%s</code>", html.EscapeString(errStr))
	}

	statusIcon := "🟢"
	statusText := "BERHASIL TERHUBUNG"
	if !res.Success || err != nil {
		statusIcon = "🔴"
		statusText = "KONEKSI GAGAL"
	}

	msgText := res.Message
	if msgText == "" && err != nil {
		msgText = err.Error()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s <b>HASIL DIAGNOSTIK KONEKSI EMBEDDING (%s):</b>\n\n", statusIcon, statusText))
	sb.WriteString(fmt.Sprintf("• <b>Provider:</b> <code>%s</code>\n", html.EscapeString(res.Provider)))
	sb.WriteString(fmt.Sprintf("• <b>Model:</b> <code>%s</code>\n", html.EscapeString(res.Model)))
	sb.WriteString(fmt.Sprintf("• <b>Sumber Key:</b> <code>%s</code>\n", html.EscapeString(res.KeySource)))
	sb.WriteString(fmt.Sprintf("• <b>Endpoint:</b> <code>%s</code>\n", html.EscapeString(res.Endpoint)))
	sb.WriteString(fmt.Sprintf("• <b>Status HTTP:</b> <code>%d</code>\n", res.StatusCode))
	sb.WriteString(fmt.Sprintf("• <b>Latency:</b> <code>%s</code>\n", res.Latency.Round(time.Millisecond)))
	if res.Success {
		sb.WriteString(fmt.Sprintf("• <b>Dimensi Vektor Dihasilkan:</b> <code>%d dims</code>\n\n", res.Dimensions))
		sb.WriteString(fmt.Sprintf("✅ <i>%s</i>", html.EscapeString(msgText)))
	} else {
		sb.WriteString(fmt.Sprintf("• <b>Detail Masalah:</b>\n  <code>%s</code>\n\n", html.EscapeString(msgText)))
		sb.WriteString("💡 <b>Tips & Solusi:</b>\n")
		if strings.EqualFold(res.Provider, "gemini") {
			sb.WriteString("• Model Gemini (seperti <code>text-embedding-004</code>) membutuhkan Google AI Studio API Key resmi (diawali <code>AIzaSy...</code>).\n")
			sb.WriteString("• <i>Catatan:</i> Provider <code>Gemini Web</code> adalah scraper obrolan berbasis cookie browser dan tidak mendukung REST embedding API.\n")
			sb.WriteString("• Dapatkan API Key gratis di <a href=\"https://aistudio.google.com\">Google AI Studio</a> lalu setel ke bot:\n")
			sb.WriteString("  <code>/memory key &lt;AIzaSy...&gt;</code>\n")
		} else if strings.EqualFold(res.Provider, "openai") {
			sb.WriteString("• Pastikan API Key OpenAI atau OmniRoute valid dan kuota mencukupi.\n")
			sb.WriteString("  Setel via: <code>/memory key &lt;api_key&gt;</code>\n")
		} else if strings.EqualFold(res.Provider, "ollama") {
			sb.WriteString("• Pastikan daemon Ollama aktif di server (default <code>http://localhost:11434/v1</code>) dan model embedding sudah di-pull:\n")
			sb.WriteString(fmt.Sprintf("  <code>ollama pull %s</code>\n", html.EscapeString(res.Model)))
		}
	}

	return sb.String()
}

// HandleCallback handles all inline callback queries with prefix `mem_`
func (ui *MemoryUI) HandleCallback(c tele.Context, data string) error {
	if ui.memoryManager == nil {
		_ = c.Respond(&tele.CallbackResponse{Text: "Memory manager belum siap"})
		return nil
	}
	userID := strconv.FormatInt(c.Sender().ID, 10)

	switch data {
	// Navigation Callbacks
	case "mem_refresh", "mem_menu_dashboard":
		_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Memperbarui dashboard..."})
		text, menu := ui.RenderMemoryDashboard(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_menu_general":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := ui.RenderMemoryGeneralMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_menu_strategy":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_menu_retention":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_menu_compaction":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_menu_embedding":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_menu_data":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := ui.RenderMemoryDataMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_menu_models":
		_ = c.Respond(&tele.CallbackResponse{})
		text, menu := ui.RenderModelPicker()
		return c.EditOrSend(text, menu, tele.ModeHTML)

	// General & Auto-Extract Toggles
	case "mem_toggle_engine":
		newState := !ui.memoryManager.IsEnabled()
		ui.memoryManager.SetEnabled(newState)
		statusTxt := "dimatikan"
		if newState {
			statusTxt = "diaktifkan"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "⚙️ Master engine " + statusTxt})
		text, menu := ui.RenderMemoryGeneralMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_toggle_autoextract":
		cfg := ui.memoryManager.GetConfig()
		newState := !cfg.AutoExtract
		ui.memoryManager.SetAutoExtract(newState)
		statusTxt := "dimatikan"
		if newState {
			statusTxt = "diaktifkan"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🧠 Auto-extract " + statusTxt})
		text, menu := ui.RenderMemoryGeneralMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_reset_defaults":
		ui.memoryManager.ResetToDefaults()
		_ = c.Respond(&tele.CallbackResponse{Text: "🔄 Pengaturan di-reset ke default pabrik"})
		text, menu := ui.RenderMemoryGeneralMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	// Strategy Presets
	case "mem_strat_hybrid":
		ui.memoryManager.SetStrategy("hybrid")
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Strategi: Hybrid"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_strat_recent":
		ui.memoryManager.SetStrategy("recent")
		_ = c.Respond(&tele.CallbackResponse{Text: "⚡ Strategi: Recent"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_strat_semantic":
		ui.memoryManager.SetStrategy("semantic")
		_ = c.Respond(&tele.CallbackResponse{Text: "🧠 Strategi: Semantic"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	// Token Presets
	case "mem_tok_500":
		ui.memoryManager.SetMaxTokens(500)
		_ = c.Respond(&tele.CallbackResponse{Text: "Anggaran token: 500"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_tok_1000":
		ui.memoryManager.SetMaxTokens(1000)
		_ = c.Respond(&tele.CallbackResponse{Text: "Anggaran token: 1000"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_tok_2000":
		ui.memoryManager.SetMaxTokens(2000)
		_ = c.Respond(&tele.CallbackResponse{Text: "Anggaran token: 2000"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_tok_4000":
		ui.memoryManager.SetMaxTokens(4000)
		_ = c.Respond(&tele.CallbackResponse{Text: "Anggaran token: 4000"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	// Max Items Presets
	case "mem_item_10":
		ui.memoryManager.SetMaxContextItems(10)
		_ = c.Respond(&tele.CallbackResponse{Text: "Max items: 10"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_item_20":
		ui.memoryManager.SetMaxContextItems(20)
		_ = c.Respond(&tele.CallbackResponse{Text: "Max items: 20"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_item_50":
		ui.memoryManager.SetMaxContextItems(50)
		_ = c.Respond(&tele.CallbackResponse{Text: "Max items: 50"})
		text, menu := ui.RenderMemoryStrategyMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	// Retention Presets
	case "mem_ret_7":
		ui.memoryManager.SetRetentionDays(7)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 7 hari"})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_ret_14":
		ui.memoryManager.SetRetentionDays(14)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 14 hari"})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_ret_30":
		ui.memoryManager.SetRetentionDays(30)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 30 hari"})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_ret_60":
		ui.memoryManager.SetRetentionDays(60)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 60 hari"})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_ret_365":
		ui.memoryManager.SetRetentionDays(365)
		_ = c.Respond(&tele.CallbackResponse{Text: "Retensi: 365 hari"})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	// Promotion Threshold Presets
	case "mem_prom_1":
		ui.memoryManager.SetPromotionThreshold(1)
		_ = c.Respond(&tele.CallbackResponse{Text: "Promosi: ≥ 1x akses"})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_prom_2":
		ui.memoryManager.SetPromotionThreshold(2)
		_ = c.Respond(&tele.CallbackResponse{Text: "Promosi: ≥ 2x akses"})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_prom_3":
		ui.memoryManager.SetPromotionThreshold(3)
		_ = c.Respond(&tele.CallbackResponse{Text: "Promosi: ≥ 3x akses"})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_prom_5":
		ui.memoryManager.SetPromotionThreshold(5)
		_ = c.Respond(&tele.CallbackResponse{Text: "Promosi: ≥ 5x akses"})
		text, menu := ui.RenderMemoryRetentionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	// Compaction Presets & Toggles
	case "mem_toggle_compaction":
		cfg := ui.memoryManager.GetConfig()
		newState := !cfg.AutoCompaction
		ui.memoryManager.SetAutoCompaction(newState)
		statusTxt := "dimatikan"
		if newState {
			statusTxt = "diaktifkan"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🧹 Compaction " + statusTxt})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_cint_6":
		ui.memoryManager.SetCompactionIntervalHours(6)
		_ = c.Respond(&tele.CallbackResponse{Text: "Interval: 6 jam"})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_cint_12":
		ui.memoryManager.SetCompactionIntervalHours(12)
		_ = c.Respond(&tele.CallbackResponse{Text: "Interval: 12 jam"})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_cint_24":
		ui.memoryManager.SetCompactionIntervalHours(24)
		_ = c.Respond(&tele.CallbackResponse{Text: "Interval: 24 jam"})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_cint_48":
		ui.memoryManager.SetCompactionIntervalHours(48)
		_ = c.Respond(&tele.CallbackResponse{Text: "Interval: 48 jam"})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_cthr_50":
		ui.memoryManager.SetCompactionThreshold(50)
		_ = c.Respond(&tele.CallbackResponse{Text: "Ambang batas: 50 item"})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_cthr_100":
		ui.memoryManager.SetCompactionThreshold(100)
		_ = c.Respond(&tele.CallbackResponse{Text: "Ambang batas: 100 item"})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_cthr_200":
		ui.memoryManager.SetCompactionThreshold(200)
		_ = c.Respond(&tele.CallbackResponse{Text: "Ambang batas: 200 item"})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_compact_now":
		rep, err := ui.memoryManager.Compact(context.Background(), "user", userID)
		if err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal compaction"})
			return nil
		}
		_ = c.Respond(&tele.CallbackResponse{Text: fmt.Sprintf("🧹 Selesai: %d usang dihapus", rep.PrunedExpired)})
		text, menu := ui.RenderMemoryCompactionMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	// Embedding Toggles, Providers & Settings
	case "mem_toggle_embed":
		cfg := ui.memoryManager.GetConfig()
		emb := cfg.GetEmbeddingConfig()
		newState := !emb.Enabled
		ui.memoryManager.SetEmbeddingEnabled(newState)
		statusTxt := "dimatikan"
		if newState {
			statusTxt = "diaktifkan"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🔌 Embedding " + statusTxt})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_prov_openai":
		ui.memoryManager.SetEmbeddingProvider("openai")
		_ = c.Respond(&tele.CallbackResponse{Text: "Provider: OpenAI"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_prov_gemini":
		ui.memoryManager.SetEmbeddingProvider("gemini")
		_ = c.Respond(&tele.CallbackResponse{Text: "Provider: Gemini"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_prov_ollama":
		ui.memoryManager.SetEmbeddingProvider("ollama")
		_ = c.Respond(&tele.CallbackResponse{Text: "Provider: Ollama"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_prov_custom":
		ui.memoryManager.SetEmbeddingProvider("custom")
		_ = c.Respond(&tele.CallbackResponse{Text: "Provider: Custom"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_dims_768":
		ui.memoryManager.SetEmbeddingDimensions(768)
		_ = c.Respond(&tele.CallbackResponse{Text: "Dimensi: 768"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_dims_1536":
		ui.memoryManager.SetEmbeddingDimensions(1536)
		_ = c.Respond(&tele.CallbackResponse{Text: "Dimensi: 1536"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_dims_3072":
		ui.memoryManager.SetEmbeddingDimensions(3072)
		_ = c.Respond(&tele.CallbackResponse{Text: "Dimensi: 3072"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_sim_50":
		ui.memoryManager.SetSimilarityThreshold(0.50)
		_ = c.Respond(&tele.CallbackResponse{Text: "Threshold: 0.50"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_sim_60":
		ui.memoryManager.SetSimilarityThreshold(0.60)
		_ = c.Respond(&tele.CallbackResponse{Text: "Threshold: 0.60"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_sim_70":
		ui.memoryManager.SetSimilarityThreshold(0.70)
		_ = c.Respond(&tele.CallbackResponse{Text: "Threshold: 0.70"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_sim_80":
		ui.memoryManager.SetSimilarityThreshold(0.80)
		_ = c.Respond(&tele.CallbackResponse{Text: "Threshold: 0.80"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	// Preset Embedding Models
	case "mem_set_model_openai_text-embedding-3-small":
		ui.memoryManager.SetEmbeddingProvider("openai")
		ui.memoryManager.SetEmbeddingModel("text-embedding-3-small")
		ui.memoryManager.SetEmbeddingDimensions(1536)
		ui.memoryManager.SetEmbeddingEnabled(true)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ OpenAI text-embedding-3-small"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_set_model_gemini_text-embedding-004":
		ui.memoryManager.SetEmbeddingProvider("gemini")
		ui.memoryManager.SetEmbeddingModel("text-embedding-004")
		ui.memoryManager.SetEmbeddingDimensions(768)
		ui.memoryManager.SetEmbeddingEnabled(true)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Gemini text-embedding-004"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_set_model_ollama_nomic-embed-text":
		ui.memoryManager.SetEmbeddingProvider("ollama")
		ui.memoryManager.SetEmbeddingModel("nomic-embed-text")
		ui.memoryManager.SetEmbeddingDimensions(768)
		ui.memoryManager.SetEmbeddingBaseURL("http://localhost:11434/v1")
		ui.memoryManager.SetEmbeddingEnabled(true)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Ollama nomic-embed-text"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_set_model_ollama_mxbai-embed-large":
		ui.memoryManager.SetEmbeddingProvider("ollama")
		ui.memoryManager.SetEmbeddingModel("mxbai-embed-large")
		ui.memoryManager.SetEmbeddingDimensions(1024)
		ui.memoryManager.SetEmbeddingBaseURL("http://localhost:11434/v1")
		ui.memoryManager.SetEmbeddingEnabled(true)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Ollama mxbai-embed-large"})
		text, menu := ui.RenderMemoryEmbeddingMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_test_embed":
		_ = c.Respond(&tele.CallbackResponse{Text: "🧪 Menguji koneksi embedding..."})
		res, err := ui.memoryManager.TestEmbedding(context.Background())
		return c.Send(formatAdminEmbeddingDiagnosticReport(res, err), tele.ModeHTML)

	// Data Management Callbacks
	case "mem_list_now":
		_ = c.Respond(&tele.CallbackResponse{})
		items, err := ui.db.ListMemoriesByScope("user", userID, "", 15)
		if err != nil || len(items) == 0 {
			return c.Send("📭 <i>Belum ada catatan memori yang tersimpan untuk akun Anda.</i>", tele.ModeHTML)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📋 <b>DAFTAR MEMORI ANDA (%d item):</b>\n\n", len(items)))
		for i, it := range items {
			promTag := ""
			if it.IsPromoted {
				promTag = " ⭐️[PERMANEN]"
			}
			contentExcerpt := it.Content
			if len([]rune(contentExcerpt)) > 70 {
				contentExcerpt = string([]rune(contentExcerpt)[:70]) + "..."
			}
			sb.WriteString(fmt.Sprintf("<b>%d.</b> <code>[%s]</code>%s\n   %s\n\n",
				i+1, html.EscapeString(it.Key), promTag, html.EscapeString(contentExcerpt)))
		}
		return c.Send(sb.String(), tele.ModeHTML)

	case "mem_import_seed":
		_ = c.Respond(&tele.CallbackResponse{Text: "📥 Mengimpor seed CSV..."})
		res, err := ui.memoryManager.ImportSeedCSV(context.Background(), "", userID)
		if err != nil {
			return c.Send(fmt.Sprintf("❌ Gagal mengimpor seed CSV: %v", err), tele.ModeHTML)
		}
		msg := fmt.Sprintf("📥 <b>HASIL IMPOR SEED CSV MEMORY</b>\n\n"+
			"• <b>Total Diproses:</b> <code>%d item</code>\n"+
			"• <b>Berhasil Diimpor:</b> <code>%d item</code>\n"+
			"• <b>Error / Dilewati:</b> <code>%d</code>",
			res.TotalProcessed, res.Inserted, len(res.Errors))
		_ = c.Send(msg, tele.ModeHTML)
		text, menu := ui.RenderMemoryDataMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)

	case "mem_clear_confirm":
		if err := ui.memoryManager.ClearUserMemory(userID); err != nil {
			_ = c.Respond(&tele.CallbackResponse{Text: "❌ Gagal menghapus"})
			return c.Send(fmt.Sprintf("❌ Gagal menghapus memori: %v", err), tele.ModeHTML)
		}
		_ = c.Respond(&tele.CallbackResponse{Text: "🗑️ Memori dibersihkan"})
		_ = c.Send("🗑️ <b>Semua catatan memori pribadi Anda telah dihapus.</b>", tele.ModeHTML)
		text, menu := ui.RenderMemoryDataMenu(userID)
		return c.EditOrSend(text, menu, tele.ModeHTML)
	}

	return nil
}

// RenderMemorySummary returns summary of stored memories in HTML format (legacy)
func (ui *MemoryUI) RenderMemorySummary(currentUserID int64) string {
	userIDStr := "399999658"
	if currentUserID > 0 {
		userIDStr = strconv.FormatInt(currentUserID, 10)
	}

	globals, _ := ui.memoryManager.ListMemoryItems("global", "system", "", 50)
	userMems, _ := ui.memoryManager.ListMemoryItems("user", userIDStr, "", 100)

	// Count by type
	factualCount := 0
	proceduralCount := 0
	episodicCount := 0
	semanticCount := 0

	for _, m := range append(globals, userMems...) {
		switch strings.ToLower(m.Type) {
		case "procedural":
			proceduralCount++
		case "episodic":
			episodicCount++
		case "semantic":
			semanticCount++
		default:
			factualCount++
		}
	}

	var sb strings.Builder
	sb.WriteString("🧠 <b>MANAJEMEN MEMORI GOASSISTANT (SQLite FTS5)</b>\n\n")

	sb.WriteString(fmt.Sprintf("📊 <b>Statistik Kategori Memori (User: <code>%s</code>):</b>\n", userIDStr))
	sb.WriteString(fmt.Sprintf("• 📌 <b>Faktual:</b> %d catatan (profil & preferensi)\n", factualCount))
	sb.WriteString(fmt.Sprintf("• 📜 <b>Prosedural:</b> %d catatan (SOP & panduan teknis)\n", proceduralCount))
	sb.WriteString(fmt.Sprintf("• 🔄 <b>Episodik:</b> %d catatan (progres & riwayat tugas)\n", episodicCount))
	sb.WriteString(fmt.Sprintf("• 💡 <b>Semantik:</b> %d catatan (konsep & pemahaman sistem)\n\n", semanticCount))

	if len(globals) > 0 {
		sb.WriteString("🌐 <b>SOP Global Sistem:</b>\n")
		for i, g := range globals {
			if i >= 5 {
				sb.WriteString(fmt.Sprintf("<i>...dan %d SOP lainnya</i>\n", len(globals)-5))
				break
			}
			sb.WriteString(fmt.Sprintf("%d. [<code>%s</code>] %s\n", i+1, html.EscapeString(g.Key), html.EscapeString(g.Content)))
		}
		sb.WriteString("\n")
	}

	if len(userMems) > 0 {
		sb.WriteString("👤 <b>Catatan Terbaru Pengguna:</b>\n")
		count := 0
		for _, u := range userMems {
			if count >= 6 {
				sb.WriteString(fmt.Sprintf("<i>...dan %d memori lainnya</i>\n", len(userMems)-6))
				break
			}
			sb.WriteString(fmt.Sprintf("• [<code>%s</code>|%s] %s\n", html.EscapeString(u.Key), u.Type, html.EscapeString(u.Content)))
			count++
		}
		sb.WriteString("\n")
	}

	sb.WriteString("📋 <b>Perintah Manajemen Memori:</b>\n")
	sb.WriteString("• <code>/searchmemory &lt;kata kunci&gt;</code> (Uji pencarian hybrid FTS5)\n")
	sb.WriteString("• <code>/savefact &lt;global|user&gt; &lt;id&gt; &lt;tag&gt; &lt;content&gt;</code>\n")
	sb.WriteString("• <code>/clearmemory &lt;user_id&gt;</code> (Hapus memori preferensi user)\n")
	sb.WriteString("• <code>/resetsession &lt;chat_id&gt;</code> (Reset riwayat chat topik ini)\n")

	return sb.String()
}

// HandleSaveFact processes `/savefact`
func (ui *MemoryUI) HandleSaveFact(c tele.Context) error {
	args := c.Args()
	if len(args) < 4 {
		return c.Reply("⚠️ Format salah!\nContoh: <code>/savefact global system company_name Perusahaan XYZ</code>", tele.ModeHTML)
	}

	scope := strings.ToLower(args[0])
	scopeID := args[1]
	tag := args[2]
	content := strings.Join(args[3:], " ")

	if err := ui.memoryManager.SaveFact(scope, scopeID, tag, content, "fact"); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal menyimpan memori ke SQLite: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("✅ Fakta [<code>%s</code>] berhasil disimpan ke SQLite Memory (scope <code>%s:%s</code>)!", html.EscapeString(tag), html.EscapeString(scope), html.EscapeString(scopeID)), tele.ModeHTML)
}

// HandleSearchMemory processes `/searchmemory <query>`
func (ui *MemoryUI) HandleSearchMemory(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Masukkan kata kunci atau pertanyaan pencarian.\nContoh: <code>/searchmemory bypass ssl</code>", tele.ModeHTML)
	}

	query := strings.Join(args, " ")
	userIDStr := "399999658"
	if c.Sender() != nil {
		userIDStr = strconv.FormatInt(c.Sender().ID, 10)
	}

	results, err := ui.memoryManager.SearchMemoriesAdvanced("user", userIDStr, query, "hybrid", 5)
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Error pencarian memori: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	if len(results) == 0 {
		return c.Reply(fmt.Sprintf("🔍 Tidak ditemukan memori dengan kata kunci <i>\"%s\"</i> untuk user <code>%s</code>.", html.EscapeString(query), userIDStr), tele.ModeHTML)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 <b>Hasil Pencarian Memori Hybrid (\"%s\"):</b>\n\n", html.EscapeString(query)))
	for i, r := range results {
		scoreStr := ""
		if r.Score > 0 {
			scoreStr = fmt.Sprintf(" <i>(skor: %.2f)</i>", r.Score)
		}
		sb.WriteString(fmt.Sprintf("%d. <b>[%s|%s]</b> <code>%s</code>%s\n  %s\n\n", i+1, r.Type, r.Category, html.EscapeString(r.Key), scoreStr, html.EscapeString(r.Content)))
	}

	return c.Reply(sb.String(), tele.ModeHTML)
}

// HandleClearMemory processes `/clearmemory <user_id>`
func (ui *MemoryUI) HandleClearMemory(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Tentukan user ID yang ingin dibersihkan memorinya.\nContoh: <code>/clearmemory 12345678</code>", tele.ModeHTML)
	}

	userID := args[0]
	if err := ui.memoryManager.ClearUserMemory(userID); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal membersihkan memori user di SQLite: %v", html.EscapeString(err.Error())), tele.ModeHTML)
	}

	return c.Reply(fmt.Sprintf("🧹 Memori untuk user <code>%s</code> di SQLite berhasil dibersihkan!", html.EscapeString(userID)), tele.ModeHTML)
}

// HandleResetSession processes `/resetsession`
func (ui *MemoryUI) HandleResetSession(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Reply("⚠️ Tentukan chat ID yang ingin di-reset.")
	}

	chatID := args[0]
	session, err := ui.sessionManager.GetOrCreate("admin", chatID, "admin")
	if err != nil {
		return c.Reply(fmt.Sprintf("❌ Error: %v", html.EscapeString(err.Error())))
	}

	if err := ui.sessionManager.ResetSession(session.ID); err != nil {
		return c.Reply(fmt.Sprintf("❌ Gagal mereset sesi: %v", html.EscapeString(err.Error())))
	}

	return c.Reply(fmt.Sprintf("🧹 Riwayat percakapan untuk chat <code>%s</code> berhasil dibersihkan!", html.EscapeString(chatID)), tele.ModeHTML)
}
