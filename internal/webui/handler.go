// Package webui 负责把内嵌静态资源包装为 HTTP 处理器。
// T10 仅挂载占位首页；T12 将在此基础上扩展为四视图管理界面。
package webui

import (
	"io/fs"
	"net/http"

	"feilian-sms/web"
)

// staticSubtree 是内嵌资源中对外提供服务的固定子目录。
const staticSubtree = "static"

// Handler 返回内嵌 web/static 的静态资源处理器（SPA 子树）。
func Handler() (http.Handler, error) {
	return handlerFrom(web.StaticFS, staticSubtree)
}

// handlerFrom 从任意 fs.FS 取出指定子树并构造文件服务器；
// 子树不存在时返回错误，保证打包路径配置错误能在启动时暴露。
func handlerFrom(root fs.FS, dir string) (http.Handler, error) {
	sub, err := fs.Sub(root, dir)
	if err != nil {
		return nil, err
	}
	return http.FileServerFS(sub), nil
}
