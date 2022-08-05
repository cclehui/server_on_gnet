package commonutil

import (
	"context"
	"encoding/json"
	"fmt"
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

type DefaultLogger struct{}

func (l *DefaultLogger) Debugf(ctx context.Context, format string, args ...interface{}) {
	content := fmt.Sprintf(format, args...)
	logStr, _ := (&LogData{Level: LevelDebug, Content: content}).EncodeToStr()
	fmt.Println(string(logStr))
}

func (l *DefaultLogger) Infof(ctx context.Context, format string, args ...interface{}) {
	content := fmt.Sprintf(format, args...)
	logStr, _ := (&LogData{Level: LevelInfo, Content: content}).EncodeToStr()
	fmt.Println(string(logStr))
}

func (l *DefaultLogger) Warnf(ctx context.Context, format string, args ...interface{}) {
	content := fmt.Sprintf(format, args...)
	logStr, _ := (&LogData{Level: LevelWarn, Content: content}).EncodeToStr()
	fmt.Println(string(logStr))
}

func (l *DefaultLogger) Errorf(ctx context.Context, format string, args ...interface{}) {
	content := fmt.Sprintf(format, args...)
	logStr, _ := (&LogData{Level: LevelError, Content: content}).EncodeToStr()
	fmt.Println(string(logStr))
}
