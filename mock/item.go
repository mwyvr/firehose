package mock

import (
	"context"
	"time"

	"github.com/mwyvr/firehose"
)

var _ firehose.ItemService = (*ItemService)(nil)

// ItemService is a mock firehose.ItemService.
type ItemService struct {
	FindItemsFn    func(ctx context.Context, filter firehose.ItemFilter) ([]*firehose.Item, int, error)
	ItemStatsFn    func(ctx context.Context) ([]firehose.ItemStat, error)
	UpsertItemsFn  func(ctx context.Context, items []*firehose.Item) error
	PurgeExpiredFn func(ctx context.Context, olderThan time.Time) (int, error)
}

func (s *ItemService) FindItems(ctx context.Context, filter firehose.ItemFilter) ([]*firehose.Item, int, error) {
	return s.FindItemsFn(ctx, filter)
}

func (s *ItemService) UpsertItems(ctx context.Context, items []*firehose.Item) error {
	return s.UpsertItemsFn(ctx, items)
}

func (s *ItemService) PurgeExpired(ctx context.Context, olderThan time.Time) (int, error) {
	return s.PurgeExpiredFn(ctx, olderThan)
}

func (s *ItemService) ItemStats(ctx context.Context) ([]firehose.ItemStat, error) {
	if s.ItemStatsFn == nil {
		return nil, nil
	}
	return s.ItemStatsFn(ctx)
}
