package service

import "context"

// Probe readiness never evicts an account or synthesizes a production 503.
// Optional route failures are handled before forwarding by the route adapter.
func (s *OpenAIGatewayService) astraRouteFailover(_ context.Context, _ *Account, err error) error {
	return err
}

func (s *OpenAIGatewayService) checkAstraSchedulingRoute(context.Context, *Account) error {
	return nil
}
