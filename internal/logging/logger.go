package logging

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New 按引导配置构造 Zap 日志：json（生产，journald 采集）/ console（本地排障）。
// 返回的 logger 所有出口都经过 redactCore 脱敏。
func New(level, format string) (*zap.Logger, error) {
	return newWithWriteSyncer(level, format, zapcore.Lock(os.Stdout))
}

// newWithWriteSyncer 与 New 相同但允许注入输出目标，供单元测试捕获日志文本。
func newWithWriteSyncer(level, format string, ws zapcore.WriteSyncer) (*zap.Logger, error) {
	parsed, err := zapcore.ParseLevel(level)
	if err != nil {
		return nil, err
	}
	var enc zapcore.Encoder
	if format == "console" {
		enc = zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	} else {
		enc = zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	}
	base := zapcore.NewCore(enc, ws, zap.NewAtomicLevelAt(parsed))
	return NewWithCore(base), nil
}

// NewWithCore 用脱敏 core 包装任意底层 core；业务包测试可挂接 zaptest
// observer 到底层，从而对“实际落盘内容”做无泄漏断言。
func NewWithCore(base zapcore.Core) *zap.Logger {
	return zap.New(&redactCore{Core: base},
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel))
}

// redactCore 在日志落盘前统一脱敏消息文本、堆栈与全部字段。
// 必须同时包装 With/Write/Check：Check 若透传到底层 core，
// 后续 Write 会绕过本包装，脱敏即失效。
type redactCore struct {
	zapcore.Core
}

func (c *redactCore) With(fields []zap.Field) zapcore.Core {
	return &redactCore{Core: c.Core.With(redactFields(fields))}
}

func (c *redactCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *redactCore) Write(ent zapcore.Entry, fields []zap.Field) error {
	ent.Message = RedactString(ent.Message)
	ent.Stack = RedactString(ent.Stack)
	return c.Core.Write(ent, redactFields(fields))
}
