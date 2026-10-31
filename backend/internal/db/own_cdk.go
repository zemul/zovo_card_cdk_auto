package db

// 自有卡密（fork）：本站自己发的码，兑换时现场向卡台购买一张对应档位/地区的卡台码再兑换。
// 表在首次使用时创建，不改上游的 createTables，减少同步上游时的冲突。

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// 自有码状态
const (
	OwnStatusUnused   = "unused"   // 未使用，尚未购买卡台码
	OwnStatusIssuing  = "issuing"  // 正在向卡台购买（抢占锁）
	OwnStatusIssued   = "issued"   // 已绑定卡台码；兑换结果看卡台码本身
	OwnStatusDisabled = "disabled" // 管理员停用
)

// OwnPlans 第一版支持的档位 → 码里的档位标识。
var OwnPlans = map[string]string{
	"go":      "GO",
	"plus":    "PLUS",
	"pro_5x":  "5X",
	"pro_20x": "20X",
	"pro_50x": "50X",
}

// OwnRegions 卡台当前支持的付款地区，加 auto（按价格表自动选，阶段 2 生效前按 PH）。
var OwnRegions = map[string]bool{"PH": true, "US": true, "JP": true, "CL": true, "EG": true, "AUTO": true}

const ownCodePrefix = "GPT"

type OwnCDK struct {
	ID             int64  `json:"id"`
	Code           string `json:"code"`
	Plan           string `json:"plan"`
	Region         string `json:"region"`
	Status         string `json:"status"`
	PlatformCode   string `json:"platform_code,omitempty"`
	PlatformPlan   string `json:"platform_plan,omitempty"`
	PlatformRegion string `json:"platform_region,omitempty"`
	PlatformID     int64  `json:"platform_id,omitempty"`
	SpareCodes     string `json:"spare_codes,omitempty"` // 换发后闲置的卡台码（逗号分隔），仍可兑换给别的账号
	// 当前这次兑换的令牌和请求号：网络重试时原样重放，卡台返回原订单而不是再建一单
	RedemptionToken string `json:"-"`
	PreflightToken  string `json:"-"`
	ClientRequestID string `json:"-"`
	Note            string `json:"note,omitempty"`
	CreatedAt       string `json:"created_at"`
	IssuedAt        string `json:"issued_at,omitempty"`
}

var ownTableOnce sync.Once
var ownTableErr error

func ensureOwnCDKTable() error {
	if DB == nil {
		return errors.New("db not ready")
	}
	ownTableOnce.Do(func() {
		_, ownTableErr = DB.Exec(`CREATE TABLE IF NOT EXISTS own_cdk_codes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT NOT NULL UNIQUE,
			plan TEXT NOT NULL,
			region TEXT NOT NULL DEFAULT 'PH',
			status TEXT NOT NULL DEFAULT 'unused',
			platform_code TEXT NOT NULL DEFAULT '',
			platform_plan TEXT NOT NULL DEFAULT '',
			platform_region TEXT NOT NULL DEFAULT '',
			platform_id INTEGER NOT NULL DEFAULT 0,
			spare_codes TEXT NOT NULL DEFAULT '',
			redemption_token TEXT NOT NULL DEFAULT '',
			preflight_token TEXT NOT NULL DEFAULT '',
			client_request_id TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			issued_at DATETIME,
			claimed_at DATETIME
		)`)
		if ownTableErr == nil {
			_, ownTableErr = DB.Exec(`CREATE INDEX IF NOT EXISTS idx_own_cdk_status ON own_cdk_codes(status)`)
		}
		for _, col := range []string{"redemption_token", "preflight_token", "client_request_id"} {
			if ownTableErr != nil {
				break
			}
			var n int
			if ownTableErr = DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('own_cdk_codes') WHERE name = ?`, col).Scan(&n); ownTableErr == nil && n == 0 {
				_, ownTableErr = DB.Exec(`ALTER TABLE own_cdk_codes ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`)
			}
		}
	})
	return ownTableErr
}

// 去掉 0/O/1/I/L，避免用户抄错。
const ownAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

func randomGroup(n int) (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(ownAlphabet)))
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(ownAlphabet[v.Int64()])
	}
	return b.String(), nil
}

// NewOwnCode 生成 GPTPLUS-XXXXXXXXXX-XXXXXXXXXX 形式的码。
func NewOwnCode(plan string) (string, error) {
	tag, ok := OwnPlans[plan]
	if !ok {
		return "", fmt.Errorf("unsupported plan %q", plan)
	}
	a, err := randomGroup(10)
	if err != nil {
		return "", err
	}
	b, err := randomGroup(10)
	if err != nil {
		return "", err
	}
	return ownCodePrefix + tag + "-" + a + "-" + b, nil
}

// CreateOwnCDKs 批量生成并入库，返回生成的码。
func CreateOwnCDKs(plan, region, note string, count int) ([]string, error) {
	if err := ensureOwnCDKTable(); err != nil {
		return nil, err
	}
	plan = strings.ToLower(strings.TrimSpace(plan))
	region = strings.ToUpper(strings.TrimSpace(region))
	if region == "" {
		region = "PH"
	}
	if _, ok := OwnPlans[plan]; !ok {
		return nil, fmt.Errorf("不支持的套餐: %s", plan)
	}
	if !OwnRegions[region] {
		return nil, fmt.Errorf("不支持的地区: %s", region)
	}
	if count < 1 || count > 200 {
		return nil, errors.New("数量须在 1–200 之间")
	}
	tx, err := DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]string, 0, count)
	for len(out) < count {
		code, err := NewOwnCode(plan)
		if err != nil {
			return nil, err
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO own_cdk_codes (code, plan, region, note) VALUES (?, ?, ?, ?)`,
			code, plan, strings.ToLower(region), strings.TrimSpace(note))
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			out = append(out, code)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

const ownCols = `id, code, plan, region, status, platform_code, platform_plan, platform_region, platform_id,
	spare_codes, redemption_token, preflight_token, client_request_id, note, COALESCE(created_at,''), COALESCE(issued_at,'')`

func scanOwn(row interface{ Scan(...any) error }) (*OwnCDK, error) {
	var o OwnCDK
	if err := row.Scan(&o.ID, &o.Code, &o.Plan, &o.Region, &o.Status, &o.PlatformCode, &o.PlatformPlan,
		&o.PlatformRegion, &o.PlatformID, &o.SpareCodes, &o.RedemptionToken, &o.PreflightToken, &o.ClientRequestID,
		&o.Note, &o.CreatedAt, &o.IssuedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

// GetOwnCDK 按码查（大小写不敏感）。不存在返回 nil, nil。
func GetOwnCDK(code string) (*OwnCDK, error) {
	if err := ensureOwnCDKTable(); err != nil {
		return nil, err
	}
	code = normalizeCDKCode(code)
	if code == "" {
		return nil, nil
	}
	o, err := scanOwn(DB.QueryRow(`SELECT `+ownCols+` FROM own_cdk_codes WHERE code = ?`, code))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return o, err
}

// ListOwnCDKs 后台列表，按创建时间倒序。
func ListOwnCDKs(status, q string, page, pageSize int) ([]OwnCDK, int, error) {
	if err := ensureOwnCDKTable(); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	where, args := []string{"1=1"}, []any{}
	if s := strings.TrimSpace(status); s != "" {
		where = append(where, "status = ?")
		args = append(args, s)
	}
	if s := strings.ToUpper(strings.TrimSpace(q)); s != "" {
		where = append(where, "(code LIKE ? OR platform_code LIKE ? OR note LIKE ?)")
		args = append(args, "%"+s+"%", "%"+s+"%", "%"+strings.TrimSpace(q)+"%")
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM own_cdk_codes WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := DB.Query(`SELECT `+ownCols+` FROM own_cdk_codes WHERE `+cond+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []OwnCDK{}
	for rows.Next() {
		o, err := scanOwn(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, *o)
	}
	return list, total, rows.Err()
}

// ClaimOwnCDKForIssue 抢占一张码去购买卡台码（unused → issuing），同时把这次要买的档位、地区写进库。
// 只有一个请求能抢到。卡在 issuing 超过 staleAfter 的视为上次请求中断，允许重新抢占，
// 但★档位、地区保留第一次写下的★：重试用同一个幂等键，卡台会返回原来那张码，不会重复扣费。
func ClaimOwnCDKForIssue(code, plan, region string, staleAfter time.Duration) (bool, error) {
	if err := ensureOwnCDKTable(); err != nil {
		return false, err
	}
	cutoff := time.Now().UTC().Add(-staleAfter).Format("2006-01-02 15:04:05")
	res, err := DB.Exec(`UPDATE own_cdk_codes SET status = ?, claimed_at = CURRENT_TIMESTAMP,
			platform_plan = CASE WHEN platform_plan = '' THEN ? ELSE platform_plan END,
			platform_region = CASE WHEN platform_region = '' THEN ? ELSE platform_region END
		WHERE code = ? AND platform_code = '' AND (status = ? OR (status = ? AND (claimed_at IS NULL OR claimed_at < ?)))`,
		OwnStatusIssuing, plan, strings.ToUpper(region), normalizeCDKCode(code), OwnStatusUnused, OwnStatusIssuing, cutoff)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ReleaseOwnCDKClaim 购买失败（确定没发出码）时退回 unused。
func ReleaseOwnCDKClaim(code string) error {
	if err := ensureOwnCDKTable(); err != nil {
		return err
	}
	_, err := DB.Exec(`UPDATE own_cdk_codes SET status = ?, claimed_at = NULL, platform_plan = '', platform_region = ''
		WHERE code = ? AND status = ? AND platform_code = ''`,
		OwnStatusUnused, normalizeCDKCode(code), OwnStatusIssuing)
	return err
}

// MarkOwnCDKIssued 记下买到的卡台码（issuing → issued）。
func MarkOwnCDKIssued(code, platformCode, platformPlan, platformRegion string, platformID int64) error {
	if err := ensureOwnCDKTable(); err != nil {
		return err
	}
	_, err := DB.Exec(`UPDATE own_cdk_codes SET status = ?, platform_code = ?, platform_plan = ?, platform_region = ?,
		platform_id = ?, issued_at = CURRENT_TIMESTAMP, claimed_at = NULL,
		redemption_token = '', preflight_token = '', client_request_id = '' WHERE code = ?`,
		OwnStatusIssued, strings.TrimSpace(platformCode), platformPlan, strings.ToUpper(platformRegion), platformID, normalizeCDKCode(code))
	return err
}

// SaveOwnRedeemAttempt 记下这次兑换的令牌和请求号（发兑换请求之前写入，网络重试时原样重放）。
// 比较并交换：只有库里的请求号还是 prevRequestID 时才写，防止并发的另一个请求覆盖掉已发出那次的令牌。
func SaveOwnRedeemAttempt(code, prevRequestID, redemptionToken, preflightToken, clientRequestID string) error {
	if err := ensureOwnCDKTable(); err != nil {
		return err
	}
	res, err := DB.Exec(`UPDATE own_cdk_codes SET redemption_token = ?, preflight_token = ?, client_request_id = ?
		WHERE code = ? AND status = ? AND client_request_id = ?`,
		redemptionToken, preflightToken, clientRequestID, normalizeCDKCode(code), OwnStatusIssued, prevRequestID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("own code not issued, or another redeem attempt was saved first")
	}
	return nil
}

// SetOwnCDKDisabled 停用 / 启用。已绑定卡台码的不允许改（兑换结果由卡台码决定）。
func SetOwnCDKDisabled(ids []int64, disabled bool) (int64, error) {
	if err := ensureOwnCDKTable(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	from, to := OwnStatusUnused, OwnStatusDisabled
	if !disabled {
		from, to = OwnStatusDisabled, OwnStatusUnused
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{to, from}
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := DB.Exec(`UPDATE own_cdk_codes SET status = ? WHERE status = ? AND id IN (`+ph+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetOwnCDKNote 改备注。
func SetOwnCDKNote(ids []int64, note string) (int64, error) {
	if err := ensureOwnCDKTable(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{strings.TrimSpace(note)}
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := DB.Exec(`UPDATE own_cdk_codes SET note = ? WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// BindOwnToRedemption 让「自有码」能直接查进度 / 查邮箱：
// 把卡台码的 redemption_token 和 Session 也绑到自有码上（cdk_session_bindings 以码为主键）。
func BindOwnToRedemption(ownCode, redemptionToken, sessionPayload string) error {
	if err := BindCDKRedemptionToken(ownCode, redemptionToken); err != nil {
		return err
	}
	if strings.TrimSpace(sessionPayload) == "" {
		return nil
	}
	return BindCDKSession(ownCode, redemptionToken, sessionPayload)
}
