package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// A single settings row makes the switch and entries atomic, including across
// instances. Deliberately do not cache this security policy in process memory.
const SettingKeyLegacyAdminKeyIPPolicy = "legacy_admin_key_ip_policy"

type LegacyAdminKeyIPPolicy struct {
	Enabled    bool     `json:"enabled"`
	AllowedIPs []string `json:"allowed_ips"`
}

func NormalizeLegacyAdminKeyIPPolicy(in LegacyAdminKeyIPPolicy) (LegacyAdminKeyIPPolicy, error) {
	invalid := func() (LegacyAdminKeyIPPolicy, error) {
		return LegacyAdminKeyIPPolicy{}, infraerrors.BadRequest("INVALID_ADMIN_KEY_IP_POLICY", "最多填写 100 条有效 IP 或 CIDR，不允许全网段；开启时至少填写一条")
	}
	if len(in.AllowedIPs) > 100 {
		return invalid()
	}
	out := LegacyAdminKeyIPPolicy{Enabled: in.Enabled, AllowedIPs: []string{}}
	seen := map[string]bool{}
	for _, raw := range in.AllowedIPs {
		value := strings.TrimSpace(raw)
		if addr, err := netip.ParseAddr(value); err == nil {
			if addr.Zone() != "" {
				return invalid()
			}
			value = addr.Unmap().String()
		} else {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix.Bits() == 0 {
				return invalid()
			}
			if prefix.Addr().Is4In6() {
				if prefix.Bits() <= 96 {
					return invalid()
				}
				prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
			}
			value = prefix.Masked().String()
		}
		if !seen[value] {
			out.AllowedIPs = append(out.AllowedIPs, value)
			seen[value] = true
		}
	}
	if out.Enabled && len(out.AllowedIPs) == 0 {
		return invalid()
	}
	return out, nil
}

func (p LegacyAdminKeyIPPolicy) Allows(source string) bool {
	if !p.Enabled {
		return true
	}
	addr, err := netip.ParseAddr(source)
	if err != nil || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	for _, entry := range p.AllowedIPs {
		if ip, e := netip.ParseAddr(entry); e == nil && ip.Unmap() == addr {
			return true
		}
		if prefix, e := netip.ParsePrefix(entry); e == nil && prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (s *SettingService) GetLegacyAdminKeyIPPolicy(ctx context.Context) (LegacyAdminKeyIPPolicy, error) {
	if s == nil || s.settingRepo == nil {
		return LegacyAdminKeyIPPolicy{}, fmt.Errorf("legacy admin key policy store unavailable")
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyLegacyAdminKeyIPPolicy)
	if errors.Is(err, ErrSettingNotFound) {
		return LegacyAdminKeyIPPolicy{AllowedIPs: []string{}}, nil
	}
	if err != nil {
		return LegacyAdminKeyIPPolicy{}, err
	}
	// Missing fields, null, blank strings and malformed rows must not silently
	// turn an existing restriction off. Only a genuinely absent row defaults off.
	var stored struct {
		Enabled    *bool     `json:"enabled"`
		AllowedIPs *[]string `json:"allowed_ips"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || stored.Enabled == nil || stored.AllowedIPs == nil {
		return LegacyAdminKeyIPPolicy{}, fmt.Errorf("invalid legacy admin key IP policy")
	}
	return NormalizeLegacyAdminKeyIPPolicy(LegacyAdminKeyIPPolicy{Enabled: *stored.Enabled, AllowedIPs: *stored.AllowedIPs})
}

func (s *SettingService) SetLegacyAdminKeyIPPolicy(ctx context.Context, in LegacyAdminKeyIPPolicy) (LegacyAdminKeyIPPolicy, error) {
	out, err := NormalizeLegacyAdminKeyIPPolicy(in)
	if err != nil {
		return LegacyAdminKeyIPPolicy{}, err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return LegacyAdminKeyIPPolicy{}, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyLegacyAdminKeyIPPolicy, string(data)); err != nil {
		return LegacyAdminKeyIPPolicy{}, err
	}
	return out, nil
}
