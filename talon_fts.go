/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */
// Talon Go SDK — 全文搜索（FTS）方法。

package talon

import "encoding/json"

// FtsCreateIndex 创建全文索引。
func (db *DB) FtsCreateIndex(name string) error {
	_, err := db.execute("fts", "create_index", map[string]interface{}{"name": name})
	return err
}

// FtsDropIndex 删除全文索引。
func (db *DB) FtsDropIndex(name string) error {
	_, err := db.execute("fts", "drop_index", map[string]interface{}{"name": name})
	return err
}

// FtsIndex 索引单个文档。fields 为 field_name → text 映射。
func (db *DB) FtsIndex(name, docID string, fields map[string]string) error {
	_, err := db.execute("fts", "index", map[string]interface{}{
		"name": name, "doc_id": docID, "fields": fields,
	})
	return err
}

// FtsIndexBatch 批量索引文档。每个 doc 须含 doc_id 和 fields。
func (db *DB) FtsIndexBatch(name string, docs []map[string]interface{}) (int, error) {
	data, err := db.execute("fts", "index_batch", map[string]interface{}{
		"name": name, "docs": docs,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		Count int `json:"count"`
	}
	return out.Count, json.Unmarshal(data, &out)
}

// FtsDelete 删除文档，返回是否存在并被删除。
func (db *DB) FtsDelete(name, docID string) (bool, error) {
	data, err := db.execute("fts", "delete", map[string]interface{}{
		"name": name, "doc_id": docID,
	})
	if err != nil {
		return false, err
	}
	var out struct {
		Deleted bool `json:"deleted"`
	}
	return out.Deleted, json.Unmarshal(data, &out)
}

// FtsGet 获取文档字段，返回原始 JSON。
func (db *DB) FtsGet(name, docID string) (json.RawMessage, error) {
	return db.execute("fts", "get", map[string]interface{}{
		"name": name, "doc_id": docID,
	})
}

// FtsSearch BM25 精确搜索，返回原始 JSON。
func (db *DB) FtsSearch(name, query string, limit int) (json.RawMessage, error) {
	return db.execute("fts", "search", map[string]interface{}{
		"name": name, "query": query, "limit": limit,
	})
}

// FtsSearchFuzzy 模糊搜索，返回原始 JSON。
func (db *DB) FtsSearchFuzzy(name, query string, maxDist uint32, limit int) (json.RawMessage, error) {
	return db.execute("fts", "search_fuzzy", map[string]interface{}{
		"name": name, "query": query, "max_dist": maxDist, "limit": limit,
	})
}

// FtsHybridSearch 混合搜索（BM25 + 向量），返回原始 JSON。
func (db *DB) FtsHybridSearch(ftsIndex, vecIndex, query string, vector []float32, opts *HybridSearchOpts) (json.RawMessage, error) {
	p := map[string]interface{}{
		"name": ftsIndex, "vec_index": vecIndex,
		"query": query, "vector": vector,
	}
	if opts != nil {
		if opts.Metric != "" {
			p["metric"] = opts.Metric
		}
		if opts.Limit > 0 {
			p["limit"] = opts.Limit
		}
		if opts.FtsWeight != 0 {
			p["fts_weight"] = opts.FtsWeight
		}
		if opts.VecWeight != 0 {
			p["vec_weight"] = opts.VecWeight
		}
		if opts.NumCandidates > 0 {
			p["num_candidates"] = opts.NumCandidates
		}
		if opts.PreFilter != nil {
			p["pre_filter"] = opts.PreFilter
		}
	}
	return db.execute("fts", "hybrid_search", p)
}

// HybridSearchOpts 混合搜索选项。
type HybridSearchOpts struct {
	Metric        string            `json:"metric,omitempty"`
	Limit         int               `json:"limit,omitempty"`
	FtsWeight     float64           `json:"fts_weight,omitempty"`
	VecWeight     float64           `json:"vec_weight,omitempty"`
	NumCandidates int               `json:"num_candidates,omitempty"`
	PreFilter     map[string]string `json:"pre_filter,omitempty"`
}

// FtsAddAlias 添加索引别名。
func (db *DB) FtsAddAlias(alias, index string) error {
	_, err := db.execute("fts", "add_alias", map[string]interface{}{
		"alias": alias, "index": index,
	})
	return err
}

// FtsRemoveAlias 移除索引别名。
func (db *DB) FtsRemoveAlias(alias string) error {
	_, err := db.execute("fts", "remove_alias", map[string]interface{}{
		"alias": alias,
	})
	return err
}

// FtsReindex 重建索引，返回重建数量。
func (db *DB) FtsReindex(name string) (uint64, error) {
	data, err := db.execute("fts", "reindex", map[string]interface{}{"name": name})
	if err != nil {
		return 0, err
	}
	var out struct {
		Reindexed uint64 `json:"reindexed"`
	}
	return out.Reindexed, json.Unmarshal(data, &out)
}

// FtsCloseIndex 关闭索引（释放内存）。
func (db *DB) FtsCloseIndex(name string) error {
	_, err := db.execute("fts", "close_index", map[string]interface{}{"name": name})
	return err
}

// FtsOpenIndex 打开索引。
func (db *DB) FtsOpenIndex(name string) error {
	_, err := db.execute("fts", "open_index", map[string]interface{}{"name": name})
	return err
}

// FtsGetMapping 获取索引映射信息，返回原始 JSON。
func (db *DB) FtsGetMapping(name string) (json.RawMessage, error) {
	return db.execute("fts", "get_mapping", map[string]interface{}{"name": name})
}

// FtsListIndexes 列出所有索引，返回原始 JSON。
func (db *DB) FtsListIndexes() (json.RawMessage, error) {
	return db.execute("fts", "list_indexes", nil)
}
