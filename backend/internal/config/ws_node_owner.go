package config

import "fmt"

// WSNodeOwnerConfig is an opt-in safety gate, not a cross-node router.
type WSNodeOwnerConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	NodeID  string `mapstructure:"node_id"`
}

func (c WSNodeOwnerConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if len(c.NodeID) < 1 || len(c.NodeID) > 64 {
		return fmt.Errorf("runtime.ws_node_owner.node_id requires 1 to 64 safe identifier characters")
	}
	for _, ch := range c.NodeID {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' {
			return fmt.Errorf("runtime.ws_node_owner.node_id only permits lowercase letters, digits, - and _")
		}
	}
	return nil
}
