package workflow

import (
	"go.temporal.io/sdk/log"
	"go.uber.org/zap"
)

type zapTemporalLogger struct{ l *zap.Logger }

func newZapTemporalLogger(l *zap.Logger) log.Logger { return &zapTemporalLogger{l: l} }

func (z *zapTemporalLogger) Debug(msg string, kvs ...interface{}) {
	z.l.Sugar().Debugw(msg, kvs...)
}
func (z *zapTemporalLogger) Info(msg string, kvs ...interface{}) {
	z.l.Sugar().Infow(msg, kvs...)
}
func (z *zapTemporalLogger) Warn(msg string, kvs ...interface{}) {
	z.l.Sugar().Warnw(msg, kvs...)
}
func (z *zapTemporalLogger) Error(msg string, kvs ...interface{}) {
	z.l.Sugar().Errorw(msg, kvs...)
}
