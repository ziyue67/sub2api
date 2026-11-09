package domain

// APIKeyGroupRouting is the sole authority for newly saved routing policies.
// A nil policy preserves the legacy single/default-group behavior.
type APIKeyGroupRouting struct {
	Strategy string             `json:"strategy"`
	Failover bool               `json:"failover"`
	Routes   []APIKeyGroupRoute `json:"routes"`
}

// APIKeyGroupRoute configures a candidate group for one API key.
// Lower priority values win; equal-priority routes use Weight.
type APIKeyGroupRoute struct {
	GroupID         int64 `json:"group_id"`
	Priority        int   `json:"priority"`
	Weight          int   `json:"weight"`
	Enabled         bool  `json:"enabled"`
	CooldownSeconds int   `json:"cooldown_seconds"`
}
