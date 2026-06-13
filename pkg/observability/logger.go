package observability

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger builds a production-grade structured logger.
// prod=true → JSON, INFO level; prod=false → console, DEBUG level.
// Fixed fields injected on every log line: app, env, host.
func NewLogger(env string) (*zap.Logger, error) {
	prod := env == "production"

	level := zapcore.DebugLevel
	if prod {
		level = zapcore.InfoLevel
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.LevelKey = "level"
	encCfg.CallerKey = "caller"
	encCfg.MessageKey = "msg"
	encCfg.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	encCfg.EncodeLevel = zapcore.LowercaseLevelEncoder
	encCfg.EncodeCaller = zapcore.ShortCallerEncoder

	var enc zapcore.Encoder
	if prod {
		enc = zapcore.NewJSONEncoder(encCfg)
	} else {
		encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		enc = zapcore.NewConsoleEncoder(encCfg)
	}

	host, _ := os.Hostname()
	core := zapcore.NewCore(enc, zapcore.Lock(os.Stdout), level)
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)).With(
		zap.String("app", "stratum"),
		zap.String("env", env),
		zap.String("host", host),
	)
	return logger, nil
}

// Logger is kept for backward-compatibility; embed *zap.Logger directly in new code.
type Logger struct {
	*zap.Logger
}
