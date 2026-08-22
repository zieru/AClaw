package admin

import (
	"fmt"
	"log"
	"sync"
	"time"

	"goassistant/internal/agent"
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
) (*AdminBot, error) {
	pref := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: time.Duration(cfg.AdminTelegram.PollTimeout) * time.Second},
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

		modelUI:      NewModelUI(db, pm),
		limitsUI:     NewLimitsUI(db),
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
