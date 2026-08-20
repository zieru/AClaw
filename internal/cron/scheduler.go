package cron

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"goassistant/internal/agent"
	"goassistant/internal/storage"
)

// MessageSender is a function type that dispatches messages to channels
type MessageSender func(channelType, targetID, message string) error

type Scheduler struct {
	mu           sync.Mutex
	cronRunner   *cron.Cron
	db           *storage.DB
	orchestrator *agent.Orchestrator
	sender       MessageSender
	jobEntries   map[string]cron.EntryID
}

func NewScheduler(db *storage.DB, orch *agent.Orchestrator, sender MessageSender) *Scheduler {
	return &Scheduler{
		cronRunner:   cron.New(cron.WithSeconds()),
		db:           db,
		orchestrator: orch,
		sender:       sender,
		jobEntries:   make(map[string]cron.EntryID),
	}
}

// Start loads all active cron jobs from DB and starts the scheduler
func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cronRunner.Start()
	return s.reloadLocked()
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cronRunner.Stop()
}

// Reload refreshes all active cron jobs from the database
func (s *Scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadLocked()
}

func (s *Scheduler) reloadLocked() error {
	// Remove all current entries
	for id, entryID := range s.jobEntries {
		s.cronRunner.Remove(entryID)
		delete(s.jobEntries, id)
	}

	jobs, err := s.db.ListCronJobs()
	if err != nil {
		return err
	}

	for _, j := range jobs {
		if !j.IsActive {
			continue
		}
		jobRecord := j
		entryID, err := s.cronRunner.AddFunc(j.CronExpr, func() {
			s.executeJob(jobRecord)
		})
		if err != nil {
			log.Printf("[Cron] Gagal mendaftarkan cron job '%s' (%s): %v", j.Name, j.CronExpr, err)
			continue
		}
		s.jobEntries[j.ID] = entryID
	}
	return nil
}

// RunNow executes a specific cron job immediately on demand
func (s *Scheduler) RunNow(jobID string) error {
	job, err := s.db.GetCronJob(jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("job dengan ID %s tidak ditemukan", jobID)
	}

	go s.executeJob(*job)
	return nil
}

func (s *Scheduler) executeJob(j storage.CronJobRecord) {
	log.Printf("[Cron] Menjalankan scheduled job: %s (%s)", j.Name, j.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := s.orchestrator.ProcessMessage(ctx, agent.UserRequest{
		ChannelType: j.TargetChannel,
		ChannelID:   "cron",
		ChannelName: "Cron Scheduler",
		ChatID:      j.TargetChatID,
		UserID:      "cron_system",
		UserName:    "System Scheduler",
		UserPrompt:  j.Prompt,
	})

	if err != nil {
		log.Printf("[Cron] Error saat mengeksekusi prompt AI untuk job '%s': %v", j.Name, err)
		return
	}

	// Update last run time
	_ = s.db.UpdateCronLastRun(j.ID, time.Now())

	// Dispatch response to target channel
	if s.sender != nil && resp.Text != "" {
		if sendErr := s.sender(j.TargetChannel, j.TargetChatID, resp.Text); sendErr != nil {
			log.Printf("[Cron] Gagal mengirim pesan scheduled job ke %s:%s: %v", j.TargetChannel, j.TargetChatID, sendErr)
		}
	}
}
