package handler

import "github.com/Wei-Shaw/sub2api/internal/service"

func (h *GatewayHandler) ConfigureAPIKeyGroupRouting(keys *service.APIKeyService) {
	if h != nil && h.gatewayService != nil && keys != nil {
		keys.SetGroupRouter(h.gatewayService)
	}
}
