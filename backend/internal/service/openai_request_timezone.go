// Adapted from MACOS-DO/sub4api at 254e7ec4932e65ab8830e7d10e66f184140041bd.
package service

import (
	_ "embed"
	"net/http"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const DefaultOpenAIRequestTimezone = "Asia/Singapore"
const openAIRequestTimezoneExtraKey = "openai_request_timezone"

//go:embed openai_request_timezones.txt
var openAIRequestTimezoneNames string

var openAIRequestTimezoneOptions = strings.Fields(openAIRequestTimezoneNames)
var openAIRequestTimezoneAllowed = func() map[string]struct{} {
	allowed := make(map[string]struct{}, len(openAIRequestTimezoneOptions))
	for _, name := range openAIRequestTimezoneOptions {
		allowed[name] = struct{}{}
	}
	return allowed
}()

func OpenAIRequestTimezoneOptions() []string {
	options := append([]string(nil), openAIRequestTimezoneOptions...)
	sort.Strings(options)
	return options
}

func ValidateOpenAIRequestTimezoneExtra(platform string, extra map[string]any) error {
	if extra == nil {
		return nil
	}
	raw, exists := extra[openAIRequestTimezoneExtraKey]
	if !exists || raw == nil {
		return nil
	}
	name, ok := raw.(string)
	if !ok || (name != "" && !isAllowedOpenAIRequestTimezone(name)) || (name != "" && platform != PlatformOpenAI) {
		return infraerrors.New(http.StatusBadRequest, "INVALID_OPENAI_REQUEST_TIMEZONE", "openai_request_timezone must be an allowed IANA timezone for an OpenAI account")
	}
	return nil
}

func isAllowedOpenAIRequestTimezone(name string) bool {
	if _, ok := openAIRequestTimezoneAllowed[name]; !ok {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func (account *Account) OpenAIRequestTimezone() string {
	if account != nil && account.IsOpenAI() {
		if name := account.getExtraString(openAIRequestTimezoneExtraKey); isAllowedOpenAIRequestTimezone(name) {
			return name
		}
	}
	return DefaultOpenAIRequestTimezone
}
