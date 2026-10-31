package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tuzi/cdk-recharge-system/internal/cardplatform"
)

// 2026-10-31 线上价格表的真实快照（五个支持地区）。
var pricingSnapshot = []cardplatform.GPTPrice{
	{CountryCode: "EG", Plan: "go", Amount: 192.98, USDAmount: 3.45},
	{CountryCode: "CL", Plan: "go", Amount: 6714, USDAmount: 6.78},
	{CountryCode: "PH", Plan: "go", Amount: 0, USDAmount: 0}, // 采集失败
	{CountryCode: "PH", Plan: "plus", Amount: 982.14, USDAmount: 15.40},
	{CountryCode: "EG", Plan: "plus", Amount: 877.18, USDAmount: 15.66},
	{CountryCode: "US", Plan: "plus", Amount: 20, USDAmount: 20},
	{CountryCode: "EG", Plan: "pro_5x", Amount: 4736.84, USDAmount: 84.58},
	{CountryCode: "PH", Plan: "pro_5x", Amount: 5794.64, USDAmount: 90.88},
	{CountryCode: "PH", Plan: "pro_20x", Amount: 8919.64, USDAmount: 139.89},
	{CountryCode: "EG", Plan: "pro_20x", Amount: 8771.92, USDAmount: 156.64},
	{CountryCode: "EG", Plan: "pro_50x", Amount: 23684.21, USDAmount: 422.92},
	{CountryCode: "TR", Plan: "plus", Amount: 100, USDAmount: 3}, // 卡台不支持的地区，再便宜也不能选
}

func TestCheapestRegion(t *testing.T) {
	cases := map[string]string{
		"plus":      "PH",
		"plus_half": "PH", // 半价按 plus 比价
		"pro_5x":    "EG",
		"pro_20x":   "PH",
		"pro_50x":   "EG",
		"go":        "EG", // PH 金额为 0 被跳过
	}
	for plan, want := range cases {
		if got := cheapestRegion(pricingSnapshot, plan, ownRegionCurrency); got != want {
			t.Errorf("%s: got %q want %q", plan, got, want)
		}
	}
	if got := cheapestRegion(nil, "plus", ownRegionCurrency); got != "" {
		t.Fatalf("empty table should give empty, got %q", got)
	}
}

func TestResolveOwnRegionFixed(t *testing.T) {
	// 指定地区不查价格表
	for in, want := range map[string]string{"jp": "JP", "EG": "EG", "": "PH", "TR": "PH"} {
		if got := resolveOwnRegion(nil, in, "plus"); got != want {
			t.Errorf("region %q → %q want %q", in, got, want)
		}
	}
}

// 后台价格表接口：用缓存里的价格，输出每档「自动」会选的地区，与 cheapestRegion 一致；无标价的排最后。
func TestAdminOwnCDKPricing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ownPricingMu.Lock()
	ownPricingCache, ownPricingAt, ownPricingSnap = pricingSnapshot, time.Now(), "2026-10-31"
	ownPricingMu.Unlock()
	t.Cleanup(func() {
		ownPricingMu.Lock()
		ownPricingCache, ownPricingAt, ownPricingSnap = nil, time.Time{}, ""
		ownPricingMu.Unlock()
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/own-cdks/pricing", nil)
	AdminOwnCDKPricing(c)

	var out struct {
		SnapshotDate string `json:"snapshot_date"`
		Plans        []struct {
			Plan     string `json:"plan"`
			Cheapest string `json:"cheapest"`
			Rows     []struct {
				Region string `json:"region"`
				Usable bool   `json:"usable"`
			} `json:"rows"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.SnapshotDate != "2026-10-31" || len(out.Plans) != 5 {
		t.Fatalf("got %+v", out)
	}
	want := map[string]string{"plus": "PH", "pro_5x": "EG", "pro_20x": "PH", "pro_50x": "EG", "go": "EG"}
	for _, p := range out.Plans {
		if p.Cheapest != want[p.Plan] {
			t.Errorf("%s cheapest=%q want %q", p.Plan, p.Cheapest, want[p.Plan])
		}
		for _, r := range p.Rows {
			if r.Region == "TR" {
				t.Errorf("%s: unsupported region must not be listed", p.Plan)
			}
		}
		if p.Plan == "go" {
			last := p.Rows[len(p.Rows)-1]
			if last.Region != "PH" || last.Usable {
				t.Errorf("go: PH has no price and must be listed last as unusable, got %+v", p.Rows)
			}
		}
	}
}
