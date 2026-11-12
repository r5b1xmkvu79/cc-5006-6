<template>
  <div class="inspector">
    <h2>片段设置</h2>
    <label>名称</label>
    <input v-model="clip.label" style="width: 100%" @change="changed" />

    <div class="row">
      <span class="badge" :class="claim.cls">{{ claim.text }}</span>
      <span v-if="media" class="badge info">{{ media.width }}×{{ media.height }}</span>
    </div>

    <!-- 视频取段与照片停留时长分别建模 -->
    <template v-if="clip.kind === 'video'">
      <label>取段起点 (秒)</label>
      <input type="number" step="0.1" min="0" v-model.number="clip.in" @change="fixTrim" />
      <label>取段终点 (秒)</label>
      <input type="number" step="0.1" min="0" v-model.number="clip.out" @change="fixTrim" />
      <div class="hint" v-if="media">素材时长 {{ media.duration.toFixed(2) }}s，取段 {{ (clip.out - clip.in).toFixed(2) }}s</div>
    </template>
    <template v-else>
      <label>照片停留时长 (秒)</label>
      <input type="number" step="0.5" min="0.5" max="600" v-model.number="clip.dwell" @change="changed" />
    </template>

    <label>构图方式</label>
    <select v-model="clip.fit" @change="changed">
      <option value="full">完整保留（加边全显）</option>
      <option value="crop">裁切填充（需编辑确认）</option>
      <option value="border">背景边框（完整 + 虚化底）</option>
    </select>

    <div v-if="clip.fit === 'crop'" class="cropbox">
      <div class="warn">裁切模式不会完整展示画面，裁切位置须由编辑确认；不得在裁掉人物或文字后仍声称完整展示。</div>
      <label>水平位置 {{ clip.crop.x.toFixed(2) }}</label>
      <input type="range" min="0" max="1" step="0.01" v-model.number="clip.crop.x" @input="unconfirm" />
      <label>垂直位置 {{ clip.crop.y.toFixed(2) }}</label>
      <input type="range" min="0" max="1" step="0.01" v-model.number="clip.crop.y" @input="unconfirm" />
      <div class="confirmrow">
        <input id="cfm" type="checkbox" v-model="clip.crop.confirmed" @change="changed" />
        <label for="cfm" style="display:inline">编辑已确认裁切位置</label>
      </div>
      <template v-if="clip.crop.confirmed">
        <label>确认人</label>
        <input v-model="clip.crop.confirmedBy" placeholder="编辑姓名" @change="changed" />
      </template>
    </div>

    <template v-if="clip.fit === 'border'">
      <label>背景虚化强度 {{ clip.border.blur }}</label>
      <input type="range" min="2" max="60" v-model.number="clip.border.blur" @change="changed" />
      <label>边框宽度 (px)</label>
      <input type="number" min="0" max="120" v-model.number="clip.border.padding" @change="changed" />
      <label>边框颜色</label>
      <input v-model="clip.border.color" placeholder="black / #112233" @change="changed" />
    </template>

    <template v-if="clip.kind === 'photo' && clip.fit === 'crop'">
      <div class="panhead">
        <label style="display:inline">缓慢平移</label>
        <input type="checkbox" v-model="panEnabled" @change="togglePan" />
      </div>
      <template v-if="clip.pan">
        <label>缩放倍率 {{ clip.pan.zoom.toFixed(2) }}（≥1 才保证取景有效）</label>
        <input type="range" min="1" max="3" step="0.05" v-model.number="clip.pan.zoom" @input="checkPan" />
        <div class="pangrid">
          <span>起点</span>
          <input type="number" step="0.05" min="0" max="1" v-model.number="clip.pan.x0" @input="checkPan" />
          <input type="number" step="0.05" min="0" max="1" v-model.number="clip.pan.y0" @input="checkPan" />
          <span>终点</span>
          <input type="number" step="0.05" min="0" max="1" v-model.number="clip.pan.x1" @input="checkPan" />
          <input type="number" step="0.05" min="0" max="1" v-model.number="clip.pan.y1" @input="checkPan" />
        </div>
        <div v-if="panError" class="err">取景范围无效: {{ panError }}</div>
        <div v-else class="ok">取景范围有效</div>
      </template>
    </template>

    <div v-if="issues.length" class="issues">
      <div v-for="(i, k) in issues" :key="k" class="issue" :class="i.level">{{ i.message }}</div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { validatePan } from '../geometry.js'

const props = defineProps({
  project: { type: Object, required: true },
  clip: { type: Object, required: true },
  media: { type: Object, default: null },
  issues: { type: Array, default: () => [] },
})
const emit = defineEmits(['changed'])

const panError = ref('')
const panEnabled = computed({
  get: () => !!props.clip.pan,
  set: () => {},
})

const claim = computed(() =>
  props.clip.fit === 'crop'
    ? { text: '裁切展示（非完整画面）', cls: 'warn' }
    : { text: '完整展示', cls: 'ok' })

function changed() { emit('changed') }

function unconfirm() {
  props.clip.crop.confirmed = false
  changed()
}

function fixTrim() {
  const c = props.clip
  if (c.in < 0) c.in = 0
  if (props.media && c.out > props.media.duration) c.out = props.media.duration
  if (c.in >= c.out) c.out = c.in + 0.5
  changed()
}

function togglePan() {
  const c = props.clip
  c.pan = c.pan ? null : { zoom: 1.2, x0: 0, y0: 0.5, x1: 1, y1: 0.5 }
  if (c.pan) checkPan()
  changed()
}

function checkPan() {
  panError.value = ''
  const c = props.clip
  if (!c.pan || !props.media) { changed(); return }
  try {
    validatePan(
      props.media.width, props.media.height,
      props.project.canvas.width, props.project.canvas.height,
      c.pan,
    )
  } catch (e) {
    panError.value = e.message
  }
  changed()
}

watch(() => props.clip.id, () => { panError.value = '' })
</script>

<style scoped>
.inspector { margin-bottom: 16px; }
h2 { font-size: 14px; margin: 0 0 8px; }
.row { margin: 6px 0; }
.hint { font-size: 11px; color: #9aa0aa; margin-top: 2px; }
.warn {
  background: #4d3a17; border: 1px solid #8a6d1d; border-radius: 4px;
  padding: 6px 8px; font-size: 12px; margin: 8px 0;
}
.err { color: #ff8a8a; font-size: 12px; margin-top: 4px; }
.ok { color: #7fd67f; font-size: 12px; margin-top: 4px; }
.cropbox input[type=range] { width: 100%; }
.confirmrow { margin: 8px 0; }
.panhead { margin-top: 10px; display: flex; gap: 8px; align-items: center; }
.pangrid {
  display: grid; grid-template-columns: 40px 1fr 1fr; gap: 4px;
  align-items: center; margin-top: 6px; font-size: 12px; color: #9aa0aa;
}
.issues { margin-top: 10px; }
.issue { font-size: 12px; padding: 4px 6px; border-radius: 4px; margin-bottom: 4px; }
.issue.error { background: #4d1f1f; }
.issue.warning { background: #4d3a17; }
</style>
