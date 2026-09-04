package admin

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"goassistant/internal/agent"
	"goassistant/internal/checkin"
	"goassistant/internal/config"
	"goassistant/internal/cron"
	"goassistant/internal/memory"
	"goassistant/internal/provider"
	"goassistant/internal/proxy"
	"goassistant/internal/storage"
	"goassistant/internal/tools"

	tele "gopkg.in/telebot.v3"
)

type AdminBot struct {
	bot          *tele.Bot
	db           *storage.DB
	cfg          *config.AppConfig
	mdLoader     *agent.MDLoader
	orchestrator *agent.Orchestrator
	scheduler    *cron.Scheduler
	toolRegistry *tools.Registry
	provManager  *provider.Manager
	memManager   *memory.Manager
	sessManager  *memory.SessionManager
	proxyPool    *proxy.Pool
	checkinSvc   *checkin.Service
	activeTasks  sync.Map

	modelUI      *ModelUI
	limitsUI     *LimitsUI
	providerUI   *ProviderUI
	wizard       *ProviderWizard
	comboUI      *ComboUI
	comboWizard  *ComboWizard
	channelUI    *ChannelUI
	proxyUI      *ProxyUIHandler
	tokenSaverUI *TokenSaverUIHandler
	mdUI         *MDUI
	cronUI       *CronUI
	memoryUI     *MemoryUI
	auditUI      *AuditUI
	updateUI     *UpdateUI
	tavilyUI     *TavilyUI
	checkinUI    *CheckinUI
}

func NewAdminBot(
	token string,
	cfg *config.AppConfig,
	db *storage.DB,
	loader *agent.MDLoader,
	orch *agent.Orchestrator,
	sched *cron.Scheduler,
	tr *tools.Registry,
	pm *provider.Manager,
	mm *memory.Manager,
	sm *memory.SessionManager,
	pool *proxy.Pool,
	checkinSvc *checkin.Service,
) (*AdminBot, error) {
	pref := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: time.Duration(cfg.AdminTelegram.PollTimeout) * time.Second},
		OnError: func(err error, c tele.Context) {
			if c != nil && c.Chat() != nil {
				log.Printf("[Admin-TG] Error chat %d: %v", c.Chat().ID, err)
				if strings.Contains(err.Error(), "MESSAGE_TOO_LONG") {
					_ = c.Send("⚠️ <b>Pesan melebihi batas Telegram (4096 karakter).</b>\nSistem telah mencegah pengiriman pesan berlebih.", tele.ModeHTML)
				}
			} else {
				log.Printf("[Admin-TG] Error: %v", err)
			}
		},
	}

	bot, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("gagal menginisialisasi admin telebot: %w", err)
	}

	a := &AdminBot{
		bot:          bot,
		db:           db,
		cfg:          cfg,
		mdLoader:     loader,
		orchestrator: orch,
		scheduler:    sched,
		toolRegistry: tr,
		provManager:  pm,
		memManager:   mm,
		sessManager:  sm,
		proxyPool:    pool,
		checkinSvc:   checkinSvc,

		modelUI:      NewModelUI(db, pm),
		limitsUI:     NewLimitsUI(db, pm),
		providerUI:   NewProviderUI(db, pm, pool),
		wizard:       NewProviderWizard(db, pm, pool, bot),
		comboUI:      NewComboUI(db, pm),
		comboWizard:  NewComboWizard(db, pm, bot),
		channelUI:    NewChannelUI(db, tr),
		proxyUI:      NewProxyUIHandler(db, pool),
		tokenSaverUI: NewTokenSaverUIHandler(db),
		mdUI:         NewMDUI(loader, db, bot),
		cronUI:       NewCronUI(db, sched),
		memoryUI:     NewMemoryUI(db, mm, sm),
		auditUI:      NewAuditUI(db),
		updateUI:     NewUpdateUI(cfg, bot),
		tavilyUI:     NewTavilyUI(db, cfg),
		checkinUI:    NewCheckinUI(db, checkinSvc),
	}

	a.registerRoutes()
	return a, nil
}

func (a *AdminBot) Start() {
	log.Printf("[Admin-TG] Control Plane Bot aktif (@%s). Menunggu perintah admin...", a.bot.Me.Username)
	a.bot.Start()
}

func (a *AdminBot) Stop() {
	a.bot.Stop()
}

// Bot returns the telebot instance
func (a *AdminBot) Bot() *tele.Bot {
	return a.bot
}
