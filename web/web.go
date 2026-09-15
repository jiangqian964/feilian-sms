// Package web 通过 go:embed 内嵌 WebUI 静态资源，
// 保证部署到无 Node、无外网的 headless Ubuntu 时单二进制即可提供界面。
package web

import "embed"

// StaticFS 根目录对应仓库的 web/static 目录。
//
//go:embed all:static
var StaticFS embed.FS
