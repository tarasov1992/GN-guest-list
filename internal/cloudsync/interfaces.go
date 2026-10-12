package cloudsync

import (
	"context"
	"io"
)

type Source interface {
	Fetch(ctx context.Context) (io.ReadCloser, error)
}

var (
	_ Source = (*YDClient)(nil)
	_ Source = (*GDClient)(nil)
)
