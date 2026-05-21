/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */
// Talon Go SDK — 消息队列（MQ）方法。

package talon

import "encoding/json"

// MqCreate 创建 topic。
func (db *DB) MqCreate(topic string, maxLen int) error {
	_, err := db.execute("mq", "create", map[string]interface{}{
		"topic": topic, "max_len": maxLen,
	})
	return err
}

// MqPublish 发布消息，返回消息 ID。
func (db *DB) MqPublish(topic, payload string) (uint64, error) {
	data, err := db.execute("mq", "publish", map[string]interface{}{
		"topic": topic, "payload": payload,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		ID uint64 `json:"id"`
	}
	return out.ID, json.Unmarshal(data, &out)
}

// MqPoll 拉取消息，返回原始 JSON。
func (db *DB) MqPoll(topic, group, consumer string, count int, blockMs int) (json.RawMessage, error) {
	return db.execute("mq", "poll", map[string]interface{}{
		"topic": topic, "group": group, "consumer": consumer,
		"count": count, "block_ms": blockMs,
	})
}

// MqAck 确认消息。
func (db *DB) MqAck(topic, group, consumer string, messageID uint64) error {
	_, err := db.execute("mq", "ack", map[string]interface{}{
		"topic": topic, "group": group, "consumer": consumer,
		"message_id": messageID,
	})
	return err
}

// MqLen 获取 topic 消息数。
func (db *DB) MqLen(topic string) (uint64, error) {
	data, err := db.execute("mq", "len", map[string]interface{}{"topic": topic})
	if err != nil {
		return 0, err
	}
	var out struct {
		Len uint64 `json:"len"`
	}
	return out.Len, json.Unmarshal(data, &out)
}

// MqDrop 删除 topic。
func (db *DB) MqDrop(topic string) error {
	_, err := db.execute("mq", "drop", map[string]interface{}{"topic": topic})
	return err
}

// MqSubscribe 注册消费者组订阅到 topic（持久化绑定）。
func (db *DB) MqSubscribe(topic, group string) error {
	_, err := db.execute("mq", "subscribe", map[string]interface{}{
		"topic": topic, "group": group,
	})
	return err
}

// MqUnsubscribe 取消消费者组对 topic 的订阅。
func (db *DB) MqUnsubscribe(topic, group string) error {
	_, err := db.execute("mq", "unsubscribe", map[string]interface{}{
		"topic": topic, "group": group,
	})
	return err
}

// MqListSubscriptions 列出 topic 的所有订阅消费者组。
func (db *DB) MqListSubscriptions(topic string) ([]string, error) {
	data, err := db.execute("mq", "list_subscriptions", map[string]interface{}{
		"topic": topic,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Groups []string `json:"groups"`
	}
	return out.Groups, json.Unmarshal(data, &out)
}
