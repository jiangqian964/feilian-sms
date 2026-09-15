package logging

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// 端到端：经包装 core 写出的 JSON，消息文本与各字段都不得含完整手机号/验证码/密钥/签名。
func TestNewJSONRedactsEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	logger, err := newWithWriteSyncer("debug", "json", zapcore.AddSync(&buf))
	if err != nil {
		t.Fatalf("构造 logger 失败: %v", err)
	}
	logger.Info("向 13812345678 下发",
		zap.String("channel_id", "ch-1"),
		zap.String("params", `["482916","5"]`),
		zap.String("sign", "5d00b67e6710f1dae41db054329fd1fefebd144e"),
		zap.String("app_secret", "demo-app-secret"),
		zap.String("reason", "正常"))
	out := buf.String()
	for _, leak := range []string{"13812345678", "482916", "demo-app-secret", "5d00b67e6710f1dae41db054329fd1fefebd144e"} {
		if strings.Contains(out, leak) {
			t.Fatalf("JSON 日志泄漏敏感值 %q：%s", leak, out)
		}
	}
	for _, want := range []string{"138******78", redactedPlaceholder, `"channel_id":"ch-1"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("JSON 日志缺少期望内容 %q：%s", want, out)
		}
	}
}

// logger.With 预绑定的敏感字段在后续每条日志都必须脱敏（core.With 也要被包装）。
func TestRedactCoreWith(t *testing.T) {
	var buf bytes.Buffer
	logger, err := newWithWriteSyncer("info", "json", zapcore.AddSync(&buf))
	if err != nil {
		t.Fatalf("构造 logger 失败: %v", err)
	}
	base := logger.With(zap.String("authorization", "Bearer abcdef"), zap.String("event_id", "evt-1"))
	base.Info("第一条")
	base.Warn("第二条 13812345678")
	out := buf.String()
	if strings.Contains(out, "Bearer abcdef") {
		t.Fatalf("With 预绑定鉴权头泄漏：%s", out)
	}
	if strings.Contains(out, "13812345678") {
		t.Fatalf("With 场景手机号泄漏：%s", out)
	}
	if !strings.Contains(out, `"event_id":"evt-1"`) {
		t.Fatalf("With 非敏感字段应保留：%s", out)
	}
}

// console 格式与 Error 级堆栈同样不能泄漏。
func TestNewConsoleRedactsStack(t *testing.T) {
	var buf bytes.Buffer
	logger, err := newWithWriteSyncer("debug", "console", zapcore.AddSync(&buf))
	if err != nil {
		t.Fatalf("构造 logger 失败: %v", err)
	}
	logger.Error("向 13812345678 下发失败", zap.String("data_key", "secret-key-bytes"))
	out := buf.String()
	if strings.Contains(out, "13812345678") || strings.Contains(out, "secret-key-bytes") {
		t.Fatalf("console 日志泄漏敏感值：%s", out)
	}
}

// NewWithCore 供业务包测试：observer 放在脱敏 core 下游，
// 看到的就是“实际落盘内容”，可据此断言无敏感泄漏。
func TestNewWithCoreObserverSeesRedacted(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	logger := NewWithCore(core)
	logger.Info("向 13812345678 下发", zap.String("sign", "abc123def456"))
	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("observer 应有 1 条记录，实际 %d", len(entries))
	}
	raw := entries[0].Message + " " + fmt.Sprint(entries[0].ContextMap())
	if strings.Contains(raw, "13812345678") || strings.Contains(raw, "abc123def456") {
		t.Fatalf("observer 不应看到未脱敏内容：%s", raw)
	}
}

func TestNewRejectsBadLevel(t *testing.T) {
	if _, err := newWithWriteSyncer("verbose", "json", zapcore.AddSync(&bytes.Buffer{})); err == nil {
		t.Fatal("非法日志级别应返回错误")
	}
}

func TestNewRejectsBadFormatDoesNotError(t *testing.T) {
	// 未知格式按生产 JSON 兜底，不报错；仅级别非法才拒绝。
	var buf bytes.Buffer
	if _, err := newWithWriteSyncer("info", "weird", zapcore.AddSync(&buf)); err != nil {
		t.Fatalf("未知格式应兜底而非报错: %v", err)
	}
}
