package commonutil

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const (
	LevelDebug = "DEBUG"
	LevelInfo  = "INFO"
	LevelWarn  = "WARN"
	LevelError = "ERROR"

	LogPrefix = "SERVER_ON_GNET"
)

type LogData struct {
	Prefix  string `json:"prefix"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Content string `json:"content"`
}

func (ld *LogData) EncodeToStr() (string, error) {
	if ld.Prefix == "" {
		ld.Prefix = LogPrefix
	}

	if ld.Time == "" {
		ld.Time = time.Now().Format("2006-01-02 15:04:05")
	}

	resBytes, err := json.Marshal(ld)

	return string(resBytes), err
}

type Logger interface {
	Debugf(ctx context.Context, format string, args ...interface{})
	Infof(ctx context.Context, format string, args ...interface{})
	Warnf(ctx context.Context, format string, args ...interface{})
	Errorf(ctx context.Context, format string, args ...interface{})
}

var logger Logger = &DefaultLogger{}

func SetLogger(newLogger Logger) {
	logger = newLogger
}

func GetLogger() Logger {
	return logger
}

type DefaultLogger struct {
	// reactor 协程、worker pool、示例里的统计协程都会并发打日志, 而 os.Stdout
	// 的一次 Write 不保证字节不被打断, 不加锁时两行日志会在终端里交错成坏行
	mu sync.Mutex
}

func (l *DefaultLogger) output(level string, format string, args ...interface{}) {
	content := fmt.Sprintf(format, args...)
	logStr, err := (&LogData{Level: level, Content: content}).EncodeToStr()

	l.mu.Lock()
	if err != nil {
		fmt.Printf("{\"level\":%q,\"content\":%q}\n", level, content)
	} else {
		fmt.Println(logStr)
	}
	l.mu.Unlock()
}

func (l *DefaultLogger) Debugf(ctx context.Context, format string, args ...interface{}) {
	l.output(LevelDebug, format, args...)
}

func (l *DefaultLogger) Infof(ctx context.Context, format string, args ...interface{}) {
	l.output(LevelInfo, format, args...)
}

func (l *DefaultLogger) Warnf(ctx context.Context, format string, args ...interface{}) {
	l.output(LevelWarn, format, args...)
}

func (l *DefaultLogger) Errorf(ctx context.Context, format string, args ...interface{}) {
	l.output(LevelError, format, args...)
}
