// Package corpus 把真实文档语料灌入真实检索存储,供 RAG 评测使用。
//
// 语料 → 生产分块器(ragchunker)→ 嵌入 → 生产写入路径(ragchunk.Repository 双写
// Qdrant + Elasticsearch)。嵌入默认用本包的确定性 HashEmbedder(无需 API key、可复现),
// 也可注入真实嵌入模型客户端(corellm.Client 满足 ragsearch.Embedder)。
package corpus

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// HashEmbedder 是确定性的本地嵌入器:特征哈希 + TF 加权 + L2 归一化。
//
// 特征取 CJK 字符 bigram 与 ASCII 词/词 bigram —— 对中文技术文档是有效的词形信号。
// 不联网、完全可复现,故可在 CI 与本地重复得到同一组向量;语义深度不及真实嵌入模型,
// 但配合 Elasticsearch 的真实 BM25 通道,混合检索仍能产出有意义的排序。
//
// 需要评测真实模型的语义检索时,把真实 llm.Client 注入检索器/摄入器即可替换本实现。
type HashEmbedder struct {
	Dim int
}

// NewHashEmbedder 创建指定维度的确定性嵌入器;dim<=0 时用 512。
func NewHashEmbedder(dim int) *HashEmbedder {
	if dim <= 0 {
		dim = 512
	}
	return &HashEmbedder{Dim: dim}
}

// EmbedOne 实现 ragsearch.Embedder。dimensions>0 时以其为准,否则用构造维度。
func (e *HashEmbedder) EmbedOne(_ context.Context, text string, dimensions int) ([]float64, error) {
	dim := dimensions
	if dim <= 0 {
		dim = e.Dim
	}
	return e.embed(text, dim), nil
}

// Embed 批量嵌入,便于摄入阶段复用。
func (e *HashEmbedder) Embed(_ context.Context, texts []string, dimensions int) ([][]float64, error) {
	dim := dimensions
	if dim <= 0 {
		dim = e.Dim
	}
	out := make([][]float64, len(texts))
	for i, t := range texts {
		out[i] = e.embed(t, dim)
	}
	return out, nil
}

// embed 把文本特征哈希进 dim 维,按 1+log(tf) 加权后 L2 归一化。
func (e *HashEmbedder) embed(text string, dim int) []float64 {
	counts := map[uint32]float64{}
	for _, feat := range features(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(feat))
		counts[h.Sum32()%uint32(dim)]++
	}
	vec := make([]float64, dim)
	for idx, tf := range counts {
		vec[idx] = 1 + math.Log(tf)
	}
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	if norm == 0 {
		return vec
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

// features 抽取文本特征:CJK 逐字与相邻字 bigram,ASCII 词与相邻词 bigram。
func features(text string) []string {
	text = strings.ToLower(text)
	var feats []string
	var cjk []rune
	var word strings.Builder
	var words []string

	flushWord := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
		default:
			flushWord()
		}
	}
	flushWord()

	for i, r := range cjk {
		feats = append(feats, string(r))
		if i+1 < len(cjk) {
			feats = append(feats, string([]rune{r, cjk[i+1]}))
		}
	}
	for i, w := range words {
		feats = append(feats, w)
		if i+1 < len(words) {
			feats = append(feats, w+"_"+words[i+1])
		}
	}
	return feats
}
