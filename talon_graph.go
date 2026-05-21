/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */
// Talon Go SDK — 图引擎（Graph）方法。

package talon

import "encoding/json"

// GraphCreate 创建图。
func (db *DB) GraphCreate(graph string) error {
	_, err := db.execute("graph", "create", map[string]interface{}{"graph": graph})
	return err
}

// GraphAddVertex 添加顶点，返回 vertex_id。
func (db *DB) GraphAddVertex(graph, label string, properties map[string]string) (uint64, error) {
	p := map[string]interface{}{"graph": graph, "label": label}
	if properties != nil {
		p["properties"] = properties
	}
	data, err := db.execute("graph", "add_vertex", p)
	if err != nil {
		return 0, err
	}
	var out struct {
		VertexID uint64 `json:"vertex_id"`
	}
	return out.VertexID, json.Unmarshal(data, &out)
}

// GraphGetVertex 获取顶点信息，返回原始 JSON。nil 表示不存在。
func (db *DB) GraphGetVertex(graph string, id uint64) (json.RawMessage, error) {
	return db.execute("graph", "get_vertex", map[string]interface{}{
		"graph": graph, "id": id,
	})
}

// GraphUpdateVertex 更新顶点属性。
func (db *DB) GraphUpdateVertex(graph string, id uint64, properties map[string]string) error {
	p := map[string]interface{}{"graph": graph, "id": id}
	if properties != nil {
		p["properties"] = properties
	}
	_, err := db.execute("graph", "update_vertex", p)
	return err
}

// GraphDeleteVertex 删除顶点。
func (db *DB) GraphDeleteVertex(graph string, id uint64) error {
	_, err := db.execute("graph", "delete_vertex", map[string]interface{}{
		"graph": graph, "id": id,
	})
	return err
}

// GraphAddEdge 添加边，返回 edge_id。
func (db *DB) GraphAddEdge(graph string, from, to uint64, label string, properties map[string]string) (uint64, error) {
	p := map[string]interface{}{
		"graph": graph, "from": from, "to": to, "label": label,
	}
	if properties != nil {
		p["properties"] = properties
	}
	data, err := db.execute("graph", "add_edge", p)
	if err != nil {
		return 0, err
	}
	var out struct {
		EdgeID uint64 `json:"edge_id"`
	}
	return out.EdgeID, json.Unmarshal(data, &out)
}

// GraphGetEdge 获取边信息，返回原始 JSON。nil 表示不存在。
func (db *DB) GraphGetEdge(graph string, id uint64) (json.RawMessage, error) {
	return db.execute("graph", "get_edge", map[string]interface{}{
		"graph": graph, "id": id,
	})
}

// GraphDeleteEdge 删除边。
func (db *DB) GraphDeleteEdge(graph string, id uint64) error {
	_, err := db.execute("graph", "delete_edge", map[string]interface{}{
		"graph": graph, "id": id,
	})
	return err
}

// GraphNeighbors 获取邻居顶点 ID 列表。direction: "out"/"in"/"both"。
func (db *DB) GraphNeighbors(graph string, id uint64, direction string) ([]uint64, error) {
	p := map[string]interface{}{"graph": graph, "id": id}
	if direction != "" {
		p["direction"] = direction
	}
	data, err := db.execute("graph", "neighbors", p)
	if err != nil {
		return nil, err
	}
	var out struct {
		Neighbors []uint64 `json:"neighbors"`
	}
	return out.Neighbors, json.Unmarshal(data, &out)
}

// GraphOutEdges 获取顶点的出边，返回原始 JSON。
func (db *DB) GraphOutEdges(graph string, id uint64) (json.RawMessage, error) {
	return db.execute("graph", "out_edges", map[string]interface{}{
		"graph": graph, "id": id,
	})
}

// GraphInEdges 获取顶点的入边，返回原始 JSON。
func (db *DB) GraphInEdges(graph string, id uint64) (json.RawMessage, error) {
	return db.execute("graph", "in_edges", map[string]interface{}{
		"graph": graph, "id": id,
	})
}

// GraphVerticesByLabel 按标签查找顶点，返回原始 JSON。
func (db *DB) GraphVerticesByLabel(graph, label string) (json.RawMessage, error) {
	return db.execute("graph", "vertices_by_label", map[string]interface{}{
		"graph": graph, "label": label,
	})
}

// GraphVertexCount 获取顶点总数。
func (db *DB) GraphVertexCount(graph string) (uint64, error) {
	data, err := db.execute("graph", "vertex_count", map[string]interface{}{"graph": graph})
	if err != nil {
		return 0, err
	}
	var out struct {
		Count uint64 `json:"count"`
	}
	return out.Count, json.Unmarshal(data, &out)
}

// GraphEdgeCount 获取边总数。
func (db *DB) GraphEdgeCount(graph string) (uint64, error) {
	data, err := db.execute("graph", "edge_count", map[string]interface{}{"graph": graph})
	if err != nil {
		return 0, err
	}
	var out struct {
		Count uint64 `json:"count"`
	}
	return out.Count, json.Unmarshal(data, &out)
}

// GraphBFS 广度优先遍历，返回原始 JSON。direction: "out"/"in"/"both"。
func (db *DB) GraphBFS(graph string, start uint64, maxDepth int, direction string) (json.RawMessage, error) {
	p := map[string]interface{}{
		"graph": graph, "start": start, "max_depth": maxDepth,
	}
	if direction != "" {
		p["direction"] = direction
	}
	return db.execute("graph", "bfs", p)
}

// GraphShortestPath 最短路径，返回原始 JSON。
func (db *DB) GraphShortestPath(graph string, from, to uint64, maxDepth int) (json.RawMessage, error) {
	return db.execute("graph", "shortest_path", map[string]interface{}{
		"graph": graph, "from": from, "to": to, "max_depth": maxDepth,
	})
}

// GraphWeightedShortestPath 加权最短路径，返回原始 JSON。
func (db *DB) GraphWeightedShortestPath(graph string, from, to uint64, maxDepth int, weightKey string) (json.RawMessage, error) {
	p := map[string]interface{}{
		"graph": graph, "from": from, "to": to, "max_depth": maxDepth,
	}
	if weightKey != "" {
		p["weight_key"] = weightKey
	}
	return db.execute("graph", "weighted_shortest_path", p)
}

// GraphDegreeCentrality 度中心性排行，返回原始 JSON。
func (db *DB) GraphDegreeCentrality(graph string, limit int) (json.RawMessage, error) {
	return db.execute("graph", "degree_centrality", map[string]interface{}{
		"graph": graph, "limit": limit,
	})
}

// GraphPageRank PageRank 算法，返回原始 JSON。
func (db *DB) GraphPageRank(graph string, damping float64, iterations, limit int) (json.RawMessage, error) {
	return db.execute("graph", "pagerank", map[string]interface{}{
		"graph": graph, "damping": damping,
		"iterations": iterations, "limit": limit,
	})
}
