<template>
  <div class="bin">
    <h2>素材库</h2>
    <div class="scan">
      <button class="ghost" @click="scan" :disabled="scanning">
        {{ scanning ? '扫描中…' : '扫描媒体库' }}
      </button>
      <div v-if="scanError" class="err">{{ scanError }}</div>
    </div>
    <div v-if="available.length" class="avail">
      <div v-for="f in available" :key="f" class="availrow">
        <span class="path" :title="f">{{ basename(f) }}</span>
        <button @click="importOne(f)" :disabled="imported.has(f)">
          {{ imported.has(f) ? '已导入' : '导入' }}
        </button>
      </div>
    </div>

    <h3>已导入 ({{ project.media.length }})</h3>
    <div v-for="m in project.media" :key="m.id" class="media" :class="{ missing: m.status !== 'ok' }">
      <img
        v-if="m.status === 'ok'"
        :src="thumbnailUrl(m)"
        class="thumb"
        loading="lazy"
        alt=""
      />
      <div v-else class="thumb placeholder">缺失</div>
      <div class="meta">
        <div class="name" :title="m.title || m.id">{{ m.title || m.id }}</div>
        <div class="dims">
          {{ m.width }}×{{ m.height }}
          <span v-if="m.rotation" class="badge warn">旋转 {{ m.rotation }}°</span>
          <span v-if="m.kind === 'video'" class="badge info">{{ m.duration.toFixed(1) }}s</span>
          <span v-else class="badge info">照片</span>
          <span v-if="m.kind === 'video' && !m.hasAudio" class="badge warn">无音轨</span>
        </div>
        <div v-if="m.status !== 'ok'" class="err">{{ m.reason }}</div>
        <button class="ghost" @click="$emit('add-clip', m)">＋ 加入时间线</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { api } from '../api.js'

const props = defineProps({ project: { type: Object, required: true } })
const emit = defineEmits(['imported', 'add-clip'])

const available = ref([])
const imported = ref(new Set())
const scanning = ref(false)
const scanError = ref('')

const basename = (p) => p.split('/').pop()
const thumbnailUrl = (m) => api.thumbnailUrl(props.project.id, m.id)

async function scan() {
  scanning.value = true
  scanError.value = ''
  try {
    const res = await fetch('/api/media/scan').then((r) => r.json())
    available.value = res.files || []
    imported.value = new Set(
      props.project.media.map((m) => m.path),
    )
  } catch (e) {
    scanError.value = e.message
  } finally {
    scanning.value = false
  }
}

async function importOne(path) {
  try {
    const m = await api.importMedia(props.project.id, path)
    emit('imported', m)
    imported.value = new Set([...imported.value, path])
  } catch (e) {
    scanError.value = `导入失败: ${e.message}`
  }
}
</script>

<style scoped>
h2 { font-size: 14px; margin: 0 0 8px; }
h3 { font-size: 12px; color: #9aa0aa; margin: 14px 0 6px; }
.avail { margin: 8px 0; border: 1px solid #333; border-radius: 6px; padding: 6px; }
.availrow { display: flex; justify-content: space-between; align-items: center; gap: 6px; padding: 2px 0; }
.path { font-size: 12px; color: #9aa0aa; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.media { display: flex; gap: 8px; padding: 6px 0; border-bottom: 1px solid #2a2c31; }
.media.missing { opacity: 0.75; }
.thumb { width: 72px; height: 48px; object-fit: cover; border-radius: 4px; background: #000; flex: none; }
.thumb.placeholder { display: flex; align-items: center; justify-content: center; color: #d83a3a; font-size: 12px; border: 1px dashed #d83a3a; }
.meta { min-width: 0; }
.name { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 170px; }
.dims { font-size: 11px; color: #9aa0aa; margin: 3px 0; }
.err { color: #ff8a8a; font-size: 12px; margin: 4px 0; }
</style>
