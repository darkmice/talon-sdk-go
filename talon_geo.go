/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */
// Talon Go SDK — 地理空间（Geo）方法。

package talon

import "encoding/json"

// GeoCreate 创建地理空间索引。
func (db *DB) GeoCreate(name string) error {
	_, err := db.execute("geo", "create", map[string]interface{}{"name": name})
	return err
}

// GeoAdd 添加地理位置。
func (db *DB) GeoAdd(name, key string, lng, lat float64) error {
	_, err := db.execute("geo", "add", map[string]interface{}{
		"name": name, "key": key, "lng": lng, "lat": lat,
	})
	return err
}

// GeoMember 批量添加的成员项。
type GeoMember struct {
	Key string  `json:"key"`
	Lng float64 `json:"lng"`
	Lat float64 `json:"lat"`
}

// GeoAddBatch 批量添加地理位置。
func (db *DB) GeoAddBatch(name string, members []GeoMember) (int, error) {
	data, err := db.execute("geo", "add_batch", map[string]interface{}{
		"name": name, "members": members,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		Count int `json:"count"`
	}
	return out.Count, json.Unmarshal(data, &out)
}

// GeoPos 查询成员位置。返回 (lng, lat, found)。
func (db *DB) GeoPos(name, key string) (float64, float64, bool, error) {
	data, err := db.execute("geo", "pos", map[string]interface{}{
		"name": name, "key": key,
	})
	if err != nil {
		return 0, 0, false, err
	}
	// null 表示不存在
	if string(data) == "null" {
		return 0, 0, false, nil
	}
	var out struct {
		Lng float64 `json:"lng"`
		Lat float64 `json:"lat"`
	}
	return out.Lng, out.Lat, true, json.Unmarshal(data, &out)
}

// GeoDel 删除成员，返回是否存在并被删除。
func (db *DB) GeoDel(name, key string) (bool, error) {
	data, err := db.execute("geo", "del", map[string]interface{}{
		"name": name, "key": key,
	})
	if err != nil {
		return false, err
	}
	var out struct {
		Deleted bool `json:"deleted"`
	}
	return out.Deleted, json.Unmarshal(data, &out)
}

// GeoDist 计算两点距离。unit: "m"/"km"/"mi"。返回 (dist, found)。
func (db *DB) GeoDist(name, key1, key2, unit string) (*float64, error) {
	p := map[string]interface{}{"name": name, "key1": key1, "key2": key2}
	if unit != "" {
		p["unit"] = unit
	}
	data, err := db.execute("geo", "dist", p)
	if err != nil {
		return nil, err
	}
	if string(data) == "null" {
		return nil, nil
	}
	var out struct {
		Dist float64 `json:"dist"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out.Dist, nil
}

// GeoSearch 半径搜索，返回原始 JSON。
func (db *DB) GeoSearch(name string, lng, lat, radius float64, unit string, count *int) (json.RawMessage, error) {
	p := map[string]interface{}{
		"name": name, "lng": lng, "lat": lat, "radius": radius,
	}
	if unit != "" {
		p["unit"] = unit
	}
	if count != nil {
		p["count"] = *count
	}
	return db.execute("geo", "search", p)
}

// GeoSearchBox 矩形范围搜索，返回原始 JSON。
func (db *DB) GeoSearchBox(name string, minLng, minLat, maxLng, maxLat float64, count *int) (json.RawMessage, error) {
	p := map[string]interface{}{
		"name": name, "min_lng": minLng, "min_lat": minLat,
		"max_lng": maxLng, "max_lat": maxLat,
	}
	if count != nil {
		p["count"] = *count
	}
	return db.execute("geo", "search_box", p)
}

// GeoFence 地理围栏判定：成员是否在指定圆形区域内。返回 (*inside, error)，nil 表示成员不存在。
func (db *DB) GeoFence(name, key string, centerLng, centerLat, radius float64, unit string) (*bool, error) {
	p := map[string]interface{}{
		"name": name, "key": key,
		"center_lng": centerLng, "center_lat": centerLat, "radius": radius,
	}
	if unit != "" {
		p["unit"] = unit
	}
	data, err := db.execute("geo", "fence", p)
	if err != nil {
		return nil, err
	}
	if string(data) == "null" {
		return nil, nil
	}
	var out struct {
		Inside bool `json:"inside"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out.Inside, nil
}

// GeoMembers 列出索引中所有成员 key。
func (db *DB) GeoMembers(name string) ([]string, error) {
	data, err := db.execute("geo", "members", map[string]interface{}{"name": name})
	if err != nil {
		return nil, err
	}
	var out struct {
		Members []string `json:"members"`
	}
	return out.Members, json.Unmarshal(data, &out)
}
