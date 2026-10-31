package db

import (
	"database/sql"
	"regexp"
	"sync"
	"testing"
	"time"
)

func openOwnTestDB(t *testing.T) {
	t.Helper()
	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	old := DB
	DB = conn
	ownTableOnce = sync.Once{}
	t.Cleanup(func() { DB = old; conn.Close(); ownTableOnce = sync.Once{} })
	// BindOwnToRedemption 依赖上游的绑定表
	if _, err := conn.Exec(`CREATE TABLE cdk_session_bindings (cdk_code TEXT PRIMARY KEY, session_payload TEXT NOT NULL,
		redemption_token TEXT, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
}

func TestOwnCodeFormat(t *testing.T) {
	re := regexp.MustCompile(`^GPT(GO|PLUS|5X|20X|50X)-[2-9A-HJKMNP-Z]{10}-[2-9A-HJKMNP-Z]{10}$`)
	for plan, tag := range OwnPlans {
		code, err := NewOwnCode(plan)
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(code) || code[3:3+len(tag)] != tag {
			t.Fatalf("plan %s → %q", plan, code)
		}
	}
	if _, err := NewOwnCode("plus_half"); err == nil {
		t.Fatal("plus_half must not be issuable as an own code")
	}
}

func TestCreateOwnCDKsValidates(t *testing.T) {
	openOwnTestDB(t)
	if _, err := CreateOwnCDKs("plus_half", "PH", "", 1); err == nil {
		t.Fatal("plus_half should be rejected")
	}
	if _, err := CreateOwnCDKs("plus", "TR", "", 1); err == nil {
		t.Fatal("unsupported region should be rejected")
	}
	if _, err := CreateOwnCDKs("plus", "PH", "", 0); err == nil {
		t.Fatal("count 0 should be rejected")
	}
	codes, err := CreateOwnCDKs("pro_5x", "auto", "批次A", 3)
	if err != nil || len(codes) != 3 {
		t.Fatalf("codes=%v err=%v", codes, err)
	}
	o, err := GetOwnCDK(" " + codes[0] + " ")
	if err != nil || o == nil || o.Plan != "pro_5x" || o.Region != "auto" || o.Status != OwnStatusUnused || o.Note != "批次A" {
		t.Fatalf("got %+v err=%v", o, err)
	}
	if o, _ := GetOwnCDK("GPTPLUS-NOPE"); o != nil {
		t.Fatal("unknown code should be nil")
	}
}

// 同一张码同时被多个请求抢占时只能有一个成功：这就是「只买一张卡台码」的保证。
func TestClaimOwnCDKSingleWinner(t *testing.T) {
	openOwnTestDB(t)
	codes, err := CreateOwnCDKs("plus", "PH", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	code := codes[0]
	wins := 0
	for i := 0; i < 5; i++ {
		ok, err := ClaimOwnCDKForIssue(code, "plus", "PH", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("claims won = %d, want 1", wins)
	}
	// 确定没发出码 → 放回，可再抢
	if err := ReleaseOwnCDKClaim(code); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ClaimOwnCDKForIssue(code, "plus", "PH", time.Minute); !ok {
		t.Fatal("released code should be claimable again")
	}
	// 买到后标记 issued，不能再被抢
	if err := MarkOwnCDKIssued(code, "GPPLUS-AAA-BBB", "plus_half", "ph", 42); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ClaimOwnCDKForIssue(code, "plus", "PH", 0); ok {
		t.Fatal("issued code must not be claimable")
	}
	o, _ := GetOwnCDK(code)
	if o.Status != OwnStatusIssued || o.PlatformCode != "GPPLUS-AAA-BBB" || o.PlatformPlan != "plus_half" || o.PlatformRegion != "PH" || o.PlatformID != 42 {
		t.Fatalf("got %+v", o)
	}
}

// 上次请求中断（卡在 issuing）：超过 stale 时间才允许重新抢占。
func TestClaimOwnCDKStaleReclaim(t *testing.T) {
	openOwnTestDB(t)
	codes, _ := CreateOwnCDKs("plus", "PH", "", 1)
	code := codes[0]
	if ok, _ := ClaimOwnCDKForIssue(code, "plus_half", "PH", time.Hour); !ok {
		t.Fatal("first claim should win")
	}
	if ok, _ := ClaimOwnCDKForIssue(code, "plus_half", "PH", time.Hour); ok {
		t.Fatal("fresh issuing must not be reclaimed")
	}
	if _, err := DB.Exec(`UPDATE own_cdk_codes SET claimed_at = datetime('now','-2 hours') WHERE code = ?`, code); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ClaimOwnCDKForIssue(code, "plus_half", "PH", time.Hour); !ok {
		t.Fatal("stale issuing should be reclaimable")
	}
}

func TestDisableOnlyUnused(t *testing.T) {
	openOwnTestDB(t)
	codes, _ := CreateOwnCDKs("plus", "PH", "", 2)
	a, _ := GetOwnCDK(codes[0])
	b, _ := GetOwnCDK(codes[1])
	_, _ = ClaimOwnCDKForIssue(b.Code, "plus", "PH", time.Minute)
	_ = MarkOwnCDKIssued(b.Code, "X", "plus", "PH", 1)
	n, err := SetOwnCDKDisabled([]int64{a.ID, b.ID}, true)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v (issued code must not be disabled)", n, err)
	}
	if ok, _ := ClaimOwnCDKForIssue(a.Code, "plus", "PH", time.Minute); ok {
		t.Fatal("disabled code must not be claimable")
	}
	if n, _ := SetOwnCDKDisabled([]int64{a.ID}, false); n != 1 {
		t.Fatal("re-enable failed")
	}
}

func TestBindOwnToRedemption(t *testing.T) {
	openOwnTestDB(t)
	if err := BindOwnToRedemption("gptplus-aaa", "tok-1", `{"user":{"email":"a@b.c"},"sessionToken":"x"}`); err != nil {
		t.Fatal(err)
	}
	b, err := GetBindingByCDK("GPTPLUS-AAA")
	if err != nil || b == nil || b.RedemptionToken != "tok-1" || b.SessionPayload == "" {
		t.Fatalf("binding=%+v err=%v", b, err)
	}
}

// 过期重抢时档位、地区必须保留第一次写下的（否则换幂等键会再买一张）。
func TestStaleReclaimKeepsFirstPlan(t *testing.T) {
	openOwnTestDB(t)
	codes, _ := CreateOwnCDKs("plus", "auto", "", 1)
	code := codes[0]
	if ok, _ := ClaimOwnCDKForIssue(code, "plus_half", "EG", time.Hour); !ok {
		t.Fatal("first claim")
	}
	_, _ = DB.Exec(`UPDATE own_cdk_codes SET claimed_at = datetime('now','-2 hours') WHERE code = ?`, code)
	if ok, _ := ClaimOwnCDKForIssue(code, "plus", "PH", time.Hour); !ok {
		t.Fatal("stale reclaim")
	}
	o, _ := GetOwnCDK(code)
	if o.PlatformPlan != "plus_half" || o.PlatformRegion != "EG" {
		t.Fatalf("reclaim overwrote plan/region: %+v", o)
	}
	// 明确没发出码时放回：档位地区一起清掉，下次重新判断
	_ = ReleaseOwnCDKClaim(code)
	o, _ = GetOwnCDK(code)
	if o.Status != OwnStatusUnused || o.PlatformPlan != "" || o.PlatformRegion != "" {
		t.Fatalf("release: %+v", o)
	}
}

func TestSaveOwnRedeemAttempt(t *testing.T) {
	openOwnTestDB(t)
	codes, _ := CreateOwnCDKs("plus", "PH", "", 1)
	code := codes[0]
	if err := SaveOwnRedeemAttempt(code, "", "t", "p", "r"); err == nil {
		t.Fatal("must not save attempt before a platform code is bought")
	}
	_, _ = ClaimOwnCDKForIssue(code, "plus", "PH", time.Minute)
	_ = MarkOwnCDKIssued(code, "GPPLUS-X", "plus", "PH", 7)
	if err := SaveOwnRedeemAttempt(code, "", "tok", "pf", "own-abc-1"); err != nil {
		t.Fatal(err)
	}
	o, _ := GetOwnCDK(code)
	if o.RedemptionToken != "tok" || o.PreflightToken != "pf" || o.ClientRequestID != "own-abc-1" {
		t.Fatalf("got %+v", o)
	}
	// 并发的另一个请求（以为还是第一次）不能覆盖已写入的那次
	if err := SaveOwnRedeemAttempt(code, "", "tok2", "pf2", "own-abc-1"); err == nil {
		t.Fatal("stale attempt must not overwrite")
	}
	// 上一单失败后的下一次：基于当前请求号写入
	if err := SaveOwnRedeemAttempt(code, "own-abc-1", "tok3", "pf3", "own-abc-2"); err != nil {
		t.Fatal(err)
	}
	if o, _ := GetOwnCDK(code); o.ClientRequestID != "own-abc-2" || o.RedemptionToken != "tok3" {
		t.Fatalf("got %+v", o)
	}
}

// 换码：只换没买过卡台码的；旧码作废并记下新码；新码继承地区，档位可改。
func TestReplaceOwnCDK(t *testing.T) {
	openOwnTestDB(t)
	codes, _ := CreateOwnCDKs("plus", "auto", "发给A", 2)
	fresh, used := codes[0], codes[1]

	newCode, err := ReplaceOwnCDK(fresh, "pro_5x", "")
	if err != nil {
		t.Fatal(err)
	}
	o, _ := GetOwnCDK(fresh)
	if o.Status != OwnStatusDisabled || o.ReplacedBy != newCode {
		t.Fatalf("old: %+v", o)
	}
	n, _ := GetOwnCDK(newCode)
	if n == nil || n.Status != OwnStatusUnused || n.Plan != "pro_5x" || n.Region != "auto" || n.Note != "发给A" || n.Replaces != fresh {
		t.Fatalf("new: %+v", n)
	}
	if n.Code[:6] != "GPT5X-" {
		t.Fatalf("new code tag should follow new plan: %s", n.Code)
	}
	// 作废的旧码不能再换、不能再抢占
	if _, err := ReplaceOwnCDK(fresh, "", ""); err == nil {
		t.Fatal("disabled code must not be replaced again")
	}
	if ok, _ := ClaimOwnCDKForIssue(fresh, "plus", "PH", time.Minute); ok {
		t.Fatal("replaced code must not be claimable")
	}

	// 已买过卡台码的不能换
	_, _ = ClaimOwnCDKForIssue(used, "plus", "PH", time.Minute)
	_ = MarkOwnCDKIssued(used, "GPPLUS-X", "plus", "PH", 1)
	if _, err := ReplaceOwnCDK(used, "", ""); err == nil {
		t.Fatal("code with a platform code must not be replaced")
	}
	// 正在购买中（issuing）的也不能换
	more, _ := CreateOwnCDKs("plus", "PH", "", 1)
	_, _ = ClaimOwnCDKForIssue(more[0], "plus", "PH", time.Minute)
	if _, err := ReplaceOwnCDK(more[0], "", ""); err == nil {
		t.Fatal("issuing code must not be replaced")
	}
}
