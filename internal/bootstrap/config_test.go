package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempConfig 把内容写入临时 YAML 文件，返回路径。
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	return p
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		env       map[string]string
		wantErr   string
		assertion func(t *testing.T, c *Config)
	}{
		{
			name: "完整合法配置",
			yaml: `
server:
  listen: ":9090"
  admin_cidrs:
    - "10.0.0.0/8"
    - "192.168.1.0/24"
sqlite:
  path: "/var/lib/smsgw/sms.db"
log:
  level: "debug"
  format: "console"
secrets:
  key_path: "/etc/smsgw/data.key"
`,
			assertion: func(t *testing.T, c *Config) {
				if c.Server.Listen != ":9090" {
					t.Errorf("listen = %q", c.Server.Listen)
				}
				if len(c.Server.AdminCIDRs) != 2 || c.Server.AdminCIDRs[1] != "192.168.1.0/24" {
					t.Errorf("admin_cidrs = %#v", c.Server.AdminCIDRs)
				}
				if c.SQLite.Path != "/var/lib/smsgw/sms.db" {
					t.Errorf("sqlite.path = %q", c.SQLite.Path)
				}
				if c.Log.Level != "debug" || c.Log.Format != "console" {
					t.Errorf("log = %#v", c.Log)
				}
				if c.Secrets.KeyPath != "/etc/smsgw/data.key" {
					t.Errorf("key_path = %q", c.Secrets.KeyPath)
				}
			},
		},
		{
			name: "最小合法配置并应用日志默认值",
			yaml: `
server:
  listen: "127.0.0.1:8080"
sqlite:
  path: "./sms.db"
`,
			assertion: func(t *testing.T, c *Config) {
				if c.Log.Level != "info" {
					t.Errorf("默认 level = %q, 期望 info", c.Log.Level)
				}
				if c.Log.Format != "json" {
					t.Errorf("默认 format = %q, 期望 json", c.Log.Format)
				}
			},
		},
		{
			name:    "缺少 listen 与 sqlite.path",
			yaml:    "log:\n  level: info\n",
			wantErr: "listen",
		},
		{
			name:    "只缺 listen",
			yaml:    "sqlite:\n  path: \"./sms.db\"\n",
			wantErr: "listen",
		},
		{
			name:    "只缺 sqlite.path",
			yaml:    "server:\n  listen: \":8080\"\n",
			wantErr: "sqlite",
		},
		{
			name:    "非法 YAML",
			yaml:    "server:\n  listen: [unterminated\n",
			wantErr: "yaml",
		},
		{
			name:    "未知字段被拒绝",
			yaml:    "server:\n  listen: \":8080\"\n  lisn: \"x\"\nsqlite:\n  path: \"./sms.db\"\n",
			wantErr: "lisn",
		},
		{
			name: "非法日志级别",
			yaml: `
server:
  listen: ":8080"
sqlite:
  path: "./sms.db"
log:
  level: "trace"
`,
			wantErr: "log.level",
		},
		{
			name: "非法日志格式",
			yaml: `
server:
  listen: ":8080"
sqlite:
  path: "./sms.db"
log:
  format: "xml"
`,
			wantErr: "log.format",
		},
		{
			name: "非法 admin CIDR",
			yaml: `
server:
  listen: ":8080"
  admin_cidrs:
    - "not-a-cidr"
sqlite:
  path: "./sms.db"
`,
			wantErr: "admin_cidrs",
		},
		{
			name: "环境变量覆盖全部引导项",
			yaml: `
server:
  listen: ":8080"
  admin_cidrs:
    - "10.0.0.0/8"
sqlite:
  path: "./sms.db"
log:
  level: info
  format: json
secrets:
  key_path: "./old.key"
`,
			env: map[string]string{
				"SMSGW_SERVER_LISTEN":      ":7070",
				"SMSGW_SERVER_ADMIN_CIDRS": "172.16.0.0/12,127.0.0.1/32",
				"SMSGW_SQLITE_PATH":        "/data/sms.db",
				"SMSGW_LOG_LEVEL":          "error",
				"SMSGW_LOG_FORMAT":         "console",
				"SMSGW_SECRETS_DATA_KEY":   "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXowMTIzNDU=",
				"SMSGW_SECRETS_KEY_PATH":   "/data/new.key",
			},
			assertion: func(t *testing.T, c *Config) {
				if c.Server.Listen != ":7070" {
					t.Errorf("env listen = %q", c.Server.Listen)
				}
				if len(c.Server.AdminCIDRs) != 2 || c.Server.AdminCIDRs[0] != "172.16.0.0/12" {
					t.Errorf("env admin_cidrs = %#v", c.Server.AdminCIDRs)
				}
				if c.SQLite.Path != "/data/sms.db" {
					t.Errorf("env sqlite.path = %q", c.SQLite.Path)
				}
				if c.Log.Level != "error" || c.Log.Format != "console" {
					t.Errorf("env log = %#v", c.Log)
				}
				if c.Secrets.DataKey == "" || c.Secrets.KeyPath != "/data/new.key" {
					t.Errorf("env secrets = %#v", c.Secrets)
				}
			},
		},
		{
			name: "无配置文件时仅靠环境变量启动",
			env: map[string]string{
				"SMSGW_SERVER_LISTEN": ":8080",
				"SMSGW_SQLITE_PATH":   "/data/sms.db",
			},
			assertion: func(t *testing.T, c *Config) {
				if c.Server.Listen != ":8080" || c.SQLite.Path != "/data/sms.db" {
					t.Errorf("env-only 配置异常: %#v", c)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			path := ""
			if tt.yaml != "" {
				path = writeTempConfig(t, tt.yaml)
			}
			c, err := Load(path)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("期望错误包含 %q，实际 nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("错误 %q 不包含 %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("未期望错误: %v", err)
			}
			if tt.assertion != nil {
				tt.assertion(t, c)
			}
		})
	}
}

func TestConfigFilePathRequiredWhenNoEnv(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("文件不存在且无环境变量时应快速失败")
	}
}
