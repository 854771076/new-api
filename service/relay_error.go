package service

import (
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// ChannelConcurrencyExceededCode is the rejection code produced when an attempt
// cannot take a slot on its selected channel (CHN-03). The "channel:" prefix is
// deliberate: it makes types.IsChannelError hit, so DecideRelayRetry routes the
// saturation rejection to a retry for free (BCD D-4). Normalizing what the
// client finally sees (type/code "concurrency_exceeded" + Retry-After) belongs
// to 02-15 — this constant is the seam it consumes.
const ChannelConcurrencyExceededCode types.ErrorCode = "channel:concurrency_exceeded"

// channelConcurrencyRejectionMessage is fixed neutral wording shared by every
// saturation rejection. It must never embed the scope literal ("channel:<id>")
// or the channel id (CNT-01 information hygiene): neither helps the caller and
// both leak routing internals.
const channelConcurrencyRejectionMessage = "Concurrency limit exceeded for the selected channel. Please retry after the current requests finish."

// NewChannelConcurrencyRejection builds the saturation rejection signal: code
// ChannelConcurrencyExceededCode, status 429, and the fixed neutral message.
// It carries no scope string and no channel id.
func NewChannelConcurrencyRejection() *types.NewAPIError {
	return types.NewError(
		errors.New(channelConcurrencyRejectionMessage),
		ChannelConcurrencyExceededCode,
		types.ErrOptionWithStatusCode(http.StatusTooManyRequests),
	)
}

// IsChannelConcurrencyRejection reports whether err is the channel saturation
// rejection. Callers that must NOT treat saturation as a channel fault (for
// example the auto-ban chokepoint in ShouldDisableChannel) use this to exclude
// it: saturation is a scheduling refusal, not evidence the channel is broken.
func IsChannelConcurrencyRejection(err *types.NewAPIError) bool {
	return err != nil && err.GetErrorCode() == ChannelConcurrencyExceededCode
}

// Upstream429CrossChannelReason is the decision reason recorded when the
// relay grants the single RTE-02 cross-channel hop for an upstream 429. It
// rides the existing RecordPolicyFailure decision event (Action "retry"), so
// the policy event surface gains no new field — the reason string is the only
// observable marker.
const Upstream429CrossChannelReason = "upstream_429_cross_channel"

// ShouldGrantUpstream429CrossChannelRetry decides the RTE-02 rule "an
// upstream 429 must not terminate the request after a single channel": the
// relay may grant exactly one extra cross-channel attempt when ALL FOUR
// conditions hold:
//
//  1. decision.Action == "stop" && decision.Reason == "attempt_budget_exhausted"
//     — the budget check in DecideRelayRetry runs BEFORE the status-code
//     check, so under the default RetryTimes=0 a non-channel terminal always
//     carries this reason; structurally the same branch is unreachable for
//     "channel:"-prefixed errors (IsChannelError returns earlier with
//     Action "retry") and for the request-level/explicit stops below, so the
//     grant never overrides pinned_channel, strict_session, or
//     non_retryable_error — those three stop categories are white-listed out
//     by construction, not by an extra check. Reachability boundary for
//     "status_not_retryable": it is only reachable when RetryTimes >= 1 and
//     the status sits outside the retryable ranges — under the default 0 the
//     reason cannot occur on this path, so operator config on the default
//     path does not suppress the grant; with RetryTimes >= 1 the config does
//     suppress it through this condition. The predicate itself is a pure
//     unit: it rejects that reason whenever it is passed.
//  2. err.StatusCode == 429 — an upstream 429 is refused before the response
//     body is dispatched, so re-selecting cannot duplicate client output.
//  3. distinctTriedChannels == 1 — the attempt history is the per-request
//     tried set registered next to AppendUsedChannel (selected = counted, so
//     a request that already crossed channels never grants again).
//  4. priorGrants == 0 — at most one grant per client request; the loop
//     additionally consumes the grant flag on the next iteration's first
//     line, so the two checks together rule out self-spin.
//
// A nil err or nil decision is not a grant.
func ShouldGrantUpstream429CrossChannelRetry(err *types.NewAPIError, decision *PolicyDecision, distinctTriedChannels, priorGrants int) bool {
	if err == nil || decision == nil {
		return false
	}
	return decision.Action == "stop" &&
		decision.Reason == "attempt_budget_exhausted" &&
		err.StatusCode == http.StatusTooManyRequests &&
		distinctTriedChannels == 1 &&
		priorGrants == 0
}

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) || operation_setting.IsAlwaysSkipRetryStatusCode(code) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	upstreamURLs := common.UpstreamAddresses(c)
	logger.LogError(c, common.LogText("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode(upstreamURLs...))))
	if ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode(upstreamURLs...)
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		// The operator-facing reason explains what actually went wrong
		// upstream. It is admin-only: it lives in the admin section, so a log
		// owner never receives it.
		if reason := err.AdminReason(); reason != "" {
			other.SetAdmin("upstream_error_reason", reason)
		}
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendResponseModelLogInfo(relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(upstreamURLs...), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}
