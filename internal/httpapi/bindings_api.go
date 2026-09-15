package httpapi

import (
	"fmt"
	"net/http"

	"feilian-sms/internal/feilian"
	"feilian-sms/internal/store"
)

// bindingItem 是单条场景绑定的表单项。
type bindingItem struct {
	SMSType      string `json:"sms_type"`
	ChannelID    string `json:"channel_id"`
	TemplateCode string `json:"template_code"`
	ParamIndex   []int  `json:"param_index"`
	Enabled      bool   `json:"enabled"`
}

// bindingsResponse 同时回传 9 种 sms_type 目录（供表格渲染）与已存绑定。
type bindingsResponse struct {
	SMSTypes []string        `json:"sms_types"`
	Bindings []store.Binding `json:"bindings"`
}

type bindingsUpdate struct {
	Bindings []bindingItem `json:"bindings"`
}

func (s *Server) listBindings(w http.ResponseWriter, r *http.Request) {
	bindings, err := s.deps.Store.ListBindings(r.Context())
	if err != nil {
		s.failInternal(w, r, "读取场景绑定失败", err)
		return
	}
	if bindings == nil {
		bindings = []store.Binding{}
	}
	writeJSON(w, bindingsResponse{
		SMSTypes: append([]string(nil), feilian.KnownSMSTypes...),
		Bindings: bindings,
	})
}

func (s *Server) replaceBindings(w http.ResponseWriter, r *http.Request) {
	var req bindingsUpdate
	if !decodeAdminBody(w, r, &req) {
		return
	}

	channels, err := s.deps.Store.ListChannels(r.Context())
	if err != nil {
		s.failInternal(w, r, "读取通道列表失败", err)
		return
	}
	channelSet := make(map[string]struct{}, len(channels))
	for _, c := range channels {
		channelSet[c.ID] = struct{}{}
	}

	// 全量校验通过后才写库（拒绝半成品批次）。
	seen := make(map[string]struct{}, len(req.Bindings))
	out := make([]store.Binding, 0, len(req.Bindings))
	for i, b := range req.Bindings {
		row := fmt.Sprintf("bindings[%d]", i)
		if !feilian.IsKnownSMSType(b.SMSType) {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "bindings",
				fmt.Sprintf("%s.sms_type %q 不是飞连支持的场景类型", row, b.SMSType))
			return
		}
		if _, dup := seen[b.SMSType]; dup {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "bindings",
				fmt.Sprintf("sms_type %q 在同批中重复", b.SMSType))
			return
		}
		seen[b.SMSType] = struct{}{}
		if _, ok := channelSet[b.ChannelID]; !ok {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "bindings",
				fmt.Sprintf("%s.channel_id %q 指向不存在的通道", row, b.ChannelID))
			return
		}
		if b.TemplateCode == "" {
			writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "bindings",
				fmt.Sprintf("%s.template_code 不能为空", row))
			return
		}
		for _, idx := range b.ParamIndex {
			if idx < 0 {
				writeFieldErrorJSON(w, http.StatusBadRequest, codeBadRequest, "bindings",
					fmt.Sprintf("%s.param_index 不能包含负下标 %d", row, idx))
				return
			}
		}
		out = append(out, store.Binding{
			SMSType:      b.SMSType,
			ChannelID:    b.ChannelID,
			TemplateCode: b.TemplateCode,
			ParamIndex:   append([]int(nil), b.ParamIndex...),
			Enabled:      b.Enabled,
		})
	}

	if err := s.deps.Store.ReplaceBindings(r.Context(), out); err != nil {
		s.failInternal(w, r, "批量保存场景绑定失败", err)
		return
	}
	s.listBindings(w, r)
}
