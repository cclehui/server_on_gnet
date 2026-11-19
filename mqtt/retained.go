package mqtt

import "sync"

// retainedStore 保存每个 topic 最后一条 retain=1 的消息.
//
// 只按精确 topic 存. 规范里 retain 的消息在订阅时要按 filter 匹配后重放,
// 因此 Match 需要遍历所有保留 topic —— 保留消息本来就少(状态类 topic),
// 这里不做 filter 索引, 用一次线性比较换实现上的简单.
type retainedStore struct {
	mu       sync.RWMutex
	messages map[string][]byte
}

func newRetainedStore() *retainedStore {
	return &retainedStore{messages: make(map[string][]byte)}
}

// Set 保存或覆盖. payload 为空且 retain=1 表示"清除该 topic 的保留消息",
// 这是规范定义的删除方式, 不是存一条空消息.
func (store *retainedStore) Set(topic string, payload []byte) {
	store.mu.Lock()
	defer store.mu.Unlock()

	if len(payload) == 0 {
		delete(store.messages, topic)
		return
	}

	copied := make([]byte, len(payload))
	copy(copied, payload)
	store.messages[topic] = copied
}

// Matching 返回所有匹配 filter 的保留消息副本.
// 订阅时要按订阅时授予的 QoS 发出, 这里 QoS 恒为 0, 所以只带 topic + payload.
func (store *retainedStore) Matching(filter string) map[string][]byte {
	store.mu.RLock()
	defer store.mu.RUnlock()

	matched := make(map[string][]byte)
	for topic, payload := range store.messages {
		if !TopicMatches(filter, topic) {
			continue
		}

		copied := make([]byte, len(payload))
		copy(copied, payload)
		matched[topic] = copied
	}

	return matched
}

func (store *retainedStore) Len() int {
	store.mu.RLock()
	defer store.mu.RUnlock()

	return len(store.messages)
}
