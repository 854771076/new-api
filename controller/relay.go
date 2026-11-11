package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func relayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		err = relay.ImageHelper(c, info)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = relay.AudioHelper(c, info)
	case relayconstant.RelayModeRerank:
		err = relay.RerankHelper(c, info)
	case relayconstant.RelayModeEmbeddings:
		err = relay.EmbeddingHelper(c, info)
	case relayconstant.RelayModeResponses, relayconstant.RelayModeResponsesCompact:
		err = relay.ResponsesHelper(c, info)
	case relayconstant.RelayModeAlphaSearch:
		err = relay.AlphaSearchHelper(c, info)
	case relayconstant.RelayModeToolsSearch, relayconstant.RelayModeToolsFetch:
		err = relay.DeepSeekToolsHelper(c, info)
	default:
		err = relay.TextHelper(c, info)
	}
	return err
}

func geminiRelayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	if strings.Contains(c.Request.URL.Path, "embed") {
		err = relay.GeminiEmbeddingHandler(c, info)
	} else {
		err = relay.GeminiHelper(c, info)
	}
	return err
}

// runChannelSlotAttempt owns the channel-slot lifecycle of ONE relay attempt
// (BCD D-1): every call is its own inner scope with its own defer release, so
// the retry loop never carries a slot across iterations (a limit=1 channel must
// not self-deadlock) and all three exits — success return, error return, panic
// unwind — free the slot before control leaves this frame.
//
// The cap comes from constant.ContextKeyChannelConcurrencyLimit, published by
// middleware.SetupContextForSelectedChannel on both the first hop
// (middleware.Distribute) and every retry hop (getChannel), so the value always
// belongs to the channel selected for THIS attempt. 0 means unlimited while the
// bucket still counts in-flight holders (D-18).
//
// The acquire rides context.Background() (D-06): a client disconnect must never
// cancel a slot take that is already in flight. With no limiter installed the
// helper is a no-op, matching ConcurrencyMiddleware's stance.
func runChannelSlotAttempt(c *gin.Context, channelID int, body func() *types.NewAPIError) *types.NewAPIError {
	limiter := getConcurrencyLimiter()
	if limiter == nil {
		return body()
	}
	limit := common.GetContextKeyInt(c, constant.ContextKeyChannelConcurrencyLimit)
	release, err := limiter.Acquire(context.Background(), concurrency.ChannelScope(channelID), limit)
	if err != nil {
		if !errors.Is(err, concurrency.ErrConcurrencyExceeded) {
			logger.LogError(c, common.LogText("channel concurrency acquire failed (channel #%d): %s", channelID, err.Error()))
		}
		// Refusal (or an unexpected backend failure) never enters the attempt
		// body. The rejection signal carries the "channel:" prefix, so
		// DecideRelayRetry's IsChannelError branch routes it to a retry for
		// free (BCD D-4); 02-15 builds the channel-exclusion set and the final
		// 429 rendering on this seam.
		return service.NewChannelConcurrencyRejection()
	}
	defer release()
	return body()
}

func Relay(c *gin.Context, relayFormat types.RelayFormat) {

	requestId := c.GetString(common.RequestIdKey)
	//group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	//originalModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	// 分段耗时采集载体（relay/common.RequestTiming）：与并发准入、流式扫描
	// 共享同一份，逐段耗时最终落到消费/错误日志的 admin_info.timing。
	timing := relaycommon.RequestTimingOf(c)

	var (
		newAPIError *types.NewAPIError
		ws          *websocket.Conn
	)

	if relayFormat == types.RelayFormatOpenAIRealtime {
		var err error
		ws, err = upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			helper.WssError(c, ws, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry()).ToOpenAIError())
			return
		}
		defer ws.Close()
	}

	defer func() {
		if newAPIError != nil {
			service.RecordRequestPolicyTermination(c, newAPIError)
			logger.LogError(c, common.LogText("relay error: %s", common.LocalLogPreview(newAPIError.Error())))
			newAPIError.SetMessage(common.MessageWithRequestId(newAPIError.Error(), requestId))
			renderRelayTerminalError(c, relayFormat, ws, newAPIError)
		}
	}()

	request, err := helper.GetAndValidateRequest(c, relayFormat)
	if err != nil {
		// Map "request body too large" to 413 so clients can handle it correctly
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		} else {
			newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
		}
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
		return
	}

	defer func() {
		recovered := recover()
		resultErr := newAPIError
		if recovered != nil {
			resultErr = types.NewError(fmt.Errorf("relay panic: %v", recovered), types.ErrorCodeBadResponse)
		}
		if relayFormat != types.RelayFormatOpenAIRealtime {
			perfmetrics.RecordRelayResult(c.Request.Context(), relayInfo, resultErr)
		}
		if recovered != nil {
			panic(recovered)
		}
	}()

	generalSettings := operation_setting.GetGeneralSetting()
	if relayFormat == types.RelayFormatClaude && relayInfo.IsStream && generalSettings.PingIntervalEnabled {
		stopKeepAlive := helper.StartStreamKeepAlive(c, time.Duration(generalSettings.PingIntervalSeconds)*time.Second)
		defer stopKeepAlive()
	}

	if newAPIError = relay.PrepareRequestBilling(c, relayInfo); newAPIError != nil {
		return
	}
	defer func() {
		newAPIError = relay.RefundFailedRequestBilling(c, relayInfo, newAPIError)
	}()

	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayInfo.TokenGroup,
		ModelName:   relayInfo.OriginModelName,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}
	relayInfo.RetryIndex = 0
	relayInfo.LastError = nil

	// RTE-02 (02-18): per-request state for the "upstream 429 must not
	// terminate after a single channel" grant — grantUpstream429Retry gates
	// exactly one extra loop iteration, upstream429Grants counts grants (the
	// predicate's exactly-once condition), upstream429Fallback keeps the
	// original upstream 429 so a failed grant re-selection still renders it,
	// and triedChannels mirrors the use_channel registrations so the grant
	// condition can see "exactly one distinct channel tried". All four live
	// on this stack frame: one client request, never shared state.
	var (
		grantUpstream429Retry bool
		upstream429Grants     int
		upstream429Fallback   *types.NewAPIError
		triedChannels         = make(map[int]struct{}, 2)
	)

	for ; retryParam.GetRetry() <= common.RetryTimes || grantUpstream429Retry; retryParam.IncreaseRetry() {
		// RTE-02: consume the grant flag on the first line of the iteration
		// it unlocked — the loop condition may be re-entered through the
		// budget clause later, and consuming here guarantees the grant opens
		// exactly one iteration (anti-self-spin, double insurance with the
		// predicate's priorGrants == 0).
		grantUpstream429Retry = false
		relayInfo.StreamStatus = nil
		relayInfo.PerformanceBusinessRejection = false
		relayInfo.PerformanceOutputTokens = 0
		relayInfo.RetryIndex = retryParam.GetRetry()
		channelSelectStartedAt := time.Now()
		channel, channelErr := getChannel(c, relayInfo, retryParam)
		// 选渠道耗时按 attempt 累加；首跳复用 distributor 已钉住的渠道时耗时
		// 接近 0，这是真实值，不做修饰。
		timing.AddChannelSelect(time.Since(channelSelectStartedAt))
		if channelErr != nil {
			logger.LogError(c, channelErr.Error())
			newAPIError = channelErr
			// RTE-02: a grant re-selection failure (all remaining candidates
			// excluded, so getChannel classifies the empty selection as the
			// CNT-01 concurrency rejection or a channel-not-found error) must
			// not replace the upstream 429 that triggered the grant — the
			// client contract is the ORIGINAL 429 passthrough. The log above
			// still records the selection error itself, and getChannel's
			// empty-selection rule stays untouched for every other request.
			if upstream429Fallback != nil {
				newAPIError = upstream429Fallback
			}
			break
		}
		timing.AddAttempt()
		service.AppendUsedChannel(c, channel.Id)
		// RTE-02: register the attempt in the per-request tried set at the
		// exact position of the use_channel registration, so the grant
		// predicate reads the same attempt history the retry log records.
		triedChannels[channel.Id] = struct{}{}
		// Apply this attempt's channel-level model_mapping to the relayInfo
		// and the outgoing request. Model mapping depends on the selected
		// channel (each channel carries its own model_mapping), so this must
		// run AFTER getChannel sets model_mapping into the gin context and
		// INSIDE the retry loop — every retry picks a different channel and
		// may translate the model name differently. Running it once before
		// the loop would only translate with whichever channel the first
		// retry happened to pick, leaving later retries sending the client's
		// raw model name upstream and triggering 404 "model not found" on
		// channels whose upstream name differs from the client's name.
		if request != nil {
			if err := helper.ModelMappedHelper(c, relayInfo, request); err != nil {
				newAPIError = types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
				break
			}
		}
		if billingErr := service.PrepareTieredBillingForSelectedGroup(c, relayInfo); billingErr != nil {
			newAPIError = billingErr
			break
		}

		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			// Ensure consistent 413 for oversized bodies even when error occurs later (e.g., retry path)
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
			} else {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		// BCD D-1: the dispatch runs inside the attempt scope so the channel
		// slot taken for THIS iteration is released here (success / error /
		// panic) and never survives into the next iteration.
		var attemptStartedAt time.Time
		newAPIError = runChannelSlotAttempt(c, channel.Id, func() *types.NewAPIError {
			// The health-latency clock starts HERE — inside the attempt body,
			// i.e. after the slot Acquire succeeded (02-16 queue wait can block
			// for seconds and must never fold into the sample, or latency and
			// loadFactor would double-count the same congestion) and before the
			// upstream request is dispatched.
			attemptStartedAt = time.Now()
			switch relayFormat {
			case types.RelayFormatOpenAIRealtime:
				return relay.WssHelper(c, relayInfo)
			case types.RelayFormatClaude:
				return relay.ClaudeHelper(c, relayInfo)
			case types.RelayFormatGemini:
				return geminiRelayHandler(c, relayInfo)
			default:
				return relayHandler(c, relayInfo)
			}
		})

		// RTE-01 (02-17): record the dispatched attempt for channel routing
		// health — exactly one sample per attempt that actually ran, with its
		// wall-clock duration and outcome. The gateway-side saturation
		// rejection is skipped before the body ever runs (and never sets
		// attemptStartedAt, so the IsZero guard is a second fence), so a
		// client-visible 429 can never pollute channel health; the panic path
		// unwinds past this line and records nothing by design. The
		// slot/defer/refund/retry semantics above and below are untouched.
		if !service.IsChannelConcurrencyRejection(newAPIError) && !attemptStartedAt.IsZero() {
			latencyMs := time.Since(attemptStartedAt).Milliseconds()
			if relayFormat == types.RelayFormatOpenAIRealtime {
				// A WS session's whole lifetime (up to hours) is connection
				// duration, not upstream latency. Record the outcome only:
				// latencyMs 0 keeps latencyEWMA at 0, which the warm-mean and
				// latencyFactor guards (latencyEWMA > 0) exclude from the
				// same-model mean and leave this record's latencyFactor at the
				// neutral 1.0 — success/failure still feeds successEWMA.
				latencyMs = 0
			}
			model.RecordChannelAttempt(channel.Id, relayInfo.OriginModelName, latencyMs, newAPIError == nil)
		}

		if newAPIError == nil {
			service.MarkRequestPolicySuccess(c, relayInfo.StreamStatus)
			relayInfo.LastError = nil
			return
		}

		newAPIError = service.NormalizeViolationFeeError(newAPIError)
		relayInfo.LastError = newAPIError

		decision := service.DecideRelayRetry(c, newAPIError, common.RetryTimes-retryParam.GetRetry())

		// RTE-02 (02-18): "upstream 429 must not terminate after a single
		// channel". When the four-condition predicate holds, rewrite the
		// stop into ONE cross-channel retry before RecordPolicyFailure, so
		// the existing policy event channel records the grant
		// (retry/upstream_429_cross_channel) without a new event field.
		// Keep the original 429 as the fallback terminal, exclude the
		// channel that produced it from the next selection through the
		// 02-15 per-request set (so it is never re-picked and an empty
		// selection still classifies through getChannel's existing rule),
		// and arm the grant flag the loop condition consumes exactly once.
		if service.ShouldGrantUpstream429CrossChannelRetry(newAPIError, &decision, len(triedChannels), upstream429Grants) {
			upstream429Fallback = newAPIError
			upstream429Grants++
			grantUpstream429Retry = true
			retryParam.ExcludeSaturatedChannel(channel.Id)
			decision = service.PolicyDecision{Action: "retry", Reason: service.Upstream429CrossChannelReason, Source: "system"}
		}

		service.RecordPolicyFailure(c, channel.Id, newAPIError, decision)
		processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError, relayInfo)

		// CHN-02: the saturation refusal of THIS attempt's channel joins the
		// per-request exclusion set before the next selection, so the retry
		// walks the remaining candidates instead of re-taking the saturated
		// channel (BCD D-3 option A). Non-saturation errors never record. The
		// set lives on retryParam — one per client request — and never reaches
		// shared state.
		if service.IsChannelConcurrencyRejection(newAPIError) {
			retryParam.ExcludeSaturatedChannel(channel.Id)
		}

		if decision.Action != "retry" {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := common.LogText("retry: %s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}
}

// renderRelayTerminalError writes the terminal error of one relay request in
// the envelope of the negotiated relay format. A channel-saturation rejection
// is normalized HERE — at the client-visible rendering boundary only (BCD
// D-4): type/code become exactly "concurrency_exceeded" (CNT-01), the message
// stays the neutral signal wording plus the request-id suffix, no internal
// "channel:" routing code, scope literal, or channel id crosses the wire, and
// the HTTP response carries Retry-After read from
// middleware.ConcurrencyRetryAfterSeconds (single source, never a literal).
// The internal chain — logs, RecordRequestPolicyTermination,
// RecordPolicyFailure, ProcessChannelError, DecideRelayRetry, error_code
// records — keeps seeing the raw channel:-prefixed signal: the IsChannelError
// retry route and the ShouldDisableChannel auto-ban exemption depend on it, so
// normalization must never rewrite the error itself, only this rendering.
// Realtime upgrades its transport before any terminal write, so that format's
// contract surface is the WS error frame (no HTTP headers are writable there);
// every other format (default, Gemini, Claude) renders through c.JSON with the
// signal's own 429 status.
func renderRelayTerminalError(c *gin.Context, relayFormat types.RelayFormat, ws *websocket.Conn, newAPIError *types.NewAPIError) {
	// 心跳提交响应头后，最终错误继续通过同一个流返回。
	if relayFormat != types.RelayFormatOpenAIRealtime && c.Writer.Written() &&
		strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
		writeRelayStreamError(c, relayFormat, newAPIError)
		return
	}
	upstreamURLs := common.UpstreamAddresses(c)
	// Normalize the client-visible error payload and the Retry-After flag first,
	// then render once per relay format.
	var (
		openaiErr  types.OpenAIError
		claudeErr  types.ClaudeError
		retryAfter bool
	)
	if service.IsChannelConcurrencyRejection(newAPIError) {
		concurrency.AddRejection()
		message := newAPIError.Error()
		openaiErr = types.OpenAIError{
			Message: message,
			Type:    "concurrency_exceeded",
			Param:   "",
			Code:    "concurrency_exceeded",
		}
		claudeErr = types.ClaudeError{Type: "concurrency_exceeded", Message: message}
		retryAfter = true
	} else {
		openaiErr = newAPIError.ToOpenAIError(upstreamURLs...)
		claudeErr = newAPIError.ToClaudeError(upstreamURLs...)
	}
	if relayFormat != types.RelayFormatOpenAIRealtime {
		c.Header("Content-Type", "application/json; charset=utf-8")
	}
	// Realtime upgrades its transport before any terminal write, so its contract
	// surface is the WS error frame (no HTTP headers are writable there).
	if retryAfter && relayFormat != types.RelayFormatOpenAIRealtime {
		c.Header("Retry-After", strconv.Itoa(middleware.ConcurrencyRetryAfterSeconds))
	}
	switch relayFormat {
	case types.RelayFormatOpenAIRealtime:
		helper.WssError(c, ws, openaiErr)
	case types.RelayFormatClaude:
		c.JSON(newAPIError.StatusCode, gin.H{
			"type":  "error",
			"error": claudeErr,
		})
	default:
		c.JSON(newAPIError.StatusCode, gin.H{
			"error": openaiErr,
		})
	}
}

func writeRelayStreamError(c *gin.Context, format types.RelayFormat, apiError *types.NewAPIError) {
	// 心跳提交响应头后，最终错误继续通过同一个流返回。
	status, _ := common.GetContextKeyType[*relaycommon.StreamStatus](c, constant.ContextKeyResponseStreamStatus)
	if status == nil {
		status = relaycommon.NewStreamStatus()
		common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
	}
	status.MarkFailed(string(apiError.GetErrorCode()), string(apiError.GetErrorType()), apiError.StatusCode)
	upstreamURLs := common.UpstreamAddresses(c)
	// The stream is a client-visible rendering boundary too, so the saturation
	// signal is normalized here exactly like in renderRelayTerminalError: the
	// internal "channel:" routing code must not reach the client. The internal
	// error chain keeps the raw signal (status.MarkFailed above and the retry
	// routing both read it).
	openaiErr := apiError.ToOpenAIError(upstreamURLs...)
	claudeErr := apiError.ToClaudeError(upstreamURLs...)
	if service.IsChannelConcurrencyRejection(apiError) {
		message := apiError.Error()
		openaiErr = types.OpenAIError{
			Message: message,
			Type:    "concurrency_exceeded",
			Param:   "",
			Code:    "concurrency_exceeded",
		}
		claudeErr = types.ClaudeError{Type: "concurrency_exceeded", Message: message}
	}
	if format == types.RelayFormatClaude {
		_ = helper.ClaudeData(c, dto.ClaudeResponse{Type: "error", Error: claudeErr})
		return
	}
	_ = helper.ObjectData(c, gin.H{"error": openaiErr})
	helper.Done(c)
}

// CountClaudeTokens implements Anthropic's token-counting utility endpoint.
// It deliberately skips upstream generation and billing; callers use this
// endpoint to size prompts before creating a Message.
func CountClaudeTokens(c *gin.Context) {
	request, err := helper.GetAndValidateClaudeRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": common.MessageWithRequestId(err.Error(), c.GetString(common.RequestIdKey)),
			},
		})
		return
	}

	info := relaycommon.GenRelayInfoClaude(c, request)
	inputTokens, err := service.CountRequestToken(c, request.GetTokenCountMeta(), info)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": common.MessageWithRequestId(err.Error(), c.GetString(common.RequestIdKey)),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"input_tokens": inputTokens})
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime", "responses"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func getChannel(c *gin.Context, info *relaycommon.RelayInfo, retryParam *service.RetryParam) (*model.Channel, *types.NewAPIError) {
	// First hop: use the context-pinned channel unless this request already
	// recorded a saturation refusal on it (CHN-02, BCD D-3 option A). A
	// refused attempt never runs InitChannelMeta, so without this gate every
	// retry would deterministically re-take the same saturated channel and
	// never reach the selector. An excluded pin falls through to re-select.
	if info.ChannelMeta == nil && !retryParam.IsSaturatedChannel(c.GetInt("channel_id")) {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		channel := &model.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}
		service.RequestPolicy(c).BeginAttempt(channel, info.UsingGroup)
		return channel, nil
	}
	channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(retryParam)
	if err != nil {
		return nil, types.NewError(errors.New(i18n.T(c, i18n.MsgRelayRetryGetChannelFailed, map[string]any{"Group": selectGroup, "Model": info.OriginModelName, "Error": err.Error()})), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if channel == nil {
		// CHN-02 all-saturated rule (BCD D-4): an empty selection while this
		// request carries saturation exclusions means every remaining
		// candidate was refused for concurrency, so the terminal is the
		// CNT-01 429 rejection — not a misleading "channel does not exist"
		// error. An EMPTY exclusion set keeps today's error verbatim: a
		// selection that finds nothing without any prior refusal must never be
		// misreported as a concurrency 429 (first hop with no channel).
		//
		// Known race boundary (02-15 key_links, recorded deliberately — NOT
		// tightened here): when the candidate pool collapses for a
		// NON-saturation reason (a channel disabled or removed between
		// attempts by an admin action or auto-disable), "empty selection +
		// non-empty exclusions" can also arise from that collapse, so this
		// 429 classification transiently deviates from CNT-01's "429 only
		// driven by over-limit". The window is an attempt gap and self-heals
		// with channel recovery / subsequent requests.
		if retryParam.HasSaturatedChannels() {
			return nil, service.NewChannelConcurrencyRejection()
		}
		return nil, types.NewError(errors.New(i18n.T(c, i18n.MsgRelayRetryNoAvailableChannel, map[string]any{"Group": selectGroup, "Model": info.OriginModelName})), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}

	info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)

	service.RequestPolicy(c).BeginAttempt(channel, selectGroup)
	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName)
	if newAPIError != nil {
		return nil, newAPIError
	}
	return channel, nil
}

func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	service.ProcessChannelError(c, channelError, err, relayInfo)
}

func RelayMidjourney(c *gin.Context) {
	policy := service.RequestPolicy(c)
	defer func() {
		if policy.Attempts > 0 && !policy.Successful {
			service.RecordRequestPolicyTermination(c, types.NewErrorWithStatusCode(errors.New("Midjourney submission failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway, types.ErrOptionWithSkipRetry()))
		}
	}()
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatMjProxy, nil, nil)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"description": fmt.Sprintf("failed to generate relay info: %s", err.Error()),
			"type":        "upstream_error",
			"code":        4,
		})
		return
	}

	var mjErr *taskdto.MidjourneyResponse
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		mjErr = relay.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		mjErr = relay.RelayMidjourneyTask(c, relayInfo.RelayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		mjErr = relay.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		mjErr = relay.RelaySwapFace(c, relayInfo)
	default:
		mjErr = relay.RelayMidjourneySubmit(c, relayInfo)
	}
	//err = relayMidjourneySubmit(c, relayMode)
	log.Println(mjErr)
	if mjErr != nil {
		policy.Successful = false
		statusCode := http.StatusBadRequest
		if mjErr.Code == 30 {
			mjErr.Result = i18n.T(c, i18n.MsgRelayGroupSaturated)
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result),
			"type":        "upstream_error",
			"code":        mjErr.Code,
		})
		channelId := c.GetInt("channel_id")
		logger.LogError(c, common.LogText("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result)))
	}
}

func RelayNotImplemented(c *gin.Context) {
	err := types.OpenAIError{
		Message: "API not implemented",
		Type:    "new_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	// The web fallback may already have applied static-asset cache headers.
	// A missing API or asset can appear after an upgrade; never cache its 404.
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	err := types.OpenAIError{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

// RelayTaskPluginEndpoint keeps unclaimed shared-endpoint traffic on its
// existing handler while claimed requests enter the generation-pinned
// host-owned protocol bridge.
func RelayTaskPluginEndpoint(c *gin.Context, fallback gin.HandlerFunc) {
	pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
	if !exists {
		fallback(c)
		return
	}
	pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint)
	if !ok || pinned.Plugin == nil || pinned.Generation == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Task protocol request failed",
				"type":    "new_api_error",
				"code":    "task_protocol_error",
			},
		})
		return
	}
	switch pinned.Protocol {
	case "openai_responses":
		serveTaskPluginProtocol(c, pinned, defaultPluginProtocolBridgeDeps())
	case pluginruntime.ProtocolOpenAIImage:
		serveTaskPluginImageProtocol(c, pinned, defaultPluginProtocolBridgeDeps())
	default:
		fallback(c)
	}
}

func RelayTaskFetch(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if taskErr := relay.RelayTaskFetch(c, relayInfo.RelayMode); taskErr != nil {
		respondTaskError(c, taskErr)
	}
}

type taskSubmissionOutcome struct {
	Result    *relay.TaskSubmitResult
	Task      *model.Task
	RelayInfo *relaycommon.RelayInfo
}

func RelayTask(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		respondTaskSubmissionError(c, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if action := c.GetString("task_action"); action != "" {
		relayInfo.Action = action
	}

	if taskErr := relay.ResolveOriginTask(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	if taskErr := relay.ApplyOriginTaskAffinity(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}

	outcome, taskErr := executeTaskSubmission(c, relayInfo)
	if taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	presentTaskSubmission(c, outcome)
}

// executeTaskSubmission owns the retry, billing, and persistence lifecycle.
// It deliberately performs no client response writes so JSON and protocol
// presenters share the same durable task barrier. Its cancellation semantics
// come from c.Request.Context: native task endpoints use the client context,
// while the Responses bridge supplies an independently bounded context.
func executeTaskSubmission(c *gin.Context, relayInfo *relaycommon.RelayInfo) (*taskSubmissionOutcome, *taskdto.TaskError) {
	return executeTaskSubmissionWith(c, relayInfo, relay.RelayTaskSubmit)
}

type taskSubmitAttempt func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *taskdto.TaskError)

func executeTaskSubmissionWith(
	c *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	submit taskSubmitAttempt,
) (*taskSubmissionOutcome, *taskdto.TaskError) {
	policy := service.RequestPolicy(c)
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	diagnostics.start(relayInfo)
	var result *relay.TaskSubmitResult
	var taskErr *taskdto.TaskError
	durable := false
	stage := "start"
	defer func() {
		// The client of a relayed event stream already holds the vendor's
		// work, so the submission is billed instead of refunded.
		if !durable && relayInfo.Billing != nil && !c.Writer.Written() {
			diagnostics.refund(stage)
			relayInfo.Billing.Refund(c)
		}
	}()
	stage = "before_attempt"
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_attempt", 0)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayInfo.TokenGroup,
		ModelName:   relayInfo.OriginModelName,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		stage = "select_channel"
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("before_attempt", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}
		var channel *model.Channel

		if lockedCh, ok := relayInfo.LockedChannel.(*model.Channel); ok && lockedCh != nil {
			channel = lockedCh
			policy.BeginAttempt(channel, relayInfo.UsingGroup)
			if retryParam.GetRetry() > 0 {
				if setupErr := middleware.SetupContextForSelectedChannel(c, channel, relayInfo.OriginModelName); setupErr != nil {
					taskErr = service.TaskErrorWrapperLocal(setupErr.Err, "setup_locked_channel_failed", http.StatusInternalServerError)
					break
				}
			}
		} else {
			var channelErr *types.NewAPIError
			channel, channelErr = getChannel(c, relayInfo, retryParam)
			if channelErr != nil {
				logger.LogError(c, channelErr.Error())
				taskErr = service.TaskErrorWrapperLocal(channelErr.Err, "get_channel_failed", channelErr.StatusCode)
				break
			}
		}
		diagnostics.attempt(retryParam.GetRetry()+1, channel, relayInfo.LockedChannel != nil)

		service.AppendUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			stage = "read_body"
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusRequestEntityTooLarge)
			} else {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusBadRequest)
			}
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		stage = "submit"
		result, taskErr = submit(c, relayInfo)
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("after_submit", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}
		if taskErr == nil {
			diagnostics.attemptSucceeded(retryParam.GetRetry()+1, result)
			break
		}

		taskAPIError := taskSubmissionAPIError(taskErr)
		relayInfo.LastError = taskAPIError
		decision := decideTaskRetry(c, taskErr, common.RetryTimes-retryParam.GetRetry())
		service.RecordPolicyFailure(c, channel.Id, taskAPIError, decision)
		if !taskErr.LocalError {
			processChannelError(c,
				*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey,
					common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()),
				taskAPIError,
				relayInfo)
		}

		willRetry := decision.Action == "retry"
		diagnostics.attemptFailed(retryParam.GetRetry()+1, channel, taskErr, willRetry)
		if !willRetry {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := common.LogText("retry: %s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}

	if taskErr != nil {
		diagnostics.failed(stage, "task_error", taskErr, false)
		return nil, taskErr
	}
	if result == nil {
		taskErr = service.TaskErrorWrapperLocal(errors.New("task submission returned no result"), "task_submit_failed", http.StatusInternalServerError)
		diagnostics.failed("submit", "missing_result", taskErr, false)
		return nil, taskErr
	}
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_reserve", retryParam.GetRetry()+1)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	// Reserve any submit-time upward billing adjustment before persistence.
	// This keeps insertion failures fully refundable while ensuring settlement
	// after the barrier normally has a zero positive delta. A relayed event
	// stream is billed even when this reserve fails: settlement then charges
	// past the available quota, as it does for any usage above its reservation.
	if relayInfo.Billing != nil {
		stage = "reserve"
		diagnostics.reserve("reserve_start", result.Quota)
		if reserveErr := relayInfo.Billing.Reserve(result.Quota); reserveErr != nil {
			common.SysError(common.LogText("reserve adjusted task billing error: %s", reserveErr.Error()))
			if !c.Writer.Written() {
				taskErr = service.TaskErrorWrapperLocal(errors.New("insufficient quota for adjusted task cost"), string(types.ErrorCodeInsufficientUserQuota), http.StatusForbidden)
				diagnostics.failed("reserve", "insufficient_quota", taskErr, false)
				return nil, taskErr
			}
		} else {
			diagnostics.reserve("reserve_complete", result.Quota)
		}
	}
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_insert", retryParam.GetRetry()+1)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	stage = "insert"
	task := model.InitTask(result.Platform, relayInfo)
	task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(c)
	task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
	task.PrivateData.BillingSource = relayInfo.BillingSource
	task.PrivateData.SubscriptionId = relayInfo.SubscriptionId
	task.PrivateData.TokenId = relayInfo.TokenId
	task.PrivateData.NodeName = common.NodeName
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		ModelPrice:      relayInfo.PriceData.ModelPrice,
		GroupRatio:      relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		ModelRatio:      relayInfo.PriceData.ModelRatio,
		OtherRatios:     relayInfo.PriceData.OtherRatios(),
		OriginModelName: relayInfo.OriginModelName,
		PerCallBilling:  common.StringsContains(constant.TaskPricePatches, relayInfo.OriginModelName) || relayInfo.PriceData.UsePrice,
		TieredSnapshot:  relayInfo.TieredBillingSnapshot,
	}
	task.Quota = result.Quota
	task.Data = result.TaskData
	if len(result.PluginState) > 0 {
		task.PrivateData.PluginState = result.PluginState
	}
	task.Action = relayInfo.Action
	if immediate := result.Immediate; immediate != nil {
		task.Status = model.TaskStatus(immediate.Status)
		task.Progress = immediate.Progress
		if immediate.Status == model.TaskStatusSuccess || immediate.Status == model.TaskStatusFailure {
			task.FinishTime = time.Now().Unix()
		}
		if immediate.Status == model.TaskStatusFailure {
			task.FailReason = immediate.Reason
		}
		if immediate.Url != "" {
			task.PrivateData.ResultURL = immediate.Url
		} else if immediate.Status == model.TaskStatusSuccess {
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
	}
	// A native submit route may declare retainResult: false. It applies only
	// to immediate terminal results: the client receives the complete response
	// once, the upstream snapshot is never persisted, and the task is not
	// retrievable afterwards. An asynchronous result on such a route keeps its
	// snapshot because polling and retrieval need it. The OpenAI Images
	// protocol delivers its images inline in the same HTTP response, so its
	// immediate results follow the same rule; an asynchronous image task is
	// expected there and is polled inside the request.
	var insertOmits []string
	immediateTerminal := result.Immediate != nil && (result.Immediate.Status == model.TaskStatusSuccess || result.Immediate.Status == model.TaskStatusFailure)
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		pinned, ok := pinnedValue.(pluginruntime.PinnedRoute)
		if ok && pinned.Route.RetainResult != nil && !*pinned.Route.RetainResult {
			if immediateTerminal {
				task.PrivateData.ResultDiscarded = true
				insertOmits = append(insertOmits, "data")
			} else {
				logger.LogWarn(c, common.LogText("task plugin route %s %s declares retainResult: false but returned an asynchronous result; retaining task %s", pinned.Route.Method, pinned.Route.Path, task.TaskID))
			}
		}
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists && immediateTerminal {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == pluginruntime.ProtocolOpenAIImage {
			task.PrivateData.ResultDiscarded = true
			insertOmits = append(insertOmits, "data")
		}
	}
	diagnostics.insertStart(task)
	if insertErr := task.InsertWithContext(c.Request.Context(), insertOmits...); insertErr != nil {
		common.SysError(common.LogText("insert task error: %s", insertErr.Error()))
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to persist task"), "task_insert_failed", http.StatusInternalServerError)
		diagnostics.failed("insert", "database_error", taskErr, false)
		// The client of a relayed event stream already holds the vendor's
		// work, so it is settled and logged without a task row.
		if c.Writer.Written() {
			if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
				common.SysError(common.LogText("settle task billing error: %s", settleErr.Error()))
				return nil, taskErr
			}
			service.LogTaskConsumption(c, relayInfo, task)
		}
		return nil, taskErr
	}
	durable = true
	stage = "settle"
	diagnostics.durable(task)
	diagnostics.settleStart(task, result.Quota)

	if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
		common.SysError(common.LogText("settle task billing error: %s", settleErr.Error()))
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to settle task billing"), "task_billing_settlement_failed", http.StatusInternalServerError)
		diagnostics.failed("settle", "billing_error", taskErr, true)
		return nil, taskErr
	}
	perfmetrics.RecordImmediateTaskResult(relayInfo, result.Immediate)
	if task.Status != model.TaskStatusFailure {
		service.MarkRequestPolicySuccess(c, nil)
	} else {
		policy.AddEvent(service.PolicyEvent{Decision: service.PolicyDecision{Action: "stop", Reason: "task_failed", Source: "upstream"}})
	}
	service.LogTaskConsumption(c, relayInfo, task)
	diagnostics.complete(task, result.Quota)

	return &taskSubmissionOutcome{Result: result, Task: task, RelayInfo: relayInfo}, nil
}

func presentTaskSubmission(c *gin.Context, outcome *taskSubmissionOutcome) {
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	// A native route that relayed the vendor's event stream has answered.
	if c.Writer.Written() {
		diagnostics.present(outcome.Task, "native_stream")
		return
	}
	otherRatios := outcome.RelayInfo.PriceData.OtherRatios()
	if otherRatios == nil {
		otherRatios = map[string]float64{}
	}
	if ratiosJSON, err := common.Marshal(otherRatios); err == nil {
		c.Header("X-New-Api-Other-Ratios", string(ratiosJSON))
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedRoute); ok && pinned.Plugin != nil && pinned.Route.Render != "" {
			view, err := service.BuildTaskPluginView(outcome.Task)
			requestValue, _ := c.Get(pluginruntime.ContextKeyRouteRequest)
			requestContext, _ := requestValue.(pluginruntime.RouteRequestContext)
			if err == nil {
				viewValue, valueErr := taskPluginProtocolJSONValue(view)
				if valueErr == nil {
					if body, callErr := pinned.Plugin.Engine.CallPath(c.Request.Context(), "native", []string{pinned.Route.Render}, requestContext.JSValue(), viewValue); callErr == nil {
						if replyErr := middleware.WriteNativeReply(c, http.StatusOK, body); replyErr == nil {
							diagnostics.present(outcome.Task, "native_presenter")
							return
						} else {
							logger.LogError(c, common.LogText("task plugin native submit presenter failed: %s", replyErr.Error()))
						}
					} else {
						logger.LogError(c, common.LogText("task plugin native submit presenter failed: %s", callErr.Error()))
					}
				} else {
					logger.LogError(c, common.LogText("encode task plugin native submit view failed: %s", valueErr.Error()))
				}
			} else {
				logger.LogError(c, common.LogText("build task plugin native submit view failed: %s", err.Error()))
			}
		}
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == "openai_video" && pinned.Operation.Name == "create" {
			diagnostics.present(outcome.Task, "openai_video_create")
			c.JSON(http.StatusOK, outcome.Task.ToOpenAIVideo())
			return
		}
		// An accepted audio task answers with the same transcription object the
		// retrieve endpoint later returns, so one client parser covers both.
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == pluginruntime.ProtocolOpenAIAudio && pinned.Operation.Name == "create" {
			body, renderErr := relay.RenderOpenAIAudioTask(outcome.Task)
			if renderErr == nil {
				diagnostics.present(outcome.Task, "openai_audio_create")
				c.Data(http.StatusOK, "application/json", body)
				return
			}
			logger.LogError(c, "task plugin openai_audio create presenter failed: "+renderErr.Message)
		}
	}
	createdAt := outcome.Task.CreatedAt
	if createdAt == 0 {
		createdAt = outcome.Task.SubmitTime
	}
	diagnostics.present(outcome.Task, "host_fallback")
	c.JSON(http.StatusOK, map[string]any{
		"id":         outcome.Task.TaskID,
		"task_id":    outcome.Task.TaskID,
		"status":     "queued",
		"model":      outcome.RelayInfo.OriginModelName,
		"created_at": createdAt,
	})
}

func respondTaskSubmissionError(c *gin.Context, taskErr *taskdto.TaskError) {
	service.RecordRequestPolicyTermination(c, taskSubmissionAPIError(taskErr))
	newTaskPluginSubmitDiagnostics(c).presentError(taskErr)
	// The client of a relayed event stream already has the vendor's events,
	// including any error event; nothing can follow them.
	if c.Writer.Written() {
		return
	}
	if middleware.RespondTaskPluginError(c, taskErr) {
		return
	}
	respondTaskError(c, taskErr)
}

// respondTaskError 统一输出 Task 错误响应（含 429 限流提示改写）
func respondTaskError(c *gin.Context, taskErr *taskdto.TaskError) {
	if taskErr.StatusCode == http.StatusTooManyRequests {
		taskErr.Message = i18n.T(c, i18n.MsgRelayGroupUpstreamSaturated)
	}
	c.JSON(taskErr.StatusCode, taskErr)
}

// taskSubmissionAPIError adapts a task error for the shared relay error paths.
// TaskError.Error is nil for many local rejections, so fall back to the message.
func taskSubmissionAPIError(taskErr *taskdto.TaskError) *types.NewAPIError {
	err := taskErr.Error
	if err == nil {
		err = errors.New(taskErr.Message)
	}
	return types.NewOpenAIError(err, types.ErrorCodeBadResponseStatusCode, taskErr.StatusCode)
}

// decideTaskRetry is the single retry decision for task submissions. The
// reason is recorded in the request policy decision events of the log details.
func decideTaskRetry(c *gin.Context, taskErr *taskdto.TaskError, retryTimes int) service.PolicyDecision {
	stop := service.PolicyDecision{Action: "stop", Source: "system"}
	retry := service.PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "system"}
	switch {
	case taskErr == nil:
		stop.Reason = "request_completed"
	case taskErr.NoRetry:
		stop.Reason = "task_accepted"
	case service.ShouldSkipRetryAfterChannelAffinityFailure(c):
		stop.Reason, stop.Source = "strict_session", "session_rule"
		if source := service.RequestPolicy(c).SessionModeSource; source != "" {
			stop.Source = source
		}
	case retryTimes <= 0:
		stop.Reason, stop.Source = "attempt_budget_exhausted", "global"
	case service.GetChannelConstraints(c).SuppressesRetry():
		stop.Reason, stop.Source = "pinned_channel", "channel_constraint"
	case taskErr.StatusCode == http.StatusTooManyRequests, taskErr.StatusCode == 307:
		return retry
	case taskErr.StatusCode/100 == 5:
		// 超时不重试
		if operation_setting.IsAlwaysSkipRetryStatusCode(taskErr.StatusCode) {
			stop.Reason = "system_retry_exclusion"
			break
		}
		return retry
	case taskErr.StatusCode == http.StatusBadRequest, taskErr.StatusCode == 408:
		// azure处理超时不重试
		stop.Reason = "status_not_retryable"
	case taskErr.LocalError:
		stop.Reason = "local_rejection"
	case taskErr.StatusCode/100 == 2:
		stop.Reason = "system_retry_exclusion"
	default:
		return retry
	}
	return stop
}
