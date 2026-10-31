package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tuzi/cdk-recharge-system/internal/cardplatform"
	"github.com/tuzi/cdk-recharge-system/internal/db"
)

func TestParseOwnCheckHalfEligibility(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"eligible", `{"promotions":{"plus_half":{"eligible":true}}}`, true},
		{"not eligible", `{"promotions":{"plus_half":{"eligible":false}}}`, false},
		{"unknown counts as full price", `{"promotions":{"plus_half":{"eligible":null}}}`, false},
		{"no promotions", `{}`, false},
	}
	for _, c := range cases {
		if got := parseOwnCheck(json.RawMessage(c.raw)).HalfEligible; got != c.want {
			t.Errorf("%s: HalfEligible=%v want %v", c.name, got, c.want)
		}
	}
}

// 只有卡台明确的业务拒绝才放回 unused；可能已经发出码的情况都不能放回，否则下次会再买一张。
func TestIssueDefinitelyRejected(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"balance insufficient", &cardplatform.APIError{HTTPStatus: 400, Code: 400, Msg: "余额不足"}, true},
		{"plan not sellable", &cardplatform.APIError{HTTPStatus: 403, Msg: "forbidden"}, true},
		{"server error", &cardplatform.APIError{HTTPStatus: 502, Msg: "bad gateway"}, false},
		{"proxy html page", &cardplatform.APIError{HTTPStatus: 400, Msg: "invalid json from card platform: <html>"}, false},
		{"idempotency in flight", &cardplatform.APIError{HTTPStatus: 409, Msg: "processing"}, false},
		{"rate limited", &cardplatform.APIError{HTTPStatus: 429}, false},
		{"timeout", context.DeadlineExceeded, false},
		{"network", errors.New("connection reset"), false},
	}
	for _, c := range cases {
		if got := issueDefinitelyRejected(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestNextOwnRequestID(t *testing.T) {
	a := nextOwnRequestID("GPTPLUS-A", "GPPLUS-X", "")
	if !strings.HasSuffix(a, "-1") {
		t.Fatalf("first attempt = %q", a)
	}
	b := nextOwnRequestID("GPTPLUS-A", "GPPLUS-X", a)
	if !strings.HasSuffix(b, "-2") || strings.TrimSuffix(a, "-1") != strings.TrimSuffix(b, "-2") {
		t.Fatalf("second attempt = %q (prev %q)", b, a)
	}
	// 换了卡台码（半价失效改买普通 Plus）就从 1 重新开始，前缀也不同
	c := nextOwnRequestID("GPTPLUS-A", "GPPLUS-Y", b)
	if !strings.HasSuffix(c, "-1") || strings.TrimSuffix(c, "-1") == strings.TrimSuffix(a, "-1") {
		t.Fatalf("new platform code = %q", c)
	}
	if len(a) > 80 {
		t.Fatalf("client_request_id must be <= 80 bytes, got %d", len(a))
	}
}

func TestStripHalf(t *testing.T) {
	got := string(stripHalf(json.RawMessage(`{"data":{"order":{"plan":"plus_half","status":"queued"}}}`)))
	if strings.Contains(got, "plus_half") || !strings.Contains(got, `"plan":"plus"`) {
		t.Fatalf("got %s", got)
	}
}

func TestParseOwnCheckAccountFields(t *testing.T) {
	r := parseOwnCheck(json.RawMessage(`{"email":"a@b.c","currentPlan":"pro","subscription_has_active":true,
		"subscription_active_until":"2026-11-05T08:42:19Z","subscription_recovery_required":true,"blocking_reason":"x"}`))
	if r.Email != "a@b.c" || r.CurrentPlan != "pro" || r.HasActive == nil || !*r.HasActive || !r.RecoveryRequired || r.BlockingReason != "x" {
		t.Fatalf("got %+v", r)
	}
}

// 用户看到的档位里不能出现 plus_half。
func TestPublicPlanHidesHalf(t *testing.T) {
	if publicPlan("plus_half") != "plus" || publicPlan("pro_5x") != "pro_5x" {
		t.Fatal("publicPlan must map plus_half → plus only")
	}
}

func TestIsHalfIneligible(t *testing.T) {
	if !isHalfIneligible(json.RawMessage(`{"code":400,"error_code":"half_price_ineligible"}`)) {
		t.Fatal("should detect half_price_ineligible")
	}
	if !isHalfIneligible(json.RawMessage(`{"code":0,"data":{"order":{"status":"failed_precharge","error_code":"half_price_ineligible"}}}`)) {
		t.Fatal("should detect it on the order")
	}
	// 只在事件文案里出现这个词，不算（进行中订单不能被误判后再买一张）
	if isHalfIneligible(json.RawMessage(`{"code":0,"data":{"order":{"status":"running"},"events":[{"public_message":"half_price_ineligible 重试中"}]}}`)) {
		t.Fatal("event text must not trigger fallback")
	}
	if isHalfIneligible(json.RawMessage(`{"code":400,"error_code":"GPT_PLAN_ALREADY_ACTIVE"}`)) {
		t.Fatal("other errors are not half-price errors")
	}
}

func TestPublicOK(t *testing.T) {
	if !publicOK(200, json.RawMessage(`{"code":0,"data":{}}`)) || !publicOK(200, json.RawMessage(`{"data":{}}`)) {
		t.Fatal("success envelopes")
	}
	if publicOK(200, json.RawMessage(`{"code":400,"msg":"x"}`)) || publicOK(400, json.RawMessage(`{}`)) {
		t.Fatal("failures")
	}
}

// 幂等键：同一张自有码 + 同一档位必须稳定，换档位必须不同（否则换发普通 Plus 时会拿回旧的半价码）。
func TestOwnIdemKey(t *testing.T) {
	a := ownIdemKey("GPTPLUS-AAA", "plus_half")
	if a != ownIdemKey("gptplus-aaa", "plus_half") {
		t.Fatal("key must be case-insensitive and stable")
	}
	if a == ownIdemKey("GPTPLUS-AAA", "plus") {
		t.Fatal("different plan must give a different key")
	}
}

func TestOwnRegionFallback(t *testing.T) {
	for in, want := range map[string]string{"jp": "JP", "auto": "PH", "": "PH", "TR": "PH"} {
		if got := ownRegion(&db.OwnCDK{Region: in}); got != want {
			t.Errorf("region %q → %q want %q", in, got, want)
		}
	}
}

// 重试时判断「原订单失败了没有」「卡台码释放了没有」，两者都以卡台返回为准。
func TestOrderFailedAndCDKStatus(t *testing.T) {
	failed := json.RawMessage(`{"code":0,"data":{"order":{"status":"failed_precharge","cdk_status":"unused"},"events":[]}}`)
	if !orderFailed(failed) || cdkStatusOf(failed) != "unused" {
		t.Fatal("failed order with released code")
	}
	running := json.RawMessage(`{"code":0,"data":{"order":{"status":"running","cdk_status":"reserved"}}}`)
	if orderFailed(running) || cdkStatusOf(running) != "reserved" {
		t.Fatal("in-progress order must not count as failed")
	}
	// 兑换接口的简短返回：data 直接是 {id,status,stage}
	if !orderFailed(json.RawMessage(`{"code":0,"data":{"id":456,"status":"declined"}}`)) {
		t.Fatal("flat redeem response")
	}
	if orderFailed(json.RawMessage(`{"code":0,"data":{"id":456,"status":"completed"}}`)) {
		t.Fatal("completed is not failed")
	}
	if cdkStatusOf(json.RawMessage(`{"code":0,"data":{"id":1,"status":"queued"}}`)) != "" {
		t.Fatal("no cdk_status → empty")
	}
}
