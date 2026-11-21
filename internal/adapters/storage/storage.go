// Package storage 는 첨부 파일 저장소 어댑터다(로컬 디스크 ↔ S3 호환).
package storage

import (
	"context"
	"io"
)

// Store 는 코어가 아는 유일한 저장소 인터페이스다.
type Store interface {
	Kind() string
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}
