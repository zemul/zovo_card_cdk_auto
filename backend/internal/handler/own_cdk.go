package handler

// 自有卡密（fork）：用户拿本站自己发的码兑换时，现场向卡台购买一张卡台 CDK 再走现有兑换流程。
// ★一张自有码只对应一张卡台码★：买码只在 ensurePlatformCode 一处，数据库抢占保证只买一次。
// 半价资格在兑换时失效也不换发，直接提示失败（那张 plus_half 卡台码仍是 unused，后台可见）。
//
//	/own-cdk/preview  判断是不是自有码 + 套餐（不调卡台）
//	/own-cdk/check    直充预检：账号信息、宽限期、到期时间、Plus 半价资格（不发码、不扣钱）
//	/own-cdk/recover  宽限期：先买好卡台码，再取消原订阅（卡台的 recover-subscription 需要卡台码的预检票）
//	/own-cdk/redeem   买卡台码（每张自有码每个档位只买一次）→ CDK preview → preflight → redeem
//
// ★钱相关的规则★
//   - 档位和地区在「第一次抢占」时就写进库里，之后所有重试都用库里的，不再重新判断，
//     否则半价资格一变就会换个幂等键再买一张。
//   - 只有卡台明确的业务拒绝（4xx，非 408/409/429）才算「没发出码」并放回 unused；
//     超时、5xx、代理错误页、409 处理中都算「不确定」，保持占用，过期后用同一幂等键重试拿回原码。
//   - 兑换请求号 client_request_id 和这次的令牌一起落库，重试原样重放（卡台按请求号返回原订单）；
//     只有确认卡台码回到 unused（上一单失败已释放）时才换新请求号重新兑换。
//   - 买码 / 兑换用脱离浏览器连接的 context：用户中途关页面，后端仍把这一步做完并记账。
//
// 半价对用户不可见：公开接口只回 plus，实际买的是 plus_half 还是 plus 只在后台可见。
// 新文件、只在路由里加几行，同步上游时不冲突。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tuzi/cdk-recharge-system/internal/cardplatform"
	"github.com/tuzi/cdk-recharge-system/internal/db"
)

const (
	ownPlanHalf      = "plus_half"
	ownClaimStale    = 5 * time.Minute // issuing 超过这个时间视为上次请求中断（卡台发码超时是 180 秒）
	ownDefaultRegion = "PH"
	ownUpstreamLimit = 6 * time.Minute // 一次买码 + 兑换的总时限
)

var ownRegionCurrency = map[string]string{"PH": "PHP", "US": "USD", "JP": "JPY", "CL": "CLP", "EG": "EGP"}

// ---- 每张自有码一把进程内锁：同一张码的 recover / redeem 串行。拿不到锁立刻返回「处理中」，不排队 ----

var ownLocks sync.Map

func tryLockOwn(code string) (unlock func(), ok bool) {
	m, _ := ownLocks.LoadOrStore(code, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	// 不删除：删了会出现「同一张码两把锁」。只有真实存在的自有码才会走到这里（先查库再加锁），map 大小以码的数量为上限
	return mu.Unlock, true
}

// ---- 直充预检结果（只在服务端用；半价资格不下发浏览器） ----

type ownCheckResult struct {
	Email            string
	CurrentPlan      string
	HasActive        *bool
	ActiveUntil      string
	CanPurchaseAt    string
	WillRenew        *bool
	RecoveryRequired bool
	Delinquent       bool
	HalfEligible     bool
	BlockingReason   string
}

func parseOwnCheck(data json.RawMessage) ownCheckResult {
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	r := ownCheckResult{
		Email:            firstStr(m["email"], m["account_email"]),
		CurrentPlan:      firstStr(m["currentPlan"], m["current_plan"]),
		ActiveUntil:      str(m["subscription_active_until"]),
		CanPurchaseAt:    str(m["can_purchase_at"]),
		RecoveryRequired: m["subscription_recovery_required"] == true,
		Delinquent:       m["subscription_is_delinquent"] == true,
		BlockingReason:   str(m["blocking_reason"]),
	}
	if b, ok := m["subscription_has_active"].(bool); ok {
		r.HasActive = &b
	}
	if b, ok := m["subscription_will_renew"].(bool); ok {
		r.WillRenew = &b
	}
	if promos, ok := m["promotions"].(map[string]any); ok {
		if half, ok := promos[ownPlanHalf].(map[string]any); ok {
			r.HalfEligible = half["eligible"] == true // null / false 一律按全价，宁可多花不能失败
		}
	}
	return r
}

func firstStr(vals ...any) string {
	for _, v := range vals {
		if s := str(v); s != "" {
			return s
		}
	}
	return ""
}

// ownRegion 指定地区；auto 由 resolveOwnRegion 按价格表决定。
func ownRegion(o *db.OwnCDK) string {
	r := strings.ToUpper(strings.TrimSpace(o.Region))
	if _, ok := ownRegionCurrency[r]; ok {
		return r
	}
	return ownDefaultRegion
}

// directPreflight 调卡台直充预检（OpenAPI，需 API Key）。只读：不建单、不扣款、不冻结服务费。
// Plus 码按 plan=plus 查：返回里仍带 promotions.plus_half.eligible，且不会因没有半价资格而拦截（2026-11-01 实测）。
func directPreflight(ctx context.Context, plan, region, session string) (ownCheckResult, error) {
	cli := cardplatform.NewFromSettings()
	body := map[string]any{
		"plan":             ownPricingPlan(plan),
		"credential":       map[string]any{"mode": "session", "session": session},
		"payment_country":  region,
		"payment_currency": ownRegionCurrency[region],
	}
	data, err := cli.DirectPreflight(ctx, body)
	if err != nil {
		return ownCheckResult{}, err
	}
	return parseOwnCheck(data), nil
}

// ownIdemKey 买卡台码的幂等键：同一张自有码 + 同一档位永远是同一个键，重试只会拿回原码。
func ownIdemKey(ownCode, plan string) string {
	sum := sha256.Sum256([]byte("own-cdk|" + strings.ToUpper(ownCode) + "|" + plan))
	return "own-cdk-" + hex.EncodeToString(sum[:12])
}

// issueDefinitelyRejected 卡台是否明确拒绝发码（这时肯定没扣钱，可以放回 unused）。
// 超时 / 网络错误 / 5xx / 代理返回的非 JSON 错误页 / 409 幂等键处理中 / 408 / 429 都可能已经发出，算不确定。
func issueDefinitelyRejected(err error) bool {
	var ae *cardplatform.APIError
	if !errors.As(err, &ae) {
		return false
	}
	if strings.HasPrefix(ae.Msg, "invalid json") {
		return false
	}
	switch {
	case ae.HTTPStatus >= 500, ae.HTTPStatus == 408, ae.HTTPStatus == 409, ae.HTTPStatus == 429:
		return false
	case ae.HTTPStatus >= 400:
		return true
	}
	return false
}

// buyPlatformCode 买一张卡台码并存进本站完整码缓存。档位、地区由调用方从库里取，保证重试时请求体不变。
func buyPlatformCode(ctx context.Context, ownCode, plan, region string) (*cardplatform.IssuedCDK, error) {
	cli := cardplatform.NewFromSettings()
	// 不带本站选卡偏好：偏好是可变配置，带上会让同一幂等键的重试请求体不一致；兑换时的选卡策略照常生效。
	res, err := cli.IssueCDKs(ctx, plan, 1, ownIdemKey(ownCode, plan), cardplatform.IssueCardPref{PaymentCountry: region})
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Issued) == 0 || strings.TrimSpace(res.Issued[0].Code) == "" {
		return nil, errors.New("卡台未返回完整码")
	}
	it := res.Issued[0]
	prefix := strings.TrimSpace(it.CodePrefix)
	if prefix == "" && len(it.Code) >= 14 {
		prefix = it.Code[:14]
	}
	// 存进本站完整码缓存，但状态记 own_reserved：它已绑定给这张自有码，不能出现在「未使用」库存导出里被再卖一次
	if err := db.SaveCardplatformCDKCodeWithStatus(it.ID, it.Code, prefix, it.Plan, it.FeeAmountMinor, "own_reserved", region); err != nil {
		log.Printf("[own-cdk] save platform code failed id=%d: %v", it.ID, err)
	}
	_ = db.SetCardplatformCDKNote(it.ID, "自有卡密 "+maskCode(ownCode)+" 专用")
	db.WriteAudit("own-cdk", "own_cdk_buy_platform", fmt.Sprintf("own=%s plan=%s region=%s platform_id=%d", maskCode(ownCode), plan, region, it.ID), "")
	return &it, nil
}

func maskCode(code string) string {
	if len(code) <= 12 {
		return code
	}
	return code[:8] + "…" + code[len(code)-4:]
}

// publicPlan 下发给浏览器的档位：plus_half 一律显示成 plus。
func publicPlan(plan string) string {
	if plan == ownPlanHalf {
		return "plus"
	}
	return plan
}

// ---- 公开接口 ----

// OwnCDKPreview POST /api/v1/public/own-cdk/preview  {code}
// 是自有码返回 {own:true, plan}；不是返回 {own:false}，前端改走卡台码流程。
func OwnCDKPreview(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入卡密"})
		return
	}
	o, err := db.GetOwnCDK(req.Code)
	if err != nil {
		log.Printf("[own-cdk] preview lookup: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败，请稍后重试"})
		return
	}
	if o == nil {
		c.JSON(http.StatusOK, gin.H{"own": false})
		return
	}
	if o.Status == db.OwnStatusDisabled {
		c.JSON(http.StatusBadRequest, gin.H{"own": true, "error": "卡密已停用"})
		return
	}
	out := gin.H{"own": true, "code": o.Code, "plan": publicPlan(o.Plan)}
	// 已提交过兑换（有兑换会话）才去进度页；只买了码、取消原订阅后还没兑换的，仍留在填写页
	if o.RedemptionToken != "" {
		out["started"] = true
	}
	c.JSON(http.StatusOK, out)
}

type ownCredReq struct {
	Code       string `json:"code"`
	Credential struct {
		Mode    string `json:"mode"`
		Session string `json:"session"`
	} `json:"credential"`
}

func (r *ownCredReq) session() string {
	s := strings.TrimSpace(r.Credential.Session)
	if s == "" || looksLikeBareAccessToken(s) {
		return ""
	}
	return s
}

// OwnCDKCheck POST /api/v1/public/own-cdk/check  {code, credential:{mode:"session", session}}
// 直充预检拿账号信息，供确认框显示。不发码、不扣钱。
func OwnCDKCheck(c *gin.Context) {
	var req ownCredReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	sess := req.session()
	if sess == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请粘贴完整 Session JSON（必须含 sessionToken）"})
		return
	}
	o, err := db.GetOwnCDK(req.Code)
	if err != nil || o == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "卡密无效"})
		return
	}
	if o.Status == db.OwnStatusDisabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "卡密已停用"})
		return
	}
	plan, region := o.Plan, o.PlatformRegion
	if o.PlatformPlan != "" {
		plan = o.PlatformPlan
	}
	if region == "" {
		region = resolveOwnRegion(c.Request.Context(), o.Region, plan)
	}
	r, err := directPreflight(c.Request.Context(), plan, region, sess)
	if err != nil {
		writeCardErr(c, err)
		return
	}
	out := gin.H{
		"email":                          r.Email,
		"currentPlan":                    r.CurrentPlan,
		"subscription_active_until":      r.ActiveUntil,
		"can_purchase_at":                r.CanPurchaseAt,
		"subscription_recovery_required": r.RecoveryRequired,
		"subscription_is_delinquent":     r.Delinquent,
		"blocking_reason":                r.BlockingReason,
		"plan":                           publicPlan(plan),
	}
	if r.HasActive != nil {
		out["subscription_has_active"] = *r.HasActive
	}
	if r.WillRenew != nil {
		out["subscription_will_renew"] = *r.WillRenew
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": out})
}

// ensurePlatformCode 保证自有码已绑定一张卡台码：没有就（按第一次抢占时定下的档位/地区）买一张。
// 返回 nil error 时 o 已是 issued 且 PlatformCode 非空。
func ensurePlatformCode(ctx context.Context, o *db.OwnCDK, sess string, forRecover ...bool) error {
	recovering := len(forRecover) > 0 && forRecover[0]
	if o.Status == db.OwnStatusIssued && o.PlatformCode != "" {
		return nil
	}
	plan, region := o.PlatformPlan, o.PlatformRegion
	if plan == "" || region == "" {
		// 第一次：定档位和地区（Plus 有半价资格买 plus_half）。之后的重试一律用库里的，不再重新判断。
		region = resolveOwnRegion(ctx, o.Region, o.Plan)
		plan = o.Plan
		if o.Plan == "plus" {
			r, err := directPreflight(ctx, "plus", region, sess)
			if err != nil {
				return err
			}
			// 宽限期账号的拦截原因就是「先处理原订阅」，走取消流程时不拦
			if r.BlockingReason != "" && !recovering {
				return &ownUserErr{http.StatusBadRequest, r.BlockingReason}
			}
			if r.HalfEligible {
				plan = ownPlanHalf
			}
		}
	}
	ok, err := db.ClaimOwnCDKForIssue(o.Code, plan, region, ownClaimStale)
	if err != nil {
		return err
	}
	if !ok {
		return &ownUserErr{http.StatusConflict, "这张卡密正在处理中，请稍后查看进度，不要重复提交"}
	}
	// 抢占成功后以库里的为准（过期重抢时用的是第一次定下的档位/地区）。读不到就不买，绝不用本地算的档位（幂等键会变）
	cur, err := db.GetOwnCDK(o.Code)
	if err != nil || cur == nil || cur.PlatformPlan == "" || cur.PlatformRegion == "" {
		return &ownUserErr{http.StatusInternalServerError, "处理失败，请几分钟后用同一张卡密重试"}
	}
	plan, region = cur.PlatformPlan, cur.PlatformRegion
	it, err := buyPlatformCode(ctx, o.Code, plan, region)
	if err != nil {
		if issueDefinitelyRejected(err) {
			_ = db.ReleaseOwnCDKClaim(o.Code)
		}
		log.Printf("[own-cdk] buy platform code failed own=%s plan=%s: %v", maskCode(o.Code), plan, err)
		return &ownUserErr{http.StatusBadGateway, "兑换服务暂时不可用，请稍后用同一张卡密重试"}
	}
	var markErr error
	for i := 0; i < 3; i++ {
		if markErr = db.MarkOwnCDKIssued(o.Code, it.Code, plan, region, it.ID); markErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if markErr != nil {
		// 记账失败：保持 issuing，过期后用同一幂等键重试会拿回同一张码，不会重复扣费
		log.Printf("[own-cdk] mark issued failed own=%s platform_id=%d: %v", maskCode(o.Code), it.ID, markErr)
		return &ownUserErr{http.StatusInternalServerError, "处理失败，请几分钟后用同一张卡密重试"}
	}
	o.Status, o.PlatformCode, o.PlatformPlan, o.PlatformRegion, o.PlatformID = db.OwnStatusIssued, it.Code, plan, region, it.ID
	o.RedemptionToken, o.PreflightToken, o.ClientRequestID = "", "", ""
	return nil
}

// ownUserErr 给用户看的错误（不含卡台地址）。
type ownUserErr struct {
	status int
	msg    string
}

func (e *ownUserErr) Error() string { return e.msg }

func writeOwnErr(c *gin.Context, scope string, err error) {
	var ue *ownUserErr
	if errors.As(err, &ue) {
		c.JSON(ue.status, gin.H{"error": ue.msg})
		return
	}
	var se *upstreamStepErr
	if errors.As(err, &se) {
		proxyPublicJSON(c, se.status, stripHalf(se.body))
		return
	}
	writeCardErr(c, err)
}

// OwnCDKRecover POST /api/v1/public/own-cdk/recover  {code, credential, confirmed:true}
// 宽限期账号：先买好卡台码并预检，再取消原订阅。取消后兑换继续用这张码，不会再买。
func OwnCDKRecover(c *gin.Context) {
	var req ownCredReq
	var extra struct {
		Confirmed bool `json:"confirmed"`
	}
	raw, _ := c.GetRawData()
	if json.Unmarshal(raw, &req) != nil || json.Unmarshal(raw, &extra) != nil || !extra.Confirmed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请确认后再操作"})
		return
	}
	sess := req.session()
	if sess == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请粘贴完整 Session JSON（必须含 sessionToken）"})
		return
	}
	o, err := db.GetOwnCDK(req.Code)
	if err != nil || o == nil || o.Status == db.OwnStatusDisabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "卡密无效"})
		return
	}
	unlock, ok := tryLockOwn(o.Code)
	if !ok {
		c.JSON(http.StatusConflict, gin.H{"error": "这张卡密正在处理中，请稍后再试"})
		return
	}
	defer unlock()
	if o, err = db.GetOwnCDK(o.Code); err != nil || o == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败，请稍后重试"})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), ownUpstreamLimit)
	defer cancel()
	device := deviceFrom(c)

	if err := ensurePlatformCode(ctx, o, sess, true); err != nil {
		writeOwnErr(c, "own-cdk-recover", err)
		return
	}
	tok, pfTok, err := previewAndPreflight(ctx, o, sess, device)
	if err != nil {
		writeOwnErr(c, "own-cdk-recover", err)
		return
	}
	cli := cardplatform.NewFromSettings()
	st, out, err := cli.RecoverSubscription(ctx, map[string]any{
		"redemption_token": tok, "preflight_token": pfTok, "confirmed": true,
	}, device)
	if err != nil {
		writeUpstreamTransportErr(c, "own-cdk-recover", err)
		return
	}
	proxyPublicJSON(c, st, stripHalf(out))
}

// OwnCDKRedeem POST /api/v1/public/own-cdk/redeem  {code, credential}
func OwnCDKRedeem(c *gin.Context) {
	var req ownCredReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	sess := req.session()
	if sess == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请粘贴完整 Session JSON（必须含 sessionToken）"})
		return
	}
	o, err := db.GetOwnCDK(req.Code)
	if err != nil || o == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "卡密无效"})
		return
	}
	if o.Status == db.OwnStatusDisabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "卡密已停用"})
		return
	}
	unlock, ok := tryLockOwn(o.Code)
	if !ok {
		c.JSON(http.StatusConflict, gin.H{"error": "这张卡密正在处理中，请稍后查看进度，不要重复提交"})
		return
	}
	defer unlock()
	if o, err = db.GetOwnCDK(o.Code); err != nil || o == nil { // 加锁后重读，拿到别的请求刚写入的状态
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败，请稍后重试"})
		return
	}
	// 脱离浏览器连接：用户关页面不会让买码 / 兑换停在一半
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), ownUpstreamLimit)
	defer cancel()
	device := deviceFrom(c)

	if err := ensurePlatformCode(ctx, o, sess); err != nil {
		writeOwnErr(c, "own-cdk-redeem", err)
		return
	}
	st, data, err := redeemPlatformCode(ctx, o, sess, device)
	if errors.Is(err, errHalfIneligible) {
		// 预检时有半价资格、兑换时活动没了（卡台在扣款前拒绝）：不换发，一张自有码只对应一张卡台码。
		// 这张 plus_half 卡台码仍是 unused，后台可见；需要时人工处理。
		log.Printf("[own-cdk] half price became ineligible at redeem own=%s platform_id=%d", maskCode(o.Code), o.PlatformID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "兑换未成功，未扣款。这张卡密暂时无法为该账号开通，请联系发码方处理。"})
		return
	}
	if err != nil {
		writeOwnErr(c, "own-cdk-redeem", err)
		return
	}
	proxyPublicJSON(c, st, stripHalf(data))
}

var errHalfIneligible = errors.New("half price ineligible")

// upstreamStepErr 卡台公开兑换某一步返回的业务错误，原样转给前端（文案里没有卡台地址）。
type upstreamStepErr struct {
	status int
	body   json.RawMessage
}

func (e *upstreamStepErr) Error() string { return "upstream step " + strconv.Itoa(e.status) }

// isHalfIneligible 卡台明确报「没有半价资格」：只看错误码字段，不在整段响应里搜（进行中订单的事件里也可能出现这个词）。
func isHalfIneligible(raw json.RawMessage) bool {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	for _, src := range []map[string]any{m, asMap(m["data"]), asMap(asMap(m["data"])["order"])} {
		for _, k := range []string{"error_code", "public_code", "code"} {
			if v, ok := src[k].(string); ok && strings.EqualFold(v, "half_price_ineligible") {
				return true
			}
		}
	}
	return false
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// stripHalf 把转给浏览器的卡台响应里的 plus_half 改成 plus（档位名不给用户看）。
func stripHalf(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	s := strings.ReplaceAll(string(raw), `"plus_half"`, `"plus"`)
	return json.RawMessage(s)
}

func publicOK(st int, raw json.RawMessage) bool {
	if st < 200 || st >= 300 {
		return false
	}
	var env struct {
		Code *int `json:"code"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Code != nil && *env.Code != 0 {
		return false
	}
	return true
}

// previewAndPreflight 卡台码的 preview + preflight，并把兑换会话 / Session 绑到自有码上（查进度、查邮箱用）。
func previewAndPreflight(ctx context.Context, o *db.OwnCDK, sess, device string) (tok, pfTok string, err error) {
	cli := cardplatform.NewFromSettings()
	st, raw, err := cli.Preview(ctx, o.PlatformCode, device)
	if err != nil {
		return "", "", err
	}
	if !publicOK(st, raw) {
		return "", "", &upstreamStepErr{st, raw}
	}
	tok = firstNonEmptyStr(extractJSONNestedString(raw, "data", "redemption_token"), extractJSONString(raw, "redemption_token", "token"))
	if tok == "" {
		return "", "", errors.New("preview 未返回 redemption_token")
	}
	_ = db.BindCDKRedemptionToken(o.PlatformCode, tok)

	st, raw, err = cli.Preflight(ctx, map[string]any{
		"redemption_token": tok,
		"credential":       map[string]any{"mode": "session", "session": sess},
	}, device)
	if err != nil {
		return "", "", err
	}
	if !publicOK(st, raw) {
		if isHalfIneligible(raw) {
			return "", "", errHalfIneligible
		}
		return "", "", &upstreamStepErr{st, raw}
	}
	pfTok = firstNonEmptyStr(extractJSONNestedString(raw, "data", "preflight_token"), extractJSONString(raw, "preflight_token"))
	if pfTok == "" {
		return "", "", &upstreamStepErr{http.StatusBadRequest, json.RawMessage(`{"error":"账号验证未完成，请稍后重试"}`)}
	}
	_ = db.BindCDKSession(o.PlatformCode, tok, sess)
	return tok, pfTok, nil
}

// ownFailedStatuses 订单失败终态（卡台文档：declined / failed_precharge / cancelled；failed 为兼容旧写法）。
var ownFailedStatuses = map[string]bool{"declined": true, "failed_precharge": true, "cancelled": true, "failed": true}

// orderFailed 兑换 / 结果响应里的订单是否已失败结束。
func orderFailed(raw json.RawMessage) bool {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	data := asMap(m["data"])
	for _, src := range []map[string]any{asMap(data["order"]), data, asMap(m["order"]), m} {
		if st := strings.ToLower(str(src["status"])); st != "" {
			return ownFailedStatuses[st]
		}
	}
	return false
}

// cdkStatusOf 结果响应里卡台码自身的状态（unused 表示上一单失败后已释放、可以再兑换）。
func cdkStatusOf(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	data := asMap(m["data"])
	for _, src := range []map[string]any{asMap(data["order"]), data, asMap(m["order"]), m} {
		if st := strings.ToLower(str(src["cdk_status"])); st != "" {
			return st
		}
	}
	return ""
}

// platformCodeReusable 卡台码现在能不能重新兑换：以卡台码自己的状态为准，只有 unused 才行。
// 先看这次兑换的结果（和进度页同一个来源），拿不到再查所有者码列表。
func platformCodeReusable(ctx context.Context, o *db.OwnCDK, device string) bool {
	cli := cardplatform.NewFromSettings()
	if o.RedemptionToken != "" {
		if st, raw, err := cli.Result(ctx, o.RedemptionToken, device); err == nil && publicOK(st, raw) {
			if cs := cdkStatusOf(raw); cs != "" {
				return cs == "unused"
			}
		}
	}
	if o.PlatformID <= 0 {
		return false
	}
	// q 只匹配 id / code_prefix，不能用完整码查（与 agent_exchange.upstreamCDKRegion 同一用法）
	res, err := cli.ListCDKsQuery(ctx, cardplatform.CDKListQuery{Page: 1, PageSize: 50, Query: strconv.FormatInt(o.PlatformID, 10)})
	if err != nil || res == nil {
		return false
	}
	for _, it := range res.List {
		if it.ID == o.PlatformID {
			return strings.EqualFold(it.Status, "unused")
		}
	}
	return false
}

// redeemPlatformCode 用自有码绑定的卡台码兑换。
//   - 已有一次兑换记录：原样重放（同请求号、同令牌），卡台返回原订单，不会建第二单。
//     只有确认卡台码已回到 unused（上一单失败释放）时，才换新请求号重新兑换。
//   - 首次：preview → preflight → 记下令牌和请求号 → redeem。
func redeemPlatformCode(ctx context.Context, o *db.OwnCDK, sess, device string) (int, json.RawMessage, error) {
	cli := cardplatform.NewFromSettings()

	if o.ClientRequestID != "" && o.RedemptionToken != "" && o.PreflightToken != "" {
		body := ownRedeemBody(o.RedemptionToken, o.PreflightToken, o.ClientRequestID)
		st, raw, err := cli.Redeem(ctx, body, device)
		if err != nil {
			return 0, nil, err
		}
		if isHalfIneligible(raw) {
			return 0, nil, errHalfIneligible
		}
		// 重放拿回的是原订单：成功或还在处理就原样返回；
		// 原订单已失败（或令牌过期等报错）时，看卡台码是否回到 unused，回到了才重新兑换。
		if publicOK(st, raw) && !orderFailed(raw) {
			return st, raw, nil
		}
		if !platformCodeReusable(ctx, o, device) {
			if publicOK(st, raw) {
				return st, raw, nil // 原订单失败、码没有释放（核对中等）：如实返回，不重新兑换
			}
			return 0, nil, &upstreamStepErr{st, raw}
		}
		// 上一单已结束且码回到 unused：下面按新的一次兑换走
		log.Printf("[own-cdk] platform code back to unused, new attempt own=%s", maskCode(o.Code))
	}

	tok, pfTok, err := previewAndPreflight(ctx, o, sess, device)
	if err != nil {
		return 0, nil, err
	}
	reqID := nextOwnRequestID(o.Code, o.PlatformCode, o.ClientRequestID)
	if err := db.SaveOwnRedeemAttempt(o.Code, o.ClientRequestID, tok, pfTok, reqID); err != nil {
		// 记不下来就不兑换：否则网络重试时无法重放，可能建两单
		log.Printf("[own-cdk] save redeem attempt failed own=%s: %v", maskCode(o.Code), err)
		return 0, nil, &ownUserErr{http.StatusInternalServerError, "处理失败，请稍后用同一张卡密重试"}
	}
	o.RedemptionToken, o.PreflightToken, o.ClientRequestID = tok, pfTok, reqID
	if err := db.BindOwnToRedemption(o.Code, tok, sess); err != nil {
		log.Printf("[own-cdk] bind own code failed own=%s: %v", maskCode(o.Code), err)
	}

	st, raw, err := cli.Redeem(ctx, ownRedeemBody(tok, pfTok, reqID), device)
	if err != nil {
		return 0, nil, err
	}
	if !publicOK(st, raw) {
		if isHalfIneligible(raw) {
			return 0, nil, errHalfIneligible
		}
		return 0, nil, &upstreamStepErr{st, raw}
	}
	return st, raw, nil
}

// nextOwnRequestID 兑换请求号：按「自有码 + 卡台码」固定前缀，末尾是第几次兑换。
// 同一次兑换的重试沿用旧号（不走这里）；上一单失败、码回到 unused 后才 +1。
func nextOwnRequestID(ownCode, platformCode, prev string) string {
	base := "own-" + ownIdemKey(ownCode, platformCode)[8:]
	attempt := 1
	if strings.HasPrefix(prev, base+"-") {
		if n, err := strconv.Atoi(strings.TrimPrefix(prev, base+"-")); err == nil {
			attempt = n + 1
		}
	}
	return fmt.Sprintf("%s-%d", base, attempt)
}

// ownRedeemBody 兑换请求体。选卡策略随请求号一起固定：重放时必须和第一次完全一样，否则卡台返回 409。
func ownRedeemBody(tok, pfTok, reqID string) map[string]any {
	return map[string]any{
		"redemption_token":  tok,
		"preflight_token":   pfTok,
		"client_request_id": reqID,
	}
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// ---- 后台接口 ----

// AdminOwnCDKCreate POST /api/v1/admin/own-cdks  {plan, region, count, note}
func AdminOwnCDKCreate(c *gin.Context) {
	var req struct {
		Plan   string `json:"plan"`
		Region string `json:"region"`
		Count  int    `json:"count"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	codes, err := db.CreateOwnCDKs(req.Plan, req.Region, req.Note, req.Count)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, _ := c.Get("username")
	username, _ := u.(string)
	db.WriteAudit(username, "own_cdk_create", fmt.Sprintf("plan=%s region=%s count=%d", req.Plan, req.Region, len(codes)), c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"codes": codes, "count": len(codes)})
}

// AdminOwnCDKList GET /api/v1/admin/own-cdks?status=&q=&page=&page_size=
func AdminOwnCDKList(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	ps, _ := strconv.Atoi(c.Query("page_size"))
	list, total, err := db.ListOwnCDKs(c.Query("status"), c.Query("q"), page, ps)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"list": list, "total": total})
}

// AdminOwnCDKSetStatus POST /api/v1/admin/own-cdks/status  {ids, disabled}
func AdminOwnCDKSetStatus(c *gin.Context) {
	var req struct {
		IDs      []int64 `json:"ids"`
		Disabled bool    `json:"disabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	n, err := db.SetOwnCDKDisabled(req.IDs, req.Disabled)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	u, _ := c.Get("username")
	username, _ := u.(string)
	db.WriteAudit(username, "own_cdk_status", fmt.Sprintf("disabled=%v n=%d", req.Disabled, n), c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"updated": n})
}

// AdminOwnCDKSetNote POST /api/v1/admin/own-cdks/note  {ids, note}
func AdminOwnCDKSetNote(c *gin.Context) {
	var req struct {
		IDs  []int64 `json:"ids"`
		Note string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	n, err := db.SetOwnCDKNote(req.IDs, req.Note)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": n})
}
