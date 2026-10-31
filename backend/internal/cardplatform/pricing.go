package cardplatform

// GPT 套餐参考价（fork，自有卡密「自动」地区用）：GET /openapi/v1/pricing/gpt
// 平台定时采集的 OpenAI 免税标价快照，约每小时检查、按日更新；只读、不收费。
// 不是直充实际报价：实际扣款以下单时的实时报价为准。单独成文件，不改 client.go。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type GPTPrice struct {
	CountryCode     string  `json:"country_code"`
	Currency        string  `json:"currency"`
	Plan            string  `json:"plan"`
	Amount          float64 `json:"amount"`
	USDAmount       float64 `json:"usd_amount"`
	TaxDataComplete bool    `json:"tax_data_complete"`
}

type GPTPricing struct {
	SnapshotDate string     `json:"snapshot_date"`
	FetchedAt    string     `json:"fetched_at"`
	Stale        bool       `json:"stale"`
	Prices       []GPTPrice `json:"prices"`
}

func (c *Client) GetGPTPricing(ctx context.Context, plans, countries []string) (*GPTPricing, error) {
	q := url.Values{}
	if len(plans) > 0 {
		q.Set("plan", strings.Join(plans, ","))
	}
	if len(countries) > 0 {
		q.Set("country", strings.Join(countries, ","))
	}
	data, err := c.doOpenAPI(ctx, http.MethodGet, "/pricing/gpt?"+q.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	var out GPTPricing
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
