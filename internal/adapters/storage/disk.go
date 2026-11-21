package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Disk 는 로컬 디스크 저장소다(설치형 기본).
type Disk struct{ Dir string }

// NewDisk 는 디렉터리를 만들고 저장소를 연다.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("첨부 디렉터리: %w", err)
	}
	return &Disk{Dir: dir}, nil
}

func (d *Disk) Kind() string { return "disk" }

func (d *Disk) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if strings.Contains(key, "..") || clean == "/" {
		return "", errors.New("잘못된 저장 키")
	}
	return filepath.Join(d.Dir, clean), nil
}

func (d *Disk) Put(_ context.Context, key string, data []byte) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (d *Disk) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}
