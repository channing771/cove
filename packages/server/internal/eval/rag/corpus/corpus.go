package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/boxify/api-go/internal/models"
	"github.com/google/uuid"
)

// evalNamespace 用于从稳定 key 派生确定性 UUID,使同一语料多次摄入得到同一批 id。
var evalNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("cove:eval:corpus"))

// DeterministicID 由稳定 key(如相对路径)派生确定性 UUID。
func DeterministicID(key string) uuid.UUID {
	return uuid.NewSHA1(evalNamespace, []byte(key))
}

// Doc 是一篇待摄入的真实语料文档。
type Doc struct {
	Key      string // 稳定标识(相对路径),用于派生确定性文档 id
	Name     string // 文档名,评测数据集可用它做可读的 golden 标注(match_on: "name")
	Content  string
	Document *models.Document // 交给生产写入路径的文档元数据
}

// LoadOptions 控制语料装载时赋予文档的归属信息。
type LoadOptions struct {
	UserID uuid.UUID
	KBID   *uuid.UUID
	Root   string // 相对路径基准目录;为空则用传入路径原样作为 key
}

// LoadFiles 读取给定文件为语料文档。
//
// 每篇文档的 id 由相对 Root 的路径确定性派生,文档名取文件名(base),使评测数据集可用
// 可读文件名标注 golden。空文件会被跳过。
func LoadFiles(paths []string, opts LoadOptions) ([]Doc, error) {
	docs := make([]Doc, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read corpus file %s: %w", p, err)
		}
		content := strings.TrimSpace(string(raw))
		if content == "" {
			continue
		}
		key := p
		if opts.Root != "" {
			if rel, err := filepath.Rel(opts.Root, p); err == nil {
				key = rel
			}
		}
		name := filepath.Base(p)
		id := DeterministicID(key)
		docs = append(docs, Doc{
			Key:     key,
			Name:    name,
			Content: content,
			Document: &models.Document{
				ID:         id,
				UserID:     opts.UserID,
				KBID:       opts.KBID,
				FileName:   name,
				FileExt:    strings.TrimPrefix(filepath.Ext(p), "."),
				FileSize:   int64(len(raw)),
				SourceType: "file",
				Status:     "done",
			},
		})
	}
	return docs, nil
}
