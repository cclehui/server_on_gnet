package mqtt

import (
	"fmt"
	"strings"
	"sync"
)

// ValidateTopicName 校验 PUBLISH 的 topic name: 不能为空, 不能含通配符, 不能含 NUL.
// topic 里的 "/" 允许出现, 也允许首尾是 "/" —— "a//b" 和 "/a" 都是合法 topic,
// 按规范切分时会产生空层级, 所以切分函数不能用 strings.Fields 之类会吞空段的写法.
func ValidateTopicName(topic string) error {
	if topic == "" {
		return fmt.Errorf("%w: topic name is empty", ErrInvalidTopic)
	}

	if strings.ContainsAny(topic, "+#") {
		return fmt.Errorf("%w: topic name %q contains wildcard", ErrInvalidTopic, topic)
	}

	if strings.ContainsRune(topic, 0) {
		return fmt.Errorf("%w: topic name contains NUL", ErrInvalidTopic)
	}

	return nil
}

// ValidateTopicFilter 校验 SUBSCRIBE 的 filter:
//   - 非空, 不含 NUL
//   - "+" 必须独占一层("sport/+" 合法, "sport+" 非法)
//   - "#" 必须是最后一层, 且独占一层
func ValidateTopicFilter(filter string) error {
	if filter == "" {
		return fmt.Errorf("%w: topic filter is empty", ErrInvalidTopicFilter)
	}

	if strings.ContainsRune(filter, 0) {
		return fmt.Errorf("%w: topic filter contains NUL", ErrInvalidTopicFilter)
	}

	levels := SplitTopic(filter)
	for i, level := range levels {
		switch {
		case level == "+":
			// 合法, 独占一层
		case level == "#":
			if i != len(levels)-1 {
				return fmt.Errorf("%w: %q has '#' before the last level", ErrInvalidTopicFilter, filter)
			}
		case strings.ContainsAny(level, "+#"):
			return fmt.Errorf("%w: %q has wildcard inside level %q", ErrInvalidTopicFilter, filter, level)
		}
	}

	return nil
}

// SplitTopic 按 "/" 切分且保留空段: "a//b" -> ["a", "", "b"], "/a" -> ["", "a"].
// 空段在匹配时按普通层级参与, 这样 "sport//player1" 只能被 "sport/+/player1"
// 匹配, 而不会被 "sport/+/+" 之外的写法误匹配.
func SplitTopic(topic string) []string {
	return strings.Split(topic, "/")
}

// TopicMatches 判断 topic 是否匹配 filter, 语义和订阅树 Match 完全一致:
//   - "+" 匹配恰好一层, "#" 匹配零到多层
//   - "sport/#" 也匹配 "sport" 本身(规范的 parent 例外)
//   - 顶层以通配符开头的 filter 不匹配以 "$" 开头的 topic($SYS 等系统 topic)
//
// 订阅树里每个节点自带的 filter 都是合法的, 所以这里不重复做合法性校验.
func TopicMatches(filter, topic string) bool {
	filterLevels := SplitTopic(filter)
	topicLevels := SplitTopic(topic)

	// $ 开头只看 filter 的第一层是不是通配符: "$SYS/#" 是字面量开头, 合法
	dollarTopic := len(topicLevels) > 0 && strings.HasPrefix(topicLevels[0], "$")

	for i := 0; ; i++ {
		if i == len(filterLevels) {
			// filter 消耗完: topic 也必须正好消耗完, "sport" 不匹配 "sport/a"
			return i == len(topicLevels)
		}

		filterLevel := filterLevels[i]

		switch filterLevel {
		case "#":
			// "#" 必须是最后一层(ValidateTopicFilter 保证), 匹配剩余全部层级
			return !(i == 0 && dollarTopic)
		case "+":
			if i == 0 && dollarTopic {
				return false
			}

			if i >= len(topicLevels) {
				return false
			}
		default:
			if i >= len(topicLevels) || filterLevel != topicLevels[i] {
				return false
			}
		}
	}
}

// SubscriptionNode 是订阅树的一个节点. children 用 map 而不是数组:
// 层级名是任意字符串, 线性扫描在订阅数上来后是 O(订阅数) 每条消息.
type SubscriptionNode struct {
	children map[string]*SubscriptionNode

	// subscribers 只挂在 filter 的最后一个层级上, 中间节点为空.
	// key 是 client id, value 是授予的 QoS(恒为 0, 保留字段是为了日志和将来扩展).
	subscribers map[string]byte
}

func newSubscriptionNode() *SubscriptionNode {
	return &SubscriptionNode{
		children:    make(map[string]*SubscriptionNode),
		subscribers: make(map[string]byte),
	}
}

// SubscriptionTree 是 topic filter -> client 集合的索引.
//
// 并发模型: 开 multicore 后每个 event loop 是一个独立 reactor 协程, SUBSCRIBE /
// UNSUBSCRIBE / PUBLISH 匹配 / 断连清理会并发进出这棵树; 维护协程也会在
// keepalive 超时时间接触发清理. 所以整棵树由它自己的 RWMutex 保护:
// 读(Match)共享, 写(Subscribe/Unsubscribe/RemoveClient)独占.
// 不要在持锁期间调用 AsyncWrite 或做日志以外的 I/O —— 那会把分发拖成串行.
type SubscriptionTree struct {
	mu   sync.RWMutex
	root *SubscriptionNode
}

func NewSubscriptionTree() *SubscriptionTree {
	return &SubscriptionTree{root: newSubscriptionNode()}
}

// Subscribe 插入一条订阅. 返回 false 表示该 client 已经订阅过同一个 filter
// (MQTT 规范要求替换而不是重复投递).
func (tree *SubscriptionTree) Subscribe(filter, clientID string, qos byte) bool {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	node := tree.root
	for _, level := range SplitTopic(filter) {
		child := node.children[level]
		if child == nil {
			child = newSubscriptionNode()
			node.children[level] = child
		}

		node = child
	}

	_, existed := node.subscribers[clientID]
	node.subscribers[clientID] = qos

	return !existed
}

// Unsubscribe 删除订阅, 并回收因此变空的节点, 避免长期运行时树无限增长.
// 返回 false 表示该 client 本来就没订阅这个 filter.
func (tree *SubscriptionTree) Unsubscribe(filter, clientID string) bool {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	levels := SplitTopic(filter)

	// path 记录走过的节点, 用于回溯删除空节点
	path := make([]*SubscriptionNode, 0, len(levels)+1)
	path = append(path, tree.root)

	node := tree.root
	for _, level := range levels {
		child := node.children[level]
		if child == nil {
			return false
		}

		path = append(path, child)
		node = child
	}

	if _, ok := node.subscribers[clientID]; !ok {
		return false
	}

	delete(node.subscribers, clientID)

	// 从叶子往根回溯: 只删既没有订阅者也没有子节点的节点
	for i := len(path) - 1; i > 0; i-- {
		if len(path[i].subscribers) > 0 || len(path[i].children) > 0 {
			break
		}

		delete(path[i-1].children, levels[i-1])
	}

	return true
}

// RemoveClient 在连接断开时清掉该 client 的全部订阅.
// 遍历整棵树而不是维护 client -> filters 的反向索引:
// 断开是低频事件, 反向索引的一致性问题比这点遍历成本更麻烦.
func (tree *SubscriptionTree) RemoveClient(clientID string) int {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	removed := 0
	tree.removeClientFrom(tree.root, clientID, &removed)

	return removed
}

func (tree *SubscriptionTree) removeClientFrom(node *SubscriptionNode, clientID string, removed *int) {
	if _, ok := node.subscribers[clientID]; ok {
		delete(node.subscribers, clientID)

		*removed++
	}

	for level, child := range node.children {
		tree.removeClientFrom(child, clientID, removed)

		if len(child.subscribers) == 0 && len(child.children) == 0 {
			delete(node.children, level)
		}
	}
}

// Match 返回所有匹配 topic 的 client id -> 授予 QoS 的副本.
// 返回值是拷贝: 调用方要在锁外做写回, 不能持有树的内部 map.
func (tree *SubscriptionTree) Match(topic string) map[string]byte {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	matched := make(map[string]byte)
	topicLevels := SplitTopic(topic)

	// $ 开头的 topic 不能被顶层通配符匹配: 否则订阅 "#" 会连 $SYS 一起收到
	dollarTopic := strings.HasPrefix(topicLevels[0], "$")

	tree.matchNode(tree.root, 0, topicLevels, dollarTopic, matched)

	return matched
}

func (tree *SubscriptionTree) matchNode(node *SubscriptionNode, level int, topicLevels []string,
	dollarTopic bool, out map[string]byte) {

	// "sport/#" 匹配 "sport": topic 已经到末尾时, 当前节点的 "#" 子节点仍然命中
	if hash := node.children["#"]; hash != nil && !(level == 0 && dollarTopic) {
		for clientID, qos := range hash.subscribers {
			out[clientID] = qos
		}
	}

	if level == len(topicLevels) {
		// 精确订阅(比如 "a/b")挂在最后一层的节点上
		for clientID, qos := range node.subscribers {
			out[clientID] = qos
		}

		return
	}

	if child := node.children[topicLevels[level]]; child != nil {
		tree.matchNode(child, level+1, topicLevels, dollarTopic, out)
	}

	if child := node.children["+"]; child != nil && !(level == 0 && dollarTopic) {
		tree.matchNode(child, level+1, topicLevels, dollarTopic, out)
	}
}
