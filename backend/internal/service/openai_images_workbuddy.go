package service

import (
	"fmt"
	"math"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// WorkBuddy providers use the compatible API-key path, not OpenAI OAuth or
// native Responses image tools. Keep this allowlist separate from native models.
func isWorkBuddySeedreamModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "seedream-5.0-lite", "seedream-5.0-pro":
		return true
	default:
		return false
	}
}

func isAPIKeyCompatibleImageModel(model string) bool {
	return isGeminiCompatibleImageModel(model) || isWorkBuddySeedreamModel(model)
}

// Normalize standard OpenAI image options to WorkBuddy's Seedream contract.
// Explicit native options win over standard client defaults; reference inputs
// and all other fields remain intact for upstream validation.
func normalizeWorkBuddySeedreamBody(body []byte, model string) ([]byte, error) {
	if !isWorkBuddySeedreamModel(model) {
		return body, nil
	}
	model = strings.ToLower(strings.TrimSpace(model))
	readString := func(key string) (string, error) {
		value := gjson.GetBytes(body, key)
		if !value.Exists() {
			return "", nil
		}
		if value.Type != gjson.String {
			return "", fmt.Errorf("%s must be a string for Seedream", key)
		}
		return strings.TrimSpace(value.String()), nil
	}
	resolution, err := readString("resolution")
	if err != nil {
		return nil, err
	}
	aspect, err := readString("aspect_ratio")
	if err != nil {
		return nil, err
	}
	if resolution == "" || aspect == "" {
		size, sizeErr := readString("size")
		if sizeErr != nil {
			return nil, sizeErr
		}
		if size != "" && !strings.EqualFold(size, "auto") {
			if width, height, ok := parseImageBillingDimensions(size); ok {
				if resolution == "" {
					maxEdge := max(width, height)
					switch {
					case maxEdge >= 3840:
						resolution = "4K"
					case maxEdge >= 1536:
						resolution = "2K"
					default:
						resolution = "1K"
					}
					if model == "seedream-5.0-lite" && resolution == "1K" {
						resolution = "2K"
					}
				}
				if aspect == "" {
					for _, candidate := range []struct {
						name string
						w, h float64
					}{
						{"1:1", 1, 1}, {"3:2", 3, 2}, {"2:3", 2, 3},
						{"3:4", 3, 4}, {"4:3", 4, 3}, {"16:9", 16, 9},
						{"9:16", 9, 16}, {"21:9", 21, 9},
					} {
						if math.Abs(float64(width)/float64(height)-candidate.w/candidate.h) < 0.001 {
							aspect = candidate.name
							break
						}
					}
					if aspect == "" {
						return nil, fmt.Errorf("Seedream size must use a supported aspect ratio; use resolution and aspect_ratio")
					}
				}
			} else if resolution == "" {
				resolution = strings.ToUpper(size)
			}
		}
	}
	resolution = strings.ToUpper(resolution)
	if resolution == "" {
		resolution = "1K"
		if model == "seedream-5.0-lite" {
			resolution = "2K"
		}
	}
	validResolution := resolution == "2K"
	if model == "seedream-5.0-lite" {
		validResolution = validResolution || resolution == "4K"
	} else {
		validResolution = validResolution || resolution == "1K" || resolution == "1.5K"
	}
	if !validResolution {
		return nil, fmt.Errorf("unsupported resolution %q for %s", resolution, model)
	}
	if aspect != "" {
		switch aspect {
		case "1:1", "3:2", "2:3", "3:4", "4:3", "16:9", "9:16", "21:9":
		default:
			return nil, fmt.Errorf("unsupported aspect_ratio %q for Seedream", aspect)
		}
	}
	for _, key := range []string{"size", "quality"} {
		body, err = sjson.DeleteBytes(body, key)
		if err != nil {
			return nil, err
		}
	}
	body, err = sjson.SetBytes(body, "resolution", resolution)
	if err != nil {
		return nil, err
	}
	if aspect != "" {
		body, err = sjson.SetBytes(body, "aspect_ratio", aspect)
	}
	return body, err
}

func applyWorkBuddySeedreamRequestMetadata(req *OpenAIImagesRequest, body []byte) {
	req.Size = gjson.GetBytes(body, "resolution").String()
	req.SizeTier = normalizeOpenAIImageSizeTier(req.Size)
	if req.Size == "1.5K" {
		req.SizeTier = ImageBillingSize2K
	}
	// Seedream has no quality option; do not attach a GPT quality pricing suffix.
	req.Quality = ""
}

// Seedream resolution names describe provider tiers, not maximum pixel edges:
// a 16:9 2K image can be 2816x1584. Keep actual dimensions for audit while
// charging the requested provider tier instead of the generic pixel heuristic.
func resolveWorkBuddySeedreamBilling(result *OpenAIForwardResult) (ImageBillingSizeResolution, bool) {
	model := firstNonEmptyString(result.UpstreamModel, result.Model)
	if !isWorkBuddySeedreamModel(model) {
		return ImageBillingSizeResolution{}, false
	}
	resolution := strings.ToUpper(strings.TrimSpace(result.ImageInputSize))
	tier := resolution
	if resolution == "1.5K" {
		tier = ImageBillingSize2K
	}
	switch tier {
	case ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K:
		return ImageBillingSizeResolution{
			BillingSize: tier,
			InputSize:   result.ImageInputSize,
			OutputSize:  firstNonEmptyString(result.ImageOutputSize, firstDisplayImageOutputSize(result.ImageOutputSizes)),
			Source:      ImageSizeSourceInput,
		}, true
	default:
		return ImageBillingSizeResolution{}, false
	}
}
