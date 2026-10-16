package telegram

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/ABreadTree/vohive-sms-forward/internal/notify"
	"github.com/ABreadTree/vohive-sms-forward/internal/store"
)

type UpdateClient interface {
	GetUpdates(context.Context, int64) ([]notify.Update, error)
	SendTelegram(context.Context, string) error
}
type OffsetStore interface {
	LastTelegramUpdateID() (int64, error)
	SetLastTelegramUpdateID(int64) error
	PendingTelegramReplies(int) ([]store.Reply, error)
	MarkTelegramReplySent(int64) error
}
type Poller struct {
	client    UpdateClient
	processor *Processor
	store     OffsetStore
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
}

func NewPoller(client UpdateClient, processor *Processor, st OffsetStore) *Poller {
	return &Poller{client: client, processor: processor, store: st}
}

func (p *Poller) Start(parent context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancel, p.done = cancel, make(chan struct{})
	go func() {
		defer close(p.done)
		for ctx.Err() == nil {
			err := p.runOnce(ctx)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				var apiErr *notify.APIError
				wait := 5 * time.Second
				if errors.As(err, &apiErr) && apiErr.RetryAfter > wait {
					wait = apiErr.RetryAfter
				}
				if isWebhookConflict(err) {
					log.Printf("Telegram webhook 与长轮询冲突；请关闭 webhook")
				} else {
					log.Printf("Telegram polling/reply unavailable; retry scheduled")
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}
	}()
}
func (p *Poller) Stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (p *Poller) runOnce(ctx context.Context) error {
	if err := p.flushReplies(ctx); err != nil {
		return err
	}
	last, err := p.store.LastTelegramUpdateID()
	if err != nil {
		return err
	}
	updates, err := p.client.GetUpdates(ctx, last+1)
	if err != nil {
		if isWebhookConflict(err) {
			return errors.New("Telegram webhook 与 getUpdates 冲突")
		}
		return err
	}
	for _, u := range updates {
		if u.UpdateID <= last {
			continue
		}
		if _, err := p.processor.Process(ctx, u); err != nil {
			return err
		}
		if err := p.store.SetLastTelegramUpdateID(u.UpdateID); err != nil {
			return err
		}
		last = u.UpdateID
		// Deliver the preview before consuming a later /confirm from the
		// same getUpdates batch. A failed send pauses command processing.
		if err := p.flushReplies(ctx); err != nil {
			return err
		}
	}
	return nil
}
func (p *Poller) flushReplies(ctx context.Context) error {
	for {
		replies, err := p.store.PendingTelegramReplies(50)
		if err != nil {
			return err
		}
		if len(replies) == 0 {
			return nil
		}
		for _, r := range replies {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := p.client.SendTelegram(ctx, r.Text); err != nil {
				return err
			}
			if err := p.store.MarkTelegramReplySent(r.UpdateID); err != nil {
				return err
			}
		}
	}
}
func isWebhookConflict(err error) bool {
	var apiErr *notify.APIError
	return errors.As(err, &apiErr) && apiErr.Platform == "telegram" && apiErr.Status == 409
}
