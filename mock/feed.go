// Package mock provides hand-written mocks of the root service interfaces for
// testing, in the WTF style: a struct of function fields, each method
// delegating to its field.
package mock

import (
	"context"

	"github.com/mwyvr/firehose"
)

var _ firehose.FeedService = (*FeedService)(nil)

// FeedService is a mock firehose.FeedService.
type FeedService struct {
	FindFeedsFn     func(ctx context.Context, filter firehose.FeedFilter) ([]*firehose.Feed, int, error)
	FindFeedByURLFn func(ctx context.Context, url string) (*firehose.Feed, error)
	SyncFeedsFn     func(ctx context.Context, feeds []*firehose.Feed) error
	UpdateFeedFn    func(ctx context.Context, id int64, upd firehose.FeedUpdate) (*firehose.Feed, error)
}

func (s *FeedService) FindFeeds(ctx context.Context, filter firehose.FeedFilter) ([]*firehose.Feed, int, error) {
	return s.FindFeedsFn(ctx, filter)
}

func (s *FeedService) FindFeedByURL(ctx context.Context, url string) (*firehose.Feed, error) {
	return s.FindFeedByURLFn(ctx, url)
}

func (s *FeedService) SyncFeeds(ctx context.Context, feeds []*firehose.Feed) error {
	return s.SyncFeedsFn(ctx, feeds)
}

func (s *FeedService) UpdateFeed(ctx context.Context, id int64, upd firehose.FeedUpdate) (*firehose.Feed, error) {
	return s.UpdateFeedFn(ctx, id, upd)
}
