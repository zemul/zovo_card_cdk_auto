# ZovoCard · CDK 卡密分销 API 文档

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


## 6. CDK 接口详解

> CDK 有三组互不相同的地址，**不可混用**：卡台所有者接口 `/openapi/v1`（服务端用 `X-API-Key`）；卡台公开兑换 `/api/v1/cdk`（凭完整码/令牌，绑定出口 IP 与设备）；你自托管的 CDK 站（你自己的域名，见 6.20）。沙盒仅用 `https://sandbox.zovocard.com` 与沙盒密钥、测试数据。下列相对路径若未写全，均以 `/openapi/v1` 为 Base。

- 卡台所有者接口：`https://zovocard.com/openapi/v1`，服务端使用 `X-API-Key`。
- 卡台公开兑换：`https://zovocard.com/api/v1/cdk`，凭码/令牌，绑定出口 IP 和设备。
- 自托管 CDK 站：你自己的域名，完整路径见 §6.20。三组地址不可混用。
- 沙盒仅使用 `https://sandbox.zovocard.com`、沙盒密钥与测试数据。

### 6.18.6 发放和查询 CDK

> **50x 套餐参数：`"plan": "pro_50x"`，产品为 `"product": "gpt"`。** 创建直充订单、发放 CDK 都使用这个值；请求字段是 `plan`，不是 `plan_type`。

以下相对路径以 `/openapi/v1` 为 Base，均由接入方服务端使用 API Key 调用。发码时扣服务费；兑换时的卡片注资与订阅实付由 CDK 所有者承担。金额以 `GET /gpt-direct/plans` 的账户实时配置为准，不固定为免费。

`POST /gpt-direct/cdks`：

```json
{"plan":"plus","count":2,"funding_confirmed":true,"payment_country":"PH"}
```

| 字段 | 要求与含义 |
| --- | --- |
| `plan` | 必填。当前 CDK 档位为 `go`、`plus`、`plus_half`、`pro_5x`、`pro_20x`、`pro_50x`、`pro_20x_renew`、`pro_5x_renew`、`credit250`、`credit500`、`credit1000`、`credit2500`、`credit5000`、`credit25000`，还须处于可售状态 |
| `count` | 默认 1，建议传 1–200；单次最多 **200** 张 |
| `funding_confirmed` | 必须为 `true`，确认由所有者承担兑换资金 |
| `payment_country` | 可选，PH / US / JP / CL / EG；以 `/gpt-direct/plans` 的 `payment_regions` 为准，省略默认 PH |
| `payment_currency` | 可省略，由平台根据国家补齐；显式提供时必须与国家匹配 |
| `preferred_issuer` / `preferred_segment_type` / `preferred_segment_key` | 可选的购码展示快照；**不绑定银行卡，也不决定之后兑换时的选卡** |

- 本次发码在同一事务内完成：整批成功或整批回滚，不再逐张部分成功。成功返回 `data.requested`、`data.issued[]`，每项含 `id`、`code`、`code_prefix`、`plan`、`fee_amount_minor`（USD 分）。
- 带唯一 `Idempotency-Key`，网络重试保留原请求和原键；结果不确定先查列表，不能用新键重复购码。
- 保存完整 `code`；所有者列表也会返回库中保存的明文 `code`，极早期只有哈希的码可能无此字段。不要用 `code_prefix` 兑换，不要限制码长度；新码和历史码都支持。
- 当前发码请求**没有** `owner_funding_cap_minor` 可写参数，新购码不设置硬上限。已有码的授权仍按库内配置执行，`preview.funding_cap_minor=0` 表示未设上限。
- `pro_20x_renew` 是续费绑卡档：当前账号须符合 Pro 20x 续费条件，本次绑定不发起订阅扣款，未来账单由所绑卡支付，须留足余额。`pro_5x_renew` 同理，适用于 Pro 5x 账号。点数档需已有有效 Plus/Pro；预检仍会校验账号资格。

| 方法与路径 | 用法与响应 |
| --- | --- |
| `GET /gpt-direct/cdks` | `page`、`page_size`（默认20，最大200）、`status`、`plan`、`q`；返回 `data.list`、`data.total`。可见本 Key 签发的码及官网购买未绑定 App 的码 |
| `POST /gpt-direct/cdks/{id}/disable` | 停用自己账户的未使用码；成功返回 `data.id/status`，不退发码服务费 |
| `POST /gpt-direct/cdks/{id}/enable` | 恢复自己账户可启用的码为 `unused` |
| `POST /gpt-direct/cdks/batch-disable` | `{"ids":[101,102]}`，最多100个；`data.disabled`、`data.failed` 为 ID 数组，另有两个计数字段 |
| `POST /gpt-direct/cdks/batch-enable` | 同上，成功数组/计数改为 `enabled` / `enabled_count` |

批量启停逐项处理，HTTP 200 不代表全部成功；检查 `failed`。列表与订单按 Key/App 可见范围过滤，启停接口按账户所有权校验，不能将同账户不同 Key 当成启停权限隔离边界。

CDK 状态：`unused` 可兑换，`reserved` 已关联进行中的订单，`consumed` 已消耗，`review` 保留待核验，`frozen` 暂时冻结，`disabled` 已停用。

#### 自定义 CDK 品牌前缀

在「我的 CDK → CDK 品牌前缀」保存品牌缩写，或由服务端调用以下接口。账号须已开通 GPT 直充权限，并满足该账号的充值及子账号权限要求；不需要调用支付上游。

| 方法与完整路径 | 用途 |
| --- | --- |
| `GET /openapi/v1/gpt-direct/cdk-branding` | 查询当前账号的品牌前缀及各套餐格式预览 |
| `PUT /openapi/v1/gpt-direct/cdk-branding` | 保存当前账号的品牌前缀 |

两者均使用 `X-API-Key`。PUT 正文示例：

```json
{"brand_prefix":"UUU"}
```

成功返回 `code=0`；`data` 包含 `brand_prefix`、`plan_labels` 和 `examples`。例如 `plan_labels.plus="PLUS"`、`examples.plus="UUUPLUS-XXXXXXXXXX-XXXXXXXXXX"`；X 仅为预览占位，不能兑换。

- 前缀限 1–8 位 ASCII 英文字母或数字，首位须为字母，自动转为大写；不支持空格、连字符或 HTML。传 `{"brand_prefix":""}` 恢复默认；字段不能省略或为 null。格式错误返回 HTTP 400、`error_code=cdk_brand_prefix_invalid`。
- 填 `UUU` 后：Plus 为 `UUUPLUS-…`，Pro 5x 为 `UUU5X-…`，Pro 20x 为 `UUU20X-…`，Pro 50x 为 `UUU50X-…`；续费绑卡档为 `UUU20XRENEW-…`，Go 为 `UUUGO-…`，点数档为 `UUUC250-…` / `UUUC500-…` / `UUUC1000-…`。随机部分仍为两段各 10 位十六进制字符。
- 设置作用于账号之后的网页单张/批量购码、API 发码及管理员给该账号发码。同账号的 API Key 共用，主子账号独立；不接收 `user_id/owner_id/api_key_id` 等代设参数。发码请求仍使用原参数，不要在购码请求中添加 `brand_prefix`。
- 未设置或清空后仍生成原 `ZC-…` 格式。修改只影响新码，已发出的所有码保留原样，继续按原套餐兑换。套餐判断以响应中的 `plan` 为准，勿从前缀推断；`code_prefix` 只是截取片段，不能用于兑换。请保存并原样提交完整 `code`，不要限定固定前缀或长度。
- PUT 可使用现有 `Idempotency-Key`；重试保留原键及正文，修改另一份设置须使用新键。保存设置本身不收费，也不会发码、兑换或退款。

确认付款（包括卡流水纠正为已付款）后，CDK核销为 `consumed`，不可再次兑换、重新启用或删除退费。执行中的单次尝试失败不代表整单失败；订单仍在处理或已提交待确认时，应继续查询原订单，不要重新兑换。

### 6.18.7 CDK 订单对账

- `GET /gpt-direct/cdk-orders?page=1&page_size=20` 返回当前 Key 可见 CDK 的兑换订单（本 Key 签发 + 官网购入未绑定 App）。
- `GET /gpt-direct/cdk-orders/{order_id}` 返回单笔订单与公开时间线 `events`。

列表支持 `updated_after`（RFC3339）、`status`、`cdk_id`、`order_id`、`page` 和 `page_size`（最大 100）。结果包括 CDK 前缀/状态、脱敏账号与卡号、金额、服务费/资金状态、订单阶段和时间；不返回凭据、代理、内部排障详情、完整 CDK 或完整卡号。同一用户的其他 API Key 签发 CDK 的订单不可见。官网购入码的兑换 Webhook 不会推到后建的 Key，请用本接口对账。


CDK订单列表和详情均返回 `renewal_status`（2026-09-25新增），与付款的 `status` 分开判断。`success` 表示系统已记录续费取消成功；`pending/retrying` 表示仍在处理；`warning/manual_required` 需要复查或人工处理；`not_requested` 表示该订单没有要求取消（例如 `pro_20x_renew` 续费绑卡需要保留自动续费）。空值表示未知，不能当作成功。订单 `completed` 后仍可继续只读查询此字段，不要因等待取消结果而重新兑换或重复付款；查询本身不触发新的取消请求。

### 6.18.8 CDK 兑换（公开兑换接口，独立于 API Key）

公开兑换 Base 为 `https://zovocard.com/api/v1/cdk`，无需 API Key，凭完整有效码获取短期兑换会话。四步必须使用同一出口 IP 和同一 `X-Redemption-Device`；未带此头时采用 `User-Agent`。

公开接口限流按 IP：preview 30次/分钟、redeem 20次/分钟、result 60次/分钟；预检另有频率限制，遇429须退避。

**异步受理（2026-09-24）**：自动选卡兑换在订单、CDK预留、预检消耗及查询会话一起落库后返回，选卡、注资和付款由后台继续执行。保持原接口的 HTTP 200、`code=0`、`data.id`，不要求接入方切换为202。新单初始 `status=awaiting_card`、`stage=cdk_accepted`，表示已受理；`card_id` 此时可能缺省/为0，须继续查询结果。进程重启后由持久化订单恢复处理。

网络重试必须保留原 `redemption_token`、`preflight_token`、`client_request_id` 和选卡参数；同一请求返回原订单，变更参数返回409 `IDEMPOTENCY_CONFLICT`。受理成功不等于支付成功，后台发现余额/授权不足时仍会通过订单状态和事件告知。

```json
{"code":0,"msg":"ok","data":{"id":456,"status":"awaiting_card","stage":"cdk_accepted","async_card_selection":true}}
```


| 步骤 | 方法与完整路径 | 请求与结果 |
| --- | --- | --- |
| 1. 预览 | `POST /api/v1/cdk/preview` | `{"code":"<完整CDK>"}` → `data.redemption_token`、`expires_at`、`plan`、`plan_flow`、`funding_cap_minor`；会话15分钟有效 |
| 2. 预检 | `POST /api/v1/cdk/preflight` | `redemption_token` + `credential` → `data.preflight_token`、`preflight_expires_at`、账号资格与报价；使用码上保存的付款地区 |
| 3. 兑换 | `POST /api/v1/cdk/redeem` | `redemption_token` + `preflight_token` + `client_request_id` → `data.id/status/stage`；Grok / Claude 码改传 `credential`（见下） |

> **Grok / Claude 码**：这两个产品的预检只查账号状态、**不返回 `preflight_token`**。兑换时不传 `preflight_token`，改传与预检相同的 `credential`（`{"mode":"session","session":"<grok.com 的 sso cookie 或 Claude sk>"}`）；缺少时返回 `CREDENTIAL_REQUIRED`。GPT / X 码仍必须传 `preflight_token`，缺少时返回 `PREFLIGHT_REQUIRED`。重试须带同一账号，换账号视为改参数（409 `IDEMPOTENCY_CONFLICT`）。开放 API 兑换（`/openapi/v1/gpt-direct/cdks/redeem`）规则相同。发 Grok 码：`plan` 传 `grok_` 前缀档位（如 `grok_premium_monthly`，见 `GET /gpt-direct/plans?product=grok`），须 Grok 全局开关已开且档位可购买。Grok 必须带 `grok_` 前缀：裸 `premium_monthly` 不会按 Grok 发码。
>
> **2026-10-19 Grok 档位改名**（对齐 x.ai Plus / Premium / Super / Ultra）：旧键 `grok_lite_*` / `grok_monthly` / `grok_yearly` / `grok_heavy_*` 仍可传，自动换成 `grok_plus_*` / `grok_premium_*` / `grok_ultra_*`。★但 `grok_plus_monthly` / `grok_plus_yearly` 现在是 $8 的 Grok Plus★；原 $100 档（旧 `grok_plus_*`）已改为 `grok_super_monthly` / `grok_super_yearly`，原来用 `grok_plus_*` 发 $100 码的须改传 `grok_super_*`。

> **Grok 半价 CDK（严格档）**：发码用半价键 `grok_premium_monthly_half` / `grok_super_monthly_half` / `grok_ultra_monthly_half`（仅 Premium / Super / Ultra 月付）。严格语义：兑换时**硬性要求**上游首月折扣存在；若不存在（促销失效、或该档此刻无折扣），兑换在**扣款前失败**（`grok_half_ineligible`），**绝不回落正价**——因此发给顾客的半价 CDK 不会被意外按正价扣款。无需特殊标记，半价 plan key 本身携带该行为。（普通 `grok_*` 码不变：有首月折扣自动享，否则正价。）

| 4. 查结果 | `GET /api/v1/cdk/result?token=<redemption_token>` | `data.order` + `data.events`；同样受原 IP/设备绑定限制 |

预检请求示例：

```json
{"redemption_token":"<preview token>","credential":{"mode":"session","session":"<customer session>"}}
```

第三步不重复传凭据。可选 `exclude_card_ids` 排除本单自动选择的卡，`strict_card_preference=true` 不追加平台后备卡段，`no_auto_card_switch=true` 关闭本单失败后自动换卡。自动选卡读取卡主**当前用卡规则**，没有规则时用平台默认，发码时的 `preferred_*` 不锁定兑换选择。

需要指定原卡时使用 §6.18.8a 的所有者接口；本公开入口收到非零 `card_id` 返回 `403 CARD_SELECTION_REQUIRES_API_KEY`。

订单进入 `queued`、`awaiting_card`、`funding_pending`、`dispatching`、`running`、`requires_action`、`pending`、`plus_paid` 或各类 review 时继续查询，不重新付款。`completed` 表示该套餐流程完成，续费绑卡档不等于下一期账单已扣款。`declined` / `failed_precharge` / `cancelled` 是订单终态，但**不能单凭失败状态推断码已释放或钱已退**；所有者还应核对 `cdk_status`、`funding_hold_status`、`service_fee_status` 和事件。只有 CDK 回到 `unused` 才能再次兑换；有扣款证据或不确定时可能继续保留为 `review`。

公开结果新增 `data.order.card_id`、`card_last_four`；后四位可能为空，不能据此推断失败，也不能用后四位唯一识别卡。API Key 和完整 Session 不得暴露在页面源码或日志中。

### 6.18.8a 兑换时指定已有银行卡（所有者 API）

`POST /openapi/v1/gpt-direct/cdks/redeem` 使用卡主的 API Key 鉴权。与普通直充 `/orders` 不同，此接口兑换已购买的 CDK，不再收取一遍 CDK 服务费。

不传 `card_id` 的所有者兑换也采用上述异步受理；显式指定卡仍先校验该卡并锁定用卡，不会改为自动选择。

先由接入方**服务端**依次调用上文公开 `/api/v1/cdk/preview`、`/preflight`，再在同一出口 IP、同一 `X-Redemption-Device` 下调用本接口。API Key 不得放入终端网页。CDK 必须属于当前账户，且为本 Key 签发或官网购入未绑定 App 的码；不能兑换同账户其它 Key 的码。

```json
{
  "redemption_token": "<preview 返回值>",
  "preflight_token": "<preflight 返回值>",
  "client_request_id": "redeem-example-001",
  "card_id": 123
}
```

- `card_id` 可选，使用平台本地卡 ID；不传或为 0 时保持自动选卡。购码、发码与持有码不绑定卡。
- 指定后锁定本单用卡，强制禁止自动换卡/另开新卡。不能以重试或“改自动开卡”替换这张卡。
- 卡必须属于 CDK 所有者、ACTIVE、有效期正常，且无在途充值/退款/销卡；继续执行使用次数、冷却、归档、渠道及续费独占校验。
- 所选卡余额不足时，按既有注资规则仅为该卡补款，受 CDK 所有者资金授权、额度上限和账户可用余额限制。不会退款其它卡来凑钱，也不会偷偷改用另一张卡。
- 同卡复用不等于自动续费；套餐仍有效等订阅资格限制不变，`pro_20x_renew` 的独占卡限制不放宽。
- 选卡失败时建单事务回滚，不消耗 CDK/预检。资金结果未知时保持待对账，勿重建订单。
- 幂等以必填 `client_request_id` 为准：重试需保留 CDK、卡、预检令牌和选卡参数；改变参数返回 `409 IDEMPOTENCY_CONFLICT`。本接口不使用通用 `Idempotency-Key` 响应缓存。
- 可选 `exclude_card_ids`、`strict_card_preference`、`no_auto_card_switch` 与公开入口语义一致；指定卡若同时在排除列表中会明确拒绝，指定卡不能通过 `no_auto_card_switch=false` 解除锁定。

返回 `data.order`（含 `order_id`、`card_id`、`card_last_four`、状态和资金状态）及 `data.events`。后续可通过 §6.18.7 订单接口查询。公开 `/api/v1/cdk/result` 也返回 `order.card_id` / `order.card_last_four`，不返回完整卡号/CVV。

常见拒绝码：`CARD_NOT_FOUND`、`CARD_NOT_PAYABLE`、`CARD_EXPIRED`、`CARD_DETAILS_UNAVAILABLE`、`CARD_FUNDING_BUSY`、`CARD_IN_USE`、`CARD_CAPACITY_REACHED`、`CARD_CAPACITY_PRO20`、`CARD_RENEWAL_BOUND`、`CARD_COOLING_DOWN`、`CARD_PLAN_UNSUPPORTED`、`CARD_SELECTION_LOCKED`。公开 `/api/v1/cdk/redeem` 收到非零 `card_id` 返回 `403 CARD_SELECTION_REQUIRES_API_KEY`，不会静默忽略。

**参数与接入顺序**

`redemption_token`、`client_request_id` 必填；GPT / X 码另须 `preflight_token`，Grok / Claude 码另须 `credential`；请求编号为 1–80 字节（建议使用 ASCII）。`card_id` 是正整数的**本地卡 ID**，不是完整卡号、卡后四位或上游字符串 ID。可从上次 `cdk-orders/{order_id}` 的 `order.card_id` 保存，也可从本人卡列表取得。

下面四次调用都由接入方服务端完成；把前一步返回的令牌填入后一步，保持出口 IP/设备标识一致。购买或生成 CDK 时不传 `card_id`。

```bash
# Server-side only. Set these variables privately; never ship the key to a browser.
ZOVOCARD_ORIGIN='https://sandbox.zovocard.com'
# Production: https://zovocard.com with a separate production key.
curl --fail-with-body "$ZOVOCARD_ORIGIN/api/v1/cdk/preview" \
  -H 'Content-Type: application/json' -H 'X-Redemption-Device: merchant-session-001' \
  -d '{"code":"<full CDK>"}'

curl --fail-with-body "$ZOVOCARD_ORIGIN/api/v1/cdk/preflight" \
  -H 'Content-Type: application/json' -H 'X-Redemption-Device: merchant-session-001' \
  -d '{"redemption_token":"<preview token>","credential":{"mode":"session","session":"<customer session>"}}'

curl --fail-with-body "$ZOVOCARD_ORIGIN/openapi/v1/gpt-direct/cdks/redeem" \
  -H "X-API-Key: $ZOVOCARD_API_KEY" -H 'Content-Type: application/json' \
  -H 'X-Redemption-Device: merchant-session-001' \
  -d '{"redemption_token":"<preview token>","preflight_token":"<preflight token>","client_request_id":"merchant-redeem-001","card_id":123}'

# Use data.order.order_id returned above.
curl --fail-with-body "$ZOVOCARD_ORIGIN/openapi/v1/gpt-direct/cdk-orders/456" \
  -H "X-API-Key: $ZOVOCARD_API_KEY"
```

成功响应示例（只列主要字段，HTTP 200 表示建单成功，**不是已经充值成功**）：

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "order": {
      "order_id": 456,
      "client_request_id": "merchant-redeem-001",
      "cdk_id": 78,
      "cdk_status": "reserved",
      "status": "queued",
      "card_id": 123,
      "card_last_four": "1234"
    },
    "events": []
  }
}
```

保存 `data.order.order_id`、`card_id`、`card_last_four`，在终态确认实际用卡。`data.order` 不含完整 PAN/CVV。卡不能用时不得自行改成 `card_id=0` 或改走普通直充订单，否则会改变用户的指定卡意图。

| HTTP / error_code | 处理方式 |
| --- | --- |
| 409 `IDEMPOTENCY_CONFLICT` | 请求号已绑定另一组兑换参数；先查原订单，不要用新编号绕过结果不明的请求 |
| 403 `CDK_NOT_ACCESSIBLE` | 检查所有者及签发 Key/App 是否匹配 |
| 400 `REDEMPTION_SESSION_INVALID` | 检查令牌有效期、出口 IP、设备；先确认旧请求未建单，再重新预览/预检 |
| 400 `CDK_UNAVAILABLE` | 码已使用或正在兑换，查询已有订单 |
| 400 `CARD_*` 或 `REDEMPTION_REJECTED` | 按原因处理卡状态、归属、额度、冷却或资金授权；不要自动换卡 |

鉴权/IP白名单/直充权限/子账户权限仍由平台校验。接入方服务端也须核验自己客户的身份及允许复用的卡，不能仅凭浏览器传入的 `card_id` 授权。若旧版服务返回404，先确认平台已升级此接口，不要回退到自动选卡。

### 6.18.8b CDK 项目复用的卡台接口

这些接口均在卡台 `/openapi/v1` 下，使用卡主 API Key。模板的后台管理路由不是卡台开放 API。

| 方法与路径 | 用途 |
| --- | --- |
| `GET /gpt-direct/plans` | 读取账户实时服务费、可售套餐与付款地区（§6.18.2） |
| `GET /balance` | 读取可用余额，优先使用 `spendable_balance` |
| `GET /gpt-direct/cdks` | 按状态/套餐/q同步自己可见的库存完整码（§6.18.6） |
| `GET /gpt-direct/cdk-orders` | 按 `updated_after/status/cdk_id/order_id` 对账，翻完分页（§6.18.7） |
| `GET /gpt-direct/cdk-orders/{order_id}` | 读取实际用卡、资金状态及公开时间线 |
| `GET /gpt-direct/card-products` | 读取可选卡头及 `enabled/suspended/channel_open/auto_open_allowed/usable` |
| `GET /gpt-direct/card-rules` | 读取当前账户用卡规则，可带 `product=gpt` |
| `PUT /gpt-direct/card-rules` | 保存当前账户规则；影响后续相关订单，不是只影响某一张码（§6.18.5a） |
| `GET /gpt-direct/cards/{id}/usage` | 选择原卡前查看用量、占用和冷却；查询不预留卡，兑换时再次校验 |

### 6.20 自托管 CDK 项目的公开查询接口

以下按 `zovo_card_cdk_auto` **v1.4.22** 源码核对，仅属于你部署的 CDK 站，例如 `https://cdk.example.com`。它们**不在** `https://zovocard.com/openapi/v1` 下，也不属于卡台的 `/api/v1/cdk/*` 路由。

目前模板的 `/api/v1/public/cdk/redeem` 仍转发卡台公开兑换，尚未接入所有者指定卡接口。仅在模板请求里加 `card_id` 不会获得指定卡能力；需要接入方服务端按 §6.18.8a 适配。以下查询接口本身无需卡台 API Key，完整 CDK/Session 就是敏感访问凭据。

| 方法与路径 | 用途 |
| --- | --- |
| `GET /api/v1/public/cdk/plans` | 返回顶层 `source/plans/registry/version`；`cardplatform_live` 为实时配置，`docs_default` / `docs_default_fallback` 仅参考，不可当实际售价 |
| `POST /api/v1/public/cdk/preview` | `{code}`；代转卡台预览，并保存本站码与兑换令牌绑定 |
| `POST /api/v1/public/cdk/preflight` | 同卡台预检请求；供本站恢复会话/账单绑定 |
| `POST /api/v1/public/cdk/redeem` | 同卡台公开兑换；本站可能追加自己的排除卡/选卡策略 |
| `GET /api/v1/public/cdk/result` | `?token=`，透传卡台结果；维持原 `X-Redemption-Device` |
| `GET /api/v1/public/cdk/result-by-code` | `?code=`（兼容 `cdk_code`），按本站绑定恢复进度；未绑定404，仍受卡台 IP/设备限制 |
| `GET /api/v1/lookup/cdk` | `?code=`（兼容 `cdk_code`），查本站记录，顶层返回状态摘要；找不到404 |
| `POST /api/v1/lookup/cdk/batch` | `{codes:[...]}` 或 `{text:"多行卡密"}`，去重后最多100张；返回顶层 `total/max/results` |
| `POST /api/v1/public/billing/check` | `{cdk_code}` 查本站绑定的Session，或 `{token_input}` / `{session}` 直接提交凭据；返回顶层 `summary/invoices/auth_source`，可能有 `invoice_url` |

单码摘要字段为 `cdk_code`、`status`、`used`、`can_resubmit`、`message`，以及可选 `account_email/plan/used_at/notes`。常见状态为 `unused/used/failed/disabled/expired/processing/unknown`；这是**本站摘要**，不是扣款对账凭证，不应只凭 `used=false` 判定可以重付。批量HTTP 200也要逐项看结果。资金结果以卡台订单及事件为准。

`result-by-code` 的响应保留卡台结果封装，并在顶层追加 `cdk_code/redemption_token/has_session_binding`，有时会附账单链接；处理时同时检查HTTP 状态和原 `code`，不能看到令牌就认为成功。账单查询要求本站保存了有效Session，邮箱模式或其它站兑换的码不保证能查；无绑定404、无输入或凭据无效400。账单与摘要可能含完整邮箱，兑换结果可能含令牌：只交给有权持有码或Session的用户，不写公共日志。

示例（调用你自己的CDK域名；`CDK_SITE` 由站主提供）：

```bash
curl --get "$CDK_SITE/api/v1/public/cdk/result-by-code" \
  -H 'X-Redemption-Device: merchant-session-001' --data-urlencode 'code=<full CDK>'
curl "$CDK_SITE/api/v1/lookup/cdk/batch" -H 'Content-Type: application/json' \
  -d '{"codes":["<full CDK A>","<full CDK B>"]}'
curl "$CDK_SITE/api/v1/public/billing/check" -H 'Content-Type: application/json' \
  -d '{"cdk_code":"<full CDK>"}'
```

这些接口响应并不统一：兑换代理保留卡台的 `code/msg/data`；查询/账单多为顶层对象，本站错误通常为 `{error:...}`。不要统一只读取 `data`。`/api/v1/admin/*` 需要本站管理员JWT，不能把卡台API Key当作管理员凭据，也不应向终端用户开放。


## 宽限期原订阅处理

预检返回 `subscription_recovery_required=true` 时，可让持码人确认取消宽限期原订阅（可能立即结束宽限期权益），再调用 `POST /api/v1/cdk/recover-subscription`。请求为 `{ "redemption_token": "...", "preflight_token": "...", "confirmed": true }`，沿用同一 `X-Redemption-Device` 和出口IP，无需API Key。

成功响应 `data.status` 为 `cleared` 或 `pending`；`data.preflight`（如有）包含刷新后的账号状态和新 `preflight_token`。旧预检凭证已不可重复使用。只有确认原订阅已解除才显示可再次兑换；处理中或网络错误应重新检测，不能自动重发取消或自动兑换。正常有效订阅不支持此操作。这不是普通关闭自动续费，不消耗CDK、不创建订单。


## Pro 50x：套餐参数与调用示例

50x 的正式套餐键为 **`pro_50x`**。客户调用卡台接口时统一使用 `plan` 字段；`promax` 是订阅状态别名，`chatgptpromax` 是上游标识，两者均不作为本文的建单套餐参数。

| 场景 | 接口 / 字段 | 值或规则 |
| --- | --- | --- |
| 直充建单 | `POST /openapi/v1/gpt-direct/orders` | `"product":"gpt", "plan":"pro_50x"` |
| 发放 CDK | `POST /openapi/v1/gpt-direct/cdks` | `"plan":"pro_50x"` |
| 查询可用套餐 | `GET /openapi/v1/gpt-direct/plans?product=gpt` | 从 `registry` 读取 `key="pro_50x"`、`purchasable` 和 `service_fee_usd_minor`；`plans.pro_50x` 为该档配置 |
| 已有订阅升级 | `POST /openapi/v1/gpt-direct/upgrade-quotes` | 选择 `quotes[]` 中 `plan="pro_50x"` 且 `available=true` 的结果，使用该项新 `preflight_token` 和返回的付款币种建单 |

50x 默认服务费为 **$0.15/单**，已配置专属 0 服务费的账号返回 0；始终以当前账号 `GET /gpt-direct/plans?product=gpt` 返回的费率为准。上游订阅实付、开卡/充值手续费及可能适用的 cs_live 附加费另计。

**免费账号首次开通 50x**：先按同一地区预检，再向 `POST /openapi/v1/gpt-direct/orders` 提交。示例中的 `card_id` 须替换为当前 API 账号可用的卡，凭据及预检令牌也须替换为自己的。

```json
{
  "product": "gpt",
  "plan": "pro_50x",
  "card_id": 123,
  "credential": {
    "mode": "session",
    "session": "<YOUR_SESSION>"
  },
  "preflight_token": "<PREFLIGHT_TOKEN>",
  "payment_country": "PH",
  "payment_currency": "PHP",
  "client_request_id": "merchant-pro50x-20260930-001"
}
```

请求头使用 `X-API-Key`、`Content-Type: application/json` 和唯一 `Idempotency-Key`。网络重试保留原幂等键与 `client_request_id`；HTTP 202 仅表示受理，继续查询该订单。

**发放 50x CDK**：`POST /openapi/v1/gpt-direct/cdks`。`funding_confirmed=true` 表示码主承担兑换资金；兑换时套餐从 CDK 读取，不要把一张其他套餐的码在兑换请求里改成 50x。

```json
{
  "plan": "pro_50x",
  "count": 1,
  "funding_confirmed": true,
  "payment_country": "PH",
  "payment_currency": "PHP"
}
```

已有 Go/Plus/5x/20x 订阅需要升 50x 时，使用 `POST /openapi/v1/gpt-direct/upgrade-quotes` 的 `pro_50x` 报价凭证。升级差价由该账号实时账单决定，不能用两个套餐标价相减代替。


PH采集的50x免税价为PHP 29,008.93（2,900,893最小单位），含税展示价PHP 32,490。真实扣款以账号账单为准，服务费及开关读取套餐注册表。50x升级/非PH注资使用独立上限（默认USD 1,000，可配置），其他套餐原有上限不变。


## 附录 A：CDK 错误码对照

按 `error_code` 判断，不要匹配 `msg` 文案。限流一律 HTTP 429，须退避重试。

| HTTP / error_code | 含义与处理 |
| --- | --- |
| 400 `cdk_brand_prefix_invalid` | 品牌前缀格式非法（1–8 位字母数字、首位字母）。改正后重试 |
| 409 `IDEMPOTENCY_CONFLICT` | `client_request_id` 已绑定另一组兑换参数；先查原订单，勿用新编号绕过结果不明的请求 |
| 403 `CDK_NOT_ACCESSIBLE` | 码不属于当前账户，或不是本 Key 签发 / 官网购入未绑定 App 的码 |
| 400 `REDEMPTION_SESSION_INVALID` | 令牌过期、出口 IP 或设备不符；先确认旧请求未建单，再重新预览/预检 |
| 400 `CDK_UNAVAILABLE` | 码已使用或正在兑换，查询已有订单，不要重兑 |
| 403 `CARD_SELECTION_REQUIRES_API_KEY` | 公开兑换入口传了非零 `card_id`；指定卡请走所有者接口（6.18.8a）|
| 400 `CARD_*` / `REDEMPTION_REJECTED` | 卡状态、归属、额度、冷却或资金授权问题；不要自动换卡 |

> 失败终态（`declined` / `failed_precharge` / `cancelled`）**不能单独**推断码已释放或钱已退：还要核对 `cdk_status`、`funding_hold_status`、`service_fee_status` 和事件。只有 CDK 回到 `unused` 才能再次兑换。

## 附录 B：接入自检清单

- [ ] 发码、对账、指定卡兑换都在**服务端**用 `X-API-Key`，密钥绝不下发到浏览器或日志
- [ ] 保存完整 `code`，不用 `code_prefix` 兑换，不限制码长度
- [ ] 公开兑换四步（preview → preflight → redeem → result）用**同一出口 IP + 同一 `X-Redemption-Device`**
- [ ] 幂等：发码带 `Idempotency-Key`，指定卡兑换用 `client_request_id`；重试保留原键与原参数，改参数返回 409
- [ ] HTTP 200 / 受理 ≠ 支付成功；按订单 `status` + `cdk_status` + 事件对账，不重复兑换/付款
- [ ] 套餐以响应里的 `plan` 为准，不从前缀推断；`pro_50x` 的套餐键是 `pro_50x`
- [ ] 官网购入码的兑换 Webhook 不会推到后建的 Key，用 `GET /gpt-direct/cdk-orders` 对账
