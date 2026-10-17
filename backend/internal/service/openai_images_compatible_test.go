//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompatibleImagesWorkBuddyModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ model, resolution, tier string }{
		{"seedream-5.0-lite", "4K", "4K"},
		{"seedream-5.0-lite", "2K", "2K"},
		{"seedream-5.0-pro", "2K", "2K"},
		{"seedream-5.0-pro", "1K", "1K"},
		{"seedream-5.0-pro", "1.5K", "2K"},
	} {
		t.Run(tc.model+"/"+tc.resolution, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw","resolution":%q,"aspect_ratio":"16:9","n":1,"response_format":"b64_json"}`, tc.model, tc.resolution))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapability)
			require.Equal(t, tc.tier, parsed.SizeTier)
			require.False(t, isOpenAIImageGenerationModel(tc.model), "WorkBuddy must not enter native Responses conversion")
			require.Equal(t, OpenAIImagesCapabilityAPIKey, (&OpenAIImagesRequest{}).RequiredCapabilityForModel(tc.model))
		})
	}
	for _, model := range []string{"seedream-unknown", "seedance-2.5", "seedream-5.0-lite-extra"} {
		require.Error(t, validateCompatibleImagesModel(model))
	}
}

func TestCompatibleImagesForwardWorkBuddy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"seedream-5.0-lite", "seedream-5.0-pro"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				resolution := "2K"
				if model == "seedream-5.0-lite" {
					resolution = "4K"
				}
				body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw","resolution":%q,"aspect_ratio":"16:9","n":1,"response_format":"b64_json","stream":%t,"reference_images":[{"image_url":"https://input.example/ref.png"}]}`, model, resolution, stream))
				responseBody := `{"id":"img_test","model":"` + model + `","size":"5504x3040","data":[{"b64_json":"aW1hZ2U="}],"usage":{"credit":6.29,"output_image_counts":1}}`
				header := http.Header{"Content-Type": {"application/json"}}
				if stream {
					header.Set("Content-Type", "text/event-stream")
					responseBody = "event: image_generation.completed\ndata: " + `{"type":"image_generation.completed","id":"img_test","model":"` + model + `","size":"5504x3040","b64_json":"aW1hZ2U=","usage":{"credit":6.29,"output_image_counts":1}}` + "\n\n"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(responseBody))}}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-only", "base_url": "https://wb.example/shipin/v1"}}
				result, err := svc.ForwardImages(c.Request.Context(), c, account, body, parsed, "")
				require.NoError(t, err)
				require.Equal(t, "https://wb.example/shipin/v1/images/generations", upstream.lastReq.URL.String())
				require.JSONEq(t, string(body), string(upstream.lastBody))
				require.Equal(t, 1, result.ImageCount)
				require.Equal(t, resolution, result.ImageSize)
				require.Contains(t, rec.Body.String(), "6.29")
				require.Contains(t, rec.Body.String(), "aW1hZ2U=")
			})
		}
	}
}

func TestCompatibleImagesWorkBuddyStandardOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ model, size, resolution string }{
		{"seedream-5.0-lite", "3840x2160", "4K"},
		{"seedream-5.0-lite", "1024x1024", "2K"},
		{"seedream-5.0-pro", "2048x1152", "2K"},
	} {
		t.Run(tc.model+"/"+tc.size, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw","size":%q,"quality":"high","reference_images":[{"image_url":"https://input.example/ref.png"}]}`, tc.model, tc.size))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.Equal(t, tc.resolution, parsed.SizeTier)
			rewritten, _, err := rewriteOpenAIImagesModel(body, "application/json", tc.model)
			require.NoError(t, err)
			require.Equal(t, tc.resolution, gjson.GetBytes(rewritten, "resolution").String())
			expectedAspect := "16:9"
			if tc.size == "1024x1024" {
				expectedAspect = "1:1"
			}
			require.Equal(t, expectedAspect, gjson.GetBytes(rewritten, "aspect_ratio").String())
			require.False(t, gjson.GetBytes(rewritten, "quality").Exists())
			require.False(t, gjson.GetBytes(rewritten, "size").Exists())
			require.Equal(t, "https://input.example/ref.png", gjson.GetBytes(rewritten, "reference_images.0.image_url").String())
		})
	}
}

func TestCompatibleImagesWorkBuddyBillingResolution(t *testing.T) {
	for _, tc := range []struct{ model, resolution, output, tier string }{
		{"seedream-5.0-lite", "4K", "5504x3040", "4K"},
		{"seedream-5.0-lite", "2K", "2816x1584", "2K"},
		{"seedream-5.0-pro", "2K", "2816x1584", "2K"},
		{"seedream-5.0-pro", "1.5K", "2112x1184", "2K"},
		{"seedream-5.0-pro", "1K", "1408x784", "1K"},
	} {
		t.Run(tc.model+"/"+tc.resolution, func(t *testing.T) {
			result := &OpenAIForwardResult{
				Model: "public-image", UpstreamModel: tc.model, ImageCount: 1,
				ImageInputSize: tc.resolution, ImageOutputSizes: []string{tc.output},
			}
			ApplyOpenAIImageBillingResolution(result)
			require.Equal(t, tc.tier, result.ImageSize)
			require.Equal(t, tc.output, result.ImageOutputSize, "retain real dimensions for audit")
			require.Equal(t, ImageSizeSourceInput, result.ImageSizeSource)
			ApplyOpenAIImageBillingResolution(result)
			require.Equal(t, tc.tier, result.ImageSize, "usage resolution may be applied twice")
		})
	}
}

func TestCompatibleImagesWorkBuddyNativeFence(t *testing.T) {
	for _, model := range []string{"seedream-5.0-lite", "seedream-5.0-pro"} {
		for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
			for _, mapped := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/mapped=%t", model, typ, mapped), func(t *testing.T) {
					inputModel := model
					account := &Account{Platform: PlatformOpenAI, Type: typ, Credentials: map[string]any{"access_token": "unused"}}
					if mapped {
						inputModel = "gpt-image-2"
						account.Credentials["model_mapping"] = map[string]any{inputModel: model}
					}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
					upstream := &httpUpstreamRecorder{}
					svc := &OpenAIGatewayService{httpUpstream: upstream}
					_, err := svc.ForwardImages(context.Background(), c, account, nil, &OpenAIImagesRequest{Model: inputModel}, "")
					require.ErrorContains(t, err, "images endpoint requires an image model")
					require.Empty(t, upstream.requests)
				})
			}
		}
	}
}

func TestCompatibleImagesWorkBuddyInvalidOptions(t *testing.T) {
	for _, body := range []string{
		`{"model":"seedream-5.0-pro","resolution":"4K"}`,
		`{"model":"seedream-5.0-lite","resolution":"1K"}`,
		`{"model":"seedream-5.0-lite","resolution":4}`,
		`{"model":"seedream-5.0-pro","aspect_ratio":"auto"}`,
		`{"model":"seedream-5.0-pro","size":"1234x1000"}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		_, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, []byte(body))
		require.Error(t, err)
	}
	body := []byte(`{"model":"seedream-5.0-lite","resolution":"4K","aspect_ratio":"16:9","size":"1024x1024","quality":"low"}`)
	normalized, err := normalizeWorkBuddySeedreamBody(body, "seedream-5.0-lite")
	require.NoError(t, err)
	require.Equal(t, "4K", gjson.GetBytes(normalized, "resolution").String())
	require.Equal(t, "16:9", gjson.GetBytes(normalized, "aspect_ratio").String())
	require.False(t, gjson.GetBytes(normalized, "size").Exists())
	require.False(t, gjson.GetBytes(normalized, "quality").Exists())
}

func TestCompatibleImagesGeminiModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gemini-2.5-flash-image", "gemini-2.5-flash-image-preview", "gemini-3-pro-image", "gemini-3.1-flash-image"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw"}`, model))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, bytes.NewReader(body))
			parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.True(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}).SupportsOpenAIImageCapability(parsed.RequiredCapability))
			for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
				require.False(t, (&Account{Platform: PlatformOpenAI, Type: typ}).SupportsOpenAIImageCapability(parsed.RequiredCapability))
			}
			require.False(t, isOpenAIImageGenerationModel(model), "compatible image IDs must not enter native Responses normalization")
		})
	}
	for _, model := range []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-3-pro-imageless", "unknown-image", "gpt-5.5"} {
		t.Run("reject_"+model, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw"}`, model))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, bytes.NewReader(body))
			_, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
			require.ErrorContains(t, err, "images endpoint requires an image model")
		})
	}
}

func TestCompatibleImagesForwardGemini(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []string{"generation", "json_edit", "multipart_edit", "composite_multipart_alias", "channel_mapping", "account_mapping"} {
		t.Run(kind, func(t *testing.T) {
			model := "gemini-3.1-flash-image"
			endpoint := openAIImagesGenerationsEndpoint
			contentType := "application/json"
			channelModel := ""
			credentials := map[string]any{"api_key": "image-key", "base_url": "https://compatible.example/v1"}
			requestModel := model
			if kind == "channel_mapping" {
				requestModel, channelModel = "gpt-image-2", model
			}
			if kind == "account_mapping" {
				requestModel = "gpt-image-2"
				credentials["model_mapping"] = map[string]any{requestModel: model}
			}
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw","size":"1024x1024","custom_field":"preserved"}`, requestModel))
			if kind == "json_edit" {
				endpoint = openAIImagesEditsEndpoint
				body = []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw","images":[{"image_url":"https://source.example/input.png"}],"custom_field":"preserved"}`, model))
			}
			if strings.Contains(kind, "multipart") {
				endpoint = openAIImagesEditsEndpoint
				if kind == "composite_multipart_alias" {
					requestModel = "public-image"
				}
				var buf bytes.Buffer
				writer := multipart.NewWriter(&buf)
				require.NoError(t, writer.WriteField("model", requestModel))
				require.NoError(t, writer.WriteField("prompt", "draw"))
				require.NoError(t, writer.WriteField("custom_field", "preserved"))
				part, err := writer.CreateFormFile("image", "input.png")
				require.NoError(t, err)
				_, err = part.Write([]byte("original-image-bytes"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				body, contentType = buf.Bytes(), writer.FormDataContentType()
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", contentType)
			if kind == "composite_multipart_alias" {
				ctx := WithResolvedTargetPlatform(c.Request.Context(), PlatformOpenAI)
				ctx = WithCompositeRouteDecision(ctx, CompositeRouteDecision{Matched: true, TargetPlatform: PlatformOpenAI, UpstreamModel: model, PublicModel: requestModel})
				c.Request = c.Request.WithContext(ctx)
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aW1hZ2U="}]}`))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			if kind == "channel_mapping" {
				require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapabilityForModel(channelModel))
			}
			result, err := svc.ForwardImages(c.Request.Context(), c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: credentials}, body, parsed, channelModel)
			require.NoError(t, err)
			require.Equal(t, 1, result.ImageCount)
			require.Equal(t, model, result.UpstreamModel)
			require.Equal(t, firstNonEmptyString(channelModel, parsed.Model), result.Model)
			require.Equal(t, "https://compatible.example"+endpoint, upstream.lastReq.URL.String())
			require.Equal(t, "Bearer image-key", upstream.lastReq.Header.Get("Authorization"))
			if strings.Contains(kind, "multipart") {
				r := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(upstream.lastBody))
				r.Header.Set("Content-Type", upstream.lastReq.Header.Get("Content-Type"))
				require.NoError(t, r.ParseMultipartForm(1<<20))
				require.Equal(t, model, r.FormValue("model"))
				require.Equal(t, "preserved", r.FormValue("custom_field"))
				file, _, err := r.FormFile("image")
				require.NoError(t, err)
				defer file.Close()
				data, err := io.ReadAll(file)
				require.NoError(t, err)
				require.Equal(t, "original-image-bytes", string(data))
			} else {
				require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "preserved", gjson.GetBytes(upstream.lastBody, "custom_field").String())
			}
		})
	}
}

func TestCompatibleImagesNativeAccountsRejectGeminiBeforeForwarding(t *testing.T) {
	for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, mapping := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/mapping=%t", typ, mapping), func(t *testing.T) {
				model := "gemini-3-pro-image"
				account := &Account{Platform: PlatformOpenAI, Type: typ, Credentials: map[string]any{"access_token": "unused"}}
				if mapping {
					account.Credentials["model_mapping"] = map[string]any{"gpt-image-2": model}
					model = "gpt-image-2"
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
				upstream := &httpUpstreamRecorder{}
				svc := &OpenAIGatewayService{httpUpstream: upstream}
				_, err := svc.ForwardImages(context.Background(), c, account, nil, &OpenAIImagesRequest{Model: model}, "")
				require.ErrorContains(t, err, "images endpoint requires an image model")
				require.Empty(t, upstream.requests)
			})
		}
	}
}
