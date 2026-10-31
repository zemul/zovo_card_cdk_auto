package handler

import (
	"testing"

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
