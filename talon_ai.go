/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */
// Talon Go SDK — AI-Native + Backup 方法。

package talon

import "encoding/json"

// ── AI: Session ──

// AiCreateSession 创建 AI 会话。
func (db *DB) AiCreateSession(id string, metadata map[string]string, ttl *uint64) error {
	p := map[string]interface{}{"id": id}
	if metadata != nil {
		p["metadata"] = metadata
	}
	if ttl != nil {
		p["ttl"] = *ttl
	}
	_, err := db.execute("ai", "create_session", p)
	return err
}

// AiGetSession 获取会话信息，返回原始 JSON。
func (db *DB) AiGetSession(id string) (json.RawMessage, error) {
	return db.execute("ai", "get_session", map[string]interface{}{"id": id})
}

// AiCreateSessionIfNotExists 幂等创建会话。
func (db *DB) AiCreateSessionIfNotExists(id string, metadata map[string]string, ttl *uint64) (json.RawMessage, error) {
	p := map[string]interface{}{"id": id}
	if metadata != nil {
		p["metadata"] = metadata
	}
	if ttl != nil {
		p["ttl"] = *ttl
	}
	return db.execute("ai", "create_session_if_not_exists", p)
}

// AiListSessions 列出所有会话，返回原始 JSON。
func (db *DB) AiListSessions() (json.RawMessage, error) {
	return db.execute("ai", "list_sessions", nil)
}

// AiDeleteSession 删除会话。
func (db *DB) AiDeleteSession(id string) error {
	_, err := db.execute("ai", "delete_session", map[string]interface{}{"id": id})
	return err
}

// AiUpdateSession 更新会话元数据。
func (db *DB) AiUpdateSession(id string, metadata map[string]string) error {
	_, err := db.execute("ai", "update_session", map[string]interface{}{
		"id": id, "metadata": metadata,
	})
	return err
}

// AiCleanupExpiredSessions 清理过期 Session，返回清理数量。
func (db *DB) AiCleanupExpiredSessions() (uint64, error) {
	data, err := db.execute("ai", "cleanup_expired_sessions", nil)
	if err != nil {
		return 0, err
	}
	var out struct {
		Cleaned uint64 `json:"cleaned"`
	}
	return out.Cleaned, json.Unmarshal(data, &out)
}

// AiArchiveSession 归档会话。
func (db *DB) AiArchiveSession(id string) error {
	_, err := db.execute("ai", "archive_session", map[string]interface{}{"id": id})
	return err
}

// AiUnarchiveSession 取消归档会话。
func (db *DB) AiUnarchiveSession(id string) error {
	_, err := db.execute("ai", "unarchive_session", map[string]interface{}{"id": id})
	return err
}

// AiExportSession 导出会话（含上下文和 trace）。
func (db *DB) AiExportSession(id string) (json.RawMessage, error) {
	return db.execute("ai", "export_session", map[string]interface{}{"id": id})
}

// AiSessionStats 获取会话统计信息。
func (db *DB) AiSessionStats(id string) (json.RawMessage, error) {
	return db.execute("ai", "session_stats", map[string]interface{}{"id": id})
}

// AiAddSessionTag 为会话添加标签。
func (db *DB) AiAddSessionTag(id, tag string) error {
	_, err := db.execute("ai", "add_session_tag", map[string]interface{}{"id": id, "tag": tag})
	return err
}

// AiRemoveSessionTag 移除会话标签。
func (db *DB) AiRemoveSessionTag(id, tag string) error {
	_, err := db.execute("ai", "remove_session_tag", map[string]interface{}{"id": id, "tag": tag})
	return err
}

// AiListSessionsByTag 按标签查询会话。
func (db *DB) AiListSessionsByTag(tag string) (json.RawMessage, error) {
	return db.execute("ai", "list_sessions_by_tag", map[string]interface{}{"tag": tag})
}

// ── AI: Context ──

// AiClearContext 清空会话上下文，返回清理数量。
func (db *DB) AiClearContext(sessionID string) (uint64, error) {
	data, err := db.execute("ai", "clear_context", map[string]interface{}{
		"session_id": sessionID,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		Purged uint64 `json:"purged"`
	}
	return out.Purged, json.Unmarshal(data, &out)
}

// AiAppendMessage 追加消息到会话上下文。
func (db *DB) AiAppendMessage(sessionID string, message map[string]interface{}) error {
	_, err := db.execute("ai", "append_message", map[string]interface{}{
		"session_id": sessionID, "message": message,
	})
	return err
}

// AiGetHistory 获取会话历史，返回原始 JSON。
func (db *DB) AiGetHistory(sessionID string, limit *int) (json.RawMessage, error) {
	p := map[string]interface{}{"session_id": sessionID}
	if limit != nil {
		p["limit"] = *limit
	}
	return db.execute("ai", "get_history", p)
}

// AiGetContextWindow 获取 token 限制内的上下文窗口。
func (db *DB) AiGetContextWindow(sessionID string, maxTokens int) (json.RawMessage, error) {
	return db.execute("ai", "get_context_window", map[string]interface{}{
		"session_id": sessionID, "max_tokens": maxTokens,
	})
}

// AiGetContextWindowWithPrompt 获取上下文窗口（含 system_prompt + summary）。
func (db *DB) AiGetContextWindowWithPrompt(sessionID string, maxTokens int) (json.RawMessage, error) {
	return db.execute("ai", "get_context_window_with_prompt", map[string]interface{}{
		"session_id": sessionID, "max_tokens": maxTokens,
	})
}

// AiGetRecentMessages 获取最近 n 条消息。
func (db *DB) AiGetRecentMessages(sessionID string, n int) (json.RawMessage, error) {
	return db.execute("ai", "get_recent_messages", map[string]interface{}{
		"session_id": sessionID, "n": n,
	})
}

// AiSetSystemPrompt 设置会话 System Prompt。
func (db *DB) AiSetSystemPrompt(sessionID, prompt string, tokenCount int) error {
	_, err := db.execute("ai", "set_system_prompt", map[string]interface{}{
		"session_id": sessionID, "prompt": prompt, "token_count": tokenCount,
	})
	return err
}

// AiSetContextSummary 设置上下文摘要。
func (db *DB) AiSetContextSummary(sessionID, summary string, tokenCount int) error {
	_, err := db.execute("ai", "set_context_summary", map[string]interface{}{
		"session_id": sessionID, "summary": summary, "token_count": tokenCount,
	})
	return err
}

// AiGetContextSummary 获取上下文摘要。
func (db *DB) AiGetContextSummary(sessionID string) (json.RawMessage, error) {
	return db.execute("ai", "get_context_summary", map[string]interface{}{"session_id": sessionID})
}

// AiAutoSummarize 自动生成上下文摘要。
func (db *DB) AiAutoSummarize(sessionID string, maxTokens *int) (json.RawMessage, error) {
	p := map[string]interface{}{"session_id": sessionID}
	if maxTokens != nil {
		p["max_tokens"] = *maxTokens
	}
	return db.execute("ai", "auto_summarize", p)
}

// ── AI: Memory ──

// AiStoreMemory 存储记忆。
func (db *DB) AiStoreMemory(entry map[string]interface{}, embedding []float32) error {
	_, err := db.execute("ai", "store_memory", map[string]interface{}{
		"entry": entry, "embedding": embedding,
	})
	return err
}

// AiSearchMemory 向量搜索记忆。
func (db *DB) AiSearchMemory(embedding []float32, k int) (json.RawMessage, error) {
	return db.execute("ai", "search_memory", map[string]interface{}{
		"embedding": embedding, "k": k,
	})
}

// AiDeleteMemory 删除记忆。
func (db *DB) AiDeleteMemory(id uint64) error {
	_, err := db.execute("ai", "delete_memory", map[string]interface{}{"id": id})
	return err
}

// AiMemoryCount 记忆总数。
func (db *DB) AiMemoryCount() (uint64, error) {
	data, err := db.execute("ai", "memory_count", nil)
	if err != nil {
		return 0, err
	}
	var out struct {
		Count uint64 `json:"count"`
	}
	return out.Count, json.Unmarshal(data, &out)
}

// AiUpdateMemory 更新记忆。
func (db *DB) AiUpdateMemory(id uint64, content *string, metadata map[string]string) error {
	p := map[string]interface{}{"id": id}
	if content != nil {
		p["content"] = *content
	}
	if metadata != nil {
		p["metadata"] = metadata
	}
	_, err := db.execute("ai", "update_memory", p)
	return err
}

// AiStoreMemoriesBatch 批量存储记忆。
func (db *DB) AiStoreMemoriesBatch(entries []map[string]interface{}, embeddings [][]float32) error {
	_, err := db.execute("ai", "store_memories_batch", map[string]interface{}{
		"entries": entries, "embeddings": embeddings,
	})
	return err
}

// AiDeduplicateMemories 检测重复记忆。
func (db *DB) AiDeduplicateMemories(threshold float64) (json.RawMessage, error) {
	return db.execute("ai", "deduplicate_memories", map[string]interface{}{"threshold": threshold})
}

// AiCleanupExpiredMemories 清理过期记忆。
func (db *DB) AiCleanupExpiredMemories() (uint64, error) {
	data, err := db.execute("ai", "cleanup_expired_memories", nil)
	if err != nil {
		return 0, err
	}
	var out struct {
		Cleaned uint64 `json:"cleaned"`
	}
	return out.Cleaned, json.Unmarshal(data, &out)
}

// AiMemoryStats 获取记忆统计。
func (db *DB) AiMemoryStats() (json.RawMessage, error) {
	return db.execute("ai", "memory_stats", nil)
}

// ── AI: RAG ──

// AiRagIngestDocument 导入 RAG 文档。
func (db *DB) AiRagIngestDocument(document, chunks interface{}, embeddings [][]float32) error {
	_, err := db.execute("ai", "rag_ingest_document", map[string]interface{}{
		"document": document, "chunks": chunks, "embeddings": embeddings,
	})
	return err
}

// AiRagIngestBatch 批量导入 RAG 文档。
func (db *DB) AiRagIngestBatch(documents interface{}) error {
	_, err := db.execute("ai", "rag_ingest_batch", map[string]interface{}{"documents": documents})
	return err
}

// AiRagSearch RAG 语义搜索。
func (db *DB) AiRagSearch(queryEmbedding []float32, k int, filter interface{}) (json.RawMessage, error) {
	p := map[string]interface{}{"query_embedding": queryEmbedding, "k": k}
	if filter != nil {
		p["filter"] = filter
	}
	return db.execute("ai", "rag_search", p)
}

// AiRagGetDocument 获取 RAG 文档。
func (db *DB) AiRagGetDocument(docID string) (json.RawMessage, error) {
	return db.execute("ai", "rag_get_document", map[string]interface{}{"doc_id": docID})
}

// AiRagListDocuments 列出所有 RAG 文档。
func (db *DB) AiRagListDocuments() (json.RawMessage, error) {
	return db.execute("ai", "rag_list_documents", nil)
}

// AiRagDeleteDocument 删除 RAG 文档。
func (db *DB) AiRagDeleteDocument(docID string) error {
	_, err := db.execute("ai", "rag_delete_document", map[string]interface{}{"doc_id": docID})
	return err
}

// AiRagUpdateDocument 更新 RAG 文档。
func (db *DB) AiRagUpdateDocument(docID string, chunks interface{}, embeddings [][]float32) error {
	_, err := db.execute("ai", "rag_update_document", map[string]interface{}{
		"doc_id": docID, "chunks": chunks, "embeddings": embeddings,
	})
	return err
}

// AiRagDocumentVersions 获取文档版本历史。
func (db *DB) AiRagDocumentVersions(docID string) (json.RawMessage, error) {
	return db.execute("ai", "rag_document_versions", map[string]interface{}{"doc_id": docID})
}

// ── AI: Agent ──

// AiAgentLogStep 记录 Agent 执行步骤。
func (db *DB) AiAgentLogStep(sessionID string, step map[string]interface{}) error {
	_, err := db.execute("ai", "agent_log_step", map[string]interface{}{
		"session_id": sessionID, "step": step,
	})
	return err
}

// AiAgentGetSteps 获取 Agent 执行步骤。
func (db *DB) AiAgentGetSteps(sessionID string, runID *string) (json.RawMessage, error) {
	p := map[string]interface{}{"session_id": sessionID}
	if runID != nil {
		p["run_id"] = *runID
	}
	return db.execute("ai", "agent_get_steps", p)
}

// AiAgentCacheToolResult 缓存工具调用结果。
func (db *DB) AiAgentCacheToolResult(key string, result interface{}, ttl *uint64) error {
	p := map[string]interface{}{"key": key, "result": result}
	if ttl != nil {
		p["ttl"] = *ttl
	}
	_, err := db.execute("ai", "agent_cache_tool_result", p)
	return err
}

// AiAgentGetToolCache 获取缓存的工具调用结果。
func (db *DB) AiAgentGetToolCache(key string) (json.RawMessage, error) {
	return db.execute("ai", "agent_get_tool_cache", map[string]interface{}{"key": key})
}

// ── AI: Intent ──

// AiClassifyIntent 意图分类。
func (db *DB) AiClassifyIntent(query string, embedding []float32) (json.RawMessage, error) {
	return db.execute("ai", "classify_intent", map[string]interface{}{
		"query": query, "embedding": embedding,
	})
}

// AiRegisterIntent 注册意图模板。
func (db *DB) AiRegisterIntent(intent interface{}, embeddings [][]float32) error {
	_, err := db.execute("ai", "register_intent", map[string]interface{}{
		"intent": intent, "embeddings": embeddings,
	})
	return err
}

// AiListIntents 列出所有意图。
func (db *DB) AiListIntents() (json.RawMessage, error) {
	return db.execute("ai", "list_intents", nil)
}

// ── AI: Trace ──

// AiLogTrace 记录 trace。
func (db *DB) AiLogTrace(record map[string]interface{}) error {
	_, err := db.execute("ai", "log_trace", map[string]interface{}{"record": record})
	return err
}

// AiTokenUsage 查询会话 token 用量。
func (db *DB) AiTokenUsage(sessionID string) (uint64, error) {
	data, err := db.execute("ai", "token_usage", map[string]interface{}{
		"session_id": sessionID,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		TotalTokens uint64 `json:"total_tokens"`
	}
	return out.TotalTokens, json.Unmarshal(data, &out)
}

// AiTokenUsageByRun 按 run_id 查询 token 用量。
func (db *DB) AiTokenUsageByRun(runID string) (uint64, error) {
	data, err := db.execute("ai", "token_usage_by_run", map[string]interface{}{
		"run_id": runID,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		TotalTokens uint64 `json:"total_tokens"`
	}
	return out.TotalTokens, json.Unmarshal(data, &out)
}

// AiTraceStats 获取 trace 统计。
func (db *DB) AiTraceStats(sessionID string) (json.RawMessage, error) {
	return db.execute("ai", "trace_stats", map[string]interface{}{"session_id": sessionID})
}

// AiTracePerformanceReport 获取性能报告。
func (db *DB) AiTracePerformanceReport(sessionID, runID *string) (json.RawMessage, error) {
	p := map[string]interface{}{}
	if sessionID != nil {
		p["session_id"] = *sessionID
	}
	if runID != nil {
		p["run_id"] = *runID
	}
	return db.execute("ai", "trace_performance_report", p)
}

// AiQueryTraces 查询 trace 记录。
func (db *DB) AiQueryTraces(sessionID, runID, operation *string, limit *int) (json.RawMessage, error) {
	p := map[string]interface{}{}
	if sessionID != nil {
		p["session_id"] = *sessionID
	}
	if runID != nil {
		p["run_id"] = *runID
	}
	if operation != nil {
		p["operation"] = *operation
	}
	if limit != nil {
		p["limit"] = *limit
	}
	return db.execute("ai", "query_traces", p)
}

// ── AI: Embedding Cache ──

// AiEmbeddingCacheGet 获取缓存的 embedding。
func (db *DB) AiEmbeddingCacheGet(key string) (json.RawMessage, error) {
	return db.execute("ai", "embedding_cache_get", map[string]interface{}{"key": key})
}

// AiEmbeddingCacheSet 缓存 embedding。
func (db *DB) AiEmbeddingCacheSet(key string, embedding []float32, ttl *uint64) error {
	p := map[string]interface{}{"key": key, "embedding": embedding}
	if ttl != nil {
		p["ttl"] = *ttl
	}
	_, err := db.execute("ai", "embedding_cache_set", p)
	return err
}

// ── AI: Token Count ──

// AiTokenCount 计算文本 token 数量。
func (db *DB) AiTokenCount(text, encoding string) (int, error) {
	data, err := db.execute("ai", "token_count", map[string]interface{}{
		"text": text, "encoding": encoding,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		Count int `json:"count"`
	}
	return out.Count, json.Unmarshal(data, &out)
}

// ── AI: LLM Config ──

// AiSetLlmConfig 设置 LLM 配置。
func (db *DB) AiSetLlmConfig(config map[string]interface{}) error {
	_, err := db.execute("ai", "set_llm_config", map[string]interface{}{"config": config})
	return err
}

// AiGetLlmConfig 获取 LLM 配置。
func (db *DB) AiGetLlmConfig() (json.RawMessage, error) {
	return db.execute("ai", "get_llm_config", nil)
}

// ── AI: Auto Embed ──

// AiAutoEmbed 自动调用 embedding API。
func (db *DB) AiAutoEmbed(texts []string) (json.RawMessage, error) {
	return db.execute("ai", "auto_embed", map[string]interface{}{"texts": texts})
}

// AiClearLlmConfig 清除 LLM 配置。
func (db *DB) AiClearLlmConfig() error {
	_, err := db.execute("ai", "clear_llm_config", nil)
	return err
}

// AiAddMemory 存储记忆（自动 embed + 向量写 + FTS 索引 + 缓存 + 可选 EDU 提取）。
//
// 自动完成以下操作：
//  1. 调用 Embedding API 生成向量（自带 FNV 哈希缓存）
//  2. 写入向量索引（语义搜索）
//  3. 写入 FTS 索引（关键词搜索）
//  4. 存储元数据到 KV
//  5. [可选] 用 LLM 提取 EDU（结构化事件单元），每个 EDU 独立 embed + 存储
//
// 需要先调用 AiSetLlmConfig 配置 embed provider。
// 开启 extractFacts 时还需要 chat provider。
func (db *DB) AiAddMemory(content string, metadata map[string]string, ttlSecs *uint64, extractFacts bool) (uint64, error) {
	p := map[string]interface{}{"content": content}
	if metadata != nil {
		p["metadata"] = metadata
	}
	if ttlSecs != nil {
		p["ttl_secs"] = *ttlSecs
	}
	if extractFacts {
		p["extract_facts"] = true
	}
	data, err := db.execute("ai", "add_memory", p)
	if err != nil {
		return 0, err
	}
	var out struct {
		ID uint64 `json:"id"`
	}
	return out.ID, json.Unmarshal(data, &out)
}

// AiRecall 智能召回（hybrid search + 时间感知 + LLM Rerank + Graph 扩展）。
// 完整 Pipeline: Query → Graph Expand → Hybrid(BM25+Vec) → RRF → Temporal → Rerank → Top-K
func (db *DB) AiRecall(query string, k int, ftsWeight, vecWeight, temporalBoost float64, rerank bool, rerankTopK int, graphDepth int) (json.RawMessage, error) {
	p := map[string]interface{}{
		"query": query, "k": k,
		"fts_weight": ftsWeight, "vec_weight": vecWeight,
	}
	if temporalBoost > 0 {
		p["temporal_boost"] = temporalBoost
	}
	if rerank {
		p["rerank"] = true
	}
	if rerankTopK > 0 {
		p["rerank_top_k"] = rerankTopK
	}
	if graphDepth > 0 {
		p["graph_depth"] = graphDepth
	}
	return db.execute("ai", "recall", p)
}

// AiSearchMemoryWithFilter 向量搜索 + metadata 过滤。
func (db *DB) AiSearchMemoryWithFilter(embedding []float32, k int, filters map[string]string) (json.RawMessage, error) {
	p := map[string]interface{}{"embedding": embedding, "k": k}
	if filters != nil {
		p["filters"] = filters
	}
	return db.execute("ai", "search_memory_with_filter", p)
}

// AiFindDuplicateMemories 查找重复记忆对（不删除）。
func (db *DB) AiFindDuplicateMemories(threshold float64) (json.RawMessage, error) {
	return db.execute("ai", "find_duplicate_memories", map[string]interface{}{"threshold": threshold})
}

// AiListMemories 分页列出记忆。
func (db *DB) AiListMemories(offset, limit int) (json.RawMessage, error) {
	return db.execute("ai", "list_memories", map[string]interface{}{
		"offset": offset, "limit": limit,
	})
}

// AiGetContextWindowSmart 智能上下文窗口：超长时自动触发摘要压缩。
func (db *DB) AiGetContextWindowSmart(sessionID string, maxTokens int) (json.RawMessage, error) {
	return db.execute("ai", "get_context_window_smart", map[string]interface{}{
		"session_id": sessionID, "max_tokens": maxTokens,
	})
}

// ── Backup ──

// ExportDb 导出数据库，返回导出数量。
func (db *DB) ExportDb(dir string, keyspaces []string) (uint64, error) {
	p := map[string]interface{}{"dir": dir}
	if keyspaces != nil {
		p["keyspaces"] = keyspaces
	}
	data, err := db.execute("backup", "export", p)
	if err != nil {
		return 0, err
	}
	var out struct {
		Exported uint64 `json:"exported"`
	}
	return out.Exported, json.Unmarshal(data, &out)
}

// ImportDb 导入数据库，返回导入数量。
func (db *DB) ImportDb(dir string) (uint64, error) {
	data, err := db.execute("backup", "import", map[string]interface{}{"dir": dir})
	if err != nil {
		return 0, err
	}
	var out struct {
		Imported uint64 `json:"imported"`
	}
	return out.Imported, json.Unmarshal(data, &out)
}
