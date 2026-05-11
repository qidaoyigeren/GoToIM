package log

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// L is the global logger. Call Init() before use.
var L *zap.Logger
var sugar *zap.SugaredLogger

func init() {
	// Default to a nop logger until Init is called
	L = zap.NewNop()
	sugar = L.Sugar()
}

// Init initializes the global logger. Call once at startup.
// development=true: debug level, console encoder, stacktraces on warn+
// development=false: info level, json encoder, stacktraces on error+
func Init(development bool) error {
	var cfg zap.Config
	if development {
		cfg = zap.NewDevelopmentConfig()
		cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	} else {
		cfg = zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}
	var err error
	L, err = cfg.Build()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	sugar = L.Sugar()
	return nil
}

// Sync flushes any buffered log entries.
func Sync() error {
	if L != nil {
		return L.Sync()
	}
	return nil
}

// Infof logs at info level with printf-style formatting.
func Infof(template string, args ...interface{}) {
	sugar.Infof(template, args...)
}

// Warningf logs at warn level with printf-style formatting.
func Warningf(template string, args ...interface{}) {
	sugar.Warnf(template, args...)
}

// Errorf logs at error level with printf-style formatting.
func Errorf(template string, args ...interface{}) {
	sugar.Errorf(template, args...)
}

// Info logs at info level.
func Info(args ...interface{}) {
	sugar.Info(args...)
}

// Warning logs at warn level.
func Warning(args ...interface{}) {
	sugar.Warn(args...)
}

// Error logs at error level.
func Error(args ...interface{}) {
	sugar.Error(args...)
}

// Fatal logs at fatal level then calls os.Exit(1).
func Fatal(args ...interface{}) {
	sugar.Fatal(args...)
}

// Fatalf logs at fatal level with printf-style formatting then calls os.Exit(1).
func Fatalf(template string, args ...interface{}) {
	sugar.Fatalf(template, args...)
}

// verboseLogger mimics glog's V(level).Infof() pattern.
type verboseLogger struct {
	level zapcore.Level
}

// V returns a verbose logger at the given level.
// V(0) = info, V(1) = debug, V(2) = debug (lower priority).
func V(level int) verboseLogger {
	switch {
	case level <= 0:
		return verboseLogger{level: zapcore.InfoLevel}
	case level == 1:
		return verboseLogger{level: zapcore.DebugLevel}
	default:
		return verboseLogger{level: zapcore.DebugLevel}
	}
}

// Infof logs at the verbose level if enabled.
func (v verboseLogger) Infof(template string, args ...interface{}) {
	if L.Core().Enabled(v.level) {
		if ce := L.Check(v.level, fmt.Sprintf(template, args...)); ce != nil {
			ce.Write()
		}
	}
}
