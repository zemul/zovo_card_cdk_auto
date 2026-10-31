<template>
  <RedeemShell title="ChatGPT充值" subtitle="安全提交，实时查看处理进度" :step="1">
    <form class="rx-form" novalidate @submit.prevent="submit">
      <label class="rx-field">
        <span>卡密</span>
        <input v-model="code" name="card_key" autocomplete="off" autocapitalize="characters" spellcheck="false" placeholder="输入卡密" :readonly="busy" />
      </label>
      <p v-if="error" class="rx-alert" role="alert">{{ error }}</p>
      <p v-else class="rx-note">卡密仅可兑换一次，请妥善保管，避免泄露。</p>
      <button class="rx-btn is-wide" type="submit" :disabled="busy">
        <span v-if="busy" class="rx-spin" aria-hidden="true" />{{ busy ? '验证中…' : '继续' }}
      </button>
      <nav class="rx-links" aria-label="其他服务">
        <router-link :to="RX_QUERY">查询进度</router-link>
      </nav>
    </form>
  </RedeemShell>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import RedeemShell from './RedeemShell.vue'
import { buildProgressView } from './progress'
import { RX_FILL, RX_QUERY, api, cardFromQuery, errorText, extractCardInput, looksUsed, stashPreview } from './api'

const route = useRoute()
const router = useRouter()
const code = ref('')
const busy = ref(false)
const error = ref('')

document.title = 'ChatGPT 充值'

async function submit() {
  const card = extractCardInput(code.value)
  if (!card) {
    error.value = '请输入卡密'
    return
  }
  code.value = card
  await check(card)
}

/** 校验卡密：可用 → 填写页；已用过 / 处理中 → 进度页；其它 → 留在本页显示原因。 */
async function check(card: string) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const preview = await api('/api/v1/public/cdk/preview', { method: 'POST', body: JSON.stringify({ code: card }) })
    const body = preview.data?.data && typeof preview.data.data === 'object' ? preview.data.data : preview.data || {}
    const token = body.redemption_token || preview.data?.redemption_token || body.token || ''
    if (preview.ok && token) {
      stashPreview(card, token, body)
      await router.replace({ path: RX_FILL, query: { card } })
      return
    }
    const msg = errorText(preview.data, '卡密无效或不可用')
    if (preview.status === 429) {
      error.value = '请求太频繁，请稍后再试'
      return
    }
    // 不能兑换时先看本站有没有这张卡的兑换记录：有就直接去看进度。
    const progress = await api('/api/v1/public/cdk/result-by-code?code=' + encodeURIComponent(card))
    if (progress.ok && buildProgressView(progress.data).found) {
      await router.replace({ path: RX_QUERY, query: { card } })
      return
    }
    error.value = looksUsed(msg) ? '该卡密已使用。如是在其他网站兑换的，请到对应网站查询进度。' : msg
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  const card = cardFromQuery(route.query)
  if (!card) return
  code.value = card
  void check(card)
})
</script>
