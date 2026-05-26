package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TelegramConfig struct {
	BotToken string
	GroupID  string
	TopicID  string
	From     string
}

type TelegramClient struct {
	cfg           TelegramConfig
	client        *http.Client
	failures      int32
	coolDownUntil time.Time
	mu            sync.RWMutex
}

func NewClient(cfg TelegramConfig) *TelegramClient {
	return &TelegramClient{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func EscapeHTML(text string) string {
	text = strings.ToValidUTF8(text, "")
	text = strings.ReplaceAll(text, "\x00", "")
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}

func SplitMessage(text string, limit int) []string {
	var chunks []string
	for len(text) > limit {
		idx := strings.LastIndex(text[:limit], "\n")
		if idx == 0 {
			idx = 1
		} else if idx == -1 {
			idx = limit
		}
		chunks = append(chunks, text[:idx])
		text = text[idx:]
	}
	chunks = append(chunks, text)
	return chunks
}

type telegramErrorResponse struct {
	Ok          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *TelegramClient) IsCoolingDown() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Now().Before(c.coolDownUntil)
}

func (c *TelegramClient) recordFailure() {
	val := atomic.AddInt32(&c.failures, 1)
	if val >= 3 {
		c.mu.Lock()
		c.coolDownUntil = time.Now().Add(5 * time.Minute)
		c.mu.Unlock()
		slog.Error("telegram client entering 5-minute cooldown due to consecutive failures")
	}
}

func (c *TelegramClient) recordSuccess() {
	atomic.StoreInt32(&c.failures, 0)
}

func (c *TelegramClient) Send(ctx context.Context, message string) error {
	if c.IsCoolingDown() {
		return fmt.Errorf("telegram client is in cooldown mode due to consecutive failures")
	}
	if c.cfg.From != "" {
		message = fmt.Sprintf("<b>[Instance: %s]</b>\n%s", EscapeHTML(c.cfg.From), message)
	}
	chunks := SplitMessage(message, 4000)
	for _, chunk := range chunks {
		if err := c.sendChunk(ctx, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (c *TelegramClient) sendChunk(ctx context.Context, chunk string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.cfg.BotToken)
	payload := map[string]any{
		"chat_id":    c.cfg.GroupID,
		"text":       chunk,
		"parse_mode": "HTML",
	}
	if c.cfg.TopicID != "" {
		if threadID, err := strconv.Atoi(c.cfg.TopicID); err == nil {
			payload["message_thread_id"] = threadID
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var lastErr error
	delay := 500 * time.Millisecond
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.client.Do(req)
		if err == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				resp.Body.Close()
				c.recordSuccess()
				return nil
			}
			var errResp telegramErrorResponse
			if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Parameters.RetryAfter > 0 {
				resp.Body.Close()
				retryDelay := time.Duration(errResp.Parameters.RetryAfter) * time.Second
				slog.Warn("telegram api rate limited, waiting to retry", "retry_after_seconds", errResp.Parameters.RetryAfter)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryDelay):
					continue
				}
			}
			resp.Body.Close()
			lastErr = fmt.Errorf("telegram api error: status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			delay *= 2
		}
	}
	c.recordFailure()
	return lastErr
}

type TelegramHandler struct {
	next   slog.Handler
	client *TelegramClient
	queue  chan string
}

func NewHandler(next slog.Handler, client *TelegramClient, queue chan string) *TelegramHandler {
	return &TelegramHandler{
		next:   next,
		client: client,
		queue:  queue,
	}
}

func (h *TelegramHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *TelegramHandler) Handle(ctx context.Context, r slog.Record) error {
	err := h.next.Handle(ctx, r)
	if err != nil {
		return err
	}
	if r.Level >= slog.LevelWarn && h.queue != nil {
		msgLower := strings.ToLower(r.Message)
		if strings.Contains(msgLower, "telegram") || strings.Contains(msgLower, "dropped system event") {
			return nil
		}
		var buf bytes.Buffer
		buf.WriteString(fmt.Sprintf("<b>[%s]</b> %s\n\n", r.Level.String(), EscapeHTML(r.Message)))
		r.Attrs(func(a slog.Attr) bool {
			buf.WriteString(fmt.Sprintf("<code>%s: %v</code>\n", EscapeHTML(a.Key), EscapeHTML(fmt.Sprintf("%v", a.Value))))
			return true
		})
		select {
		case h.queue <- buf.String():
		default:
		}
	}
	return nil
}

func (h *TelegramHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TelegramHandler{
		next:   h.next.WithAttrs(attrs),
		client: h.client,
		queue:  h.queue,
	}
}

func (h *TelegramHandler) WithGroup(name string) slog.Handler {
	return &TelegramHandler{
		next:   h.next.WithGroup(name),
		client: h.client,
		queue:  h.queue,
	}
}

type Manager struct {
	client *TelegramClient
	queue  chan string
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func NewManager(client *TelegramClient) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		client: client,
		queue:  make(chan string, 1000),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

func (m *Manager) Handler(next slog.Handler) slog.Handler {
	return NewHandler(next, m.client, m.queue)
}

func (m *Manager) Start() {
	go func() {
		defer close(m.done)
		defer func() {
			if r := recover(); r != nil {
				slog.Error("telegram manager goroutine panicked", "panic", r)
			}
		}()
		select {
		case <-m.ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		for {
			select {
			case msg, ok := <-m.queue:
				if !ok {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				err := m.client.Send(ctx, msg)
				cancel()
				if err != nil {
					slog.Error("failed to send telegram notification", "error", err)
					select {
					case <-m.ctx.Done():
						return
					case <-time.After(60 * time.Second):
					}
				}
			case <-m.ctx.Done():
				for {
					select {
					case msg := <-m.queue:
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						if err := m.client.Send(ctx, msg); err != nil {
							slog.Error("failed to send telegram notification on shutdown", "error", err)
						}
						cancel()
					default:
						return
					}
				}
			}
		}
	}()
}

func (m *Manager) TriggerSystemEvent(event, details string) {
	msg := fmt.Sprintf("<b>[SYSTEM EVENT - %s]</b>\n\n%s", EscapeHTML(event), EscapeHTML(details))
	select {
	case m.queue <- msg:
	case <-time.After(2 * time.Second):
		slog.Error("dropped system event due to queue saturation", "event", event)
	}
}

func (m *Manager) Drain() {
	m.cancel()
	<-m.done
}

func Init(botToken, groupID, topicID, from string, handler slog.Handler) (*Manager, slog.Handler) {
	if botToken != "" && groupID != "" {
		tgClient := NewClient(TelegramConfig{
			BotToken: botToken,
			GroupID:  groupID,
			TopicID:  topicID,
			From:     from,
		})
		tgManager := NewManager(tgClient)
		tgManager.Start()
		return tgManager, tgManager.Handler(handler)
	}
	return nil, handler
}
