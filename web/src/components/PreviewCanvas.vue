<template>
  <div class="preview">
    <canvas
      ref="cv"
      :width="project.canvas.width"
      :height="project.canvas.height"
      @pointerdown="onDown"
      @pointermove="onMove"
      @pointerup="onUp"
    ></canvas>
    <div class="bar">
      <button class="ghost" @click="togglePlay">{{ playing ? '⏸ 暂停' : '▶ 播放' }}</button>
      <span class="hint">{{ hint }}</span>
    </div>
    <video
      v-if="media && media.kind === 'video' && media.status === 'ok'"
      ref="vid"
      :src="fileUrl"
      style="display:none"
      muted
      preload="auto"
      @loadeddata="onVideoReady"
    ></video>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api.js'
import { layoutFor, panViewport, even } from '../geometry.js'

const props = defineProps({
  project: { type: Object, required: true },
  clip: { type: Object, required: true },
  media: { type: Object, default: null },
})
const emit = defineEmits(['update-crop'])

const cv = ref(null)
const vid = ref(null)
const playing = ref(false)
const img = ref(null)

const fileUrl = computed(() =>
  props.media ? api.mediaFileUrl(props.project.id, props.media.id) : '')

const hint = computed(() => {
  if (!props.media) return '素材引用缺失'
  if (props.media.status !== 'ok') return '素材缺失：' + props.media.reason
  if (props.clip.fit === 'crop') return '拖动画布调整裁切位置（调整后需重新确认）'
  return '预览与输出使用同一套几何规则'
})

let raf = 0
let panStart = 0

function draw() {
  const canvas = cv.value
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  const cw = props.project.canvas.width
  const ch = props.project.canvas.height
  ctx.save()
  ctx.clearRect(0, 0, cw, ch)

  const m = props.media
  if (!m || m.status !== 'ok') {
    // Placeholder slate mirrors the export-time placeholder.
    ctx.fillStyle = '#2b2b30'
    ctx.fillRect(0, 0, cw, ch)
    ctx.strokeStyle = '#d83a3a'
    ctx.lineWidth = 16
    ctx.strokeRect(8, 8, cw - 16, ch - 16)
    ctx.fillStyle = '#fff'
    ctx.font = '42px sans-serif'
    ctx.fillText('素材缺失', 60, ch / 2 - 30)
    ctx.font = '28px sans-serif'
    ctx.fillText((m && m.reason) || 'media reference missing', 60, ch / 2 + 20)
    ctx.fillText('正式导出将被阻止，或经确认后以此占位', 60, ch / 2 + 70)
    ctx.restore()
    return
  }

  const src = m.kind === 'image' ? img.value : vid.value
  if (!src) { ctx.restore(); return }
  const sw = m.width, sh = m.height // normalized dimensions from probe
  const c = props.clip

  try {
    if (c.fit === 'border') {
      drawBorder(ctx, src, sw, sh, cw, ch, c)
    } else if (c.fit === 'crop') {
      drawCrop(ctx, src, sw, sh, cw, ch, c)
    } else {
      drawFull(ctx, src, sw, sh, cw, ch, c)
    }
  } catch (e) {
    ctx.fillStyle = '#4d1f1f'
    ctx.fillRect(0, 0, cw, ch)
    ctx.fillStyle = '#fff'
    ctx.font = '30px sans-serif'
    ctx.fillText('构图无效: ' + e.message, 40, ch / 2)
  }
  ctx.restore()
}

function drawFull(ctx, src, sw, sh, cw, ch) {
  const l = layoutFor(sw, sh, cw, ch, 'full')
  ctx.fillStyle = props.project.canvas.background || 'black'
  ctx.fillRect(0, 0, cw, ch)
  ctx.drawImage(src, 0, 0, srcWidth(src), srcHeight(src), l.fg.x, l.fg.y, l.fg.w, l.fg.h)
}

function drawCrop(ctx, src, sw, sh, cw, ch, c) {
  let vis
  if (c.pan) {
    const dur = c.dwell || 1
    const t = playing.value ? ((performance.now() - panStart) / 1000 / dur) % 1 : 0.5
    const { vx, vy, scale } = panViewport(sw, sh, cw, ch, c.pan, t)
    vis = { x: vx / scale, y: vy / scale, w: cw / scale, h: ch / scale }
  } else {
    vis = layoutFor(sw, sh, cw, ch, 'crop', c.crop.x, c.crop.y).srcVisible
  }
  ctx.fillStyle = 'black'
  ctx.fillRect(0, 0, cw, ch)
  ctx.drawImage(src, vis.x, vis.y, vis.w, vis.h, 0, 0, cw, ch)
}

function drawBorder(ctx, src, sw, sh, cw, ch, c) {
  const pad = c.border?.padding || 0
  const blur = c.border?.blur || 20
  // Background: cover + blur (approximates ffmpeg boxblur).
  const s = Math.max(cw / sw, ch / sh)
  const bw = even(sw * s), bh = even(sh * s)
  ctx.save()
  ctx.filter = `blur(${blur}px)`
  ctx.drawImage(src, 0, 0, srcWidth(src), srcHeight(src),
    (cw - bw) / 2, (ch - bh) / 2, bw, bh)
  ctx.restore()
  // Foreground: fit inside canvas minus padding, centered.
  const fs = Math.min((cw - 2 * pad) / sw, (ch - 2 * pad) / sh)
  const fw = even(sw * fs), fh = even(sh * fs)
  const fx = (cw - fw) / 2, fy = (ch - fh) / 2
  if (pad > 0) {
    ctx.fillStyle = c.border?.color || 'black'
    ctx.fillRect(fx - pad, fy - pad, fw + 2 * pad, fh + 2 * pad)
  }
  ctx.drawImage(src, 0, 0, srcWidth(src), srcHeight(src), fx, fy, fw, fh)
}

function srcWidth(src) {
  return src.naturalWidth || src.videoWidth
}
function srcHeight(src) {
  return src.naturalHeight || src.videoHeight
}

// --- media loading ---

function loadImage() {
  if (props.media?.kind !== 'image' || props.media.status !== 'ok') return
  const im = new Image()
  im.crossOrigin = 'anonymous'
  im.src = fileUrl.value
  im.onload = () => { img.value = im; draw() }
}

function onVideoReady() {
  const v = vid.value
  if (v) v.currentTime = props.clip.in || 0
}

// --- playback ---

function togglePlay() {
  if (props.media?.kind === 'video') {
    const v = vid.value
    if (!v) return
    if (playing.value) { v.pause(); playing.value = false; return }
    v.currentTime = props.clip.in || 0
    v.play()
    playing.value = true
    const tick = () => {
      if (!playing.value) return
      if (v.currentTime >= props.clip.out) { v.pause(); playing.value = false; return }
      draw()
      raf = requestAnimationFrame(tick)
    }
    tick()
  } else if (props.clip.pan) {
    panStart = performance.now()
    playing.value = true
    const tick = () => {
      if (!playing.value) return
      draw()
      raf = requestAnimationFrame(tick)
    }
    tick()
  }
}

// --- crop dragging ---

let dragging = false
let last = null

function canvasScale() {
  const canvas = cv.value
  return canvas ? canvas.getBoundingClientRect().width / canvas.width : 1
}

function onDown(e) {
  if (props.clip.fit !== 'crop' || props.clip.pan) return
  dragging = true
  last = { x: e.clientX, y: e.clientY }
}
function onMove(e) {
  if (!dragging || !props.media) return
  const m = props.media
  const cw = props.project.canvas.width
  const ch = props.project.canvas.height
  const k = canvasScale()
  const l = layoutFor(m.width, m.height, cw, ch, 'crop', props.clip.crop.x, props.clip.crop.y)
  const overX = (m.width * l.scale - cw) * k
  const overY = (m.height * l.scale - ch) * k
  const dx = (e.clientX - last.x) / (overX || 1)
  const dy = (e.clientY - last.y) / (overY || 1)
  last = { x: e.clientX, y: e.clientY }
  const clamp = (v) => Math.min(1, Math.max(0, v))
  emit('update-crop', {
    x: clamp(props.clip.crop.x - dx),
    y: clamp(props.clip.crop.y - dy),
  })
}
function onUp() { dragging = false }

watch(() => [props.clip.id, props.media?.id], () => {
  img.value = null
  playing.value = false
  loadImage()
  draw()
})
watch(() => JSON.stringify([props.clip.fit, props.clip.crop, props.clip.pan, props.clip.border, props.clip.in, props.clip.out, props.clip.dwell]), draw)

onMounted(() => { loadImage(); draw() })
onBeforeUnmount(() => cancelAnimationFrame(raf))
</script>

<style scoped>
.preview { flex: 1; display: flex; flex-direction: column; min-height: 0; padding: 10px; }
canvas {
  width: 100%; max-height: calc(100% - 44px); object-fit: contain;
  background: #000; border-radius: 6px; touch-action: none;
}
.bar { display: flex; gap: 10px; align-items: center; padding-top: 8px; }
.hint { font-size: 12px; color: #9aa0aa; }
</style>
