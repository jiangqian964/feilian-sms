// Package bootstrap 只负责进程启动期的引导配置：
// 监听地址、SQLite 路径、日志、数据密钥来源、管理端 CIDR。
// 所有业务配置（飞连参数、通道、绑定）一律存 SQLite、在 WebUI 管理，
// 不进入本 YAML，避免业务变更需要重新发版或登录主机改文件。
package bootstrap

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是引导配置根对象。
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	SQLite  SQLiteConfig  `yaml:"sqlite"`
	Log     LogConfig     `yaml:"log"`
	Secrets SecretsConfig `yaml:"secrets"`
}

// ServerConfig 控制 HTTP 监听与管理端访问控制。
type ServerConfig struct {
	// Listen 形如 ":8080" 或 "127.0.0.1:8080"。
	Listen string `yaml:"listen"`
	// AdminCIDRs 可选，作用于 /api 与 UI；为空表示不做网络层限制。
	AdminCIDRs []string `yaml:"admin_cidrs"`
}

// SQLiteConfig 指向 SQLite 数据库文件。
type SQLiteConfig struct {
	Path string `yaml:"path"`
}

// LogConfig 为 Zap 日志配置（T14 接线）。
type LogConfig struct {
	// Level: debug/info/warn/error，默认 info。
	Level string `yaml:"level"`
	// Format: json/console，默认 json。
	Format string `yaml:"format"`
}

// SecretsConfig 决定通道密钥 AES-GCM 主密钥（data key）来源。
type SecretsConfig struct {
	// DataKey 为内联密钥（base64 编码的 32 字节），通常由环境变量注入；
	// 非空时优先于 KeyPath，且不会创建密钥文件。
	DataKey string `yaml:"data_key"`
	// KeyPath 为密钥文件路径；为空时默认与 SQLite 文件同目录的 data.key，
	// 首启缺失则自动生成 0600 文件。
	KeyPath string `yaml:"key_path"`
}

// 支持的环境变量：SMSGW_ 前缀 + 大写下划线路径，列表以英文逗号分隔。
const (
	envServerListen    = "SMSGW_SERVER_LISTEN"
	envServerAdminCIDR = "SMSGW_SERVER_ADMIN_CIDRS"
	envSQLitePath      = "SMSGW_SQLITE_PATH"
	envLogLevel        = "SMSGW_LOG_LEVEL"
	envLogFormat       = "SMSGW_LOG_FORMAT"
	envSecretsDataKey  = "SMSGW_SECRETS_DATA_KEY"
	envSecretsKeyPath  = "SMSGW_SECRETS_KEY_PATH"
)

var (
	validLogLevels  = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	validLogFormats = map[string]bool{"json": true, "console": true}
)

// Default 返回引导默认值（当前仅日志两项有默认）。
func Default() Config {
	return Config{
		Log: LogConfig{Level: "info", Format: "json"},
	}
}

// Load 读取 YAML 引导文件（path 为空表示纯环境变量启动），
// 叠加 SMSGW_* 环境变量覆盖，最后校验并返回完整配置。
func Load(path string) (*Config, error) {
	c := Default()

	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取引导配置文件失败: %w", err)
		}
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("解析引导配置 YAML 失败: %w", err)
		}
	}

	applyEnv(&c)

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func applyEnv(c *Config) {
	if v, ok := os.LookupEnv(envServerListen); ok {
		c.Server.Listen = v
	}
	if v, ok := os.LookupEnv(envServerAdminCIDR); ok {
		c.Server.AdminCIDRs = splitCSV(v)
	}
	if v, ok := os.LookupEnv(envSQLitePath); ok {
		c.SQLite.Path = v
	}
	if v, ok := os.LookupEnv(envLogLevel); ok {
		c.Log.Level = v
	}
	if v, ok := os.LookupEnv(envLogFormat); ok {
		c.Log.Format = v
	}
	if v, ok := os.LookupEnv(envSecretsDataKey); ok {
		c.Secrets.DataKey = v
	}
	if v, ok := os.LookupEnv(envSecretsKeyPath); ok {
		c.Secrets.KeyPath = v
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Validate 校验引导配置，错误信息为中文并带字段标识，供启动快速失败。
func (c *Config) Validate() error {
	var errs []string

	if strings.TrimSpace(c.Server.Listen) == "" {
		errs = append(errs, "server.listen 不能为空（示例 :8080）")
	} else if _, _, err := net.SplitHostPort(c.Server.Listen); err != nil {
		errs = append(errs, fmt.Sprintf("server.listen 格式非法: %v", err))
	}

	if strings.TrimSpace(c.SQLite.Path) == "" {
		errs = append(errs, "sqlite.path 不能为空（示例 /var/lib/sms-gateway/sms.db）")
	}

	if c.Log.Level == "" {
		c.Log.Level = "info"
	} else if !validLogLevels[c.Log.Level] {
		errs = append(errs, fmt.Sprintf("log.level 非法 %q，可选 debug/info/warn/error", c.Log.Level))
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	} else if !validLogFormats[c.Log.Format] {
		errs = append(errs, fmt.Sprintf("log.format 非法 %q，可选 json/console", c.Log.Format))
	}

	for i, cidr := range c.Server.AdminCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			errs = append(errs, fmt.Sprintf("server.admin_cidrs[%d] 非法 %q: %v", i, cidr, err))
		}
	}

	return errors.Join(errsToErrors(errs)...)
}

func errsToErrors(items []string) []error {
	out := make([]error, 0, len(items))
	for _, m := range items {
		out = append(out, errors.New(m))
	}
	return out
}
