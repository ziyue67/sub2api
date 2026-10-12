package handler

import (
	"context"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// inflightReservationEstimator 估算单请求在途预留金额（USD）；false 表示无法定价。
type inflightReservationEstimator interface {
	EstimateInflightReservation(ctx context.Context, apiKey *service.APIKey, req service.InflightEstimateRequest) (float64, bool)
	// UnpricedReservationFallbackUSD 无法定价时的保守预留金额（0 = 不预留）。
	UnpricedReservationFallbackUSD() float64
}

// requestMaxOutputTokens 从请求体中提取输出 token 上限（兼容 Anthropic / OpenAI Chat / Responses / Gemini）。
func requestMaxOutputTokens(body []byte) int {
	for _, path := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig.maxOutputTokens", "generation_config.max_output_tokens"} {
		if v := gjson.GetBytes(body, path); v.Exists() && v.Type == gjson.Number && v.Int() > 0 {
			return int(v.Int())
		}
	}
	return 0
}

// tokenInflightEstimate 文本类请求的估算输入。
func tokenInflightEstimate(model string, body []byte) service.InflightEstimateRequest {
	return service.InflightEstimateRequest{
		Model:     model,
		BodyBytes: len(body),
		MaxTokens: requestMaxOutputTokens(body),
		Kind:      service.InflightEstimateToken,
	}
}

// hostedWebSearchInflightEstimate 叠加托管网页搜索的按次费：按工具声明的 max_uses
// （未声明按 1 次，见 service.RequestHostedWebSearchMaxCalls）计。检索结果的 token
// 已由 token 上界（EstimateRequestSpendUpperBound）计入。
func hostedWebSearchInflightEstimate(model string, body []byte) service.InflightEstimateRequest {
	req := tokenInflightEstimate(model, body)
	req.HostedWebSearchCalls = service.RequestHostedWebSearchMaxCalls(body)
	return req
}

func inflightNoop() {}

// billingAdmissionOptions 为 CheckBillingEligibility 组装「最坏费用闸门 + 在途预留槽位」。
//
// worstSpend 取两套估算的较高者：tokenUpperBound 是零超发预检的 token 上界
// （EstimateRequestSpendUpperBound，按需传 0），inflightReq 走计费同口径估算，
// 覆盖按次/搜索/托管搜索/音频/媒体等 token 上界无法表达的计费项。
// 预留按 scope（钱包 / 订阅 / 用户×平台配额 / API Key 额度）原子登记，
// 调用方必须 defer slot.ReleaseOnExit，并在结算任务内 defer slot.Release。
func billingAdmissionOptions(
	ctx context.Context,
	estimator inflightReservationEstimator,
	apiKey *service.APIKey,
	tokenUpperBound float64,
	inflightReq service.InflightEstimateRequest,
	slot *service.BillingReservationSlot,
) []service.BillingEligibilityOption {
	worstSpend := admissionWorstSpend(ctx, estimator, apiKey, tokenUpperBound, inflightReq)
	opts := make([]service.BillingEligibilityOption, 0, 2)
	if worstSpend > 0 {
		opts = append(opts, service.WithMaxRequestSpend(worstSpend))
	}
	return append(opts, service.WithBalanceReservation(slot))
}

// reserveInflightBalance 在 CheckBillingEligibility 之后为余额模式请求登记在途预留。
//
// 成功时把预留句柄挂到 c.Request 的 context 上：之后通过 submit*UsageRecordTask 提交的
// 计费任务会接管一个引用，直到余额缓存实际扣减后才释放。返回的 done 必须 defer 调用
// （停止续期并归还 handler 引用；没有提交计费任务时即刻释放）。
// 开关关闭、订阅模式、Redis 故障时返回 no-op（fail-open）；无法定价时默认 fail-open，
// 配置 fail_closed_on_unpriced=true 时返回 ErrInsufficientBalance。
func reserveInflightBalance(
	c *gin.Context,
	billing *service.BillingCacheService,
	estimator inflightReservationEstimator,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	req service.InflightEstimateRequest,
) (func(), error) {
	if c == nil || c.Request == nil {
		return inflightNoop, nil
	}
	ctx, done, err := reserveInflightBalanceCtx(c.Request.Context(), billing, estimator, apiKey, subscription, req)
	if err != nil {
		return inflightNoop, err
	}
	c.Request = c.Request.WithContext(ctx)
	return done, nil
}

// reserveInflightBalanceCtx 同 reserveInflightBalance，但返回携带预留句柄的新 context
// （供 WebSocket 等自管 context 的路径）。
func reserveInflightBalanceCtx(
	ctx context.Context,
	billing *service.BillingCacheService,
	estimator inflightReservationEstimator,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	req service.InflightEstimateRequest,
) (context.Context, func(), error) {
	if billing == nil || estimator == nil || apiKey == nil || apiKey.User == nil || !billing.InflightReservationEnabled() {
		return ctx, inflightNoop, nil
	}
	if apiKey.Group != nil && apiKey.Group.IsSubscriptionType() && subscription != nil {
		return ctx, inflightNoop, nil
	}
	estimate, priced := estimator.EstimateInflightReservation(ctx, apiKey, req)
	if !priced && billing.InflightReservationFailClosedOnUnpriced() {
		return ctx, inflightNoop, service.ErrInsufficientBalance
	}
	if estimate <= 0 {
		return ctx, inflightNoop, nil
	}
	res, err := billing.ReserveInflight(ctx, apiKey.User, apiKey.Group, subscription, estimate)
	if err != nil {
		return ctx, inflightNoop, err
	}
	if res == nil {
		return ctx, inflightNoop, nil
	}
	return service.WithInflightReservation(ctx, res), res.HandlerDone, nil
}

// grokMediaInflightEstimate 媒体生成请求的估算输入；状态/内容查询返回空模型（不预留：
// 查询会为已生成的媒体计费，不能因余额预留而拦截用户取回已付费结果）。
func grokMediaInflightEstimate(endpoint service.GrokMediaEndpoint, model string, info service.GrokMediaRequestInfo, body []byte) service.InflightEstimateRequest {
	if !endpoint.IsGenerationRequest() {
		return service.InflightEstimateRequest{}
	}
	switch endpoint {
	case service.GrokMediaEndpointImagesGenerations, service.GrokMediaEndpointImagesEdits:
		return service.InflightEstimateRequest{Model: model, BodyBytes: len(body), Kind: service.InflightEstimateImage, Units: info.N}
	default:
		return service.InflightEstimateRequest{
			Model:                model,
			BodyBytes:            len(body),
			Kind:                 service.InflightEstimateVideo,
			Units:                1,
			VideoResolution:      info.Resolution,
			VideoDurationSeconds: info.DurationSeconds,
		}
	}
}

// grokVoiceSTTBytesPerSecond STT 时长粗估（~128kbps 压缩音频）。
const grokVoiceSTTBytesPerSecond = 16000

// grokVoiceInflightEstimate 语音 HTTP 接口估算：TTS 按输入字符数（百万字符），STT 按音频字节粗估时长（小时）。
// 其他接口（custom-voices）无音频计量，返回的估算为 0（不预留）。
func grokVoiceInflightEstimate(endpoint string, body []byte) service.InflightEstimateRequest {
	req := service.InflightEstimateRequest{Model: endpoint, Kind: service.InflightEstimateAudio, AudioMode: endpoint}
	switch endpoint {
	case "tts":
		req.AudioUnits = float64(len([]rune(extractGrokTTSInputText(body)))) / 1e6
	case "stt":
		req.AudioUnits = float64(len(body)) / grokVoiceSTTBytesPerSecond / 3600
	}
	return req
}

// admissionWorstSpend 返回准入用的最坏费用：token 上界与计费同口径估算取较高者。
// 无法定价时只用 token 上界（两者都为 0 时由预检的余额阈值兜底）。
func admissionWorstSpend(ctx context.Context, estimator inflightReservationEstimator, apiKey *service.APIKey, tokenUpperBound float64, inflightReq service.InflightEstimateRequest) float64 {
	worstSpend := tokenUpperBound
	if estimator != nil && apiKey != nil && inflightReq.Model != "" {
		if est, priced := estimator.EstimateInflightReservation(ctx, apiKey, inflightReq); priced && est > worstSpend {
			worstSpend = est
		} else if !priced && worstSpend <= 0 {
			// 两套估算都无法定价：最坏费用为 0 会让钱包 / 订阅 / 平台配额 / API Key
			// 额度的在途预留全部跳过，并发请求按快照全部放行（审计 H3）。按配置的保守
			// 金额预留（billing.inflight_reservation.max_reservation_usd，未配置时 0 = 维持旧行为）。
			if fallback := estimator.UnpricedReservationFallbackUSD(); fallback > 0 {
				service.RecordBillingPrecheckUnpricedFallback()
				worstSpend = fallback
			}
		}
	}
	return worstSpend
}

// openAIWSTurnReservations 按 turn 持有 WebSocket 会话的在途预留槽位。
//
// 同一 turn 重复准入（首帧在其他账号上重试等）时先归还旧槽位再登记新槽位；
// take 把槽位交给结算任务（任务内 Release），releaseAll 在会话结束时归还剩余槽位。
type openAIWSTurnReservations struct {
	mu    sync.Mutex
	slots map[int]*service.BillingReservationSlot
}

func newOpenAIWSTurnReservations() *openAIWSTurnReservations {
	return &openAIWSTurnReservations{slots: map[int]*service.BillingReservationSlot{}}
}

func (r *openAIWSTurnReservations) put(ctx context.Context, turn int, slot *service.BillingReservationSlot) {
	r.mu.Lock()
	old := r.slots[turn]
	r.slots[turn] = slot
	r.mu.Unlock()
	if old != nil && old != slot {
		old.Release(ctx)
	}
}

// take 取出 turn 的槽位（可能为 nil：调用方的提交/Release 对 nil 安全）。
func (r *openAIWSTurnReservations) take(turn int) *service.BillingReservationSlot {
	r.mu.Lock()
	defer r.mu.Unlock()
	slot := r.slots[turn]
	delete(r.slots, turn)
	return slot
}

func (r *openAIWSTurnReservations) release(ctx context.Context, turn int) {
	r.take(turn).Release(ctx)
}

func (r *openAIWSTurnReservations) releaseAll(ctx context.Context) {
	r.mu.Lock()
	slots := r.slots
	r.slots = map[int]*service.BillingReservationSlot{}
	r.mu.Unlock()
	for _, slot := range slots {
		slot.ReleaseOnExit(ctx)
	}
}
