package tgprompt

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tele "gopkg.in/telebot.v3"
)

type mockBot struct {
	mu         sync.Mutex
	nextID     int
	sentMsgs   []*tele.Message
	deletedIDs []int
	editedIDs  []int
}

func newMockBot() *mockBot {
	return &mockBot{
		nextID: 100,
	}
}

func (m *mockBot) Send(to tele.Recipient, what interface{}, opts ...interface{}) (*tele.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	chat := &tele.Chat{ID: 12345}
	if c, ok := to.(*tele.Chat); ok {
		chat = c
	}
	text := ""
	if s, ok := what.(string); ok {
		text = s
	}
	msg := &tele.Message{
		ID:   m.nextID,
		Chat: chat,
		Text: text,
	}
	m.sentMsgs = append(m.sentMsgs, msg)
	return msg, nil
}

func (m *mockBot) Delete(msg tele.Editable) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg == nil {
		return nil
	}
	if mMsg, ok := msg.(*tele.Message); ok {
		m.deletedIDs = append(m.deletedIDs, mMsg.ID)
		return nil
	}
	msgSig, _ := msg.MessageSig()
	parts := strings.Split(msgSig, "$")
	if len(parts) > 0 {
		var id int
		for _, ch := range parts[0] {
			if ch >= '0' && ch <= '9' {
				id = id*10 + int(ch-'0')
			}
		}
		if id > 0 {
			m.deletedIDs = append(m.deletedIDs, id)
			return nil
		}
	}
	return nil
}

func (m *mockBot) Edit(msg tele.Editable, what interface{}, opts ...interface{}) (*tele.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mMsg, ok := msg.(*tele.Message); ok {
		m.editedIDs = append(m.editedIDs, mMsg.ID)
		return mMsg, nil
	}
	return nil, nil
}

type mockContext struct {
	tele.Context
	chat   *tele.Chat
	sender *tele.User
	msg    *tele.Message
}

func (c *mockContext) Message() *tele.Message       { return c.msg }
func (c *mockContext) Chat() *tele.Chat             { return c.chat }
func (c *mockContext) Sender() *tele.User           { return c.sender }
func (c *mockContext) Text() string                 { if c.msg != nil { return c.msg.Text }; return "" }

func TestPromptPasswordSuccess(t *testing.T) {
	bot := newMockBot()
	pm := NewPromptManager(bot)

	chatID := int64(1001)
	userID := int64(2002)

	type result struct {
		pass   string
		finish func()
		err    error
	}

	resChan := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		p, f, err := pm.PromptPassword(ctx, chatID, userID, "Konfirmasi Sudo", "Perintah: sudo apt update")
		resChan <- result{pass: p, finish: f, err: err}
	}()

	// Wait for prompt to be sent
	time.Sleep(50 * time.Millisecond)

	bot.mu.Lock()
	if len(bot.sentMsgs) == 0 {
		bot.mu.Unlock()
		t.Fatalf("expected prompt message to be sent")
	}
	promptMsgID := bot.sentMsgs[0].ID
	bot.mu.Unlock()

	// Simulate user typing password
	userMsg := &tele.Message{
		ID:   999,
		Chat: &tele.Chat{ID: chatID},
		Sender: &tele.User{ID: userID},
		Text: "mySuperSecretPassword",
	}
	mCtx := &mockContext{
		chat:   &tele.Chat{ID: chatID},
		sender: &tele.User{ID: userID},
		msg:    userMsg,
	}

	handled, err := pm.HandleTextMessage(mCtx)
	if err != nil {
		t.Fatalf("unexpected error from HandleTextMessage: %v", err)
	}
	if !handled {
		t.Fatalf("expected HandleTextMessage to return handled=true")
	}

	// Verify user's message was immediately deleted
	bot.mu.Lock()
	deletedUserMsg := false
	for _, id := range bot.deletedIDs {
		if id == 999 {
			deletedUserMsg = true
			break
		}
	}
	bot.mu.Unlock()
	if !deletedUserMsg {
		t.Errorf("expected user message (ID 999) to be deleted immediately")
	}

	// Read prompt result
	res := <-resChan
	if res.err != nil {
		t.Fatalf("expected nil error, got: %v", res.err)
	}
	if res.pass != "mySuperSecretPassword" {
		t.Errorf("expected pass='mySuperSecretPassword', got %q", res.pass)
	}

	// Now execute finish callback (simulating command finished)
	if res.finish == nil {
		t.Fatalf("expected non-nil finish callback")
	}
	res.finish()

	// Verify prompt message was deleted on finish
	bot.mu.Lock()
	deletedPrompt := false
	for _, id := range bot.deletedIDs {
		if id == promptMsgID {
			deletedPrompt = true
			break
		}
	}
	bot.mu.Unlock()
	if !deletedPrompt {
		t.Errorf("expected prompt message (ID %d) to be deleted after finish()", promptMsgID)
	}
}

func TestPromptPasswordCancellation(t *testing.T) {
	bot := newMockBot()
	pm := NewPromptManager(bot)

	chatID := int64(1001)
	userID := int64(2002)

	resChan := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_, _, err := pm.PromptPassword(ctx, chatID, userID, "Sudo", "Perintah sudo")
		resChan <- err
	}()

	time.Sleep(50 * time.Millisecond)

	// User types "batal"
	userMsg := &tele.Message{
		ID:   888,
		Chat: &tele.Chat{ID: chatID},
		Sender: &tele.User{ID: userID},
		Text: "batal",
	}
	mCtx := &mockContext{
		chat:   &tele.Chat{ID: chatID},
		sender: &tele.User{ID: userID},
		msg:    userMsg,
	}

	handled, err := pm.HandleTextMessage(mCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatalf("expected handled=true on cancel")
	}

	promptErr := <-resChan
	if promptErr == nil {
		t.Fatalf("expected cancellation error, got nil")
	}
	if !strings.Contains(promptErr.Error(), "dibatalkan") {
		t.Errorf("expected 'dibatalkan' in error message, got: %v", promptErr)
	}
}

func TestMultipleUsersConcurrency(t *testing.T) {
	bot := newMockBot()
	pm := NewPromptManager(bot)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	user1Chan := make(chan string, 1)
	user2Chan := make(chan string, 1)

	go func() {
		p, f, _ := pm.PromptPassword(ctx, 111, 111, "User1", "test")
		if f != nil { f() }
		user1Chan <- p
	}()

	go func() {
		p, f, _ := pm.PromptPassword(ctx, 222, 222, "User2", "test")
		if f != nil { f() }
		user2Chan <- p
	}()

	time.Sleep(50 * time.Millisecond)

	// User 2 responds first
	mCtx2 := &mockContext{
		chat:   &tele.Chat{ID: 222},
		sender: &tele.User{ID: 222},
		msg:    &tele.Message{ID: 20, Chat: &tele.Chat{ID: 222}, Sender: &tele.User{ID: 222}, Text: "PassForUser2"},
	}
	handled2, _ := pm.HandleTextMessage(mCtx2)
	if !handled2 {
		t.Errorf("expected user2 to be handled")
	}

	// User 1 responds second
	mCtx1 := &mockContext{
		chat:   &tele.Chat{ID: 111},
		sender: &tele.User{ID: 111},
		msg:    &tele.Message{ID: 10, Chat: &tele.Chat{ID: 111}, Sender: &tele.User{ID: 111}, Text: "PassForUser1"},
	}
	handled1, _ := pm.HandleTextMessage(mCtx1)
	if !handled1 {
		t.Errorf("expected user1 to be handled")
	}

	got1 := <-user1Chan
	got2 := <-user2Chan

	if got1 != "PassForUser1" {
		t.Errorf("user1 got %q, want 'PassForUser1'", got1)
	}
	if got2 != "PassForUser2" {
		t.Errorf("user2 got %q, want 'PassForUser2'", got2)
	}
}
