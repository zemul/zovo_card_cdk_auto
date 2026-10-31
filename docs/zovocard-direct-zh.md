# ZovoCard 直充 API 文档

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


> 本文档仅含直充（GPT / Claude / Grok）与 CDK 相关接口。账户、充值与卡片接口见「卡片操作 API」文档；鉴权、幂等、响应约定与回调签名两份文档通用。

### 6.18 GPT 直充 API

> **50x 套餐参数：`"plan": "pro_50x"`，产品为 `"product": "gpt"`。** 创建直充订单、发放 CDK 都使用这个值；请求字段是 `plan`，不是 `plan_type`。

GPT 直充接口使用你名下的卡为 GPT 账号开通或升级套餐。它与卡开通、卡充值是两条独立链路：

- **商城直付**：按当前账号的套餐服务费配置计费，专属价优先。
- **开放 API 订单**：按套餐收取 API 服务费，创建订单时冻结，成功后结算；确定未扣款的终态会释放。
- **CDK**：购买或通过开放 API 发放 CDK 时预付服务费；CDK 兑换后的上游开卡、充值、订阅实付资金由 CDK 所有者承担。
- GPT 上游订阅金额不是固定美元价。先调用预检接口取得当前地区、币种和账号状态下的实时报价。


#### 6.18.0 多产品直冲：GPT / Claude / Grok

直冲不止 GPT。`plans`、`preflight`、`orders` 三个接口都接受 `product` 参数：

| `product` | 产品 | 凭据 | 付款币种 | 定价方式 |
| --- | --- | --- | --- | --- |
| `gpt`（缺省） | ChatGPT | Session / Access Token / 邮箱 | PHP | 随卡 BIN 波动，**以预检报价为准** |
| `claude` | Claude | `sk-ant-…`（sessionKey） | USD | **恒价**，见下表 |
| `grok` | Grok（x.ai） | grok.com 的 **`sso` cookie** | USD | **恒价**，见下表 |

> **`product` 不传按 `gpt` 处理**，老调用方无需改动。

**Grok 凭证从哪来（不是账号密码，也不是 X 绑定）**

Grok 直冲要的是 **grok.com 登录后的 `sso` cookie**，通常是以 `eyJ` 开头的 JWT。
它**不是** x.ai 账号密码、**不是** Grok Ultra 页上「关联 X 账户领取 Premium+」那一步、
也**不是** ChatGPT Session / Claude `sk-ant-…`。

1. 浏览器打开 [https://grok.com](https://grok.com) 并登录。
2. 按 F12 → Application（或「存储」）→ Cookies → `grok.com`。
3. 复制名为 **`sso`** 的值（不要 `cf_clearance`、不要 `x-userid`）。

下单 / 预检都这样传：

```json
"credential": { "mode": "session", "session": "eyJ..." }
```

也可以把整段含 `sso=` 的 Cookie 贴进 `credential.session`。
Grok 的预检只校验登录态、查当前订阅，**不产预检票**；创建订单时仍须带上这份 `sso`。

**Grok 首月优惠报价**：x.ai 对部分档位有首月折扣（如首月 5 折）。Grok 预检会一次返回全部档位的报价 `quotes[<plan>]`，
默认按美国 / 美元（`US` / `USD`）计价：

| 字段 | 含义 |
| --- | --- |
| `amountMinor` | **首月实扣**（已含折扣，美分） |
| `listAmountMinor` | 目录原价（次月起的续费价，美分）；无折扣时与 `amountMinor` 相同 |
| `discounted` | `true` 表示该档首月有折扣（`listAmountMinor > amountMinor`） |

展示时可写成「~~原价~~ → 首月价」。折扣由 x.ai 按账号、地区决定，以预检返回为准；平台服务费不随折扣变化。

预检响应同时给出 `promotions[<plan>]`（`kind: "first_month_discount"`）：`eligible` 为该档首月是否有折扣，`amount_minor` / `list_amount_minor` 为首月价 / 原价，`discount_percent` 为折扣百分比。与 GPT Plus 半价用同一个字段，便于统一判断。

**套餐键与服务费**（服务费为平台按次收取，与上游订阅价分开；档位名对齐 x.ai「管理你的订阅」个人页）：

| `product` | `plan` | 官方套餐名 | 上游订阅价 | API / CDK 服务费 |
| --- | --- | --- | ---: | ---: |
| `claude` | `pro` | Claude Pro | $20 | **2 U** |
| `claude` | `max_5x` | Claude Max 5x | $100 | **8 U** |
| `claude` | `max_20x` | Claude Max 20x | $200 | **10 U** |
| `grok` | `plus_monthly` / `plus_yearly` | Grok Plus | $8 / $80 | **1 U** |
| `grok` | `premium_monthly` / `premium_yearly` | Grok Premium | $30 / $300 | **1 U** |
| `grok` | `super_monthly` / `super_yearly` | Grok Super | $100 / $1,000 | **1 U** |
| `grok` | `ultra_monthly` / `ultra_yearly` | Grok Ultra | $200 / $2,000 | **1 U** |
| `grok` | **`premium_monthly_half`** / **`super_monthly_half`** / **`ultra_monthly_half`** | **Grok 专属半价（严格档）** | 约首月半价（上游首月折扣） | **1 U** |

> **Grok 专属半价档（严格档，`*_monthly_half`）**
> - 交付与基础档相同的订阅（Premium / Super / Ultra 月付），仅首月半价（第 2 个月起按原价续费）。
> - ★严格★：扣款/兑换时**硬性要求**上游首月折扣存在；若不存在（促销失效、或该档此刻无折扣），订单在**扣款前失败**（`failed_precharge`，错误码 `grok_half_ineligible`），**绝不回落正价**——因此发给顾客的半价 CDK 不会被意外按正价扣款。
> - 普通（非半价）grok 档行为不变、灵活：有首月折扣自动享，没有则按正价。
> - 发半价 CDK 用半价键（如 `grok_premium_monthly_half`），无需特殊标记——plan key 本身携带严格语义；账号无资格则兑换时拦截、不扣款。
> - 目前仅 Premium / Super / Ultra 的**月付**有半价键（Plus 无折扣；年付无首月折扣）。

> **2026-10-19 Grok 档位改名**：对齐 x.ai 现行 Plus / Premium / Super / Ultra 四档。旧键 `lite_*` / `monthly` / `yearly` / `heavy_*`
> 仍可传，服务端自动换成 `plus_*` / `premium_*` / `ultra_*`。★但 `plus_monthly` / `plus_yearly` 现在是 $8 的 Grok Plus★；
> 原 $100 那档（旧 `plus_*`）已改为 `super_monthly` / `super_yearly`，原来用 `plus_*` 买 $100 档的调用方必须改传 `super_*`。
> CDK 发码时 Grok 一律带 `grok_` 前缀（如 `grok_premium_monthly`），裸 `premium_monthly` 不会被当成 Grok。

> **以 `GET /gpt-direct/plans?product=…` 的实时返回为准**：上表是撰写时的配置，
> 管理员随时可改价或停用某一档。Claude / Grok 的价读 `registry[].checkout_amount_minor`
> （美元分），不要只看 GPT 那套 `plans.expectedAmountMinor`，也不用等预检报价。

**每个产品都有独立的全局开关**，关闭时下单返回 `DIRECT_PRODUCT_DISABLED`。
接入前请先用 `GET /gpt-direct/plans?product=…` 确认该产品有档位返回。
（撰写时 GPT / Claude / Grok 三者均已开启。）

#### ★四个必须避开的坑★

以下都是真实行为，接入前务必看完——它们不报错，但会让你拿到错的结果。

**坑 1：`product` 拼错不会报错，会被当成 `gpt` 下单。**

创建订单时未知的 `product` 会被**静默归一化成 `gpt`**。也就是说把 `claude` 写成
`cluade`，你会开出一张 **GPT 订单并真实扣款**，而不是收到参数错误。

> ★务必在你自己这边先校验 `product`★，只允许 `gpt` / `claude` / `grok` 三个字面量。
> 不要依赖服务端拦你。

同理，`GET /gpt-direct/plans?product=<未知值>` 会返回**全部三个产品的所有档位**
（且 `registry` 为空数组），而不是报错。看到 `plans` 里同时出现
`plus` 和 `claude_pro`、`grok_premium_monthly`，说明你的 `product` 没生效。

**坑 2：`plans` 的键和 `registry[].key` 不一样——只有 GPT 一致。**

| `product` | `plans` 里的键 | `registry[].key` | 下单时 `plan` 该传哪个 |
| --- | --- | --- | --- |
| `gpt` | `plus` | `plus` | `plus`（两者相同） |
| `claude` | `claude_pro` | `pro` | **`pro`** |
| `grok` | `grok_plus_monthly` | `plus_monthly` | **`plus_monthly`** |

Claude / Grok 的 `plans` 键带产品前缀，`registry` 里不带。
**下单的 `plan` 取 `registry[].key`**（即不带前缀的那个）。
按 GPT 摸出的规律直接用 `plans` 的键去下 Claude，会得到「未知套餐」。

> **CDK 发码的 Grok `plan` 必须带 `grok_` 前缀**（如 `grok_premium_monthly`），与上面「Grok 档位改名」星标一致——上面这张表的「下单 `plan`」是针对*下单*、且裸键只对 Grok 独有档（`plus_monthly` 等）成立；裸 `premium_monthly` 会被判成 X Premium（暂未开放）。**发 CDK 时统一用 `grok_` 前缀最稳。** `GET /gpt-direct/cdk-branding` 现返回含 Grok 在内的各档位标签与码形示例，以它为准。

**坑 3：Claude / Grok 是美元恒价，不要套 GPT 的菲律宾默认。**

网站 Claude 固定走 `US/USD`。开放 API 不传地区时，GPT 默认 `PH/PHP`。
把这套默认套到 Claude / Grok 上，服务端会拿美元恒价去对菲律宾比索，返回
「定价档缺失或币种不符」——看起来像「没有配置价格」，价其实已配好。

- 下单必须带 `"product": "claude"`（或 `grok`），并加 `?product=…` 取档位。
- `plan` 用 `registry[].key`（如 `max_5x`），不要用 `plans` 里带前缀的键。
- 价读 `registry[].checkout_amount_minor`（美元分）。
- **请显式传** `"payment_country":"US","payment_currency":"USD"`。
  新版本不传也会落到美区；旧版本不传会落到菲律宾并报没配价。
- `GET /gpt-direct/plans` 的 `payment_regions` 主要给商城前端；GPT 不传地区仍默认 `PH/PHP`。

**坑 4：`enabled` / `purchasable` / 在不在 `registry` 是三个开关，不是同一个状态。**

没有 `purchased` 字段。文档和响应里的名字是 **`purchasable`**。
`plans[<key>].enabled === true` 只表示 ACC 认这个档，**不等于现在能买、能发码**。

| 字段 | 谁管 | 含义 |
| --- | --- | --- |
| 该档是否出现在 `registry` | 卡台上架 | 没上架的档不会出现在 `registry` |
| `registry[].purchasable` | 卡台注册表 | **现在能不能买**。`false` = 仍展示，灰成「即将上线」，下单/发 CDK 会被拒 |
| `plans[<acc_plan_key>].enabled` | ACC 执行层 | 兑换/履约时上游认不认。对 `plans` 要用 `registry[].acc_plan_key`（Claude / Grok 带前缀） |

**能卖当且仅当三条同时成立：**

```text
registry 里有这一档
AND registry[].purchasable === true
AND plans[registry[].acc_plan_key].enabled === true
```

`enabled=true` 且 `purchasable=false`（例如部分即将上线的档）= 上架了、ACC 也开着，卡台还没放卖。不要发码。

```bash
# 取 Claude 档位
curl -H "X-API-Key: sk_你的密钥" \
  "https://zovocard.com/openapi/v1/gpt-direct/plans?product=claude"

# 下一笔 Claude Max 5x
curl -X POST https://zovocard.com/openapi/v1/gpt-direct/orders \
  -H "X-API-Key: sk_你的密钥" -H "Content-Type: application/json" \
  -d '{
    "product": "claude",
    "plan": "max_5x",
    "payment_country": "US",
    "payment_currency": "USD",
    "card_id": 12345,
    "credential": {"mode": "session", "session": "sk-ant-sid01-..."},
    "client_request_id": "your-unique-id"
  }'
```

```bash
# 取 Grok 档位（plan 用 registry[].key，例如 premium_monthly = Grok Premium 月付）
curl -H "X-API-Key: sk_你的密钥" \
  "https://zovocard.com/openapi/v1/gpt-direct/plans?product=grok"

# 下一笔 Grok Premium 月付。session 填 grok.com 的 sso cookie
curl -X POST https://zovocard.com/openapi/v1/gpt-direct/orders \
  -H "X-API-Key: sk_你的密钥" -H "Content-Type: application/json" \
  -d '{
    "product": "grok",
    "plan": "premium_monthly",
    "payment_country": "US",
    "payment_currency": "USD",
    "card_id": 12345,
    "credential": {"mode": "session", "session": "eyJ..."},
    "client_request_id": "your-unique-id"
  }'
```

#### 6.18.0.1 自带卡（`raw_card`）——★仅开放 API 可用★

用你自己的卡直接支付，跳过平台卡的选取、余额判断与注资（卡自带余额）。

> **商城界面不提供这个功能**，只能通过开放 API 使用。

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `raw_card` | string | `卡号\|月\|年\|CVV`，例 `4111111111111111\|12\|30\|123`。年可写两位或四位 |

- `card_id` 与 `raw_card` **二选一**：必须给其一，且**不能同时提供**（同时给会返回
  `INVALID_REQUEST`——「刷哪张卡」不允许有歧义）。
- 需要平台开启该能力，否则返回 `403 EXTERNAL_CARD_DISABLED`。
- **按次收服务费**，从卡台余额扣：GPT `0.3 U` / Claude `10 U`（以平台配置为准）。
  某产品费率为 0 表示**该产品不开放自带卡**，不是免费。
- 自带卡的订单不注资、不占用单卡直充次数额度；卡上余额不足由上游直接拒付。

```bash
curl -X POST https://zovocard.com/openapi/v1/gpt-direct/orders \
  -H "X-API-Key: sk_你的密钥" -H "Content-Type: application/json" \
  -d '{
    "product": "gpt",
    "plan": "plus",
    "raw_card": "4111111111111111|12|30|123",
    "credential": {"mode": "session", "session": "你的_SESSION"},
    "client_request_id": "your-unique-id"
  }'
```

#### 6.18.1 权限与服务费

开放 API 还需要有效的 `app_secret`。账户必须是正常的主账户、没有卡片操作限制；除管理员外，还必须已有一笔已入账充值，并满足以下任一条件：

1. 管理员手工开通 GPT 直充；
2. 管理员账户；
3. 超级 SVIP、至尊 SVIP 或传奇 SVIP。

VIP 达标后自动开放，不需要管理员再写入 `gpt_direct_enabled`。子账户可使用自己的有效 API Key 调用 GPT 直充：主账户须符合上述资格且已有入账充值，子账户及主账户均正常、未限制卡片操作，主子关系有效且主账户已授予 `gpt_direct` 权限。无需再给子账户单独开启 `gpt_direct_enabled`；撤销委托或冻结主子关系后立即拒绝。服务或上游不可用时，权限仍可显示，但写操作会返回相应错误。

子账户单张及批量开卡分别使用 `POST /cards/open`、`POST /cards/batch-open`，另需 `open_card` 权限，与 `gpt_direct` 独立。API 始终保留密钥所属子账户身份，卡片和订单归子账户，常规开卡及直充从其已分配余额扣款，不因主账户余额充足而自动透支。不得将主账户 API Key 交给子账户替代授权。

| `plan` | 套餐 | API / CDK 服务费 | 上游订阅价格 |
| --- | --- | ---: | --- |
| `go` | Go（ChatGPT Go）| **0.15 U** | 以预检报价为准 |
| `plus` | Plus | **0.15 U** | 以预检报价为准 |
| `plus_half` | **Plus 半价**（OpenAI 半价促销） | **0.15 U**（部分账户 0，以接口为准） | 约为 Plus 的一半，以 OpenAI 结账实扣为准 |
| `pro_5x` | Pro 5x | **0.15 U** | 以预检报价为准 |
| `pro_20x` | Pro 20x | **0.15 U** | 以预检报价为准 |
| `pro_50x` | **Pro 50x** | **0.15 U**（专属价以接口为准） | 以预检或升级报价为准 |
| `pro_20x_renew` | Pro 20x 续费（绑卡档）| **0.15 U** | **本单不扣款**，见下方说明 |
| `pro_5x_renew` | Pro 5x 续费（绑卡档）| **0.15 U** | **本单不扣款**，见下方说明 |
| `credit250` | Codex 点数 250 | **0.15 U** | ₱565（按实时汇率折美元，约 $8–$13） |
| `credit500` | Codex 点数 500 | **0.15 U** | ₱1,130（约 $16–$26） |
| `credit1000` | Codex 点数 1000 | **0.15 U** | ₱2,260（约 $32–$51） |
| `credit2500` | Codex 点数 2500 | **0.15 U** | ₱5,650（约 $81–$130） |
| `credit5000` | Codex 点数 5000 | **0.15 U** | ₱11,300（约 $161–$260） |
| `credit25000` | Codex 点数 25000 | **0.15 U** | ₱56,500（约 $807–$1,300） |

> **Pro 20x 续费（`pro_20x_renew`）——绑卡档，与其它档语义完全不同**
>
> **`pro_5x_renew`（Pro 5x 续费）与本档完全相同**，只是账号须在 Pro 5x 上（注资按 Pro 5x 价）。
> - 它卖的**不是开通**，而是「给已在 Pro 20x 的账号换一张有钱的卡并设为默认卡」。
>   OpenAI 到点会自己从默认卡扣款，**本单不产生任何上游扣款**，
>   所以没有预检报价、也不做付款金额校验；平台只收 API / CDK 服务费。
> - **目标账号必须当前就在 Pro 20x**。不满足会在**预检阶段**直接拒绝、不建单：
>   - `GPT_RENEW_TARGET_NOT_PRO` —— 账号不在 Pro 20x（Pro 5x / Plus / Free 等）
>   - `GPT_RENEW_SUBSCRIPTION_EXPIRED` —— 曾是 Pro 20x 但订阅已到期，属于「重新订阅」
>
>   这两个都是**确定性失败**：档位不对，换张卡重试也没用，请直接改走新开/升级档。
> - 开出的卡会被该续费单**独占**，不接受其它订单复用。
> - 订单成功只代表「卡已绑定并设为默认」，**不代表已经续上**——真正的扣款发生在
>   OpenAI 自己的续费周期里。请确保卡上余额足够届时支付 Pro 账单。
> - **不涉及国家定价**：续费本单不扣款，`payment_country` / `payment_currency` 对它无意义；付款地区只影响新开、升级与点数档的报价。

> **Plus 半价（`plus_half`）**
> - 交付的就是 ChatGPT Plus，只是结账时挂上 OpenAI 给该账号的★半价促销活动★，实扣约为 Plus 的一半。
> - ★仅限有半价资格的账号★：资格由 OpenAI 按账号下发（`accounts/check` 的 `eligible_promo_campaigns`）。
>   无资格时★不会回落成全价扣款★，订单在提交扣款前失败（`failed_precharge`，错误码 `half_price_ineligible`），
>   卡上不产生扣款、冻结的服务费释放；CDK 兑换则退回该码，可换号再兑。
> - **资格查询（不建单、不扣款、不冻结服务费）**：直接调预检 `POST /gpt-direct/preflight`（带 `plan: "plus_half"`），
>   看响应里的 `promotions.plus_half.eligible`：`true` 有资格；`false` 无资格（此时 `blocking_reason` 非空，不要下单）；
>   `null` 表示上游这次没给出结论，可重试预检，下单后仍由扣款前硬门把关。判据与扣款前硬门同源。
> - `quotes` 里★没有★ `plus_half`，这是正常的：`quotes` 只放 OpenAI 目录价，半价只在结账时由活动生效。
>   半价参考价看 `promotions.plus_half.amount_minor`（`amount_estimated: true`，按折扣估算），实扣以 OpenAI 结账为准；
>   使用手动卡时，卡上余额请按 Plus 原价（`promotions.plus_half.list_amount_minor` 或 `quotes.plus`）准备，多余部分仍在卡上。
>   ★无资格绝不转全价★：不要用 `quotes.plus` 冒充半价下单——提交 `plus_half` 时，结账挂不上半价活动就在扣款前失败。
> - 账号已是 Plus 及以上时与 `plus` 一样返回 `GPT_PLAN_ALREADY_ACTIVE`，不会重复开通。
> - 2026-10-20 修复：此前 `plus_half` 订单在执行层被按全价 Plus 结账，现已按半价链执行。

> **Codex 点数（credit250 / credit500 / credit1000 / credit2500 / credit5000 / credit25000）**
> - 点数是订阅的★加购项★：目标账号必须已有生效中的 Plus/Pro 订阅，否则下单会被拒
>   （`GPT_CREDIT_REQUIRES_SUBSCRIPTION`）——免费号买了也用不了。
> - 买点数★不改变账号套餐★，同一账号可以反复购买，不会被「套餐仍在有效期内」拦下。
> - 付款币种是 PHP、卡上按 USD 结算。卡台按**当日真实汇率**（每日抓取，来源 frankfurter）
>   下浮一档安全边际（默认 12%）给卡注资，向上取整，以避免汇率波动导致拒付；
>   多注的部分随删卡退回。例：汇率 61.70 时按 54.29 折算，₱565 实付约 $9.16、注资 $10.41。
>   PHP 需在当日内跌超过该边际才可能不够付。
> - 点数订单不占用单卡直充次数额度。

除上游订阅实付（以预检 `quoted_amount` 报价为准）之外，平台**按次收取 API / CDK 服务费**。
服务费在价格配置里以**美元最小单位**返回（`serviceFeeUsdMinor`），例如 `15` 表示 `$0.15`。

> ★不要把上表的数字写死到代码里★：管理员随时可改价或停用某一档。
> **一律以 `GET /gpt-direct/plans` 的实时返回为准**。服务费读 `registry[].service_fee_usd_minor`
> （或 GPT 的 `plans[<key>].serviceFeeUsdMinor`）。能不能买见上面**坑 4**，
> 不要只看 `plans[<key>].enabled`，也没有 `purchased` 字段。
> 价格配置版本（`version`）变化后，创建订单应重新预检并带最新 `pricing_version`。

#### 6.18.2 获取套餐和实时配置

`GET /gpt-direct/plans`（可选 `?product=gpt|claude|grok`）

**响应**（节选；完整响应还带 `payment_regions` 等。判断能不能买见坑 4）
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "version": 214,
    "plans": {
      "plus": {
        "key": "plus",
        "label": "Plus",
        "currency": "PHP",
        "enabled": true,
        "serviceFeeUsdMinor": 15,
        "expectedAmountMinor": 98214,
        "minAmountMinor": 90000,
        "maxAmountMinor": 110000
      },
      "pro_50x": {
        "key": "pro_50x",
        "label": "Pro 50x",
        "currency": "PHP",
        "enabled": true,
        "serviceFeeUsdMinor": 15,
        "expectedAmountMinor": 2900893
      }
    },
    "registry": [
      {
        "key": "plus",
        "product": "gpt",
        "acc_plan_key": "plus",
        "label": "Plus",
        "funding": "bin_snapshot",
        "checkout_currency": "PHP",
        "checkout_amount_minor": 98214,
        "service_fee_usd_minor": 15,
        "purchasable": true,
        "is_credit": false,
        "requires_active_subscription": false,
        "tier": 1,
        "sort_order": 20
      },
      {
        "key": "pro_50x",
        "product": "gpt",
        "acc_plan_key": "pro_50x",
        "label": "Pro 50x",
        "checkout_currency": "PHP",
        "checkout_amount_minor": 2900893,
        "service_fee_usd_minor": 15,
        "purchasable": true,
        "is_credit": false,
        "tier": 5
      }
    ]
  }
}
```

没有 `registry[].purchased`。可购买字段是 **`purchasable`**。
`plans` 里可能同时有 `enabled=true` 的档，但对应 `registry` 行 `purchasable=false`——那一档现在不能买。

`expectedAmountMinor`、`minAmountMinor`、`maxAmountMinor` 是配置参考范围，不是订单最终扣款；订单以预检返回的 `quotes[plan]` 为准。

**支持的付款地区（`payment_regions`）**

直充订单可在下单时指定付款地区/币种；CDK 则在发码时保存地区，预检与兑换沿用该地区，兑换请求不临时改区。不传时 GPT 默认 `PH/PHP`。当前开放 5 个地区：

| 地区 | 币种 | 定价口径 |
| --- | --- | --- |
| `PH` 菲律宾（默认） | PHP | 主渠道，走卡段成交价快照 |
| `US` 美国 | USD | 按所选套餐的当地币种报价（含50x），以预检/升级返回的金额、币种和最小单位为准 |
| `JP` 日本 | JPY | 按所选套餐的当地币种报价（含50x），以预检/升级返回的金额、币种和最小单位为准 |
| `CL` 智利 | CLP | 按所选套餐的当地币种报价（含50x），以预检/升级返回的金额、币种和最小单位为准 |
| `EG` 埃及 | EGP | 按所选套餐的当地币种报价（含50x），以预检/升级返回的金额、币种和最小单位为准 |

> ★以接口下发为准★：可用地区清单实时读 `GET /gpt-direct/plans` 的 `payment_regions`，别在自己这边写死；下单/发码传 `payment_country` + `payment_currency`（Claude / Grok 为美元恒价，见坑 3）。

#### 6.18.3 凭据预检

`POST /gpt-direct/preflight`

预检会验证 GPT 凭据、查询当前套餐和支付方式，并取得实时报价。预检凭证有效期 **10 分钟且只能消费一次**。

**请求参数**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `credential.mode` | string | 是 | `session` / `access_token` / `mailbox` |
| `credential.session` | string | session 模式 | GPT：`__Secure-next-auth.session-token` 或含 `sessionToken` 的 JSON；Claude：`sk-ant-…`；**Grok：grok.com 的 `sso` cookie**（`eyJ…` 或含 `sso=` 的整段 Cookie） |
| `credential.accessToken` | string | access_token 模式 | OpenAI JWT Access Token（`Authorization: Bearer`）；寿命通常短于 Session |
| `credential.email` | string | mailbox 模式 | 邮箱地址 |
| `credential.password` | string | mailbox 模式 | 邮箱密码 |
| `payment_country` | string | 否 | GPT 默认 `PH`；Claude / Grok 请传 `US` |
| `payment_currency` | string | 否 | GPT 默认 `PHP`；Claude / Grok 请传 `USD` |

```bash
curl -X POST https://zovocard.com/openapi/v1/gpt-direct/preflight \
  -H "X-API-Key: sk_你的密钥" \
  -H "Content-Type: application/json" \
  -d '{
    "credential": {"mode":"session","session":"你的_SESSION"},
    "payment_country":"PH",
    "payment_currency":"PHP"
  }'
```

**响应关键字段**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "email": "user@example.com",
    "currentPlan": "free",
    "credentialMode": "session",
    "preflight_token": "一次性预检令牌",
    "preflight_expires_at": "2026-06-04T02:10:00Z",
    "payment_country": "PH",
    "payment_currency": "PHP",
    "quotes": {
      "plus": {
        "country": "PH",
        "currency": "PHP",
        "plan": "plus",
        "amountMinor": 98214,
        "amountMajor": 982.14,
        "minorUnitExponent": 2,
        "fetchedAt": 1780538400000
      }
    },
    "quote_error": "",
    "blocking_reason": "",
    "promotions": {
      "plus_half": {
        "kind": "half_price",
        "eligible": true,
        "discount_percent": 50,
        "amount_minor": 49107,
        "list_amount_minor": 98214,
        "currency": "PHP",
        "amount_estimated": true
      }
    }
  }
}
```

`quote_error` 非空时不要创建对应套餐订单。Session、邮箱密码等敏感凭据不会在订单响应中返回；服务端只保存短期加密副本。

**促销资格 `promotions`**（只读判断，不建单、不扣款；★不参与注资★）

| 字段 | 含义 |
| --- | --- |
| `kind` | `half_price`（GPT Plus 半价）/ `first_month_discount`（Grok 首月优惠） |
| `eligible` | `true` 有资格 / `false` 无资格 / `null` 上游未给出结论（不等于无资格） |
| `discount_percent` | 折扣百分比，`50` 即五折；上游未给出时省略 |
| `amount_minor` | 促销价（首期实付），付款币最小单位 |
| `list_amount_minor` | 原价（Plus 全价 / Grok 次月起续费价） |
| `amount_estimated` | `true` 表示 `amount_minor` 按折扣估算，实扣以上游结账为准（Plus 半价） |

GPT 的 `plus_half` 预检判出无资格时 `blocking_reason` 非空，此时不要创建 `plus_half` 订单。

#### 6.18.4 创建直充订单

`POST /gpt-direct/orders`

建议先预检，再创建订单。开放 API 当前使用手动选择的 `card_id`，该卡必须属于当前 API 用户并处于可用状态。

**请求参数**

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `card_id` | number | 是 | 用于支付的名下卡 ID |
| `product` | string | 否 | `gpt`（缺省）/ `claude` / `grok`。Claude / Grok 必须显式传 |
| `plan` | string | 是 | GPT：`go` / `plus` / **`plus_half`（Plus 半价）** / `pro_5x` / `pro_20x` / **`pro_50x`（50x）** / `pro_20x_renew` / `pro_5x_renew` 及点数档；请求字段为 `plan`。Claude / Grok 见坑 2 |
| `payment_country` | string | 否 | GPT 默认 `PH`；Claude / Grok 请传 `US`，见坑 3 |
| `payment_currency` | string | 否 | GPT 默认 `PHP`；Claude / Grok 请传 `USD` |
| `credential` | object | 是 | 与预检相同的凭据结构；使用预检令牌时以服务端预检结果为准 |
| `preflight_token` | string | 否 | 预检返回的一次性令牌；推荐使用 |
| `client_request_id` | string | 是 | 商户侧订单号，当前用户下最长 80 字符；重试必须保持不变 |
| `pricing_version` | integer | 否 | 预检时看到的价格版本；不一致会要求刷新 |
| `no_auto_card_switch` | boolean | 否 | 默认 `false`：支付被拒/失败后平台会**自动换一张卡有限次重试**（与卡台直冲/CDK 一致）。设为 `true` 则**失败即失败、不自动换卡**（适合自己管控用哪张卡的自动化调用方）；此时订单会直接进入失败态，你可自行改单/换卡后重发 |

```bash
curl -X POST https://zovocard.com/openapi/v1/gpt-direct/orders \
  -H "X-API-Key: sk_你的密钥" \
  -H "Idempotency-Key: merchant-order-20260718-001" \
  -H "Content-Type: application/json" \
  -d '{
    "card_id":123,
    "plan":"plus",
    "credential":{"mode":"session","session":"你的_SESSION"},
    "preflight_token":"一次性预检令牌",
    "client_request_id":"merchant-order-20260718-001",
    "pricing_version":2
  }'
```

成功返回 HTTP `202`，订单先进入安全队列，不能把 `202` 当作已开通：

```json
{ "code": 0, "msg": "accepted", "data": {
  "id": 981,
  "plan": "plus",
  "source": "api",
  "status": "queued",
  "service_fee_minor": 0,
  "service_fee_status": "held",
  "pricing_version": 2,
  "quoted_amount_minor": 98214,
  "currency": "PHP",
  "client_request_id": "merchant-order-20260718-001"
} }
```

同一用户的 `client_request_id` 会返回原订单，不能用于创建第二笔订单。建议同时携带唯一 `Idempotency-Key`，网络重试时两个编号都保持不变。

#### 6.18.5 查询、详情与取消

- `GET /gpt-direct/orders?page=1&page_size=20`：只返回 API Key 所属账号的 `source=api` 订单（不按当前 Key 过滤）；默认 20 条，最大 100 条。响应为 `data.list` 和 `data.total`。
- `GET /gpt-direct/orders/{id}`：返回 `data.order`、`data.events` 和可选的 `data.card_usage`。凭据、支付意图和代理凭据不会返回。
- `POST /gpt-direct/orders/{id}/cancel`：只能取消尚未开始支付的 `queued`、`awaiting_card` 或 `funding_pending` 订单。支付已提交、已扣款或进入人工对账后返回 HTTP `409`，不要重复创建订单。
- `POST /gpt-direct/orders/{id}/cancel-renewal`：取消该订单对应 GPT 账号的**自动续费**。权益保留到本期到期日，到期后不再扣款——它取消的是下一期扣款，不是本次已付的套餐，也不退款。

  取消动作由上游账号服务执行，平台不直连 ChatGPT。**下单成功时上游就会自动尝试取消一次**，所以多数订单拿到手已经是 `renewal_status=success`，无需再调本接口；本接口用于那次尝试未确认（`pending`）或需要复查（`warning`）时重试。

  | `renewal_status` | 含义 | 调本接口的结果 |
  |---|---|---|
  | `success` | 已取消 | 幂等返回当前状态，不会重复请求上游 |
  | `pending` | 上游已受理未确认 | 重试取消 |
  | `warning` | 上游尝试过、需复查 | 重试取消 |
  | `not_requested` | 上游未被要求取消（如未支付成功） | HTTP `409` |

  订单未到 `completed` 时返回 HTTP `409`。只能操作本 API 用户经 API 下的单，其它订单一律 HTTP `404`。

#### 6.18.5a 用卡规则、剩余次数与卡池调度

默认仍是平台写死规则：**失败/取消也计次数**，普通档每卡 5 次、Pro 20x 每卡 3 次，自动选卡余额优先，支付失败自动换卡最多 2 次。合作方可以按产品（`gpt` / `claude` / `grok`）改规则，也可以自行清掉失败占用的次数，而不是要求平台「只在成功后扣次」。

次数计数器按**卡**共享（一张卡跑过 GPT 和 Claude 共用同一组 `completed_*` / `failed_*`）。规则只决定「这一单按哪个产品的口径解释这张卡」。

`GET /gpt-direct/card-rules`  
不传 `product` 返回三份（缺行补默认，`is_default=true`）；`?product=gpt` 只返回一份。

`PUT /gpt-direct/card-rules`

```json
{
  "product": "gpt",
  "count_failures": true,
  "light_max_uses": 5,
  "pro20_max_uses": 3,
  "select_mode": "default",
  "select_priority": [
    {"issuer": "four", "segment_type": "product", "segment_key": "PP5259RC"},
    {"issuer": "one", "segment_type": "product", "segment_key": "P5378OX"}
  ],
  "strict_select": true,
  "auto_switch_on_fail": true,
  "max_auto_switches": 2
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `count_failures` | `true` | `false` 时失败/取消不占额度，已记下的失败也不再挡选卡 |
| `light_max_uses` | `5` | plus / 5x / Claude / Grok 普通档上限；`0` = 不限 |
| `pro20_max_uses` | `3` | `pro_20x` 上限；`0` = 不限 |
| `select_mode` | `default` | `default` 余额优先；`lowest_usage` 次数最少；`newest` 最新开的卡 |
| `auto_switch_on_fail` | `true` | 拒付/软失败后是否自动换卡（单笔仍可被 `no_auto_card_switch` 关掉） |
| `max_auto_switches` | `2` | 自动换卡次数；`0` = 不换 |

`GET /gpt-direct/cards/{id}/usage?product=gpt`  
返回该卡 `light` / `pro20` 的 `used`、`remaining`（`-1` = 不限）、`cap`、`completed` / `failed` / `reserved`，以及冷却、连败、是否占用中。只能查自己的卡。

`POST /gpt-direct/cards/reset-usage`

```json
{ "card_ids": [101, 102], "scope": "failed" }
```

`card_ids` 空 = 清理名下全部已有用量行。`scope=failed`（默认）只清失败次数并解除冷却/连败；`scope=all` 再清成功次数。**绝不**动在途 `reserved_*` / `active_order_id`，也不回滚订单上的 `card_usage_counted`。

`GET /gpt-direct/card-pool/schedule?product=gpt&plan=plus&card_mode=auto_existing`  
不落单的自动选卡预览：`data.pick` 是按当前规则会选的卡，`data.candidates` 列出其余卡及 `skip_reason`。可选 `issuer` / `segment_type` / `segment_key`。

`GET /gpt-direct/card-products`  
列出卡头及是否启动：`enabled` / `suspended` / `channel_open` / `usable`。未启动的卡头不会进入 `select_priority` 级联。

`PUT /gpt-direct/card-rules` 还可传：

| 字段 | 默认 | 说明 |
|---|---|---|
| `select_priority` | `[]` | `[{issuer,segment_type,segment_key}]` 卡头优先级；未启动的项会被跳过 |
| `strict_select` | `false` | `true` 时不回落平台 537872/星链级联（CDK 对接建议打开） |

网页商城对等接口在 `/api/v1/gpt/direct/` 下同名路径（卡片订单页可单卡/批量清理，用卡规则页可改三份配置）。

#### 6.18.5b 账号级只读订单同步（包含商城）

- `GET /gpt-direct/account-orders`：查询 **API Key 所属账号**的全部来源订单（`mall` / `api` / `cdk`），不按签发订单的 Key 过滤，不包含其他账号或主子账号的订单。仍需该账号具备开放 API、GPT 直充权限，并满足现有 IP 白名单和充值准入。
- `GET /gpt-direct/account-orders/{id}`：返回同样的安全订单摘要；非本账号订单返回 `404`。新路径只读，不提供取消、兑换或凭据导出。
- 旧 `/gpt-direct/orders` 继续只返回本账号 `source=api` 的订单，并不是只返回当前 Key 创建的订单。

完整路径前缀是 `/openapi/v1`，鉴权为 `X-API-Key`。列表参数：`page_size` 默认 20、范围 1–100；`source` 可选 `mall` / `api` / `cdk`，省略为全部；`updated_after` 可选带时区 RFC3339 时间，**包含相同时间**。未知参数、重复参数、无效时间或游标返回 `400`。

采用游标分页，不支持 `page`：首请求传筛选条件，后续只传 `cursor=<next_cursor>` 与可选 `page_size`，不要重复传 `source` / `updated_after`。结果按 `updated_at, order_id` 升序，`sync_until` 在一轮分页中固定。`has_more=false` 表示本轮读完，无总数全表扫描。

增量同步：先完整读完一轮，再保存 `sync_until`；下一轮将它适当回退（建议至少 5 分钟）作为 `updated_after`，用 `order_id` 更新本地记录并忽略更旧的 `updated_at`。这是可变化的订单快照，不是不可变事件日志：并发提交、处理中状态和晚提交事务可能需要重读，建议定期复核非终态订单。只有消费完所有页才能推进同步水位。列表不生成 Webhook `event_id`；回调使用自己的稳定 `event_id` 去重。

响应只包含下列字段；邮箱脱敏，卡号仅后四位，未分配或无法读取卡片时 `card_last_four=""`。除新增的账单链接 `invoice_url` 外，不返回完整卡号、CVV、Session、凭证或代理。`invoice_url`（2026-10-07 新增）是本单对应 GPT 账号的账单链接（Stripe/OpenAI 托管），**仅在链接真实存在且指向可信账单域时返回**，否则该字段省略（`omitempty`）；它与列表一样按 `user_id` 严格隔离，**只能拿到本账号自己开通订单的账单，不会越权到其他账号或主子账号**。金额为支付币种最小单位（报价和最终金额分别提供，不代表已扣款）；`created_at` / `updated_at` / `completed_at` 为 RFC3339 UTC，未完成时 `completed_at=null`。

```http
GET /openapi/v1/gpt-direct/account-orders?source=mall&page_size=20&updated_after=2026-09-24T00%3A00%3A00Z
X-API-Key: YOUR_API_KEY
```

```json
{"code":0,"msg":"ok","data":{"list":[{"order_id":981,"source":"mall","product":"gpt","plan":"plus","account_email":"us***@example.com","status":"completed","stage":"completed","quoted_amount_minor":98000,"final_amount_minor":98214,"currency":"PHP","card_id":456,"card_last_four":"1234","invoice_url":"https://invoice.stripe.com/i/acct_xxx/test_yyy","created_at":"2026-09-24T10:00:00Z","updated_at":"2026-09-24T10:05:00Z","completed_at":"2026-09-24T10:05:00Z"}],"next_cursor":"","has_more":false,"sync_until":"2026-09-24T10:06:00Z"}}
```

接入顺序：先配置通用回调并暂存收到的事件，再用账号级列表完成首次全量同步。之后按 `updated_after` 定期增量拉取；回调按 `event_id` 去重，记录按 `order_id` 合并，避免旧 `updated_at` 覆盖新状态。需要复核单笔订单时使用 `GET /openapi/v1/gpt-direct/account-orders/{id}`；详情的 `data` 直接是订单摘要，没有凭证或时间线。

#### 6.18.6 发放和查询 CDK

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

##### 自定义 CDK 品牌前缀

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

#### 6.18.7 CDK 订单对账

- `GET /gpt-direct/cdk-orders?page=1&page_size=20` 返回当前 Key 可见 CDK 的兑换订单（本 Key 签发 + 官网购入未绑定 App）。
- `GET /gpt-direct/cdk-orders/{order_id}` 返回单笔订单与公开时间线 `events`。

列表支持 `updated_after`（RFC3339）、`status`、`cdk_id`、`order_id`、`page` 和 `page_size`（最大 100）。结果包括 CDK 前缀/状态、脱敏账号与卡号、金额、服务费/资金状态、订单阶段和时间；不返回凭据、代理、内部排障详情、完整 CDK 或完整卡号。同一用户的其他 API Key 签发 CDK 的订单不可见。官网购入码的兑换 Webhook 不会推到后建的 Key，请用本接口对账。


CDK订单列表和详情均返回 `renewal_status`（2026-09-25新增），与付款的 `status` 分开判断。`success` 表示系统已记录续费取消成功；`pending/retrying` 表示仍在处理；`warning/manual_required` 需要复查或人工处理；`not_requested` 表示该订单没有要求取消（例如 `pro_20x_renew` 续费绑卡需要保留自动续费）。空值表示未知，不能当作成功。订单 `completed` 后仍可继续只读查询此字段，不要因等待取消结果而重新兑换或重复付款；查询本身不触发新的取消请求。

#### 6.18.8 CDK 兑换（公开兑换接口，独立于 API Key）

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
| 3. 兑换 | `POST /api/v1/cdk/redeem` | `redemption_token` + `preflight_token` + `client_request_id` → `data.id/status/stage` |
| 4. 查结果 | `GET /api/v1/cdk/result?token=<redemption_token>` | `data.order` + `data.events`；同样受原 IP/设备绑定限制 |

预检请求示例：

```json
{"redemption_token":"<preview token>","credential":{"mode":"session","session":"<customer session>"}}
```

第三步不重复传凭据。可选 `exclude_card_ids` 排除本单自动选择的卡，`strict_card_preference=true` 不追加平台后备卡段，`no_auto_card_switch=true` 关闭本单失败后自动换卡。自动选卡读取卡主**当前用卡规则**，没有规则时用平台默认，发码时的 `preferred_*` 不锁定兑换选择。

需要指定原卡时使用 §6.18.8a 的所有者接口；本公开入口收到非零 `card_id` 返回 `403 CARD_SELECTION_REQUIRES_API_KEY`。

订单进入 `queued`、`awaiting_card`、`funding_pending`、`dispatching`、`running`、`requires_action`、`pending`、`plus_paid` 或各类 review 时继续查询，不重新付款。`completed` 表示该套餐流程完成，续费绑卡档不等于下一期账单已扣款。`declined` / `failed_precharge` / `cancelled` 是订单终态，但**不能单凭失败状态推断码已释放或钱已退**；所有者还应核对 `cdk_status`、`funding_hold_status`、`service_fee_status` 和事件。只有 CDK 回到 `unused` 才能再次兑换；有扣款证据或不确定时可能继续保留为 `review`。

公开结果新增 `data.order.card_id`、`card_last_four`；后四位可能为空，不能据此推断失败，也不能用后四位唯一识别卡。API Key 和完整 Session 不得暴露在页面源码或日志中。

#### 6.18.8a 兑换时指定已有银行卡（所有者 API）

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

`redemption_token`、`preflight_token`、`client_request_id` 必填；请求编号为 1–80 字节（建议使用 ASCII）。`card_id` 是正整数的**本地卡 ID**，不是完整卡号、卡后四位或上游字符串 ID。可从上次 `cdk-orders/{order_id}` 的 `order.card_id` 保存，也可从本人卡列表取得。

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

#### 6.18.8b CDK 项目复用的卡台接口

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

#### 6.18.9 GPT 直充错误处理

除通用 HTTP 错误外，调用方应按 `error_code` 编程处理：

- `GPT_DIRECT_ACCESS_DENIED` / `RECHARGE_REQUIRED`：账户尚未满足权限、充值或风控条件。
- `GPT_SESSION_INVALID`、`SESSION_REQUIRED`：重新提交凭据预检。
- `GPT_PLAN_ALREADY_ACTIVE`：目标账号套餐仍有效，不要重试扣款。
- `INSUFFICIENT_BALANCE`：API 服务费余额不足。
- `GPT_DIRECT_ORDER_REJECTED`：读取订单或事件后决定是否重试；不要盲目新建订单。

---

---

### 6.18b 刷新 Session（用 session-token 换新的 accessToken）

`POST /gpt-direct/session/refresh`

Session JSON 里的 `accessToken` 有效期较短，过期后不必让客户重新导出：用其中的 `sessionToken` 调本接口即可换到新的 `accessToken`。你的服务器直连 `chatgpt.com/api/auth/session` 通常会被 Cloudflare 拦截（HTTP 403 challenge），本接口由平台代为请求。

> 预检与下单本身已会自动用 `sessionToken` 刷新 accessToken，**直充下单不需要先调本接口**。本接口用于你自己的系统也需要新 `accessToken` 的场景。
> 不支持只传 `accessToken`：没有 `sessionToken` 无法刷新；直充也不接受仅 `accessToken` 下单（付款后无法自动取消续费）。

请求体：

```json
{ "session": "<__Secure-next-auth.session-token 或 /api/auth/session 返回的完整 JSON 字符串>" }
```

成功响应：

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "access_token": "eyJhbGciOi...",
    "expires": "2026-11-10T08:00:00.000Z",
    "session_token": "eyJhbGciOiJkaXIi...",
    "session_token_rotated": true,
    "email": "user@example.com",
    "plan_type": "plus",
    "session": { "user": { "email": "user@example.com" }, "expires": "...", "accessToken": "eyJ...", "sessionToken": "eyJ..." }
  }
}
```

| 字段 | 说明 |
|---|---|
| `access_token` / `expires` | 新的 accessToken 及其过期时间 |
| `session_token` | 之后应使用的 session-token。`session_token_rotated=true` 表示 OpenAI 已轮换，**请用新值覆盖旧值**，旧值可能很快失效 |
| `session` | 完整 Session JSON（已带最新 `sessionToken`），可原样作为 `credential.session` 用于预检/下单 |
| `email` / `plan_type` | 账号邮箱 / 当前套餐（可能缺省） |

失败：

| HTTP | `error_code` | 含义 |
|---|---|---|
| 400 | `GPT_SESSION_FORMAT_INVALID` | 没有可识别的 session-token |
| 400 | `GPT_ACCESS_TOKEN_UNSUPPORTED` | 只有 accessToken，无法刷新 |
| 422 | `GPT_SESSION_EXPIRED` | session-token 已被注销/轮换或过期，无法刷新，需重新登录导出 |
| 502 | `GPT_SESSION_REFRESH_UPSTREAM` | ChatGPT 暂时不可用，可稍后重试 |
| 429 | `GPT_SESSION_REFRESH_RATE_LIMITED` | 刷新过于频繁（每账户每分钟 30 次） |

### 6.19 取消自动续费（任意账号，只需 Session）

`POST /gpt-direct/cancel-renewal`

**不带订单号**，只凭完整 Session 取消**任意** GPT 账号的自动续费。

上面那个接口只能操作你经本 API 下的单，所以存量账号（早期在网页端下的单、从别处拿到的号）没法通过 API 关续费。本接口把判据从「订单归属」换成「持有 Session」。

```bash
curl -X POST https://zovocard.com/openapi/v1/gpt-direct/cancel-renewal \
  -H "X-API-Key: sk_你的密钥" \
  -H "Content-Type: application/json" \
  -d '{"session":"<完整 Session JSON 或 sessionToken>"}'
```

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "renewal_status": "success",
    "renewal_message": "自动续费已取消",
    "email": "user@example.com",
    "will_renew": false,
    "active_until": 1760000000,
    "plan_type": "chatgptplusplan"
  }
}
```

必须是含 `sessionToken` 的**完整 Session**，纯 Access Token 不再受支持。

| `renewal_status` | 含义 | 你该怎么做 |
|---|---|---|
| `success` | 已取消并二次确认 `will_renew=false` | 完成 |
| `pending` | 取消请求已发出但未能二次确认 | 稍后复查；**不要立即重试** |

本接口不依赖订单，因此没有订单可回写状态，结果请自行记录。

常见订单状态：

| 状态 | 含义 |
| --- | --- |
| `queued` / `running` | 已接收，正在检查凭据或处理支付 |
| `pending` / `requires_action` | 上游仍在等待或需要后续对账 |
| `completed` | 已确认目标套餐开通 |
| `declined` | 上游拒付，未必代表服务费已释放前的最终状态 |
| `failed_precharge` | 扣款前失败；若未发生扣款，服务费会释放 |
| `cancelled` | 在支付开始前取消 |

---


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

## 7. 回调 Webhook（卡事件、GPT 直充与 CDK）

配置回调地址后，平台会主动 **POST** 卡事件；按 API Key 订阅还可接收 GPT 直充全终态、阶段进度、公开时间线和 CDK 生命周期事件。

> 卡交易/操作使用 `event`；GPT / CDK 事件使用 `type`。API Key 可订阅 `gpt_direct.*`、`cdk.*` 或具体事件。上游提供开卡、充值、冻结/解冻、删卡或卡余额退款结果时，平台会在能安全解析卡主时转发操作回调。
>
> 当前运行中的发卡渠道为渠道 1、渠道 3 与渠道 4（PhotonPay）；渠道 2 的 Webhook 已退役。各渠道事件已在平台侧**归一化**为下面同一套结构，接收端以 `channel`/`issuer` 区分即可。

### 7.5 GPT 直充与 CDK 生命周期事件

| 订阅值 | 触发 |
| --- | --- |
| `gpt_direct.completed` | 首次进入 `completed` |
| `gpt_direct.failed` | 首次进入 `declined` 或 `failed_precharge` |
| `gpt_direct.cancelled` | 首次进入 `cancelled` |
| `gpt_direct.progress` | `status` 或 `stage` 发生变化；单订单最多 30 条 |
| `gpt_direct.event` | 公开订单时间线新增事件 |
| `gpt_direct.*` | 上述全部 GPT 事件 |
| `cdk.reserved` / `cdk.consumed` / `cdk.released` | CDK 预留、成功消耗或失败释放 |
| `cdk.frozen` / `cdk.unfrozen` / `cdk.disabled` | CDK 管理状态变化 |
| `cdk.*` | 上述全部 CDK 事件 |

终态事件示例：

```json
{
  "type": "gpt_direct.completed",
  "event_id": "gpt_direct.completed:981",
  "order_id": 981,
  "client_request_id": "merchant-order-20260718-001",
  "source": "cdk",
  "app_id": "app_xxx",
  "plan": "plus",
  "status": "completed",
  "stage": "completed",
  "message": "开通成功",
  "cdk_id": 123,
  "code_prefix": "ZC-XXXX-XXXX",
  "cdk_status": "consumed",
  "account_email": "us***@example.com",
  "card_id": 456,
  "card_number": "537872****1234",
  "card_last_four": "1234",
  "final_amount_minor": 98214,
  "quoted_amount_minor": 98000,
  "currency": "PHP",
  "service_fee_minor": 100,
  "service_fee_status": "settled",
  "funding_state": "card_ready",
  "funding_hold_status": "settled",
  "funding_card_amount_minor": 10000,
  "created_at": "2026-07-28T10:00:00Z",
  "updated_at": "2026-07-28T10:05:00Z",
  "completed_at": 1784397600,
  "occurred_at": "2026-07-28T10:05:00.123Z"
}
```

失败事件保留真实 `status`，并通过 `cdk_status`、`service_fee_status`、`funding_hold_status` 表示释放结果。进度事件额外提供 `from_status` / `from_stage`；时间线事件额外提供公开的 `timeline_event_id`、`category`、`step`、`public_code`、`public_message`、`to_status` 和 `payment_fact`。

CDK 生命周期事件提供 `event_id`、`app_id`、`cdk_id`、`code_prefix`、`cdk_status`、`plan`、`order_id`、`from_status`、`to_status`、费用和时间。完整 CDK、完整卡号、凭据和 API Key 永不返回。事件严格发送给签发该 CDK 的 Key / `app_id`。

### 7.6 事件场景对照（type × status）

| type | status | 含义 | 对卡余额 |
| --- | --- | --- | --- |
| `Authorization` | `PENDING` | 消费授权成功，预扣（冻结）额度，尚未清算 | 占用可用额度 |
| `Settlement` | `COMPLETE` | 清算完成，真实扣款落地 | 扣减 |
| 任意 | `DECLINED` | 交易被拒（余额不足 / 风控等）| 不变 |
| `Refund` | `COMPLETE` / `PENDING` | 消费退款，金额退回卡内 | 增加 |
| `Reversal` | `COMPLETE` | 授权撤销（预授权取消，非真实退款）| 释放占用 |

> 一笔消费通常先收到 `Authorization` / `PENDING`，清算后再收到 `Settlement` / `COMPLETE`，两条 `auth_id` 相同——按 7.7 幂等去重。

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

### A.4 直充（GPT / Claude / Grok）

| HTTP | `error_code` | 含义 |
|---|---|---|
| 401 | `ACCOUNT_UNAVAILABLE` | 账户不可用（不存在或已禁用）|
| 403 | `GPT_DIRECT_ACCESS_DENIED` | 未开通直充 API 权限（需管理员开通或达到 SVIP）|
| 403 | `RECHARGE_REQUIRED` | 尚无已入账充值 |
| 503 | `GPT_DIRECT_UNAVAILABLE` / `GPT_DIRECT_PAUSED` | 直充服务暂不可用 / 已暂停 |
| 400 | `DIRECT_PRODUCT_DISABLED` | 该产品线全局关闭（每个产品独立开关）|
| 400 | `INVALID_REQUEST` | 参数错误；**或未知 `product`**；或 `card_id` 与 `raw_card` 同时提供 |
| 403 | `EXTERNAL_CARD_DISABLED` | 自带卡（`raw_card`）未开放 |
| 400 | `INSUFFICIENT_BALANCE` | 余额不足 |
| 400 | `PRECHECK_REJECTED` | 账号预检不通过 |
| 400 | `GPT_PLAN_ALREADY_ACTIVE` | 目标账号该套餐仍在有效期内 |
| 400 | `GPT_PRICE_UNCONFIRMED` | 报价未确认，需重新预检 |
| 400 | `GPT_SESSION_INVALID` / `CLAUDE_SESSION_INVALID` | 凭据无效 |
| 400 | `GPT_CREDIT_REQUIRES_SUBSCRIPTION` | 买点数要求目标账号已有生效订阅 |
| 400 | `GPT_CREDIT_DISABLED` | 点数购买已关闭 |
| 400 | `GPT_RENEW_TARGET_NOT_PRO` | 续费档要求目标账号当前就在 Pro 20x。**确定性失败，重试无用** |
| 400 | `GPT_RENEW_SUBSCRIPTION_EXPIRED` | 续费档：Pro 20x 订阅已到期，属于重新订阅。**确定性失败，重试无用** |
| 429 | `GPT_PREFLIGHT_RATE_LIMITED` | 预检过于频繁（60 次/分钟/用户，120 次/分钟/IP）|
| 404 | *（无）* | 订单不存在（或不属于本密钥经 API 下的单）|
| 409 | *（无）* | 订单已开始支付或已开通，无法取消 / 取消续费状态不允许 |
| **202** | — | 下单已受理（`"msg":"accepted"`），**轮询到终态才算成功** |
| 422 | `GPT_SESSION_EXPIRED` | Session 已失效（6.18b 刷新时）：session-token 被注销/轮换或过期，需重新导出 |
| 502 | `GPT_SESSION_REFRESH_UPSTREAM` | 刷新 Session 时 ChatGPT 暂时不可用 |
| 429 | `GPT_SESSION_REFRESH_RATE_LIMITED` | 刷新 Session 过于频繁 |

### A.5 CDK

| HTTP | `error_code` | 含义 |
|---|---|---|
| 400 | *（无）* | 必须确认承担资金（`funding_confirmed` 必填为 `true`）|
| 400 | *（无）* | 单次最多发放 50 张 |
| 400 | *（无）* | 未知套餐（CDK 支持 GPT / Claude / Grok；**Grok 用 `grok_` 前缀**，如 `grok_premium_monthly`）|
| 409 | `CDK_DISABLE_REJECTED` | 只能禁用未使用的 CDK / CDK 不存在 |
| 403 | `API_SCOPE_UNAVAILABLE` | 密钥作用域不可用 |
| 404 | `ORDER_NOT_FOUND` | CDK 订单不存在 |

---


### 已有订阅：查询5X/20X/50X升级差价

此接口读取指定账户的实际升级应付差价，币种取自该账户账单；不是新订阅全价，也不会因为网页所选地区而改成PHP。

1. 先调用 `POST /gpt-direct/preflight`，取得原始 `preflight_token`。
2. 调用 `POST /gpt-direct/upgrade-quotes`，正文仅传 `{ "preflight_token": "<原始预检凭证>" }`。
3. 从 `quotes` 选择 `available=true` 的目标，向 `POST /gpt-direct/orders` 传该条新 `preflight_token`、对应 `plan`、返回的 `currency`（作为 `payment_currency`）及正常用卡/幂等参数。`payment_mode` 使用 `direct`。
4. 手动卡可先调用 `POST /gpt-direct/upgrade-funding-quote`，正文传 `card_id`、`plan`、该升级 `preflight_token`，查看卡内所需资金；接口沿用已有手动充值报价响应。

查询不建单、不扣款。每档报价最长5分钟有效，并绑定当前用户、账户、目标档位和金额币种；不可跨用户/跨档位使用。新建订单仍需有效预检凭证；订单受理后，执行时的价格上涨、下降或报价过期均不拦截。执行时获取最新差价，查询暂不可用也继续由上游账单决定金额，不比较旧报价、不回退套餐全价，也不先买低档套餐。应用商店、欠费、失效订阅及同档/高档不提供可付报价。零差价暂不自动提交。缺少有效汇率时展示报价但不可充值。服务费及开卡/充值手续费仍按账户配置另计；`funding_usd_minor` 是按近期汇率加10%余量计算的卡内预充值估算，不是应付差价。本功能支持商城直接充值和OpenAPI直接充值，暂不用于CDK兑换。

```json
{
  "quotes": [
    {
      "plan": "pro_5x",
      "current_plan": "plus",
      "available": true,
      "currency": "USD",
      "amount_minor": 8801,
      "minor_unit_exponent": 2,
      "funding_usd_minor": 9682,
      "expires_at": "2026-09-29T01:05:00Z",
      "preflight_token": "<opaque-upgrade-token>"
    },
    {
      "plan": "pro_20x",
      "available": false,
      "error_code": "upgrade_quote_unavailable"
    },
    {
      "plan": "pro_50x",
      "current_plan": "plus",
      "available": true,
      "currency": "USD",
      "amount_minor": 45000,
      "minor_unit_exponent": 2,
      "funding_usd_minor": 49500,
      "expires_at": "2026-09-30T01:05:00Z",
      "preflight_token": "<opaque-50x-upgrade-token>"
    }
  ]
}
```

示例金额仅用于说明：`8801 / 10² = 88.01 USD`；JPY/KRW等以返回的 `minor_unit_exponent` 为准，不可固定除以100。


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

## X Premium 订阅（首版）

产品参数`product=x`；套餐`plan`可选`plus_monthly`, `plus_yearly`, `premium_monthly`, `premium_yearly`, `super_monthly`, `super_yearly`, `ultra_monthly`, `ultra_yearly`（档位 Plus / Premium / Super / Ultra）。支持 US/USD、JP/JPY、PH/PHP、NG/NGN、TR/TRY、EG/EGP 六区新订阅，默认 US/USD；不支持智利。预检、下单及CDK发码须保持同一地区。JPY金额为整数日元，不是分。

使用X-API-Key查询`GET /openapi/v1/gpt-direct/plans?product=x`。预检与建单仍分别是`POST /openapi/v1/gpt-direct/preflight`、`POST /openapi/v1/gpt-direct/orders`；两步都带product、plan和地区。credential.mode为session，credential.session只需填 X 的 auth_token（x.com cookie 里的登录令牌）；ct0、billing_email 不再需要，系统会自行获取 ct0。建单带本人card_id、preflight_token、client_request_id和credential。凭据只能用于对应产品，不支持外接卡。

X CDK使用相同短plan发码，兑换使用X凭据。已有订阅升级和自动取消续费暂不支持，自动续费请在X设置中管理。功能及套餐需要管理员开放；本文不表示已上线。

## 同一客户复用原卡续费或更换套餐

同一付款用户（代理）、同一客户账号和同一张仍归该用户所有的卡，存在历史成功付费记录时，再次直充可复用原卡；不要求新旧套餐相同，例如 Plus 换 5x、20x 或 50x。此时不因原卡普通档5次/Pro档累计次数已满而拒单。

无需新增 `renewal` 参数：按原接口提交 `card_id`、目标 `plan` 和当前有效凭据/预检凭证即可。客户身份由服务端预检及历史订单核对，不能通过手填邮箱获得豁免。升级仍走原有差价报价流程。

没有同一客户历史付费证据的新账号仍按次数规则执行；卡片归属、并发占用、冷却、归档、拒付、渠道及资金授权规则保持有效。`pro_20x_renew` 绑卡自动续费专用档的一卡一账号限制不变。累计次数不清零，新交易仍按原规则计数；不要仅凭次数汇总在客户端提前拒绝老客户续费。
