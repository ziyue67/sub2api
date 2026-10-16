package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"

	"github.com/Wei-Shaw/sub2api/internal/regional"
)

// Price is an immutable, text-token-only price contract. Rates are nano USD per
// million tokens. Input categories are disjoint (cached tokens are not included
// in InputTokens). Adapters must normalize provider usage before settlement.
// Media, tools, service tiers and per-request fees require another contract and
// are deliberately not representable here.
type Price struct {
	Model         string `json:"model"`
	Input         int64  `json:"input_nano_per_million"`
	Output        int64  `json:"output_nano_per_million"`
	CacheCreation int64  `json:"cache_creation_nano_per_million"`
	CacheRead     int64  `json:"cache_read_nano_per_million"`
	MaxInput      int64  `json:"max_input_tokens"`
	MaxOutput     int64  `json:"max_output_tokens"`
}

func (p Price) Validate() error {
	if p.Model == "" || p.Input < 0 || p.Output < 0 || p.CacheCreation < 0 || p.CacheRead < 0 || p.MaxInput <= 0 || p.MaxOutput <= 0 || p.MaxInput > 2147483647 || p.MaxOutput > 2147483647 {
		return errors.New("invalid bounded text pricing contract")
	}
	return nil
}

// Version is content addressed; modifying any rate or limit changes identity.
func (p Price) Version() string {
	raw, _ := json.Marshal(p)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (p Price) Charge(input, output, creation, read int64) (regional.Amount, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if input < 0 || output < 0 || creation < 0 || read < 0 || input > p.MaxInput || creation > p.MaxInput-input || read > p.MaxInput-input-creation || output > p.MaxOutput {
		return 0, errors.New("usage exceeds pricing contract bounds")
	}
	sum := new(big.Int)
	for _, pair := range [][2]int64{{input, p.Input}, {output, p.Output}, {creation, p.CacheCreation}, {read, p.CacheRead}} {
		sum.Add(sum, new(big.Int).Mul(big.NewInt(pair[0]), big.NewInt(pair[1])))
	}
	// Round UP once to the primary NUMERIC(20,8) quantum, never per category.
	sum.Add(sum, big.NewInt(9_999_999))
	sum.Quo(sum, big.NewInt(10_000_000))
	sum.Mul(sum, big.NewInt(10))
	if !sum.IsInt64() {
		return 0, errors.New("price exceeds ledger amount range")
	}
	return regional.Amount(sum.Int64()), nil
}

// Maximum reserves the entire model input window, not a character/token
// estimate. Cache writes may be more expensive than ordinary input.
func (p Price) Maximum(output int64) (regional.Amount, error) {
	if output <= 0 || output > p.MaxOutput {
		return 0, errors.New("explicit bounded output limit required")
	}
	ceiling := p
	ceiling.Input = max(p.Input, p.CacheCreation, p.CacheRead)
	return ceiling.Charge(p.MaxInput, output, 0, 0)
}

func InstallPrice(ctx context.Context, db *sql.DB, p Price) error {
	if err := p.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO regional_prices(version,price) VALUES($1,$2) ON CONFLICT DO NOTHING`, p.Version(), string(raw))
	if err != nil {
		return err
	}
	var old []byte
	if err = db.QueryRowContext(ctx, `SELECT price FROM regional_prices WHERE version=$1`, p.Version()).Scan(&old); err != nil {
		return err
	}
	if !sameJSON(old, raw) {
		return ErrConflict
	}
	return nil
}

// PricingVerifier independently loads authority-owned pricing, validates the
// final amount and inserts the canonical usage row within Primary.Settle's
// transaction. Scope MUST verify durable admission and all quota dimensions;
// current account snapshots alone are insufficient evidence. Balance was already
// debited at grant issuance and is intentionally not charged a second time.
// This verifier is not automatically installed on the public application.
func PricingVerifier(scope VerifyEvent) VerifyEvent {
	return func(ctx context.Context, tx *sql.Tx, e Event) error {
		if scope == nil {
			return errors.New("durable admission scope verifier required")
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT price FROM regional_prices WHERE version=$1`, e.PricingVersion).Scan(&raw); err != nil {
			return err
		}
		var p Price
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.Version() != e.PricingVersion || p.Model != e.Model {
			return ErrConflict
		}
		charge, err := p.Charge(e.InputTokens, e.OutputTokens, e.CacheCreationTokens, e.CacheReadTokens)
		if err != nil {
			return err
		}
		if charge != e.Usage.Charge {
			return ErrConflict
		}
		if err = scope(ctx, tx, e); err != nil {
			return err
		}
		amount, err := USD(charge)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,total_cost,actual_cost,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::numeric,$10::numeric,$11)`, e.Usage.UserID, e.Usage.APIKeyID, e.Usage.AccountID, e.Usage.EventID, e.Model, e.InputTokens, e.OutputTokens, e.CacheCreationTokens, e.CacheReadTokens, amount, e.Usage.CompletedAt)
		return err
	}
}
