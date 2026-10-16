package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ABreadTree/vohive-sms-forward/internal/store"
)

// DeliveryStore keeps each platform's progress independently of the modem.
type DeliveryStore interface {
	DueDeliveries(time.Time, int) ([]store.Delivery, error)
	DueDeliveriesByChannel(string, time.Time, int) ([]store.Delivery, error)
	GetMessage(int64) (store.Message, error)
	ListSIMs() ([]store.SIM, error)
	MarkDeliverySuccess(int64, string) error
	MarkDeliveryRetry(int64, string, time.Time, string) error
}

type Sender interface {
	PushBark(context.Context, string, string) error
	SendTelegram(context.Context, string) error
}

type Dispatcher struct {
	store    DeliveryStore
	sender   Sender
	channels []string
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewDispatcher(st DeliveryStore, sender Sender, channels ...string) *Dispatcher {
	if len(channels) == 0 {
		channels = []string{"bark", "telegram"}
	}
	return &Dispatcher{store: st, sender: sender, channels: append([]string(nil), channels...)}
}

func (d *Dispatcher) Start() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.done = cancel, make(chan struct{})
	go func() {
		defer close(d.done)
		var workers sync.WaitGroup
		for _, channel := range d.channels {
			workers.Add(1)
			go func(ch string) { defer workers.Done(); d.runChannelLoop(ctx, ch) }(channel)
		}
		workers.Wait()
	}()
}

func (d *Dispatcher) runChannelLoop(ctx context.Context, channel string) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		if err := d.runChannel(ctx, channel, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("%s notification store error: %v", channel, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (d *Dispatcher) Stop() {
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (d *Dispatcher) runOnce(ctx context.Context, now time.Time) error {
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, channel := range d.channels {
		wg.Add(1)
		go func(ch string) { defer wg.Done(); errs <- d.runChannel(ctx, ch, now) }(channel)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) runChannel(ctx context.Context, channel string, now time.Time) error {
	jobs, err := d.store.DueDeliveriesByChannel(channel, now, 100)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg, err := d.store.GetMessage(job.MessageID)
		if err != nil {
			return err
		}
		label := "未知 SIM"
		sims, err := d.store.ListSIMs()
		if err != nil {
			return err
		}
		for _, sim := range sims {
			if sim.ID == msg.SimID {
				label = sim.Label
				break
			}
		}
		title := fmt.Sprintf("短信 · %s · %s", label, msg.Peer)
		body := fmt.Sprintf("时间：%s\n发送方：%s\nSIM：%s\n\n%s", msg.Timestamp, msg.Peer, label, msg.Body)
		switch job.Channel {
		case "bark":
			err = d.sender.PushBark(ctx, title, body)
		case "telegram":
			err = d.sender.SendTelegram(ctx, title+"\n"+body)
		default:
			err = errors.New("unknown notification channel")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			if err := d.store.MarkDeliverySuccess(job.MessageID, job.Channel); err != nil {
				return err
			}
			continue
		}
		// An HTTP error can include credentials or message text. Persist and log
		// only a constant category; the retry schedule carries no sensitive data.
		wait := retryDelay(job.Attempts, err)
		if err := d.store.MarkDeliveryRetry(job.MessageID, job.Channel, now.Add(wait), "delivery failed"); err != nil {
			return err
		}
		log.Printf("%s notification failed; retry scheduled", job.Channel)
	}
	return nil
}

func retryDelay(attempts int, err error) time.Duration {
	delay := 5 * time.Second
	for i := 0; i < attempts && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > delay {
		delay = apiErr.RetryAfter
	}
	return delay
}
