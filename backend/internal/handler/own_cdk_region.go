package handler

// 自有卡密「自动」地区（fork）：按卡台价格表选当前最便宜的付款地区。
// 价格表每天更新，这里缓存 30 分钟；取不到或数据不全时回退菲律宾，保证兑换不因比价失败而卡住。

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tuzi/cdk-recharge-system/internal/cardplatform"
)

const ownPricingTTL = 30 * time.Minute

var (
	ownPricingMu    sync.Mutex
	ownPricingCache []cardplatform.GPTPrice
	ownPricingAt    time.Time
	ownPricingSnap  string // 价格表快照日期（后台展示用）
	ownPricingStale bool
)

// ownPricingPlan 价格表的档位键：plus_half 没有单独标价，按 plus 比较（各地区半价都是同一折扣）。
func ownPricingPlan(plan string) string {
	if plan == ownPlanHalf {
		return "plus"
	}
	return plan
}

// cheapestRegion 纯函数：在支持的地区里挑这个档位折合美元最便宜的。
// 金额或美元价为 0 的（价格表采集失败 / 税务数据不全）跳过。全部不可用返回 ""。
func cheapestRegion(prices []cardplatform.GPTPrice, plan string, supported map[string]string) string {
	plan = ownPricingPlan(plan)
	type cand struct {
		region string
		usd    float64
	}
	var cs []cand
	for _, p := range prices {
		region := strings.ToUpper(strings.TrimSpace(p.CountryCode))
		if p.Plan != plan || p.Amount <= 0 || p.USDAmount <= 0 {
			continue
		}
		if _, ok := supported[region]; !ok {
			continue
		}
		cs = append(cs, cand{region, p.USDAmount})
	}
	if len(cs) == 0 {
		return ""
	}
	// 价格相同时按固定顺序，保证同一份价格表每次选出同一个地区
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].usd != cs[j].usd {
			return cs[i].usd < cs[j].usd
		}
		return cs[i].region < cs[j].region
	})
	return cs[0].region
}

func ownPricing(ctx context.Context) []cardplatform.GPTPrice {
	ownPricingMu.Lock()
	defer ownPricingMu.Unlock()
	if ownPricingCache != nil && time.Since(ownPricingAt) < ownPricingTTL {
		return ownPricingCache
	}
	regions := make([]string, 0, len(ownRegionCurrency))
	for r := range ownRegionCurrency {
		regions = append(regions, r)
	}
	sort.Strings(regions)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := cardplatform.NewFromSettings().GetGPTPricing(cctx, []string{"go", "plus", "pro_5x", "pro_20x", "pro_50x"}, regions)
	if err != nil || res == nil {
		log.Printf("[own-cdk] pricing fetch failed, keep last snapshot: %v", err)
		return ownPricingCache // 取不到就用上一份（可能为空）
	}
	if res.Stale {
		log.Printf("[own-cdk] pricing snapshot stale (snapshot_date=%s)", res.SnapshotDate)
	}
	ownPricingCache, ownPricingAt = res.Prices, time.Now()
	ownPricingSnap, ownPricingStale = res.SnapshotDate, res.Stale
	return ownPricingCache
}

// ownPricingPlans 后台价格表展示的档位顺序。
var ownPricingPlans = []string{"plus", "pro_5x", "pro_20x", "pro_50x", "go"}

// AdminOwnCDKPricing GET /api/v1/admin/own-cdks/pricing
// 各套餐在支持地区的参考价，以及「自动」会选哪个地区（与兑换时用的是同一份缓存、同一个选择函数）。
func AdminOwnCDKPricing(c *gin.Context) {
	prices := ownPricing(c.Request.Context())
	ownPricingMu.Lock()
	snap, stale, fetched := ownPricingSnap, ownPricingStale, ownPricingAt
	ownPricingMu.Unlock()

	type row struct {
		Region   string  `json:"region"`
		Currency string  `json:"currency"`
		Amount   float64 `json:"amount"`
		USD      float64 `json:"usd"`
		Usable   bool    `json:"usable"` // 金额或美元价为 0（采集失败 / 税务数据不全）时不参与比较
	}
	type planOut struct {
		Plan     string `json:"plan"`
		Cheapest string `json:"cheapest"`
		Rows     []row  `json:"rows"`
	}
	out := make([]planOut, 0, len(ownPricingPlans))
	for _, plan := range ownPricingPlans {
		po := planOut{Plan: plan, Cheapest: cheapestRegion(prices, plan, ownRegionCurrency), Rows: []row{}}
		for _, p := range prices {
			region := strings.ToUpper(strings.TrimSpace(p.CountryCode))
			if p.Plan != plan {
				continue
			}
			if _, ok := ownRegionCurrency[region]; !ok {
				continue
			}
			po.Rows = append(po.Rows, row{Region: region, Currency: p.Currency, Amount: p.Amount, USD: p.USDAmount,
				Usable: p.Amount > 0 && p.USDAmount > 0})
		}
		sort.Slice(po.Rows, func(i, j int) bool {
			a, b := po.Rows[i], po.Rows[j]
			if a.Usable != b.Usable {
				return a.Usable
			}
			if a.USD != b.USD {
				return a.USD < b.USD
			}
			return a.Region < b.Region
		})
		out = append(out, po)
	}
	resp := gin.H{"snapshot_date": snap, "stale": stale, "plans": out, "fallback": ownDefaultRegion}
	if !fetched.IsZero() {
		resp["cached_at"] = fetched.UTC().Format(time.RFC3339)
	}
	c.JSON(http.StatusOK, resp)
}

// resolveOwnRegion 自有码本次用的付款地区：指定地区直接用；auto 按价格表选最便宜的，选不出来用菲律宾。
func resolveOwnRegion(ctx context.Context, region, plan string) string {
	r := strings.ToUpper(strings.TrimSpace(region))
	if _, ok := ownRegionCurrency[r]; ok {
		return r
	}
	if r == "AUTO" {
		if best := cheapestRegion(ownPricing(ctx), plan, ownRegionCurrency); best != "" {
			return best
		}
	}
	return ownDefaultRegion
}
