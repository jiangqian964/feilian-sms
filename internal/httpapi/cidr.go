package httpapi

import (
	"net"
	"net/http"
	"net/netip"

	"go.uber.org/zap"
)

// adminGuard 返回仅作用于管理面（/api 与 WebUI）的 CIDR 守卫。
// 判定严格基于直连 RemoteAddr：不解析 X-Forwarded-For 等任何可伪造头，
// 因为本服务设计上直接暴露在内网、不挂在反向代理之后。
// 未配置白名单（adminNets 为空）时不做网络层限制。
func (s *Server) adminGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.adminNets) > 0 && !s.allowAdmin(r.RemoteAddr) {
			s.deps.Logger.Warn("管理端访问被 CIDR 白名单拒绝",
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("path", r.URL.Path),
				zap.String("method", r.Method))
			writeErrorJSON(w, http.StatusForbidden, codeForbidden, "来源地址不在管理端白名单内")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowAdmin 判定直连地址是否命中任一管理网段；IPv4 映射的 IPv6 地址先解映射。
func (s *Server) allowAdmin(remoteAddr string) bool {
	addr, ok := parseDirectAddr(remoteAddr)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range s.adminNets {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// parseDirectAddr 只解析 RemoteAddr（host:port 或裸 host），绝不触碰代理头。
func parseDirectAddr(remoteAddr string) (netip.Addr, bool) {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr, true
}
