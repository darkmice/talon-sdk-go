/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */
// Talon Go SDK — 时序引擎（TS）方法。

package talon

import "encoding/json"

// TsCreate 创建时序表。
func (db *DB) TsCreate(name string, tags, fields []string) error {
	if tags == nil {
		tags = []string{}
	}
	if fields == nil {
		fields = []string{}
	}
	_, err := db.execute("ts", "create", map[string]interface{}{
		"name": name, "tags": tags, "fields": fields,
	})
	return err
}

// TsInsert 插入数据点。point 需含 timestamp/tags/fields。
func (db *DB) TsInsert(name string, point map[string]interface{}) error {
	_, err := db.execute("ts", "insert", map[string]interface{}{
		"name": name, "point": point,
	})
	return err
}

// TsQueryOpts 时序查询选项。
type TsQueryOpts struct {
	TagFilters []interface{} `json:"tag_filters,omitempty"`
	TimeStart  *int64        `json:"time_start,omitempty"`
	TimeEnd    *int64        `json:"time_end,omitempty"`
	Desc       bool          `json:"desc,omitempty"`
	Limit      *int          `json:"limit,omitempty"`
}

// TsQuery 查询时序数据，返回原始 JSON。
func (db *DB) TsQuery(name string, opts *TsQueryOpts) (json.RawMessage, error) {
	p := map[string]interface{}{"name": name}
	if opts != nil {
		if opts.TagFilters != nil {
			p["tag_filters"] = opts.TagFilters
		}
		if opts.TimeStart != nil {
			p["time_start"] = *opts.TimeStart
		}
		if opts.TimeEnd != nil {
			p["time_end"] = *opts.TimeEnd
		}
		if opts.Desc {
			p["desc"] = true
		}
		if opts.Limit != nil {
			p["limit"] = *opts.Limit
		}
	}
	return db.execute("ts", "query", p)
}

// TsAggregateOpts 时序聚合选项。
type TsAggregateOpts struct {
	TagFilters []interface{} `json:"tag_filters,omitempty"`
	TimeStart  *int64        `json:"time_start,omitempty"`
	TimeEnd    *int64        `json:"time_end,omitempty"`
	IntervalMs *int64        `json:"interval_ms,omitempty"`
}

// TsAggregate 时序聚合查询。func_name: count/sum/avg/min/max。
func (db *DB) TsAggregate(name, field, funcName string, opts *TsAggregateOpts) (json.RawMessage, error) {
	p := map[string]interface{}{
		"name": name, "field": field, "func": funcName,
	}
	if opts != nil {
		if opts.TagFilters != nil {
			p["tag_filters"] = opts.TagFilters
		}
		if opts.TimeStart != nil {
			p["time_start"] = *opts.TimeStart
		}
		if opts.TimeEnd != nil {
			p["time_end"] = *opts.TimeEnd
		}
		if opts.IntervalMs != nil {
			p["interval_ms"] = *opts.IntervalMs
		}
	}
	return db.execute("ts", "aggregate", p)
}

// TsSetRetention 设置保留策略（毫秒）。
func (db *DB) TsSetRetention(name string, retentionMs int64) error {
	_, err := db.execute("ts", "set_retention", map[string]interface{}{
		"name": name, "retention_ms": retentionMs,
	})
	return err
}

// TsPurgeExpired 清理过期数据，返回清理数量。
func (db *DB) TsPurgeExpired(name string) (uint64, error) {
	data, err := db.execute("ts", "purge_expired", map[string]interface{}{"name": name})
	if err != nil {
		return 0, err
	}
	var out struct {
		Purged uint64 `json:"purged"`
	}
	return out.Purged, json.Unmarshal(data, &out)
}

// TsPurgeByTag 按标签清理数据，返回清理数量。
func (db *DB) TsPurgeByTag(name string, tagFilters []interface{}) (uint64, error) {
	data, err := db.execute("ts", "purge_by_tag", map[string]interface{}{
		"name": name, "tag_filters": tagFilters,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		Purged uint64 `json:"purged"`
	}
	return out.Purged, json.Unmarshal(data, &out)
}
