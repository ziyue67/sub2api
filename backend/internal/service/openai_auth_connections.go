package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/google/uuid"
)

// OpenAIAuthConnection is a safe management DTO. Credentials are never serialized.
// AccountID owns security, concurrency and billing; ID identifies only this grant.
// A subject is meaningful only with its issuer/client and verification flag.
type OpenAIAuthConnection struct {
	RefreshState               string     `json:"refresh_state"`
	RefreshAttemptID           string     `json:"-"`
	RefreshCandidateCiphertext string     `json:"-"`
	LeaseOwner                 string     `json:"-"`
	LeaseEpoch                 int64      `json:"-"`
	ResetRefresh               bool       `json:"-"`
	Legacy                     bool       `json:"legacy"`
	ID                         string     `json:"id"`
	AccountID                  int64      `json:"account_id"`
	Kind                       string     `json:"kind"`
	Label                      string     `json:"label"`
	Status                     string     `json:"status"`
	Issuer                     string     `json:"issuer"`
	ClientID                   string     `json:"client_id"`
	Subject                    string     `json:"subject,omitempty"`
	IdentityVerified           bool       `json:"identity_verified"`
	Workspace                  string     `json:"workspace,omitempty"`
	Resource                   string     `json:"resource,omitempty"`
	Audience                   string     `json:"audience,omitempty"`
	Scopes                     []string   `json:"scopes"`
	Capabilities               []string   `json:"capabilities"`
	HostID                     string     `json:"host_id,omitempty"`
	Revision                   int64      `json:"revision"`
	Preferred                  bool       `json:"preferred"`
	CooldownUntil              *time.Time `json:"cooldown_until,omitempty"`
	CredentialsCiphertext      string     `json:"-"`
}

// The lease proof is returned by the atomic acquisition, never reconstructed
// from a later registration snapshot owned by another process.
type OpenAIAuthConnectionLease struct {
	Owner string
	Epoch int64
}

type OpenAIAuthConnectionRepository interface {
	BeginAuthConnectionRefresh(context.Context, *OpenAIAuthConnection, string) (bool, error)
	MarkAuthConnectionRefresh(context.Context, *OpenAIAuthConnection, string) (bool, error)
	StageAuthConnectionRefresh(context.Context, *OpenAIAuthConnection, string) (bool, error)
	FinishAuthConnectionRefresh(context.Context, *OpenAIAuthConnection, string, string, string) (bool, error)
	AuthConnectionsManaged(context.Context, int64) (bool, error)
	ResolveAuthConnectionAffinity(context.Context, int64, int64, string, string) (string, error)
	UseLegacyAuthConnections(context.Context, int64) error
	ListAuthConnections(context.Context, int64) ([]*OpenAIAuthConnection, error)
	CreateAuthConnection(context.Context, *OpenAIAuthConnection) error
	CASAuthConnection(context.Context, *OpenAIAuthConnection, int64) (bool, error)
	SelectAuthConnection(context.Context, int64, string, int64) (bool, error)
	AcquireAuthConnectionLease(context.Context, int64, string, string) (bool, error)
	AcquireAuthConnectionLeaseProof(context.Context, int64, string, string) (*OpenAIAuthConnectionLease, error)
	ReleaseAuthConnectionLease(context.Context, int64, string, string) error
}

type CreateOpenAIAuthConnectionInput struct {
	Kind   string `json:"kind" binding:"required"`
	Label  string `json:"label" binding:"required"`
	APIKey string `json:"api_key"`
}

type UpdateOpenAIAuthConnectionInput struct {
	Revision int64  `json:"revision" binding:"required"`
	Action   string `json:"action" binding:"required"`
}

func (s *OpenAIOAuthReauthService) connectionRepository() (OpenAIAuthConnectionRepository, error) {
	if s == nil || s.repo == nil || s.accounts == nil || s.encryptor == nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_CONNECTIONS_UNAVAILABLE", "Authorization connection storage is unavailable")
	}
	r, ok := s.repo.(OpenAIAuthConnectionRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("OPENAI_CONNECTIONS_UNAVAILABLE", "Authorization connection storage is unavailable")
	}
	return r, nil
}

func (s *OpenAIOAuthReauthService) connectionAccount(ctx context.Context, id int64) (*Account, error) {
	a, err := s.accounts.GetAccount(ctx, id)
	if err != nil || a == nil {
		return nil, infraerrors.NotFound("OPENAI_CONNECTION_ACCOUNT_NOT_FOUND", "Account not found")
	}
	if a.Platform != PlatformOpenAI || a.IsCredentialShadow() || (a.Type != AccountTypeOAuth && a.Type != AccountTypeAPIKey) {
		return nil, infraerrors.BadRequest("OPENAI_CONNECTION_ACCOUNT_INVALID", "Connections require an OpenAI parent account")
	}
	return a, nil
}

// Management accepts only OAuth parents. Keep connectionAccount and the internal
// readers compatible with persisted API key grants still used by inference.
func (s *OpenAIOAuthReauthService) managementConnectionAccount(ctx context.Context, id int64) (*Account, error) {
	if _, err := s.connectionRepository(); err != nil {
		return nil, err
	}
	a, err := s.connectionAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Type != AccountTypeOAuth {
		return nil, infraerrors.BadRequest("OPENAI_CONNECTION_ACCOUNT_INVALID", "Connections require an OpenAI OAuth parent account")
	}
	return a, nil
}

func (s *OpenAIOAuthReauthService) ListAuthConnections(ctx context.Context, id int64) ([]*OpenAIAuthConnection, error) {
	if _, err := s.managementConnectionAccount(ctx, id); err != nil {
		return nil, err
	}
	return s.listRuntimeAuthConnections(ctx, id)
}

// Internal inference read: also accepts historical managed API key parents.
func (s *OpenAIOAuthReauthService) listRuntimeAuthConnections(ctx context.Context, id int64) ([]*OpenAIAuthConnection, error) {
	r, err := s.connectionRepository()
	if err != nil {
		return nil, err
	}
	if _, err = s.connectionAccount(ctx, id); err != nil {
		return nil, err
	}
	managed, err := r.ListAuthConnections(ctx, id)
	if err != nil {
		return nil, err
	}
	a, err := s.connectionAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	legacy, err := s.legacyAuthConnections(ctx, a)
	if err != nil {
		return nil, err
	}
	return append(managed, legacy...), nil
}

func (s *OpenAIOAuthReauthService) CreateAuthConnection(ctx context.Context, id int64, input CreateOpenAIAuthConnectionInput) (*OpenAIAuthConnection, error) {
	r, err := s.connectionRepository()
	if err != nil {
		return nil, err
	}
	if _, err = s.managementConnectionAccount(ctx, id); err != nil {
		return nil, err
	}
	input.Label = strings.TrimSpace(input.Label)
	if input.Label == "" || len(input.Label) > 120 {
		return nil, infraerrors.BadRequest("OPENAI_CONNECTION_LABEL_INVALID", "Use a connection label of 1–120 bytes")
	}
	c := &OpenAIAuthConnection{ID: uuid.NewString(), AccountID: id, Kind: input.Kind, Label: input.Label, Status: "pending", RefreshState: "idle", Revision: 1, Scopes: []string{}, Capabilities: []string{}}
	switch input.Kind {
	case "codex", "bps", "siwc":
		if input.APIKey != "" {
			return nil, infraerrors.BadRequest("OPENAI_CONNECTION_CREDENTIAL_MISMATCH", "An OAuth connection cannot accept an API key")
		}
		c.Issuer = "https://auth.openai.com"
		if input.Kind == "siwc" {
			c.HostID = "urn:uuid:" + uuid.NewString()
			c.Resource = openai.SIWCResource
		}
	default:
		return nil, infraerrors.BadRequest("OPENAI_CONNECTION_KIND_INVALID", "Unknown authorization connection kind")
	}
	if err = r.CreateAuthConnection(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *OpenAIOAuthReauthService) authConnection(ctx context.Context, accountID int64, id string) (*OpenAIAuthConnection, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, infraerrors.BadRequest("OPENAI_CONNECTION_ID_INVALID", "Invalid connection ID")
	}
	connections, err := s.listRuntimeAuthConnections(ctx, accountID)
	if err != nil {
		return nil, err
	}
	for _, c := range connections {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, infraerrors.NotFound("OPENAI_CONNECTION_NOT_FOUND", "Authorization connection not found")
}

func (s *OpenAIOAuthReauthService) UpdateAuthConnection(ctx context.Context, accountID int64, id string, input UpdateOpenAIAuthConnectionInput) (*OpenAIAuthConnection, error) {
	if _, err := s.managementConnectionAccount(ctx, accountID); err != nil {
		return nil, err
	}
	c, err := s.authConnection(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	if c.Legacy {
		return nil, infraerrors.BadRequest("OPENAI_CONNECTION_LEGACY_READ_ONLY", "Use the existing account controls for a legacy registration")
	}
	if input.Action == "disable" || input.Action == "enable" || input.Action == "unlink" {
		lease, lockErr := s.lockAuthConnection(ctx, accountID, id)
		if lockErr != nil {
			return nil, lockErr
		}
		defer lease.release()
		operationCtx, stopOperation := context.WithTimeout(ctx, 50*time.Second)
		defer stopOperation()
		ctx = operationCtx
		c, err = s.authConnection(ctx, accountID, id)
		if err != nil {
			return nil, err
		}
		if err = lease.bind(c); err != nil {
			return nil, err
		}
	}
	if c.Revision != input.Revision {
		return nil, connectionChanged()
	}
	r, err := s.connectionRepository()
	if err != nil {
		return nil, err
	}
	var changed bool
	switch input.Action {
	case "recover":
		if _, err = s.ConnectionAccessToken(ctx, accountID, id); err != nil {
			return nil, err
		}
		return s.authConnection(ctx, accountID, id)
	case "select":
		if c.Status != "active" || len(c.Capabilities) == 0 {
			return nil, infraerrors.BadRequest("OPENAI_CONNECTION_NOT_CAPABLE", "This connection does not authorize inference")
		}
		changed, err = r.SelectAuthConnection(ctx, accountID, id, input.Revision)
	case "disable":
		c.Status = "disabled"
		changed, err = r.CASAuthConnection(ctx, c, input.Revision)
	case "enable":
		if c.Status != "disabled" || c.CredentialsCiphertext == "" || c.RefreshState != "idle" {
			return nil, infraerrors.BadRequest("OPENAI_CONNECTION_NOT_AUTHORIZED", "Authorize this connection first")
		}
		c.Status = "active"
		changed, err = r.CASAuthConnection(ctx, c, input.Revision)
	case "unlink":
		// Local unbinding never claims remote revocation. Preserve registration
		// metadata for reauthorization; erase only this connection's token set.
		c.Status = "unlinked"
		c.CredentialsCiphertext = ""
		c.ResetRefresh = true
		c.Capabilities = []string{}
		changed, err = r.CASAuthConnection(ctx, c, input.Revision)
	default:
		return nil, infraerrors.BadRequest("OPENAI_CONNECTION_ACTION_INVALID", "Unknown connection action")
	}
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, connectionChanged()
	}
	return s.authConnection(ctx, accountID, id)
}

func connectionChanged() error {
	return infraerrors.Conflict("OPENAI_CONNECTION_CHANGED", "Connection changed; reload before retrying")
}

// An explicit connection marker must never become an implicit Codex grant.
func validateOpenAIConnectionKind(a *Account) error {
	if a == nil || a.Platform != PlatformOpenAI {
		return nil
	}
	kind := a.GetExtraString("openai_connection_kind")
	if kind != "" && a.AuthConnectionID == "" {
		return infraerrors.BadRequest("OPENAI_CONNECTION_NOT_CAPABLE", "Explicit connections require a bound authorization record")
	}
	if kind == "codex" && a.GetCredential("client_id") != openai.ClientID || kind == "bps" && a.GetCredential("client_id") != openai.ExcelClientID || kind == "siwc" && (!a.IsSIWCEnabled() || a.GetCredential("client_id") == "" || a.GetCredential("client_id") == openai.SIWCDynamicClientID) || kind == "api_key" && a.Type != AccountTypeAPIKey {
		return infraerrors.BadRequest("OPENAI_CONNECTION_CLIENT_MISMATCH", "Connection credentials do not match the authorization client")
	}
	switch kind {
	case "", "codex", "bps", "siwc", "api_key":
		return nil
	default:
		return infraerrors.BadRequest("OPENAI_CONNECTION_NOT_CAPABLE", "Unknown or identity-only authorization cannot be used for inference")
	}
}
