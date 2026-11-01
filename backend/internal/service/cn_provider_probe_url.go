package service

// CN 供应商探测端点的出站 URL 安全策略校验（配额/余额探测共用）。
//
// 背景（review B4）：这两条探测路径会把账号 API key 发往 base_url 衍生端点，
// 此前完全绕过 security.url_allowlist——在加固部署里构成任意外发与内网探测
// 面（本项目此前发生过账号测试 SSRF 生产事件）。与网关转发
// （validateUpstreamBaseURL）、Grok 探测（grokOperatorPolicyValidator）一致，
// 探测发起前必须过同一套运营者策略。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// cnValidateProbeURL 按全局出站 URL 安全策略校验探测端点，返回规范化 URL。
// 白名单开启时强制 UpstreamHosts；关闭或 cfg 为空时仍默认拒绝私网目标。
// 只有显式启用白名单并允许私网时，探测端点才可落在私网地址。
func cnValidateProbeURL(cfg *config.Config, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("probe url is required")
	}

	opts := urlvalidator.ValidationOptions{}
	var allowInsecureHTTP bool
	if cfg != nil {
		policy := cfg.Security.URLAllowlist
		allowInsecureHTTP = policy.AllowInsecureHTTP
		if policy.Enabled {
			opts.AllowedHosts = policy.UpstreamHosts
			opts.RequireAllowlist = true
			opts.AllowPrivate = policy.AllowPrivateHosts
		}
	}
	normalized, err := urlvalidator.ValidateHTTPURL(trimmed, allowInsecureHTTP, opts)
	if err != nil {
		return "", fmt.Errorf("probe target rejected by URL security policy: %w", err)
	}
	return normalized, nil
}

func cnProbeRequestContext(ctx context.Context, cfg *config.Config) context.Context {
	ctx = WithHTTPUpstreamRedirectsDisabled(ctx)
	allowPrivate := cfg != nil &&
		cfg.Security.URLAllowlist.Enabled &&
		cfg.Security.URLAllowlist.AllowPrivateHosts
	if !allowPrivate {
		ctx = WithHTTPUpstreamPublicHostsOnly(ctx)
	}
	return ctx
}
