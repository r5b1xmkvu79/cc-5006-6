<template>
  <div class="timeline">
    <div class="track">
      <div
        v-for="(c, i) in project.clips"
        :key="c.id"
        class="clip"
        :class="{ sel: c.id === selectedId, missing: isMissing(c) }"
        :style="{ width: widthOf(c) + 'px' }"
        @click="$emit('select', c.id)"
      >
        <div class="cliplabel">{{ c.label }}</div>
        <div class="clipmeta">
          <span class="badge" :class="fitBadge(c).cls">{{ fitBadge(c).text }}</span>
          <span class="dur">{{ durationOf(c).toFixed(1) }}s</span>
        </div>
        <div class="clipops" @click.stop>
          <button class="ghost" :disabled="i === 0" @click="move(i, -1)">←</button>
          <button class="ghost" :disabled="i === project.clips.length - 1" @click="move(i, 1)">→</button>
          <button class="danger" @click="remove(i)">✕</button>
        </div>
      </div>
      <div v-if="!project.clips.length" class="empty">从素材库加入片段</div>
    </div>
  </div>
</template>

<script setup>
const props = defineProps({
  project: { type: Object, required: true },
  selectedId: { type: String, default: null },
})
const emit = defineEmits(['select', 'changed'])

const PX_PER_SEC = 14

function durationOf(c) {
  return c.kind === 'photo' ? c.dwell : c.out - c.in
}
function widthOf(c) {
  return Math.max(64, durationOf(c) * PX_PER_SEC)
}
function isMissing(c) {
  const m = props.project.media.find((x) => x.id === c.mediaId)
  return !m || m.status !== 'ok'
}
// The badge states the display claim honestly: crop mode never claims
// complete display.
function fitBadge(c) {
  if (c.fit === 'crop') return { text: '裁切', cls: 'warn' }
  if (c.fit === 'border') return { text: '边框·完整', cls: 'ok' }
  return { text: '完整', cls: 'ok' }
}
function move(i, d) {
  const clips = props.project.clips
  const [c] = clips.splice(i, 1)
  clips.splice(i + d, 0, c)
  emit('changed')
}
function remove(i) {
  props.project.clips.splice(i, 1)
  emit('changed')
}
</script>

<style scoped>
.timeline { border-top: 1px solid #333; background: #202227; padding: 8px; }
.track { display: flex; gap: 4px; overflow-x: auto; min-height: 74px; }
.clip {
  flex: none; background: #2c2f37; border: 2px solid #3a3d44; border-radius: 6px;
  padding: 6px; cursor: pointer; overflow: hidden;
}
.clip.sel { border-color: #2f6fed; }
.clip.missing { border-style: dashed; border-color: #d83a3a; }
.cliplabel {
  font-size: 12px; white-space: nowrap; overflow: hidden;
  text-overflow: ellipsis; max-width: 160px;
}
.clipmeta { display: flex; gap: 4px; align-items: center; margin: 4px 0; }
.dur { font-size: 11px; color: #9aa0aa; }
.clipops { display: flex; gap: 3px; }
.clipops button { padding: 1px 6px; font-size: 11px; }
.empty { color: #666; align-self: center; padding: 0 20px; }
</style>
