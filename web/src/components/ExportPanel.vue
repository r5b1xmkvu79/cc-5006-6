<template>
  <div class="export">
    <h2>校验与导出</h2>
    <div class="row">
      <input id="ap" type="checkbox" v-model="allowPlaceholder" />
      <label for="ap" style="display:inline">允许缺失素材以占位画面导出</label>
    </div>
    <div class="row btns">
      <button class="ghost" @click="doValidate" :disabled="busy">校验</button>
      <button @click="doExport" :disabled="busy">{{ busy ? '导出中…' : '导出成片' }}</button>
    </div>

    <div v-if="issues.length" class="issues">
      <div v-for="(i, k) in issues" :key="k" class="issue" :class="i.level">
        [{{ i.level === 'error' ? '阻止' : '警告' }}] {{ i.message }}
      </div>
    </div>
    <div v-else-if="validated" class="ok">校验通过，可以导出</div>

    <div v-if="manifest" class="manifest">
      <h3>导出完成</h3>
      <div>输出: {{ manifest.output }}</div>
      <div>片段: {{ manifest.clips.length }} 个
        （完整展示 {{ completeCount }} / 裁切展示 {{ croppedCount }} / 占位 {{ placeholderCount }}）</div>
      <div v-if="manifest.warnings?.length" class="warnlist">
        <div v-for="(w, k) in manifest.warnings" :key="k" class="issue warning">{{ w }}</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  project: { type: Object, required: true },
  issues: { type: Array, default: () => [] },
})
const emit = defineEmits(['validate', 'export'])

const allowPlaceholder = ref(false)
const busy = ref(false)
const validated = ref(false)
const manifest = ref(null)

const completeCount = computed(() =>
  (manifest.value?.clips || []).filter((c) => c.displayClaim === 'complete' && !c.placeholder).length)
const croppedCount = computed(() =>
  (manifest.value?.clips || []).filter((c) => c.displayClaim === 'cropped').length)
const placeholderCount = computed(() =>
  (manifest.value?.clips || []).filter((c) => c.placeholder).length)

async function doValidate() {
  busy.value = true
  try {
    await emit('validate', allowPlaceholder.value)
    validated.value = true
  } finally {
    busy.value = false
  }
}

async function doExport() {
  busy.value = true
  manifest.value = null
  try {
    manifest.value = await emit('export', allowPlaceholder.value)
  } catch {
    // error surfaced by App
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.export { border-top: 1px solid #333; padding-top: 10px; }
h2 { font-size: 14px; margin: 0 0 8px; }
h3 { font-size: 13px; margin: 10px 0 4px; }
.row { margin: 8px 0; }
.btns { display: flex; gap: 8px; }
.issue { font-size: 12px; padding: 4px 6px; border-radius: 4px; margin-bottom: 4px; }
.issue.error { background: #4d1f1f; }
.issue.warning { background: #4d3a17; }
.ok { color: #7fd67f; font-size: 12px; }
.manifest { font-size: 12px; color: #cfd3da; word-break: break-all; }
.warnlist { margin-top: 6px; }
</style>
