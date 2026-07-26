package tasks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/boxify/api-go/internal/config"
	corellm "github.com/boxify/api-go/internal/core/llm"
	ragchunker "github.com/boxify/api-go/internal/core/rag/chunker"
	ragclassifier "github.com/boxify/api-go/internal/core/rag/classifier"
	ragparser "github.com/boxify/api-go/internal/core/rag/documentparse"
	"github.com/boxify/api-go/internal/core/rag/vectorstore"
	"github.com/boxify/api-go/internal/domain/types"
	"github.com/boxify/api-go/internal/infrastructure/db/memory"
	"github.com/boxify/api-go/internal/infrastructure/security"
	"github.com/boxify/api-go/internal/models"
	"github.com/boxify/api-go/internal/repository"
	"github.com/boxify/api-go/internal/repository/ragchunk"
	"github.com/boxify/api-go/internal/svc"
	"github.com/boxify/api-go/internal/xerr"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// recordingKeyword 包装 vectorstore.KeywordIndex，用于在测试中记录标签同步（SetFields）
// 调用顺序，并可注入错误以模拟关键词存储写入失败。
type recordingKeyword struct {
	vectorstore.KeywordIndex
	events       *[]string
	setFieldsErr error
}

func (r *recordingKeyword) SetFields(ctx context.Context, filter vectorstore.Filter, fields map[string]any) error {
	if r.events != nil {
		*r.events = append(*r.events, "es:update_tags")
	}
	if r.setFieldsErr != nil {
		return r.setFieldsErr
	}
	return r.KeywordIndex.SetFields(ctx, filter, fields)
}

// fetchChunks 按来源实体 ID 从内存存储取回全部已写入的 chunk 字段。
func fetchChunks(t *testing.T, mem *memory.Store, sourceID uuid.UUID) []vectorstore.Hit {
	t.Helper()
	hits, err := mem.Keyword().Fetch(context.Background(), vectorstore.Filter{
		Must: []vectorstore.Condition{vectorstore.Eq("source_id", sourceID.String())},
	}, 100)
	if err != nil {
		t.Fatalf("fetch chunks error = %v", err)
	}
	return hits
}

// chunkTagSlice 把命中字段中的 tags 解析成字符串切片。
func chunkTagSlice(fields map[string]any) []string {
	tags, _ := fields["tags"].([]string)
	return tags
}

type fakeDocumentRepository struct {
	rows   map[uuid.UUID]*models.Document
	events *[]string
}

func newFakeDocumentRepository(rows ...*models.Document) *fakeDocumentRepository {
	repo := &fakeDocumentRepository{rows: map[uuid.UUID]*models.Document{}}
	for _, row := range rows {
		repo.rows[row.ID] = row
	}
	return repo
}

func (r *fakeDocumentRepository) Create(ctx context.Context, userID uuid.UUID, document *models.Document) (*models.Document, error) {
	document.UserID = userID
	r.rows[document.ID] = document
	return document, nil
}

func (r *fakeDocumentRepository) List(ctx context.Context, userID uuid.UUID) ([]*models.Document, error) {
	return nil, nil
}

func (r *fakeDocumentRepository) PageList(ctx context.Context, userID uuid.UUID, query repository.DocumentListQuery) ([]*models.Document, int64, error) {
	return nil, 0, nil
}

func (r *fakeDocumentRepository) CountByKnowledgeBase(ctx context.Context, userID uuid.UUID, kbIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	return nil, nil
}

func (r *fakeDocumentRepository) FindByID(ctx context.Context, userID uuid.UUID, documentID uuid.UUID) (*models.Document, error) {
	row, ok := r.rows[documentID]
	if !ok || row.UserID != userID {
		return nil, xerr.NotFound("文档不存在")
	}
	return row, nil
}

func (r *fakeDocumentRepository) Update(ctx context.Context, userID uuid.UUID, document *models.Document) (*models.Document, error) {
	r.rows[document.ID] = document
	return document, nil
}

func (r *fakeDocumentRepository) UpdateFields(ctx context.Context, userID uuid.UUID, documentID uuid.UUID, document *models.Document, fields *repository.DocumentUpdateFields) (*models.Document, error) {
	row, err := r.FindByID(ctx, userID, documentID)
	if err != nil {
		return nil, err
	}
	for _, column := range fields.Columns() {
		switch column {
		case "status":
			row.Status = document.Status
			if r.events != nil {
				*r.events = append(*r.events, "status:"+document.Status)
			}
		case "progress":
			row.Progress = document.Progress
			if r.events != nil {
				*r.events = append(*r.events, fmt.Sprintf("progress:%.1f", document.Progress))
			}
		case "chunk_num":
			row.ChunkNum = document.ChunkNum
		case "error_msg":
			row.ErrorMsg = document.ErrorMsg
		}
	}
	return row, nil
}

func (r *fakeDocumentRepository) Delete(ctx context.Context, userID uuid.UUID, documentID uuid.UUID) error {
	delete(r.rows, documentID)
	return nil
}

type fakeTagRepository struct {
	syncErr          error
	syncedUserID     uuid.UUID
	syncedDocumentID uuid.UUID
	syncedNames      []string
	events           *[]string
}

func (r *fakeTagRepository) Create(ctx context.Context, userID uuid.UUID, tag *models.Tag) (*models.Tag, error) {
	tag.UserID = userID
	return tag, nil
}

func (r *fakeTagRepository) List(ctx context.Context, userID uuid.UUID) ([]*models.Tag, error) {
	return nil, nil
}

func (r *fakeTagRepository) ListByScope(ctx context.Context, userID uuid.UUID, scope string) ([]*models.Tag, error) {
	return nil, nil
}

func (r *fakeTagRepository) PageList(ctx context.Context, userID uuid.UUID, query repository.TagListQuery) ([]*models.Tag, int64, error) {
	return nil, 0, nil
}

func (r *fakeTagRepository) CountDocumentsByTags(ctx context.Context, userID uuid.UUID, tagIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	return map[uuid.UUID]int64{}, nil
}

func (r *fakeTagRepository) CountImagesByTags(ctx context.Context, userID uuid.UUID, tagIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	return map[uuid.UUID]int64{}, nil
}

func (r *fakeTagRepository) FindByID(ctx context.Context, userID uuid.UUID, tagID uuid.UUID) (*models.Tag, error) {
	return nil, xerr.NotFound("标签不存在")
}

func (r *fakeTagRepository) Update(ctx context.Context, userID uuid.UUID, tag *models.Tag) (*models.Tag, error) {
	return tag, nil
}

func (r *fakeTagRepository) UpdateFields(ctx context.Context, userID uuid.UUID, tagID uuid.UUID, tag *models.Tag, fields *repository.TagUpdateFields) (*models.Tag, error) {
	return tag, nil
}

func (r *fakeTagRepository) SyncDocumentTags(ctx context.Context, userID uuid.UUID, documentID uuid.UUID, names []string) ([]models.Tag, error) {
	if r.events != nil {
		*r.events = append(*r.events, "pg:sync_tags")
	}
	r.syncedUserID = userID
	r.syncedDocumentID = documentID
	r.syncedNames = append([]string(nil), names...)
	if r.syncErr != nil {
		return nil, r.syncErr
	}
	rows := make([]models.Tag, 0, len(names))
	for _, name := range names {
		rows = append(rows, models.Tag{ID: uuid.New(), UserID: userID, Name: name, Color: "#155EEF"})
	}
	return rows, nil
}

func (r *fakeTagRepository) SyncImageTags(ctx context.Context, userID uuid.UUID, imageID uuid.UUID, names []string) ([]models.Tag, error) {
	if r.events != nil {
		*r.events = append(*r.events, "pg:sync_image_tags")
	}
	r.syncedUserID = userID
	r.syncedDocumentID = imageID
	r.syncedNames = append([]string(nil), names...)
	if r.syncErr != nil {
		return nil, r.syncErr
	}
	rows := make([]models.Tag, 0, len(names))
	for _, name := range names {
		rows = append(rows, models.Tag{ID: uuid.New(), UserID: userID, Name: name, Color: "#155EEF"})
	}
	return rows, nil
}

func (r *fakeTagRepository) ListDocumentIDsByTag(ctx context.Context, userID uuid.UUID, tagID uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}

func (r *fakeTagRepository) ListDocumentTagNames(ctx context.Context, userID uuid.UUID, documentIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
	return map[uuid.UUID][]string{}, nil
}

func (r *fakeTagRepository) Merge(ctx context.Context, userID uuid.UUID, sourceID uuid.UUID, targetID uuid.UUID) (*models.Tag, error) {
	return &models.Tag{ID: targetID, UserID: userID}, nil
}

func (r *fakeTagRepository) Delete(ctx context.Context, userID uuid.UUID, tagID uuid.UUID) error {
	return nil
}

type memoryStore struct {
	data map[string][]byte
}

func newMemoryStore() *memoryStore {
	return &memoryStore{data: map[string][]byte{}}
}

func (s *memoryStore) Ping(ctx context.Context) error {
	return nil
}

func (s *memoryStore) Put(ctx context.Context, key string, data []byte) error {
	s.data[key] = append([]byte(nil), data...)
	return nil
}

func (s *memoryStore) Get(ctx context.Context, key string) ([]byte, error) {
	data, ok := s.data[key]
	if !ok {
		return nil, xerr.NotFound("文件不存在")
	}
	return append([]byte(nil), data...), nil
}

func (s *memoryStore) Delete(ctx context.Context, key string) error {
	delete(s.data, key)
	return nil
}

type fakeLLMFactory struct {
	client  corellm.Client
	configs *[]corellm.ModelConfig
}

func (f fakeLLMFactory) NewClient(cfg corellm.ModelConfig) (corellm.Client, error) {
	if f.configs != nil {
		*f.configs = append(*f.configs, cfg)
	}
	return f.client, nil
}

type fakeLLMClient struct {
	err          error
	invokeAnswer string
	invokeErr    error
	embedOptions *[]corellm.EmbeddingOptions
}

func (c fakeLLMClient) Invoke(ctx context.Context, messages []*corellm.Message, opts ...corellm.ModelCallOption) (string, error) {
	if c.invokeErr != nil {
		return "", c.invokeErr
	}
	return c.invokeAnswer, nil
}

func (c fakeLLMClient) InvokeResult(ctx context.Context, messages []*corellm.Message, opts ...corellm.ModelCallOption) (*corellm.LLMResult, error) {
	text, err := c.Invoke(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return &corellm.LLMResult{Text: text}, nil
}

func (c fakeLLMClient) Stream(ctx context.Context, messages []*corellm.Message, opts ...corellm.ModelCallOption) (<-chan string, error) {
	ch := make(chan string)
	close(ch)
	return ch, nil
}

func (c fakeLLMClient) Embed(ctx context.Context, texts []string, dimensions int, opts ...corellm.EmbeddingOption) ([][]float64, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.embedOptions != nil {
		*c.embedOptions = append(*c.embedOptions, corellm.NewEmbeddingOptions(opts...))
	}
	out := make([][]float64, 0, len(texts))
	for range texts {
		out = append(out, []float64{0.1, 0.2, 0.3})
	}
	return out, nil
}

func (c fakeLLMClient) EmbedOne(ctx context.Context, text string, dimensions int) ([]float64, error) {
	if c.err != nil {
		return nil, c.err
	}
	return []float64{0.1, 0.2, 0.3}, nil
}

func newFakeLLMManager(client corellm.Client, configs ...*[]corellm.ModelConfig) *corellm.Manager {
	manager := corellm.NewManager()
	var out *[]corellm.ModelConfig
	if len(configs) != 0 {
		out = configs[0]
	}
	manager.Register("fake", fakeLLMFactory{client: client, configs: out})
	return manager
}

type fakeModelConfigRepository struct {
	rows []*models.ModelConfig
}

func (r *fakeModelConfigRepository) Create(ctx context.Context, row *models.ModelConfig) (*models.ModelConfig, error) {
	return row, nil
}

func (r *fakeModelConfigRepository) Update(ctx context.Context, row *models.ModelConfig) (*models.ModelConfig, error) {
	return row, nil
}

func (r *fakeModelConfigRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (r *fakeModelConfigRepository) List(ctx context.Context, userID uuid.UUID, modelType *types.ModelType) ([]*models.ModelConfig, error) {
	out := make([]*models.ModelConfig, 0, len(r.rows))
	for _, row := range r.rows {
		if row.UserID == userID && (modelType == nil || row.Type == string(*modelType)) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *fakeModelConfigRepository) FindByID(ctx context.Context, userID uuid.UUID, configID uuid.UUID) (*models.ModelConfig, error) {
	return nil, xerr.NotFound("模型配置不存在")
}

func TestHandleParseDocumentProcessesTextDocument(t *testing.T) {
	// 验证 parse:document handler 使用 svc 注入的 parser/chunker，按阶段更新进度，并在完成前写入分类标签。
	ctx := context.Background()
	userID := uuid.New()
	documentID := uuid.New()
	row := &models.Document{ID: documentID, UserID: userID, FileName: "a.txt", FileExt: ".txt", FileKey: "docs/a.txt", Status: "pending", Tags: []models.Tag{{Name: "手动"}}}
	store := newMemoryStore()
	store.data[row.FileKey] = []byte("hello async queue")
	var events []string
	mem := memory.New()
	cipher, err := security.NewSecretCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewSecretCipher error = %v", err)
	}
	encryptedAPIKey, err := cipher.Encrypt("db-key")
	if err != nil {
		t.Fatalf("Encrypt API key error = %v", err)
	}
	var llmConfigs []corellm.ModelConfig
	var embeddingOptions []corellm.EmbeddingOptions
	docRepo := newFakeDocumentRepository(row)
	docRepo.events = &events
	tagRepo := &fakeTagRepository{events: &events}
	handler := NewParseDocumentTask(&svc.ServiceContext{
		Config:            config.Config{Rag: config.RagConfig{EmbeddingDim: 3, EmbeddingBatchSize: 4, ChunkIndex: "boxify_chunks"}},
		DocumentRepo:      docRepo,
		TagRepo:           tagRepo,
		ModelConfigRepo:   &fakeModelConfigRepository{rows: []*models.ModelConfig{{UserID: userID, Type: string(types.EmbeddingModelType), Provider: "fake", ModelName: "db-embed", APIKeyEncrypted: encryptedAPIKey, BaseURL: "https://llm.example", IsDefault: true}, {UserID: userID, Type: string(types.ChatModelType), Provider: "fake", ModelName: "db-chat", APIKeyEncrypted: encryptedAPIKey, BaseURL: "https://llm.example", IsDefault: true}}},
		SecretCipher:      cipher,
		Storage:           store,
		RAGChunkRepo:      ragchunk.NewRepository(mem.Dense(), &recordingKeyword{KeywordIndex: mem.Keyword(), events: &events}),
		RAGClassifier:     ragclassifier.NewClassifier(),
		RAGDocumentParser: ragparser.NewParser(),
		RAGChunker:        ragchunker.NewChunker(ragchunker.WithParentChunkTokens(1200)),
		LLMManager:        newFakeLLMManager(fakeLLMClient{invokeAnswer: `["自动","手动"]`, embedOptions: &embeddingOptions}, &llmConfigs),
	})
	task, err := types.NewParseDocumentTask(userID, documentID)
	if err != nil {
		t.Fatalf("NewParseDocumentTask error = %v", err)
	}

	if err := handler.Handle(ctx, task); err != nil {
		t.Fatalf("HandleParseDocument error = %v", err)
	}
	if row.Status != "done" || row.Progress != 1 || row.ChunkNum != 1 || row.ErrorMsg != nil {
		t.Fatalf("document after parse = %+v, want done/progress/chunk/error cleared", row)
	}
	wantEvents := strings.Join([]string{"status:parsing", "progress:0.1", "progress:0.3", "progress:0.8", "pg:sync_tags", "es:update_tags", "status:done", "progress:1.0"}, "|")
	if got := strings.Join(events, "|"); got != wantEvents {
		t.Fatalf("events = %s, want %s", got, wantEvents)
	}
	if tagRepo.syncedUserID != userID || tagRepo.syncedDocumentID != documentID || !slices.Equal(tagRepo.syncedNames, []string{"手动", "自动"}) {
		t.Fatalf("synced tags user=%s document=%s names=%v, want merged manual and classified tags", tagRepo.syncedUserID, tagRepo.syncedDocumentID, tagRepo.syncedNames)
	}
	chunks := fetchChunks(t, mem, documentID)
	if len(chunks) == 0 {
		t.Fatal("indexed chunks = 0, want at least one chunk written")
	}
	if len(llmConfigs) < 2 || llmConfigs[0].Provider != "fake" || llmConfigs[0].Model != "db-embed" || llmConfigs[0].EmbeddingModel != "db-embed" || llmConfigs[0].APIKey != "db-key" || llmConfigs[0].BaseURL != "https://llm.example" || llmConfigs[1].Model != "db-chat" {
		t.Fatalf("LLM config = %#v, want database embedding model config", llmConfigs)
	}
	if len(embeddingOptions) != 1 || embeddingOptions[0].BatchSize != 4 {
		t.Fatalf("embedding options = %#v, want batch size 4 from rag config", embeddingOptions)
	}
	for _, hit := range chunks {
		if hit.Fields["source_id"] != documentID.String() || hit.Fields["user_id"] != userID.String() || hit.Fields["content"] == "" {
			t.Fatalf("indexed chunk fields = %#v, want source/user/content", hit.Fields)
		}
		tags := chunkTagSlice(hit.Fields)
		if !slices.Contains(tags, "手动") || !slices.Contains(tags, "自动") {
			t.Fatalf("chunk tags = %#v, want merged manual and classified tags", hit.Fields["tags"])
		}
		manualCount := 0
		for _, tag := range tags {
			if tag == "手动" {
				manualCount++
			}
		}
		if manualCount != 1 {
			t.Fatalf("chunk tags = %#v, want unique 手动", hit.Fields["tags"])
		}
	}
	// 稠密检索命中即证明向量已写入共享内存存储。
	denseHits, err := mem.Dense().Search(ctx, []float64{0.1, 0.2, 0.3}, 100, vectorstore.Filter{
		Must: []vectorstore.Condition{vectorstore.Eq("source_id", documentID.String())},
	})
	if err != nil {
		t.Fatalf("dense search error = %v", err)
	}
	if len(denseHits) == 0 {
		t.Fatal("dense hits = 0, want vectors written to store")
	}
}

func TestHandleParseDocumentMarksFailedWhenEmbeddingConfigMissing(t *testing.T) {
	// 验证解析任务在用户未配置向量模型时标记文档 failed，不写入 ES chunk。
	ctx := context.Background()
	userID := uuid.New()
	documentID := uuid.New()
	row := &models.Document{ID: documentID, UserID: userID, FileName: "a.txt", FileExt: ".txt", FileKey: "docs/a.txt", Status: "pending"}
	store := newMemoryStore()
	store.data[row.FileKey] = []byte("hello world")
	mem := memory.New()
	cipher, err := security.NewSecretCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewSecretCipher error = %v", err)
	}
	handler := NewParseDocumentTask(&svc.ServiceContext{
		Config:            config.Config{Rag: config.RagConfig{EmbeddingDim: 3, ChunkIndex: "boxify_chunks"}},
		DocumentRepo:      newFakeDocumentRepository(row),
		TagRepo:           &fakeTagRepository{},
		ModelConfigRepo:   &fakeModelConfigRepository{},
		SecretCipher:      cipher,
		Storage:           store,
		RAGChunkRepo:      ragchunk.NewRepository(mem.Dense(), mem.Keyword()),
		RAGDocumentParser: ragparser.NewParser(),
		RAGChunker:        ragchunker.NewChunker(ragchunker.WithParentChunkTokens(1200)),
		LLMManager:        newFakeLLMManager(fakeLLMClient{}),
	})
	task, err := types.NewParseDocumentTask(userID, documentID)
	if err != nil {
		t.Fatalf("NewParseDocumentTask error = %v", err)
	}

	if err := handler.Handle(ctx, task); err != nil {
		t.Fatalf("HandleParseDocument error = %v", err)
	}
	if row.Status != "failed" || row.ErrorMsg == nil || !strings.Contains(*row.ErrorMsg, "未配置向量模型") {
		t.Fatalf("document after missing embedding config = %+v, want failed missing config", row)
	}
	if hits := fetchChunks(t, mem, documentID); len(hits) != 0 {
		t.Fatalf("indexed chunks = %d, want none when embedding config missing", len(hits))
	}
}

func TestHandleParseDocumentCompletesWhenChatConfigMissing(t *testing.T) {
	// 验证用户未配置 chat 模型时分类不阻断解析，并将已有文档标签同步到 ES。
	ctx := context.Background()
	userID := uuid.New()
	documentID := uuid.New()
	row := &models.Document{ID: documentID, UserID: userID, FileName: "a.txt", FileExt: ".txt", FileKey: "docs/a.txt", Status: "pending", Tags: []models.Tag{{Name: "手动"}}}
	store := newMemoryStore()
	store.data[row.FileKey] = []byte("hello world")
	mem := memory.New()
	cipher, err := security.NewSecretCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewSecretCipher error = %v", err)
	}
	encryptedAPIKey, err := cipher.Encrypt("db-key")
	if err != nil {
		t.Fatalf("Encrypt API key error = %v", err)
	}
	tagRepo := &fakeTagRepository{}
	handler := NewParseDocumentTask(&svc.ServiceContext{
		Config:            config.Config{Rag: config.RagConfig{EmbeddingDim: 3, ChunkIndex: "boxify_chunks"}},
		DocumentRepo:      newFakeDocumentRepository(row),
		TagRepo:           tagRepo,
		ModelConfigRepo:   &fakeModelConfigRepository{rows: []*models.ModelConfig{{UserID: userID, Type: string(types.EmbeddingModelType), Provider: "fake", ModelName: "db-embed", APIKeyEncrypted: encryptedAPIKey, BaseURL: "https://llm.example", IsDefault: true}}},
		SecretCipher:      cipher,
		Storage:           store,
		RAGChunkRepo:      ragchunk.NewRepository(mem.Dense(), mem.Keyword()),
		RAGClassifier:     ragclassifier.NewClassifier(),
		RAGDocumentParser: ragparser.NewParser(),
		RAGChunker:        ragchunker.NewChunker(ragchunker.WithParentChunkTokens(1200)),
		LLMManager:        newFakeLLMManager(fakeLLMClient{}),
	})
	task, err := types.NewParseDocumentTask(userID, documentID)
	if err != nil {
		t.Fatalf("NewParseDocumentTask error = %v", err)
	}

	if err := handler.Handle(ctx, task); err != nil {
		t.Fatalf("HandleParseDocument error = %v", err)
	}
	if row.Status != "done" || row.Progress != 1 || row.ErrorMsg != nil {
		t.Fatalf("document after missing chat config = %+v, want done", row)
	}
	chunks := fetchChunks(t, mem, documentID)
	if len(chunks) == 0 {
		t.Fatal("indexed chunks = 0, want chunks written")
	}
	for _, hit := range chunks {
		if !slices.Contains(chunkTagSlice(hit.Fields), "手动") {
			t.Fatalf("chunk tags = %#v, want existing document tag", hit.Fields["tags"])
		}
	}
	if !slices.Equal(tagRepo.syncedNames, []string{"手动"}) {
		t.Fatalf("synced tags = %v, want existing document tag", tagRepo.syncedNames)
	}
}

func TestHandleParseDocumentMarksFailedWhenUpdateTagsFails(t *testing.T) {
	// 验证分类标签写入 ES 失败时文档不会进入 done，而是标记 failed。
	ctx := context.Background()
	userID := uuid.New()
	documentID := uuid.New()
	row := &models.Document{ID: documentID, UserID: userID, FileName: "a.txt", FileExt: ".txt", FileKey: "docs/a.txt", Status: "pending"}
	store := newMemoryStore()
	store.data[row.FileKey] = []byte("hello world")
	mem := memory.New()
	cipher, err := security.NewSecretCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewSecretCipher error = %v", err)
	}
	encryptedAPIKey, err := cipher.Encrypt("db-key")
	if err != nil {
		t.Fatalf("Encrypt API key error = %v", err)
	}
	keyword := &recordingKeyword{KeywordIndex: mem.Keyword(), setFieldsErr: errors.New("更新 chunk 标签失败")}
	handler := NewParseDocumentTask(&svc.ServiceContext{
		Config:            config.Config{Rag: config.RagConfig{EmbeddingDim: 3, ChunkIndex: "boxify_chunks"}},
		DocumentRepo:      newFakeDocumentRepository(row),
		TagRepo:           &fakeTagRepository{},
		ModelConfigRepo:   &fakeModelConfigRepository{rows: []*models.ModelConfig{{UserID: userID, Type: string(types.EmbeddingModelType), Provider: "fake", ModelName: "db-embed", APIKeyEncrypted: encryptedAPIKey, BaseURL: "https://llm.example", IsDefault: true}, {UserID: userID, Type: string(types.ChatModelType), Provider: "fake", ModelName: "db-chat", APIKeyEncrypted: encryptedAPIKey, BaseURL: "https://llm.example", IsDefault: true}}},
		SecretCipher:      cipher,
		Storage:           store,
		RAGChunkRepo:      ragchunk.NewRepository(mem.Dense(), keyword),
		RAGClassifier:     ragclassifier.NewClassifier(),
		RAGDocumentParser: ragparser.NewParser(),
		RAGChunker:        ragchunker.NewChunker(ragchunker.WithParentChunkTokens(1200)),
		LLMManager:        newFakeLLMManager(fakeLLMClient{invokeAnswer: `["自动"]`}),
	})
	task, err := types.NewParseDocumentTask(userID, documentID)
	if err != nil {
		t.Fatalf("NewParseDocumentTask error = %v", err)
	}

	if err := handler.Handle(ctx, task); err != nil {
		t.Fatalf("HandleParseDocument error = %v", err)
	}
	if row.Status != "failed" || row.Progress != 0.8 || row.ErrorMsg == nil || !strings.Contains(*row.ErrorMsg, "更新 chunk 标签失败") {
		t.Fatalf("document after update tags failure = %+v, want failed progress=0.8 chunk tag update error", row)
	}
}

func TestHandleParseDocumentMarksFailedWhenSyncDocumentTagsFails(t *testing.T) {
	// 验证分类标签同步到 PostgreSQL 失败时文档标记 failed，并且不会继续更新 ES 标签。
	ctx := context.Background()
	userID := uuid.New()
	documentID := uuid.New()
	row := &models.Document{ID: documentID, UserID: userID, FileName: "a.txt", FileExt: ".txt", FileKey: "docs/a.txt", Status: "pending"}
	store := newMemoryStore()
	store.data[row.FileKey] = []byte("hello world")
	mem := memory.New()
	var events []string
	cipher, err := security.NewSecretCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewSecretCipher error = %v", err)
	}
	encryptedAPIKey, err := cipher.Encrypt("db-key")
	if err != nil {
		t.Fatalf("Encrypt API key error = %v", err)
	}
	keyword := &recordingKeyword{KeywordIndex: mem.Keyword(), events: &events}
	handler := NewParseDocumentTask(&svc.ServiceContext{
		Config:            config.Config{Rag: config.RagConfig{EmbeddingDim: 3, ChunkIndex: "boxify_chunks"}},
		DocumentRepo:      newFakeDocumentRepository(row),
		TagRepo:           &fakeTagRepository{syncErr: errors.New("pg sync failed")},
		ModelConfigRepo:   &fakeModelConfigRepository{rows: []*models.ModelConfig{{UserID: userID, Type: string(types.EmbeddingModelType), Provider: "fake", ModelName: "db-embed", APIKeyEncrypted: encryptedAPIKey, BaseURL: "https://llm.example", IsDefault: true}, {UserID: userID, Type: string(types.ChatModelType), Provider: "fake", ModelName: "db-chat", APIKeyEncrypted: encryptedAPIKey, BaseURL: "https://llm.example", IsDefault: true}}},
		SecretCipher:      cipher,
		Storage:           store,
		RAGChunkRepo:      ragchunk.NewRepository(mem.Dense(), keyword),
		RAGClassifier:     ragclassifier.NewClassifier(),
		RAGDocumentParser: ragparser.NewParser(),
		RAGChunker:        ragchunker.NewChunker(ragchunker.WithParentChunkTokens(1200)),
		LLMManager:        newFakeLLMManager(fakeLLMClient{invokeAnswer: `["自动"]`}),
	})
	task, err := types.NewParseDocumentTask(userID, documentID)
	if err != nil {
		t.Fatalf("NewParseDocumentTask error = %v", err)
	}

	if err := handler.Handle(ctx, task); err != nil {
		t.Fatalf("HandleParseDocument error = %v", err)
	}
	if row.Status != "failed" || row.Progress != 0.8 || row.ErrorMsg == nil || !strings.Contains(*row.ErrorMsg, "pg sync failed") {
		t.Fatalf("document after PG sync failure = %+v, want failed progress=0.8 PG error", row)
	}
	if slices.Contains(events, "es:update_tags") {
		t.Fatalf("events = %v, want no chunk tag update after PG sync failure", events)
	}
}

func TestHandleParseDocumentMarksFailedWhenEmbeddingAPIKeyDecryptFails(t *testing.T) {
	// 验证解析任务在向量模型 API Key 解密失败时标记文档 failed。
	ctx := context.Background()
	userID := uuid.New()
	documentID := uuid.New()
	row := &models.Document{ID: documentID, UserID: userID, FileName: "a.txt", FileExt: ".txt", FileKey: "docs/a.txt", Status: "pending"}
	store := newMemoryStore()
	store.data[row.FileKey] = []byte("hello world")
	mem := memory.New()
	cipher, err := security.NewSecretCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewSecretCipher error = %v", err)
	}
	handler := NewParseDocumentTask(&svc.ServiceContext{
		Config:       config.Config{Rag: config.RagConfig{EmbeddingDim: 3, ChunkIndex: "boxify_chunks"}},
		DocumentRepo: newFakeDocumentRepository(row),
		TagRepo:      &fakeTagRepository{},
		ModelConfigRepo: &fakeModelConfigRepository{rows: []*models.ModelConfig{{
			UserID: userID, Type: string(types.EmbeddingModelType), Provider: "fake", ModelName: "db-embed", APIKeyEncrypted: "not-encrypted", IsDefault: true,
		}}},
		SecretCipher:      cipher,
		Storage:           store,
		RAGChunkRepo:      ragchunk.NewRepository(mem.Dense(), mem.Keyword()),
		RAGDocumentParser: ragparser.NewParser(),
		RAGChunker:        ragchunker.NewChunker(ragchunker.WithParentChunkTokens(1200)),
		LLMManager:        newFakeLLMManager(fakeLLMClient{}),
	})
	task, err := types.NewParseDocumentTask(userID, documentID)
	if err != nil {
		t.Fatalf("NewParseDocumentTask error = %v", err)
	}

	if err := handler.Handle(ctx, task); err != nil {
		t.Fatalf("HandleParseDocument error = %v", err)
	}
	if row.Status != "failed" || row.ErrorMsg == nil || !strings.Contains(*row.ErrorMsg, "模型 API Key 解密失败") {
		t.Fatalf("document after decrypt failure = %+v, want failed decrypt error", row)
	}
}

func TestHandleParseDocumentMarksUnsupportedDocumentFailed(t *testing.T) {
	// 验证无法解析的 PDF 会写入 failed 和明确错误。
	ctx := context.Background()
	userID := uuid.New()
	documentID := uuid.New()
	row := &models.Document{ID: documentID, UserID: userID, FileName: "a.pdf", FileExt: ".pdf", FileKey: "docs/a.pdf", Status: "pending"}
	store := newMemoryStore()
	store.data[row.FileKey] = []byte("%PDF")
	handler := NewParseDocumentTask(&svc.ServiceContext{DocumentRepo: newFakeDocumentRepository(row), Storage: store, RAGDocumentParser: ragparser.NewParser(), RAGChunker: ragchunker.NewChunker(), LLMManager: newFakeLLMManager(fakeLLMClient{})})
	task, err := types.NewParseDocumentTask(userID, documentID)
	if err != nil {
		t.Fatalf("NewParseDocumentTask error = %v", err)
	}

	if err := handler.Handle(ctx, task); err != nil {
		t.Fatalf("HandleParseDocument error = %v", err)
	}
	if row.Status != "failed" || row.ErrorMsg == nil {
		t.Fatalf("document after invalid pdf parse = %+v, want failed parser message", row)
	}
}

func TestHandleParseDocumentSkipsRetryWhenDocumentMissing(t *testing.T) {
	// 验证任务中的文档已被删除时返回 SkipRetry，避免无意义重试。
	ctx := context.Background()
	userID := uuid.New()
	task, err := types.NewParseDocumentTask(userID, uuid.New())
	if err != nil {
		t.Fatalf("NewParseDocumentTask error = %v", err)
	}
	handler := NewParseDocumentTask(&svc.ServiceContext{DocumentRepo: newFakeDocumentRepository(), Storage: newMemoryStore()})

	if err := handler.Handle(ctx, task); !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("HandleParseDocument missing error = %v, want SkipRetry", err)
	}
}
