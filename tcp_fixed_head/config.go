package tcp_fixed_head

import "fmt"

// ActionName 把 ActionType 转成日志里可读的名字
func ActionName(actionType uint16) string {
	switch actionType {
	case ACTION_PING:
		return "PING"
	case ACTION_PONG:
		return "PONG"
	case ACTION_DATA:
		return "DATA"
	default:
		return fmt.Sprintf("UNKNOWN(0x%04X)", actionType)
	}
}

func isCorrectAction(actionType uint16) bool {
	switch actionType {
	case ACTION_PING, ACTION_PONG, ACTION_DATA:
		return true
	default:
		return false
	}
}
