package cardplatform

// 直充预检（fork，自有卡密用）：POST /openapi/v1/gpt-direct/preflight
// 只读：验证凭据、查当前套餐 / 宽限期 / 促销资格（promotions.plus_half）、取实时报价；
// 不建单、不扣款、不冻结服务费。返回的 preflight_token 只能用于直充下单，不能用于 CDK 兑换。
// 单独成文件，不改 client.go。

import (
	"context"
	"encoding/json"
	"net/http"
)

func (c *Client) DirectPreflight(ctx context.Context, body map[string]any) (json.RawMessage, error) {
	return c.doOpenAPI(ctx, http.MethodPost, "/gpt-direct/preflight", body, "")
}
