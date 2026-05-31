package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/client"
	"github.com/junpeng-jp/gitops-sidecar/internal/model"
)

type NotificationWorker struct {
	notifier      client.NotificationClient
	ch            chan model.RepoChangedEvent
	maxBatchSize  int
	batchInterval time.Duration
	log           *slog.Logger
}

func NewNotificationWorker(notifier client.NotificationClient, ch chan model.RepoChangedEvent, cfg *model.NotificationConfig, log *slog.Logger) *NotificationWorker {
	return &NotificationWorker{
		notifier:      notifier,
		ch:            ch,
		maxBatchSize:  cfg.MaxBatchSize,
		batchInterval: time.Duration(cfg.BatchInterval),
		log:           log,
	}
}

// Chan returns the send-only end of the notification channel.
func (n *NotificationWorker) Chan() chan<- model.RepoChangedEvent {
	return n.ch
}

func (n *NotificationWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(n.batchInterval)
	defer ticker.Stop()

	batch := make([]model.RepoChangedEvent, 0, n.maxBatchSize)

	flush := func(flushCtx context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := n.notifier.Notify(flushCtx, batch); err != nil {
			n.log.Error("notify failed", "err", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Drain any events that arrived just before shutdown and do a final flush.
			drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for {
				select {
				case event := <-n.ch:
					batch = append(batch, event)
					if len(batch) >= n.maxBatchSize {
						flush(drainCtx)
					}
				default:
					flush(drainCtx)
					return
				}
			}
		case event, ok := <-n.ch:
			if !ok {
				flush(ctx)
				return
			}
			batch = append(batch, event)
			if len(batch) >= n.maxBatchSize {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		}
	}
}
