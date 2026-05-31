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

func NewNotificationWorker(notifier client.NotificationClient, cfg *model.NotificationConfig, log *slog.Logger) *NotificationWorker {
	return &NotificationWorker{
		notifier:      notifier,
		ch:            make(chan model.RepoChangedEvent, 64),
		maxBatchSize:  cfg.MaxBatchSize,
		batchInterval: time.Duration(cfg.BatchInterval),
		log:           log,
	}
}

func (n *NotificationWorker) Enqueue(event model.RepoChangedEvent) {
	select {
	case n.ch <- event:
	default:
		n.log.Warn("notification queue full, dropping event", "repo", event.Name, "state", event.State)
	}
}

func (n *NotificationWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(n.batchInterval)
	defer ticker.Stop()

	batch := make([]model.RepoChangedEvent, 0, n.maxBatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := n.notifier.Notify(ctx, batch); err != nil {
			n.log.Error("notify failed", "err", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-n.ch:
			if !ok {
				return
			}
			batch = append(batch, event)
			if len(batch) >= n.maxBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
