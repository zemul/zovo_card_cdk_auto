# ZovoCard 卡片操作 API 文档

通过开放 API 程序化完成开卡、查卡、卡充值、退款、冻结、消费查询等操作。所有调用按你的账户余额与专属费率计费（与网页端一致）。

- **生产 Base URL**：`https://zovocard.com/openapi/v1`
- **沙盒 Base URL**：`https://sandbox.zovocard.com/openapi/v1`
- **数据格式**：请求与响应均为 `application/json`，UTF-8
- **凭证获取**：登录后在「开发者」页生成 `app_id` / `app_secret`

### 代理接入前测试

新代理应先登录 `https://sandbox.zovocard.com`，在「开发者」页创建一套仅用于沙盒的 API 密钥，再用沙盒 Base URL 完成联调。沙盒账户、密钥和数据与生产完全隔离；开卡、充值、退款及交易均为模拟结果，不会请求真实发卡渠道或产生真实资金变动。

```bash
export ZOVOCARD_API_BASE='https://sandbox.zovocard.com/openapi/v1'
export ZOVOCARD_API_KEY='sk_沙盒密钥'

curl "$ZOVOCARD_API_BASE/products" \
  -H "X-API-Key: $ZOVOCARD_API_KEY"
curl "$ZOVOCARD_API_BASE/balance" \
  -H "X-API-Key: $ZOVOCARD_API_KEY"
```

沙盒验证通过后，只需改用生产 Base URL 和单独创建的生产密钥。不要在沙盒中使用生产密钥，也不要把沙盒返回的卡片或交易当作真实资产。

## 0. 当前账户与卡片约束

- 链上充值单笔最低 `50U`（USDT/USDC）；共享 TRC20 意向低于 `50U` 无法创建，专属地址收到低于 `50U` 的转账不会自动入账，会进入人工处理。
- 普通账户全账户共同保留一笔 `20U` 风险保证金，不按卡片或交易重复扣取。该金额仍包含在 `balance` 中，不产生单独扣款流水；`GET /balance` 同时返回 `balance`、`spendable_balance` 和 `account_reserve_amount`，开卡、卡充值、批量开卡、转账、商城和 GPT 直充等用户主动消费只能使用 `spendable_balance`。
- 子账户不单独保留 `20U`；主账户及其全部子账户共享主账户的保证金、拒付率、退款率、单日拒付计数和白名单资格。子账户风险手续费优先扣子账户余额，不足部分由主账户代扣，并分别记录资金流水；主账户风险保证金可用于核销该类手续费。子账户的 `account_reserve_amount` 和 `spendable_balance` 按其自身余额返回，不代表另行占用一笔保证金。
- 拒付费、授权撤销费、消费退款费、小额消费费和受限商户处理费属于平台风险费用，可以核销保证金；调用方不要用总余额自行判断用户操作是否可执行。
- 拒付规则：非白名单真实拒付前 3 笔免费，第 4 笔起基础 `$0.30`，比例档位附加 `$0.30/$0.50/$0.80`；所有账户授权撤销固定按金额 `1%` 收取，且不参与当前拒付/退款风控；消费退款金额低于 `1U` 时免收退款手续费，达到 `1U` 后继续执行比例与最低 `$0.50` 规则；Google 商户低于 `1U` 的消费验证退款不计入退款笔数、退款率分母或退款率，但保留完整流水；小额真实消费（`0 < amount < 0.50U`）每笔 `$0.30`。
- 产品和卡详情返回 `restricted_merchants` 有效禁用商户数组，以及兼容字段 `google_chatgpt_blocked`。未配置卡头时，非香港发行卡默认禁用 `GOOGLE CHATGPT`，香港发行卡默认无禁用商户；产品管理可按前 6 位卡头配置多个商户，显式空数组表示全部放行。命中任一规则的真实授权/清算时，第一次冻结卡，第二次删卡、退回卡内余额并收取 `$0.50`，按 `auth_id + rule` 幂等。
- 提现若触及最后 `20U`，属于保证金退出，必须删除全部卡片并等待 `30` 天观察期；30 天前管理端打款接口也会拒绝，不能只依赖前端按钮状态。

---

## 1. 接入流程

1. 在「开发者」页生成密钥，得到 `app_id`（`ak_` 开头，公开标识）与 `app_secret`（`sk_` 开头，请求鉴权用，务必保密）。
2. （可选）为密钥设置 IP 白名单、配置回调地址。
3. 调 `GET /products` 获取可开卡产品 → `POST /cards/open` 开卡（建议带幂等键）。
4. 用 `GET /cards/{id}` 取卡号/有效期/CVV，`GET /cards/{id}/transactions` 查消费，或配置 Webhook 实时接收卡事件。
5. `GET /balance` 查余额，`GET /balance-logs` 对账。

> 余额不足会导致开卡/充值失败。可在网页端用 USDT 充值，或用开放 API 充值（见 6.16b）：转账后贴交易哈希即可**提速到账**。API 充值支持 EVM 专属地址 `BSC` / `ERC20` / `X Layer`；TRON 请走网页端共享地址意向。

---

## 2. 鉴权

每个请求在 Header 携带 `app_secret`（`sk_` 开头），二选一：

```
X-API-Key: sk_xxxxxxxxxxxxxxxx
```
或
```
Authorization: Bearer sk_xxxxxxxxxxxxxxxx
```

可选：再带 `X-App-Id: ak_xxxx` 做双重校验。

- 密钥可在开发者页 **启用 / 停用**、设置 **IP 白名单**（仅允许指定 IP 调用）。
- 鉴权失败返回 `401`；IP 不在白名单返回 `403`。

---

## 3. 请求与响应格式

统一信封（注意字段名是 **`msg`**，不是 `message`）：

```jsonc
// 成功
{ "code": 0, "msg": "ok", "data": <结果> }
// 失败（多数错误带 error_code）
{ "code": 400, "error_code": "insufficient_balance", "msg": "余额不足" }
```

**★HTTP 状态码是有意义的，不要一律当 200 处理★**，body 里的 `code` 与 HTTP 状态通常一致：

| HTTP 状态 | 含义 | 你该怎么做 |
|------|------|------|
| 200 | 成功（`code:0`）| 读 `data` |
| **202** | **已受理，结果待定**（退款、删卡）| ★不是失败也不是成功★，见下方「异步受理」 |
| 400 | 参数错误 / 业务失败 | 看 `error_code`，多为终态，别盲目重试 |
| 401 | 密钥缺失 / 无效 / 已停用 | 检查 `X-API-Key` |
| 403 | IP 不在白名单 / 未开通权限 / 未充值 / 子账户无权 | 看 `error_code` 区分，见附录 |
| 404 | 对象不存在（订单、CDK 订单）| 确认 id 归属本密钥 |
| 409 | 冲突（幂等键仍在处理中、订单已开始支付无法取消）| 退避后重查状态，别重复提交 |
| 429 | 频率超限（默认 100 次/分钟/密钥）| 指数退避 |
| 500 | 服务端错误 | 可重试；写操作重试务必带同一 `Idempotency-Key` |
| 503 | 渠道暂时不可用（上游故障已熔断）| 稍后重试或换 `issuer` |

### ★一律按 `error_code` 判断，不要匹配 `msg` 文案★

`msg` 是给人看的中文说明，**会改**；`error_code` 是稳定的机器可读值。
完整对照见 [附录 A：错误码](#附录-a错误码对照)。

### ★异步受理（HTTP 202）：受理 ≠ 成功★

**退款**和**删卡**可能返回 HTTP `202`，表示上游已受理但结果未确认：

```jsonc
{ "code": 0, "msg": "退款已提交，正在确认结果", "data": { "pending": true } }
```

- `data.pending == true` → 结果待定，**不要重复提交**，改为轮询卡列表 / 卡详情确认。
- `data.pending == false`（HTTP 200）→ 已确认完成。
- 系统会自动对账收口；重复提交可能造成重复操作。

**直充下单**同理：`POST /gpt-direct/orders` 返回 HTTP `202` + `"msg":"accepted"`，
只表示订单已入队，**不代表订阅已开通**，必须轮询订单状态到终态。

> ★充值是例外★：卡充值结果未确认时返回的是 **HTTP 400** 加「充值结果确认中」类
> 文案（不是 202），同样不要重复提交，等系统对账。

### 两个不守规矩的响应（历史兼容，接入时请特判）

| 接口 | 异常表现 |
|---|---|
| `GET /cards/{id}/openai-payments` | 卡不存在时返回 **HTTP 200**，但 body 是 `{"code":404,...}` |
| `GET /balance-logs` | 筛选参数非法时返回 400，但**不带 `error_code`**，只有 `msg` |

**常见业务错误（`msg` 文案示例）**：余额不足、卡产品不存在或已下架、最低开卡金额限制、卡不存在、卡状态异常无法操作、退款金额超出卡内余额、退款后卡内余额不得少于 $1 等。

### 渠道暂时不可用（可编程区分）

当某发卡渠道上游故障时，系统会探活确认后**临时熔断**该渠道，开卡 / 充值接口返回 **HTTP `503`**，body 带稳定的 `error_code`：

```jsonc
{ "code": 503, "error_code": "channel_unavailable", "msg": "渠道暂时不可用，请稍后再试或选择其他渠道" }
```

- 请按 `error_code == "channel_unavailable"` 判定（勿匹配 `msg` 文案），据此**稍后重试**或**改用其他渠道的产品**（不同 `issuer`）。
- 渠道恢复后自动放开，无需人工介入；该状态只影响对应渠道，其他渠道正常。
- 此为暂时性故障，非扣费失败——熔断拦截发生在扣费之前，不会产生扣款。


---

## 4. 幂等

开卡、充值、退款等写操作，带一个唯一的 `Idempotency-Key` 头：

```
Idempotency-Key: 你的唯一订单号
```

同一密钥下、相同 `Idempotency-Key` 的请求只会真正执行一次；重试会**原样返回首次结果**（响应头带 `Idempotent-Replayed: true`），可避免网络重试导致**重复开卡 / 重复扣费**。

---

## 5. 数据字典

**卡状态 `status`**

| 状态 | 说明 |
| --- | --- |
| ACTIVE | 激活（正常使用）|
| FROZEN | 已冻结 |
| CANCELLED | 已注销 |
| DELETED | 已删除 |

**交易类型 `type`**

| 类型 | 说明 |
| --- | --- |
| Authorization | 消费授权 |
| Settlement | 清算 |
| Refund | 消费退款 |
| Reversal | 授权撤销 |

**交易状态 `status`**

| 状态 | 说明 |
| --- | --- |
| PENDING | 清算中 |
| COMPLETE | 清算完成 |
| DECLINED | 交易失败（失败原因见 `description`）|

---

## 6. 接口详解

### 6.1 获取可开卡产品

`GET /products`

返回当前可开卡的产品列表，**含你的专属开卡费/退款率**。

**响应字段（`data` 数组项）**

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| product_code | string | 产品码（开卡用）。开卡时原样回传本接口给出的值即可 |
| network | string | 卡组织：VISA / MasterCard |
| issuing_area | string | 发行区域 |
| card_type | string | 卡类型：save=储值卡 |
| open_fee | number | 开卡费（你的专属价）|
| recharge_fee | number | 卡充值手续费率 |
| rtf_rate | number | 消费退款手续费率 |
| min_amount | number | 产品最低开卡/卡充值金额（链上账户充值另受 50U 平台下限约束）|
| max_amount | number | 最高金额 |
| restricted_merchants | string[] | 当前卡头禁用商户规则；空数组表示不禁用商户 |
| google_chatgpt_blocked | boolean | 兼容字段；有效规则会命中 `GOOGLE CHATGPT` 时为 true |

**请求**
```bash
curl https://zovocard.com/openapi/v1/products -H "X-API-Key: sk_你的密钥"
```

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": [
    {
      "product_code": "P5378OX",
      "issuer": "one",
      "network": "MasterCard",
      "issuing_area": "United States",
      "card_type": "save",
      "open_fee": 1.5,
      "recharge_fee": 0,
      "rtf_rate": 0.1,
      "min_amount": 10,
      "max_amount": 10000,
      "restricted_merchants": ["GOOGLE CHATGPT"],
      "google_chatgpt_blocked": true
    }
  ]
}
```

> `issuer` 为发卡渠道：`one` / `two` / `three` / `four`。各渠道卡段、发行地、费率可能不同，开卡时用对应 `product_code` 即可，无需关心底层差异；所有卡接口（充值/退款/冻结/删卡/查询）对两渠道通用。

---

### 6.2 开卡

`POST /cards/open`

**请求参数**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| product_code | string | 是 | 产品码 |
| first_name | string | 是 | 持卡人名 |
| last_name | string | 是 | 持卡人姓 |
| init_amount | number | 是 | 初始充值金额（≥ 产品最低金额）|
| max_on_daily / max_on_monthly / max_on_percent | number | 否 | 渠道4 开卡时日/月/单笔限额（USD）|
| transaction_limit / transaction_limit_type | number / string | 否 | 渠道4 可交易总额度与 limited/unlimited |

开卡将从账户可消费余额扣除 **开卡费 + 初始充值金额**；账户必须保留 20U 风险保证金。产品或卡详情中的 `restricted_merchants` 是该卡头的有效禁用商户列表。

**请求**
```bash
curl -X POST https://zovocard.com/openapi/v1/cards/open \
  -H "X-API-Key: sk_你的密钥" \
  -H "Idempotency-Key: order-20260604-001" \
  -H "Content-Type: application/json" \
  -d '{"product_code":"P5378OX","first_name":"John","last_name":"Doe","init_amount":20}'
```

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "id": 123,
    "user_id": 31,
    "issuer": "one",
    "vm_card_id": "card55202606040031562947331",
    "card_number": "5378727109708264",
    "cvv": "123",
    "expire": "08/29",
    "product_code": "P5378OX",
    "network": "MasterCard",
    "issuing_area": "United States",
    "available_amount": 20,
    "status": "ACTIVE",
    "open_fee": 1.5,
    "first_name": "John",
    "last_name": "Doe",
    "created_at": "2026-06-04T00:31:56Z"
  }
}
```

> 开卡响应即时返回 `cvv` / `expire`（有效期 MM/YY），请妥善保存；后续 `GET /cards/{id}` 也可再取。卡列表接口出于安全不返回 CVV。

---

### 6.3 批量开卡

`POST /cards/batch-open`

在 6.2 参数基础上增加 `count`（开卡数量）。先预检总余额，再逐张开卡；单张失败自动退款并继续。

**请求**
```bash
curl -X POST https://zovocard.com/openapi/v1/cards/batch-open \
  -H "X-API-Key: sk_你的密钥" -H "Content-Type: application/json" \
  -d '{"product_code":"P5378OX","first_name":"John","last_name":"Doe","init_amount":20,"count":3}'
```

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "success": [ { "id": 124, "card_number": "5378727100000001", "status": "ACTIVE" } ],
    "failed":  [ { "index": 2, "error": "余额不足" } ]
  }
}
```

---

### 6.4 我的卡列表

`GET /cards?page=1&page_size=20`

**请求参数**：`page`（页码，默认 1）、`page_size`（每页数量，默认 20）、`q`（按卡号/备注模糊搜索，选填）、`sync`（传 `1` 时实时同步当前页各卡余额/状态后再返回，便于核对“卡里还有多少钱”；不传则返回缓存值，更快）。

> `available_amount` 默认为缓存值（由 Webhook/消费回调更新，可能有滞后）。需要**实时余额**时加 `&sync=1`，系统会逐卡向上游查询当前页余额后返回（单卡 60 秒内最多同步一次，避免触发上游限速）。

**请求**
```bash
# 实时余额：加 sync=1（不加则返回更快的缓存值）
curl "https://zovocard.com/openapi/v1/cards?page=1&page_size=20&sync=1" -H "X-API-Key: sk_你的密钥"
```

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "total": 2,
    "list": [
      {
        "id": 123,
        "vm_card_id": "card55202606040031562947331",
        "card_number": "5378727109708264",
        "product_code": "P5378OX",
        "network": "MasterCard",
        "issuing_area": "United States",
        "available_amount": 18.8,
        "status": "ACTIVE",
        "first_name": "John",
        "last_name": "Doe",
        "created_at": "2026-06-04T00:31:56Z"
      }
    ]
  }
}
```

---

### 6.5 卡详情（含卡号 / 有效期 / CVV）

`GET /cards/{id}`

`{id}` 为本地卡 ID。实时返回完整卡信息（含敏感字段）。

**响应字段（`data`）**

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| card_id | string | 卡 ID |
| card_number | string | 完整卡号 |
| cvv | string | CVV 安全码 |
| expire | string | 有效期 MM/YY |
| status | string | 卡状态 |
| user_name | string | 持卡人姓名 |
| available_amount | number | 卡内可用余额 |
| card_type | string | 卡类型 |
| first_name / last_name | string | 持卡人名 / 姓 |
| create_time | string | 开卡时间 |
| card_address | object | 账单地址 |
| limit | object | 额度设置（额度卡有效）|

**请求**
```bash
curl https://zovocard.com/openapi/v1/cards/123 -H "X-API-Key: sk_你的密钥"
```

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "card_id": "card55202606040031562947331",
    "card_number": "5378727109708264",
    "cvv": "123",
    "expire": "08/29",
    "status": "ACTIVE",
    "user_name": "John Doe",
    "available_amount": 18.8,
    "card_type": "save",
    "first_name": "John",
    "last_name": "Doe",
    "create_time": "2026-06-04 00:31:56",
    "card_address": {
      "address_line_one": "",
      "address_line_two": "",
      "city": "",
      "state": "",
      "country": "",
      "post_code": ""
    }
  }
}
```

---

### 6.6 卡消费记录

`GET /cards/{id}/transactions?page=1&page_size=50&sync=0`

**响应字段（`data` 数组项）**

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| auth_id | string | 交易 ID |
| card_id | string | 上游卡 ID（渠道卡标识）|
| auth_time | string | 交易授权时间 |
| auth_amount | number | 授权金额 |
| auth_currency | string | 授权币种 |
| settle_amount | number | 结算金额 |
| settle_currency | string | 结算币种 |
| status | string | 交易状态（见数据字典）|
| type | string | 交易类型（见数据字典）|
| merchant_name | string | 交易商户 |
| merchant_amount | number | 商户本币金额；本地历史投影可能为 0 |
| merchant_currency | string | 商户本币币种；没有时为空字符串 |
| create_time | string | 上游创建时间；本地历史投影可能为空，实时 Webhook 才保证原始值 |
| description | string | 交易详情 / 失败原因 |

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": [
    {
      "auth_id": "1059958172",
      "card_id": "card55202606040031562947331",
      "auth_time": "2026-06-04 02:29:40",
      "auth_amount": 9.99,
      "auth_currency": "USD",
      "settle_amount": 9.99,
      "settle_currency": "USD",
      "status": "COMPLETE",
      "type": "Settlement",
      "merchant_name": "OPENAI",
      "merchant_amount": 9.99,
      "merchant_currency": "USD",
      "create_time": "",
      "description": ""
    },
    {
      "auth_id": "1059957962",
      "card_id": "card55202606040031562947331",
      "auth_time": "2026-06-04 02:20:34",
      "auth_amount": 5.00,
      "auth_currency": "USD",
      "settle_amount": 0,
      "settle_currency": "USD",
      "status": "DECLINED",
      "type": "Authorization",
      "merchant_name": "STEAM",
      "merchant_amount": 5.00,
      "merchant_currency": "USD",
      "create_time": "",
      "description": "Insufficient funds"
    }
  ]
}
```

> 另有 `GET /cards/all-transactions` 一次性聚合你名下所有卡的消费记录（响应结构同上，每项额外带 `card_number`、`local_card_id`）。

---

### 6.7 卡段 OpenAI 最新支付（三档价位）

`GET /cards/{id}/openai-payments`

返回该卡**所属卡段**（卡头/BIN，渠道1/2 按产品、渠道3 按卡头）在 **OpenAI** 商户三个价位档（Plus / 5x / 20x）各自「最新一笔」支付的金额与时间，作为该卡段的**最新行情参考价**（用于参考下单）。取卡段而非单卡：新卡/未刷过某档的卡也能拿到该卡段的最新价。

**档位（按结算金额 USD 区间）**

| tier | label | 金额区间 |
| --- | --- | --- |
| plus | Plus | $15 – $20 |
| x5 | 5x | $90 – $100 |
| x20 | 20x | $140 – $160 |

**响应字段（`data` 数组，固定 3 项，按上表顺序）**

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| tier | string | 档位标识：plus / x5 / x20 |
| label | string | 档位名：Plus / 5x / 20x |
| min_usd | number | 档位下限（USD）|
| max_usd | number | 档位上限（USD）|
| amount | number | 该卡段该档最新一笔支付金额（USD），无则 0 |
| time | string | 该卡段该档最新一笔授权时间，无则空字符串 |
| found | boolean | 该档是否有匹配记录 |

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": [
    { "tier": "plus", "label": "Plus", "min_usd": 15, "max_usd": 20, "amount": 16.24, "time": "2026-06-16 09:43:25", "found": true },
    { "tier": "x5", "label": "5x", "min_usd": 90, "max_usd": 100, "amount": 99.00, "time": "2026-06-15 20:11:03", "found": true },
    { "tier": "x20", "label": "20x", "min_usd": 140, "max_usd": 160, "amount": 150.00, "time": "2026-06-16 03:02:55", "found": true }
  ]
}
```

> 仅统计非拒付（成功 / 挂账）的 OPENAI 商户交易。某档无记录时 `found=false`、`amount=0`、`time=""`。

---

### 6.8 卡充值记录

`GET /cards/{id}/recharges`

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": [
    {
      "id": 88,
      "user_id": 31,
      "card_id": 123,
      "vm_card_id": "card55202606040031562947331",
      "amount": 20,
      "fee": 0,
      "status": "success",
      "created_at": "2026-06-04T01:59:55Z"
    }
  ]
}
```

---

### 6.8b 卡资金流水

`GET /cards/{id}/fund-flows`

一张卡的**资金进出**账本：开卡初始注资、卡充值、退款回钱包、删卡退回。
与 `6.6 卡交易记录` 互不重叠——那边是**消费**（商户扣款/退款），这边是**资金进出**。

`amount` 为「对卡的影响」：正数=钱进卡，负数=钱出卡回到你的钱包。
已删卡仍可查询（删卡退回那笔正在这里）。

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": [
    {
      "id": 901792,
      "time": "2026-08-20 03:49:34",
      "type": "open_card_funding",
      "direction": "in",
      "amount": 20.4,
      "card_number": "5378727127637909",
      "remark": "开卡 P5378OX + 初始充值 $20.00（卡号 5378727127637909）"
    },
    {
      "id": 901880,
      "time": "2026-08-20 04:12:01",
      "type": "card_refund",
      "direction": "out",
      "amount": -20,
      "card_number": "5378727127637909",
      "remark": "删卡退回余额 5378727127637909"
    }
  ]
}
```

| `type` | 含义 |
|---|---|
| `open_card_funding` | 开卡费 + 初始充值（合并一条） |
| `card_recharge` | 卡充值 |
| `card_refund` | 退款回钱包 / 删卡退回 |

---

### 6.9 卡充值

`POST /cards/recharge`

**请求参数**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| card_id | number | 是 | 本地卡 ID |
| amount | number | 是 | 充值金额 |

从账户可消费余额扣除 `金额 + 手续费`（手续费 = 金额 × 卡充值费率）；账户必须保留 20U 风险保证金。产品或卡详情中的 `restricted_merchants` 是该卡头的有效禁用商户列表。

> **最低充值额**：卡详情 / 卡列表返回的 `min_recharge` 是该卡充值的**有效下限**（$），据它决定补充金额。渠道 1（vmcard）固定 `$10`、渠道 4（星链/PhotonPay）取该产品配置的最低额；其余渠道 `min_recharge=0` 表示不强制下限。低于下限会返回 `error_code=amount_below_min`（HTTP 400，**参数错误、不可重试**），切勿对同一笔重发。注意 `min_recharge` 可能与产品的 `min_amount`（开卡初始金额下限）不同——例如 `min_amount=20` 但 `min_recharge=10`。

**请求**
```bash
curl -X POST https://zovocard.com/openapi/v1/cards/recharge \
  -H "X-API-Key: sk_xxx" -H "Content-Type: application/json" \
  -d '{"card_id":123,"amount":50}'
```

**响应**
```json
{ "code": 0, "msg": "充值成功" }
```

---

### 6.10 卡退款（卡内余额退回平台余额）

`POST /cards/refund`

**请求参数**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| card_id | number | 是 | 本地卡 ID |
| amount | number | 是 | 退款金额（≤ 卡内可用余额；退款后卡内余额需 ≥ $1）|

主动从卡退回平台余额**不收手续费**，全额到账。

**请求**
```bash
curl -X POST https://zovocard.com/openapi/v1/cards/refund \
  -H "X-API-Key: sk_xxx" -H "Content-Type: application/json" \
  -d '{"card_id":123,"amount":10}'
```

**响应**
```json
{ "code": 0, "msg": "退款成功，余额已退回" }
```

---

### 6.11 设置卡消费限额（渠道 4）

`POST /cards/limits`

仅 **issuer=four（PhotonPay）** 支持。其它渠道返回业务错误。开卡时也可在 `POST /cards/open` 一并传入同名字段。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| card_id | number | 是 | 本地卡 ID |
| max_on_daily | number | 否 | 日交易限额（USD）；省略表示不改 |
| max_on_monthly | number | 否 | 月交易限额（USD） |
| max_on_percent | number | 否 | 单笔最大金额（USD） |
| transaction_limit | number | 否 | 可交易总额度变动额 |
| transaction_limit_type | string | 否 | `limited` / `unlimited` |
| transaction_limit_change_type | string | 否 | `increase` / `decrease`（调总额度时） |

```bash
curl -X POST https://zovocard.com/openapi/v1/cards/limits \
  -H "X-API-Key: sk_你的密钥" -H "Content-Type: application/json" \
  -d '{"card_id":123,"max_on_daily":500,"max_on_monthly":5000,"max_on_percent":200}'
```


### 6.11.1 渠道标识（issuer / channel）

| 值 | 含义 | 说明 |
| --- | --- | --- |
| `one` | 渠道 1 | 默认主力渠道 |
| `two` | 渠道 2 | 已退役，存量卡只读/有限操作 |
| `three` | 渠道 3 | 含卡池/按 BIN 等能力 |
| `four` | 渠道 4 PhotonPay | 支持开卡可选限额、`POST /cards/limits` 改限额 |

产品列表 `GET /products` 的 `issuer`、卡对象的 `issuer`、Webhook 的 `channel` 使用同一套取值。客户端应按 `issuer` 选择产品并展示，无需关心上游厂商名。

### 6.12 冻结 / 解冻

`POST /cards/freeze`

**请求参数**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| card_id | number | 是 | 本地卡 ID |
| freeze | boolean | 是 | true=冻结，false=解冻 |

**请求**
```bash
curl -X POST https://zovocard.com/openapi/v1/cards/freeze \
  -H "X-API-Key: sk_xxx" -H "Content-Type: application/json" \
  -d '{"card_id":123,"freeze":true}'
```

**响应**
```json
{ "code": 0, "msg": "ok" }
```

---

### 6.13 修改卡备注

`PUT /cards/{id}/remark`

设置卡片的本地备注（仅你自己的卡）。备注仅用于本地整理 / 搜索（`GET /cards` 的 `q` 可按备注模糊搜索），不影响卡片本身或上游。

**请求参数**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| remark | string | 否 | 备注内容；传空字符串即清空 |

**请求**
```bash
curl -X PUT https://zovocard.com/openapi/v1/cards/123/remark \
  -H "X-API-Key: sk_xxx" -H "Content-Type: application/json" \
  -d '{"remark":"给客户A的卡"}'
```

**响应**
```json
{ "code": 0, "msg": "已保存" }
```

---

### 6.14 删卡

`DELETE /cards/{id}`

永久删除卡，卡内剩余余额**全额退回**平台余额。

**请求**
```bash
curl -X DELETE https://zovocard.com/openapi/v1/cards/123 -H "X-API-Key: sk_xxx"
```

**响应**
```json
{ "code": 0, "msg": "删卡成功，余额已退回" }
```

---

### 6.15 账户余额

`GET /balance`

**请求**
```bash
curl https://zovocard.com/openapi/v1/balance -H "X-API-Key: sk_你的密钥"
```

**响应**
```json
{ "code": 0, "msg": "ok", "data": { "balance": 128.5, "spendable_balance": 108.5, "account_reserve_amount": 20, "account_reserve_enabled": true, "minimum_deposit_amount": 50, "currency": "USD" } }
```

`balance` 是账面余额，`spendable_balance` 是扣除持续锁定保证金后的用户可主动消费余额。风险费用可能使账面余额低于 0；请始终以接口返回的业务错误和 `spendable_balance` 为准。

子账户的 `account_reserve_amount` 固定返回 `0`，因为保证金只保留在主账户；子账户仍与主账户共享风险主体。子账户风险手续费不足时，服务端会自动从主账户代扣，不要求客户端自行拼接或转移这笔费用。

---

### 6.16 账户流水（对账）

`GET /balance-logs?page=1&page_size=20`

**响应字段（`data.list` 项）**：`created_at` 时间、`type` 类型（`recharge` 充值 / `open_card` 开卡 / `card_recharge` 卡充值 / `refund` 退款 / `decline_fee` 消费失败费 / `reversal_fee` 授权撤销费 / `refund_fee` 消费退款费 / `small_tx_fee` 小额消费费 / `merchant_violation_fee` 受限商户处理费 / `admin` 调整等）、`amount` 金额（正增负减）、`before`/`after` 变动前后余额、`remark` 备注。

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "total": 12,
    "list": [
      {
        "created_at": "2026-06-04T01:59:55Z",
        "type": "card_recharge",
        "amount": -20,
        "before": 38.8,
        "after": 18.8,
        "remark": "卡 5378727109708264 充值 $20.00 (手续费 $0.00)"
      }
    ]
  }
}
```

---

### 6.16b 账户链上充值（取地址 + 贴哈希提速到账）

为**账户余额**充值（开卡 / 卡充值 / 直充的资金来源）。链上到账本有区块确认延迟；转账后把交易哈希贴回来可**立即核验、提速到账**，无需等待自动扫链。

> 仅主账户可充值（子账户返回 `403` `forbidden`）。这组接口**不受「未充值才放行」限制**，未充值账户也能调用——充值本身就是拿到首充的方式。网络仅支持 EVM 专属地址：`BSC` / `ERC20` / `XLAYER`（同一以太坊地址格式，三链共用一个地址）；TRON 请走网页端共享地址意向。单笔最低 `50U`，低于 `50U` 的转账不会自动入账、进入人工处理。

**① 取充值地址**

`GET /recharge/address?coin=USDT&network=BSC`

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| network | 是 | `BSC` / `ERC20` / `XLAYER`（EVM 共用地址）|
| coin | 否 | 默认 `USDT` |

```json
{ "code": 0, "msg": "ok", "data": { "coin": "USDT", "network": "BSC", "address": "0x…" } }
```

把对应金额的 USDT 转入该 `address`。

**② 贴交易哈希提速到账**

`POST /recharge/submit-tx`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| network | string | 是 | 与转账网络一致：`BSC` / `ERC20` / `XLAYER` |
| tx_hash | string | 是 | 链上交易哈希（≤ 128 字符）|

```bash
curl -X POST https://zovocard.com/openapi/v1/recharge/submit-tx \
  -H "X-API-Key: sk_xxx" -H "Content-Type: application/json" \
  -d '{"network":"BSC","tx_hash":"0x…"}'
```

**响应**
```json
{ "code": 0, "msg": "已完成链上核验，等待来源检测及入账处理", "data": { "amount": 100, "credited": 0, "status": "manual" } }
```

> ★响应沿用「不守规矩」的历史契约（见 §3 两个不守规矩的响应）★：HTTP **恒为 200**，用 **body 里的 `code`** 判断——`code=0` 为受理 / 已入账，`code=400` 为参数或核验失败（看 `error_code`：`invalid_argument` 哈希/网络非法，`upstream_unavailable` 核验未就绪）。同一笔 `tx_hash` 重复提交**天然幂等**：已入账会直接回执当前 `status` / `credited`，不会重复入账；换过充值地址的，转到旧地址的钱也能用本接口提速。到账后用 `GET /balance` 复核余额变化。

---

### 6.17 会员等级

`GET /vip`

返回当前账户的会员等级、累计数据与各档达标门槛。会员按**累计充值**自动达标（也可后台手动授予），达标后享更低开卡费与充值费率，高等级含低等级全部权益。

**等级（tier）**

| tier | 名称 | 达标（累计充值）| 权益（开卡费 / 充值费率 / 退款费）|
| --- | --- | --- | --- |
| `super` | 超级SVIP | ≥ $3,000 | $1 / 1% / 7% |
| `supreme` | 至尊SVIP | ≥ $20,000 | $0.5 / 1% / 5% |
| `legend` | 传奇SVIP | ≥ $100,000 | $0.5 / 0.8% / 3% |
| `none` | 普通 | — | 产品默认 / 全局默认 / 10%（港卡 15%）|

**响应字段（`data`）**

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| tier | string | `none` / `super` / `supreme` / `legend` |
| tier_name | string | 等级中文名 |
| is_svip | boolean | 是否超级SVIP 及以上 |
| is_supreme_svip | boolean | 是否至尊SVIP 及以上 |
| is_legend_svip | boolean | 是否传奇SVIP |
| active_cards | number | 名下有效卡数（仅供参考，不作为达标门槛）|
| cumulative_recharge | number | 累计充值（USDT，已入账）|
| recharge_fee_rate | number | 当前生效充值费率（如 0.01 = 1%）|
| thresholds.super / .supreme / .legend | object | 各档门槛 `{cards, recharge}`；当前仅按 `recharge` 达标，`cards` 恒为 `0` |

**响应**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "tier": "super",
    "tier_name": "超级SVIP",
    "is_svip": true,
    "is_supreme_svip": false,
    "is_legend_svip": false,
    "active_cards": 63,
    "cumulative_recharge": 4200.50,
    "recharge_fee_rate": 0.01,
    "thresholds": {
      "super": { "cards": 0, "recharge": 3000 },
      "supreme": { "cards": 0, "recharge": 20000 },
      "legend": { "cards": 0, "recharge": 100000 }
    }
  }
}
```

---

## 7. 回调 Webhook（卡事件、GPT 直充与 CDK）

配置回调地址后，平台会主动 **POST** 卡事件；按 API Key 订阅还可接收 GPT 直充全终态、阶段进度、公开时间线和 CDK 生命周期事件。

> 卡交易/操作使用 `event`；GPT / CDK 事件使用 `type`。API Key 可订阅 `gpt_direct.*`、`cdk.*` 或具体事件。上游提供开卡、充值、冻结/解冻、删卡或卡余额退款结果时，平台会在能安全解析卡主时转发操作回调。
>
> 当前运行中的发卡渠道为渠道 1、渠道 3 与渠道 4（PhotonPay）；渠道 2 的 Webhook 已退役。各渠道事件已在平台侧**归一化**为下面同一套结构，接收端以 `channel`/`issuer` 区分即可。

### 7.1 配置回调地址

**商城订单通用回调：**「开发者 → 通用回调」保存 HTTPS 地址后，该账号后续商城订单的 `gpt_direct.completed` / `gpt_direct.failed` / `gpt_direct.cancelled` 终态事件会推送，携带 `source=mall`、`product` 和稳定的 `event_id`。通用回调不推送阶段进度或时间线，也不会补发历史订单；历史及漏收数据用 §6.18.5b 查询补齐。API Key 独立订阅不会自动接收未绑定该 Key 的商城订单。对已绑定 Key 的订单，启用 Key 独立回调后不再同时投递通用回调。

通用设置的网页登录接口为 `GET /api/v1/dev/webhook`、`POST /api/v1/dev/webhook`，POST body 为 `{"webhook_url":"https://merchant.example/webhook"}`，使用网页 JWT 登录鉴权，不接受将 `X-API-Key` 当网页令牌。首次保存生成通用 `webhook_secret`；按原始请求体计算 HMAC-SHA256 十六进制签名并比对 `X-Signature`，按 `event_id` 幂等处理，收到后返回 2xx。GPT 回调的 `completed_at` 仍是 Unix 秒（查询接口为 RFC3339）。

「开发者」页保留通用回调，并在每条 API 密钥的 Webhook 操作中提供独立配置。白标 CDK 应使用 Key 级配置：

```http
GET /api/v1/dev/keys/{key_id}/webhook
PUT /api/v1/dev/keys/{key_id}/webhook

{
  "webhook_url": "https://merchant.example/webhooks/cardplatform",
  "webhook_events": ["gpt_direct.*", "cdk.*"]
}
```

- 地址必须是有效的 **https** URL（不接受 `http`、`localhost`、内网 / 私有 IP；长度 ≤ 256）。
- **首次**保存非空回调地址时，系统自动生成该 Key 独立的签名密钥 `webhook_secret`（形如 `whsec_xxxx…`），在开发者页可见，用于校验请求来源（见 7.7）。
- 清空回调地址即停止推送。

### 7.2 请求

| 项 | 值 |
| --- | --- |
| 方法 | `POST` |
| Content-Type | `application/json` |
| 请求头 | `X-Signature`：请求体的 HMAC-SHA256 签名（见 7.7）|
| 期望响应 | HTTP `2xx`；其它状态码或超时（约 10s）视为失败并重试 |

**请求体（JSON）**
```json
{
  "event": "card_transaction",
  "auth_id": "1059958172",
  "vm_card_id": "card55202606040031562947331",
  "card_id": 123,
  "card_number": "5378721234568264",
  "auth_time": "2026-07-20 10:20:30",
  "auth_amount": 9.99,
  "auth_currency": "USD",
  "settle_amount": 9.99,
  "settle_currency": "USD",
  "merchant_name": "GOOGLE *CHATGPT 766999 GB",
  "merchant": "GOOGLE *CHATGPT 766999 GB",
  "merchant_location": "United Kingdom",
  "merchant_region": "England",
  "merchant_country": "GB",
  "description": "GOOGLE *CHATGPT 766999 GB",
  "failed_reason": "",
  "bill_status": "Settled",
  "merchant_amount": 9.99,
  "merchant_currency": "USD",
  "create_time": "2026-07-20 10:20:31",
  "status": "COMPLETE",
  "type": "Settlement",
  "source": "webhook",
  "channel": "three"
}
```

### 7.3 事件字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `event` | string | 事件类型，目前固定为 `card_transaction`（卡交易）|
| `auth_id` | string | 上游交易 / 授权唯一号，**幂等键**（见 7.7）|
| `vm_card_id` | string | 卡在系统内的上游卡标识 |
| `card_id` | number | 卡在平台的数字 ID（对应 `GET /cards/{id}`）|
| `card_number` | string | **完整卡号**（请按需妥善保管，勿明文落日志）|
| `auth_time` | string | 平台归一化的授权时间；渠道 1 保持上游格式，渠道 3 将上游 UTC `billTime` 转为北京时间（`YYYY-MM-DD HH:mm:ss`） |
| `auth_amount` | number | 授权金额 |
| `auth_currency` | string | 授权/交易币种 |
| `settle_amount` | number | 结算金额（USD，正数）。清算 / 退款为实际金额；授权与拒付可能为 `0` |
| `settle_currency` | string | 结算币种 |
| `merchant_name` | string | 商户名称或商户描述原文；描述中的验证码不会被单独提取 |
| `merchant` | string | `merchant_name` 的兼容别名 |
| `merchant_location` | string | 商户地区/位置；上游没有提供时为空字符串 |
| `merchant_region` / `merchant_country` | string | 上游提供的地区/国家别名；没有时为空字符串 |
| `description` | string | 交易描述原文；不要依赖平台另行生成验证码字段 |
| `failed_reason` | string | 上游失败原因；没有时为空字符串 |
| `bill_status` | string | 上游账单状态原值；实时回调通常是文本，渠道 3 REST 补账也可能是数字代码字符串（如 `"99"`） |
| `merchant_amount` / `merchant_currency` | number / string | 上游或渠道归一化的商户本币金额和币种；没有时为 `0` / 空字符串 |
| `create_time` | string | 上游创建时间；渠道 3 保留上游 UTC 原文，渠道 1 按上游回调提供；没有时为空字符串 |
| `status` | string | 交易状态：`PENDING` / `COMPLETE` / `DECLINED`（见 [§5 数据字典](#5-数据字典)）|
| `type` | string | 交易类型：`Authorization` / `Settlement` / `Refund` / `Reversal`（见 [§5 数据字典](#5-数据字典)）|
| `source` | string | `webhook` 为实时上游回调，`reconciled` 为单卡对账补录 |
| `channel` | string | 渠道内部标记：`one` / `three` / `four`（PhotonPay）；渠道 2 Webhook 已退役。**附加字段**，接收端可按渠道分流 |

> 旧字段全部保留。交易接收端应按 `event + auth_id + type + status` 幂等；同一授权从 `PENDING` 变为 `COMPLETE` 时不能只按 `auth_id` 丢弃后续状态。
>
> 渠道 3 的验证码如果位于上游 `merchantDescription` / `merchantName` 中，会原样进入归一化事件的 `description` / `merchant_name`；平台不会生成独立的 `verification_code` 或 `otp` 字段。

### 7.4 卡操作事件

开卡、充值、冻结、解冻、删卡和卡余额退款等操作如果有上游状态回调，平台发送独立的 `card_operation` 事件。无法安全归类的上游通用卡状态事件使用 `operation = status_change`。商户退款仍使用 `card_transaction`，`type = Refund`。

```json
{
  "event": "card_operation",
  "operation": "recharge",
  "operation_id": "R202607200001",
  "event_type": "CardRechargeStatusChanged",
  "bill_number": "R202607200001",
  "vm_card_id": "three-card-202607200001",
  "card_id": 123,
  "card_number": "5378721234568264",
  "status": "COMPLETE",
  "success": true,
  "operate_status": "Success",
  "operate_status_value": 1,
  "bill_status": "Completed",
  "failed_reason": "",
  "amount": 10,
  "currency": "USD",
  "balance": 20,
  "balance_after": 30,
  "card_status": "Activated",
  "card_status_value": 1,
  "occurred_at": "2026-07-20 10:20:30",
  "source": "webhook",
  "channel": "three"
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `event` | string | 固定为 `card_operation` |
| `operation` | string | `open_card` / `recharge` / `freeze` / `unfreeze` / `delete` / `refund` / `status_change` |
| `operation_id` | string | 上游操作单号；删卡无单号时使用卡号 |
| `event_type` | string | 上游操作事件名 |
| `bill_number` | string | 上游账单/操作单号，没有时为空 |
| `vm_card_id` / `card_id` / `card_number` | string / number / string | 能解析到本地卡时返回；`vm_card_id` 是本地上游卡标识，不是完整卡号；开卡回调可能暂时没有 `card_id` |
| `card_number_masked` | string | 上游脱敏卡号（如有） |
| `status` | string | `PENDING` / `COMPLETE` / `FAILED` |
| `success` | boolean | 上游是否明确成功 |
| `operate_status` / `operate_status_value` | string / number | 上游操作状态原值 |
| `bill_status` | string | 上游账单状态原值 |
| `failed_reason` | string | 上游失败原因 |
| `amount` / `currency` | number / string | 本次操作金额和币种 |
| `event_amount` / `settled_amount` / `refund_amount` / `revoked_amount` | number | 上游各阶段原始金额 |
| `balance` / `balance_after` | number | 上游回调中的操作前后余额 |
| `card_status` / `card_status_value` | string / number | 上游卡状态原值 |
| `occurred_at` | string | 上游事件时间 |
| `source` / `channel` | string | 回调来源和渠道；当前实时来源为 `webhook` |

> 开卡回调可能早于本地卡记录创建。无法通过操作单号或卡号安全找到卡主时只保留内部记录，不会把事件发送给错误用户；待卡主可解析后才发送。

### 7.7 签名校验（务必校验）

`X-Signature` = `HMAC-SHA256(webhook_secret, 原始请求体字节)` 的**十六进制小写**串。必须用**原始 body** 计算（不要先反序列化再重新序列化，否则字节不一致导致校验失败），并用常量时间比较：

```js
const crypto = require('crypto')
// 用 raw body（Buffer），不要用已解析的对象
app.post('/webhook', express.raw({ type: '*/*' }), (req, res) => {
  const expect = crypto.createHmac('sha256', WEBHOOK_SECRET).update(req.body).digest('hex')
  const got = req.headers['x-signature'] || ''
  const ok = expect.length === got.length &&
    crypto.timingSafeEqual(Buffer.from(expect), Buffer.from(got))
  if (!ok) return res.status(401).end()

  const evt = JSON.parse(req.body.toString('utf8'))
  // TODO: 卡事件按业务字段、GPT/CDK 事件按 evt.event_id 幂等处理
  res.status(200).end()
})
```

### 7.8 投递、重试与幂等

- **异步投递**，不阻塞上游事件处理；每次投递超时约 10 秒。
- 卡事件、手续费和资金变动回调：未收到 `2xx` 会**退避重试**，最多共 3 次（间隔约 `2s`、`4s` 递增）；仍失败则丢弃该次投递（不会无限重投）。
- GPT 直充 / CDK 回调（`gpt_direct.*`、`cdk.*`）为持久化投递：未收到 `2xx` 按约 `10s`、`30s`、`2min`、`10min`、`30min` 退避重试，最多共 6 次；同一接收地址连续失败时暂停投递、最长每 5 分钟探测一次（暂停期间不计入重试次数），恢复后按原顺序补发；超过 24 小时仍未送达的放弃。各接收地址相互隔离，同一地址按事件先后逐条投递，某个地址不可用不会延迟其他地址的回调。
- 回调是通知而不是唯一事实来源：长时间未收到回调时，请用订单查询接口核对最终状态，不要据此判定订单失败或重复下单。
- 接收端应**尽快返回 2xx**（重活丢队列异步做），否则易触发重试与重复投递。
- **幂等**：交易事件请以 `event + auth_id + type + status` 去重；操作事件请以 `event + operation + operation_id + status` 去重；GPT / CDK 事件请以稳定的 `event_id` 去重，切勿重复入账或发货。
- `source = reconciled` 表示单卡对账补录，不代表新的上游扣款；接收端应按同一业务号合并或更新记录。
- 平台侧拒付、撤销、退款、小额消费和受限商户费用也按业务号幂等；同一事件回放不会再次收费或重复执行冻结/删卡处置。
- **不保证严格顺序**：极端情况下 `COMPLETE` 可能早于 `PENDING` 到达，请以最终态为准。

### 7.9 安全建议

- 只接受 `https`；先校验 `X-Signature` 再处理，拒绝签名不符的请求。
- `card_number` 是完整卡号，按合规要求存储 / 传输，切勿写入明文日志或转发到不可信下游。
- GPT / CDK payload 中的卡号与账号均已脱敏，且不包含完整 CDK、凭据、API Key 或代理信息。
- 平台仅从公网地址回源，回调地址不可指向内网 / 本机。
- 平台不会把商户描述中的验证码单独提取成站内通知或独立回调字段；需要时请读取 `merchant_name` / `description`。

### 7.10 资金变动事件（`balance_change`）与手续费事件（`card_fee`）

**所有会动钱的操作都有回调。** `balance_change` 直接挂在账本（余额流水）上，
因此覆盖是完整的：开卡扣费、卡充值、各类退款、各种手续费、内部转账、人工调整、
链上充值到账、直充下单与释放等等，**新增任何资金操作都会自动出现在这条流里**，
不需要我们再单独接线、也不会漏。

#### `balance_change`

```json
{
  "event": "balance_change",
  "balance_log_id": 8812345,
  "type": "decline_fee",
  "amount": -0.30,
  "direction": "debit",
  "currency": "USD",
  "balance_before": 120.00,
  "balance_after": 119.70,
  "remark": "消费失败手续费 $0.30 卡 4147000000000000（insufficient funds）",
  "ref_id": 44219,
  "card_id": 365712,
  "card_number": "4147000000000000",
  "occurred_at": "2026-08-27 10:15:02"
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `balance_log_id` | number | 账本行号。**唯一且不变，请按它去重**（投递是「至少一次」） |
| `type` | string | 流水类型，与「财务明细」页显示的口径一致；见下表 |
| `amount` | number | **保留账本正负号**：正=入账，负=扣款。与你在财务明细看到的一致 |
| `direction` | string | `credit`（入账）/ `debit`（扣款）。与 `amount` 的符号冗余，便于不想判符号的接入方 |
| `balance_before` / `balance_after` | number | 该笔前后的账户余额，可用于逐笔对账 |
| `ref_id` | number | 关联单号（订单/扣费记录 id）。内部转账时是对方的用户 id |
| `card_id` / `card_number` | number / string | 仅卡类流水带；由建卡侧结构化写入，非卡类流水**不出现这两个字段** |

常见 `type`（不是封闭枚举，**收到未知类型请按 `amount` 正负记账、不要丢弃**）：

| type | 含义 |
|---|---|
| `recharge` | 链上充值到账 |
| `open_card` | 开卡扣费 |
| `card_recharge` | 卡充值扣费 |
| `refund` | 退款入账（开卡/充值失败、删卡退回等） |
| `decline_fee` / `reversal_fee` / `refund_fee` | 消费失败 / 授权撤销 / 消费退款手续费 |
| `small_tx_fee` / `low_auth_fee` / `merchant_violation_fee` | 小额消费 / 低额授权 / 受限商户处理费 |
| `*_fee_refund` | 上述手续费的退还 |
| `card_overdraft` | 卡透支补收 |
| `transfer_in` / `transfer_out` / `transfer_fee` | 内部转账 |
| `admin` / `admin_penalty` | 人工调整 / 平台核收扣款 |
| `unfreeze` | 冻结释放 |
| `gpt_direct_*` | 直充下单、服务费预扣与释放、CDK 购买 |

#### `card_fee`

手续费扣费的**明细事件**，带上游授权号，便于把这笔费对到具体那笔消费上：

```json
{
  "event": "card_fee",
  "fee_type": "decline_fee",
  "auth_id": "AUTH-20260827-0001",
  "amount": 0.30,
  "currency": "USD",
  "direction": "debit",
  "remark": "消费失败手续费 $0.30 卡 4147000000000000",
  "occurred_at": "2026-08-27 10:15:02"
}
```

> `card_fee` 的 `amount` 是**正数**，方向由 `direction` 表达；
> `balance_change` 的 `amount` 则保留账本符号。两者刻意不同：前者是「收了多少费」，
> 后者是「账本这一行是多少」。

#### ★不要重复计账★

同一笔手续费会**同时**出现在 `balance_change` 和 `card_fee` 里（消费本身还会有
`card_transaction`）。它们是同一件事的不同视角，不是三笔钱。

- 做**账务/对账**：只认 `balance_change`。它挂在账本上，完整且不重不漏。
- 做**业务提醒/归因**：用 `card_fee`（带 `auth_id`，能对到具体消费）
  和 `card_transaction`（消费明细）。

把两条流都累加进余额，会得到双倍甚至三倍的扣款。

#### 投递语义

- **至少一次**：进程在「已投递、未落标记」之间重启会重投。请按 `balance_log_id`
  （或 `card_fee` 的 `auth_id` + `fee_type`）去重。
- 只推送**新产生**的资金变动。功能上线时历史账本不会重推 —— 否则会是一场
  几万条的回调风暴，而你早就对过那些账了。
- 未配置回调地址时不推送，也不会积压：平台照样会把这些流水标记为已处理。
- 签名、重试与超时要求同 7.7 / 7.8。

---

## 附录 A：错误码对照

**一律按 `error_code` 判断，不要匹配 `msg` 文案**——`msg` 是给人看的中文说明，会随版本调整。

### A.1 通用（任意接口都可能返回）

| HTTP | `error_code` | 含义 | 可否重试 |
|---|---|---|---|
| 401 | *（无）* | `缺少 API Key` / `API Key 无效或已禁用` / `账户不存在或已禁用` | 否，先修凭据 |
| 403 | *（无）* | `客服账号不能使用开放 API` | 否 |
| 403 | *（无）* | 该密钥尚未配置 IP 白名单（新签发密钥必须先填服务器出口 IP）| 否，去后台配 |
| 403 | *（无）* | `当前 IP 不在白名单内` | 否，去后台补 IP |
| 403 | `RECHARGE_REQUIRED` | 账户尚无已入账充值，写操作被拦（`data.recharge_required=true`）| 否，先充值 |
| 403 | `FORBIDDEN` | 子账户被冻结，或缺少该操作的权限 | 否 |
| 429 | *（无）* | `调用过于频繁，请稍后再试`（默认 100 次/分钟/密钥）| 是，指数退避 |
| 400 | `invalid_argument` | 参数错误（路径 id 非法、JSON 绑定失败等）| 否 |
| 400 | `not_found` | 对象不存在（卡、产品）| 否 |
| 400 | `insufficient_balance` | 余额不足 / 风险保证金不足 | 否，先充值 |
| 400 | `request_failed` | 业务失败，具体看 `msg` | 视情况 |
| 400 | `forbidden` | 被风控限制 / 无权操作 | 否 |
| 500 | `internal_error` | 服务端错误 | 是，写操作请带同一 `Idempotency-Key` |
| 503 | `channel_unavailable` | 渠道熔断中 | 是，或换 `issuer` |

**幂等相关**

| HTTP | 含义 |
|---|---|
| 400 | `Idempotency-Key 格式错误`（允许 `A-Za-z0-9._:-`，1–80 字符）|
| 409 | `相同 Idempotency-Key 的请求仍在处理中，请稍后重试`（服务端最多等 30 秒）|

> 只有 **2xx** 响应会被幂等缓存；失败不缓存，同一 key 可以再试。

### A.2 开卡 / 卡产品

| HTTP | `error_code` | 含义 |
|---|---|---|
| 403 | `product_exclusive_access_required` | 该卡产品仅向指定用户开放 |
| 403 | `product_approval_required` | 该卡产品需申请并通过审核 |
| 409 | `product_policy_outdated` | 卡产品规则已更新，需重新确认后申请 |
| 503 | `product_unavailable` | 该卡产品暂停开通 |
| 400 | `request_failed` | 卡产品不存在或已下架 / 卡头暂停开新卡 / 渠道维护中 / 会员等级不足 / 开卡费配置无效 |
| 400 | `insufficient_balance` | 可用余额不足（批量开卡会给出「共需 $X，当前可用 $Y」）|
| 400 | `request_failed` | `API 密钥消费额度不足：剩余 $X，本次需要 $Y` / 密钥不存在 / 密钥已停用 |

> **API 密钥有独立的累计消费上限**（与账户余额是两道闸）。上限用尽时即使账户有钱也会被拒。

### A.3 卡操作（充值 / 退款 / 冻结 / 限额 / 删卡）

| HTTP | 含义 |
|---|---|
| 400 | 卡不存在 / 卡状态异常 / 卡已注销 / 删卡确认中 |
| 400 | 该卡有一笔充值正在确认结果（勿重复充值，系统自动对账）|
| 400 | 最低充值金额 $X：渠道 1 固定 $10、渠道 4 取产品最低额；`error_code=amount_below_min`，**参数错误、不可重试**（低于下限不会重试会成功，改金额后再发）|
| 400 | 退款金额需为有效正数 / 超过卡内可用余额 / 退款后余额不得少于 $X |
| 400 | 该卡单笔最低退款 $1.00（渠道 3）|
| 400 | 该卡暂不支持通过接口调整消费限额（**仅渠道 4 支持**）|
| 400 | 请至少提供一个限额字段 / 限额数值无效（范围 0–1e9）|
| 403 | `DELETE_DISABLED`：该账号的 API 删卡功能已关闭 |
| 503 | `channel_unavailable`：渠道熔断 |
| **202** | 退款/删卡已受理，`data.pending=true`，**轮询确认，勿重复提交** |

## 附录 B：接入自检清单

上线前逐项对一遍，这些都是真实踩过的坑：

- [ ] 用 `error_code` 判分支，**没有**在代码里匹配 `msg` 中文文案
- [ ] 写操作都带了 `Idempotency-Key`，重试用**同一个** key
- [ ] HTTP **202** 当「待定」处理，不重复提交；退款/删卡/下单都会遇到
- [ ] `POST /gpt-direct/orders` 之后**轮询订单状态到终态**，没把 202 当成功
- [ ] 直充 `product` 只传 `gpt` / `claude` / `grok`，**自己先校验字面量**
- [ ] Claude / Grok 下单的 `plan` 取自 `registry[].key`（不带产品前缀），不是 `plans` 的键
- [ ] Claude / Grok 下单显式传 `payment_country=US`、`payment_currency=USD`，价读 `registry[].checkout_amount_minor`
- [ ] 服务费、档位、可购买状态**实时读 `/gpt-direct/plans`**，没写死在代码里
- [ ] 「能不能买」用 `registry` 有该档 **且** `purchasable===true` **且** `plans[acc_plan_key].enabled===true`，没有去读不存在的 `purchased`，也没有只看 `enabled`
- [ ] 已在后台配好**服务器出口 IP 白名单**（不是浏览器 IP，也不是内网地址）
- [ ] 知道 API 密钥有**独立的累计消费上限**，与账户余额是两道闸
- [ ] Webhook 用**原始 body 字节**校验 `X-Signature`，并按 `event_id` / `balance_log_id` 去重
- [ ] 对账只认 `balance_change`，没有把 `card_fee` / `card_transaction` 重复累加
- [ ] 卡详情返回**完整卡号与 CVV**，日志里已脱敏

---

如需协助，请在「开发者」页联系客服或加入开发者群。

