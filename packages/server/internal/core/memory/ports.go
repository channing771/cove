package memory

import "context"

// Config 承载 memory 模块的调参项。
//
// 由调用方从应用配置映射而来，使 core/memory 只依赖本包定义的抽象，
// 不直接依赖 internal/config，与其他 core 包（如 rag/search）保持一致的分层。
type Config struct {
	// EmbeddingDim 实体名向量化维度。
	EmbeddingDim int
	// NameSimGate 进入 LLM 判定的名称相似度门槛。
	NameSimGate float64
	// LLMMergeConfidence 认定两实体相同所需的最低置信度。
	LLMMergeConfidence float64
	// CommunityClusteringMaxIterations 标签传播最大迭代次数。
	CommunityClusteringMaxIterations int
	// CommunityVoteSemWeight 社区投票中语义相似度权重。
	CommunityVoteSemWeight float64
	// CommunityVoteRelWeight 社区投票中关系连接权重。
	CommunityVoteRelWeight float64
	// CommunityMergeThreshold 合并相似社区的 cosine 阈值。
	CommunityMergeThreshold float64
}

// GraphStore 是记忆图谱抽取所需的图存储端口。
//
// 具体实现（如 Neo4j 仓库）在 infrastructure/repository 层提供；core/memory 只依赖本接口。
type GraphStore interface {
	// ListEntitiesByType 返回用户下指定类型的已有实体，用于与图谱二层融合。
	ListEntitiesByType(ctx context.Context, userId string, entityType string) ([]*EntityNode, error)
	// SaveGraph 单事务原子写入一次萃取产出的全部节点与边。
	SaveGraph(
		ctx context.Context,
		dialogues []*DialogueNode,
		chunks []*ChunkNode,
		statements []*StatementNode,
		entities []*EntityNode,
		events []*EventNode,
		mentions []*MentionEdge,
		relations []*RelationEdge,
		involves []*InvolvesEdge,
	) error
}

// CommunityStore 是社区聚类所需的社区存储端口。
type CommunityStore interface {
	HasCommunities(ctx context.Context, userId string) (bool, error)
	ListEntityEmbedding(ctx context.Context, userId string) ([]*EntityEmbedding, error)
	ListNeighborsForVote(ctx context.Context, userId string, entityIds []string) (map[string][]*EntityNeighborForVote, error)
	UpsertCommunities(ctx context.Context, userId string, communityIds []string) error
	AssignEntityToCommunity(ctx context.Context, userId string, entityIds []string, communityIds []string) error
	RefreshCommunityMemberCount(ctx context.Context, userId string, communityIds []string) ([]int, error)
	GetCommunityMembers(ctx context.Context, userId string, communityIds []string) ([][]*CommunityMember, error)
	PruneEmptyCommunity(ctx context.Context, userId string) error
	UpdateCommunityMetadata(ctx context.Context, userId string, updateItems []*UpdateCommunityMetaItem) error
}
