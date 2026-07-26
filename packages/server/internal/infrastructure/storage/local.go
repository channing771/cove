package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type LocalStore struct {
	root string
}

func NewLocalStore(root string) LocalStore {
	return LocalStore{root: root}
}

// safeKey 将 key 锚定在存储根目录内，中和 ".." 等路径穿越。
//
// filepath.Clean 会保留前导的 ".."，直接 Join 可逃出 root；先在虚拟根 "/" 下 Clean
// 再去掉前缀，任何穿越都被夹回根内。
func safeKey(key string) string {
	return strings.TrimPrefix(filepath.Clean("/"+filepath.ToSlash(key)), "/")
}

func (s LocalStore) Ping(ctx context.Context) error {
	return os.MkdirAll(s.root, 0o755)
}

func (s LocalStore) Put(ctx context.Context, key string, data []byte) error {
	path := filepath.Join(s.root, safeKey(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (s LocalStore) Get(ctx context.Context, key string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.root, safeKey(key)))
}

func (s LocalStore) Delete(ctx context.Context, key string) error {
	return os.Remove(filepath.Join(s.root, safeKey(key)))
}
