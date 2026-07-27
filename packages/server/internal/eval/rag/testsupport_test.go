package rag

import "context"

// fakeRetriever 返回预置命中,记录最后一次入参,供 hermetic 测试使用。
type fakeRetriever struct {
	hits      []RetrievedHit
	err       error
	lastQuery string
	lastK     int
}

func (f *fakeRetriever) Retrieve(ctx context.Context, query string, topK int) ([]RetrievedHit, error) {
	f.lastQuery = query
	f.lastK = topK
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}

// docHits 按给定文档顺序构造每文档一条 chunk 的命中列表(doc 级评测用)。
func docHits(docs ...string) []RetrievedHit {
	out := make([]RetrievedHit, len(docs))
	for i, d := range docs {
		out[i] = RetrievedHit{ChunkID: d + "-c", DocID: d, DocName: d, Content: "content of " + d}
	}
	return out
}
