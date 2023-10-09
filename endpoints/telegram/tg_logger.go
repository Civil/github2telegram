package telegram

import (
	"fmt"
	"go.uber.org/zap"
	"strings"
)

type tgLogger struct {
	logger *zap.Logger

	// What -> With
	replacer *strings.Replacer
}

func (l *tgLogger) Debugf(format string, args ...any) {
	var res string
	if l.replacer != nil {
		res = l.replacer.Replace(fmt.Sprintf(format, args...))
	} else {
		res = fmt.Sprintf(format, args...)
	}
	l.logger.Info("telegram api debug message",
		zap.String("data", res),
	)
}

func (l *tgLogger) Errorf(format string, args ...any) {
	var res string
	if l.replacer != nil {
		res = l.replacer.Replace(fmt.Sprintf(format, args...))
	} else {
		res = fmt.Sprintf(format, args...)
	}
	l.logger.Error("telegram api error message",
		zap.String("data", res),
	)
}

func newTgLogger(logger *zap.Logger, replaces []string) *tgLogger {
	logger = logger.With(
		zap.String("source", "telegram"),
	)

	l := &tgLogger{
		logger: logger,
	}

	if len(replaces) > 0 {
		if len(replaces)%2 != 0 {
			logger.Fatal("replaces must be even", zap.Strings("replaces", replaces))
		}
		l.replacer = strings.NewReplacer(replaces...)
	}

	return l
}
