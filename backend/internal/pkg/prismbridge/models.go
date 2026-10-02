package prismbridge

import (
	"fmt"
	"strings"
)

var modelAliases = map[string]string{
	"prism-astra": "gpt-6-astra",
	"prism-sol":   "gpt-5.6-sol",
	"prism-terra": "gpt-5.6-terra",
}

var supportedModels = map[string]bool{
	"gpt-6-astra":   true,
	"gpt-5.6-sol":   true,
	"gpt-5.6-terra": true,
}

func NormalizeModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if alias, ok := modelAliases[model]; ok {
		model = alias
	}
	if !supportedModels[model] {
		return "", fmt.Errorf("unsupported Prism model: %s", model)
	}
	return model, nil
}
