<template>
  <div class="app">
    <header>
      <h1>口述纪录片档案剪辑台</h1>
      <div class="projbar">
        <input v-model="projectId" placeholder="工程 ID" @keyup.enter="openProject" />
        <button @click="openProject">打开 / 新建</button>
        <template v-if="project">
          <input v-model="project.title" class="title" placeholder="工程标题" @change="save" />
          <span class="badge info">画布 {{ project.canvas.width }}×{{ project.canvas.height }} @{{ project.canvas.fps }}fps</span>
          <span class="badge">总时长 {{ totalDuration.toFixed(1) }}s</span>
          <span v-if="saved" class="badge ok">已保存</span>
          <span v-else class="badge warn">保存中…</span>
        </template>
      </div>
    </header>

    <main v-if="project">
      <section class="left">
        <MediaBin
          :project="project"
          @imported="onImported"
          @add-clip="addClip"
        />
      </section>

      <section class="center">
        <PreviewCanvas
          v-if="selectedClip"
          :project="project"
          :clip="selectedClip"
          :media="mediaOf(selectedClip)"
          @update-crop="onCropDrag"
        />
        <div v-else class="nopreview">选择时间线上的片段进行构图</div>

        <Timeline
          :project="project"
          :selected-id="selectedClipId"
          @select="selectedClipId = $event"
          @changed="save"
        />
      </section>

      <section class="right">
        <Inspector
          v-if="selectedClip"
          :project="project"
          :clip="selectedClip"
          :media="mediaOf(selectedClip)"
          :issues="issuesFor(selectedClip.id)"
          @changed="save"
        />
        <ExportPanel
          :project="project"
          :issues="issues"
          @validate="runValidate"
          @export="runExport"
        />
      </section>
    </main>

    <div v-if="error" class="errorbar">{{ error }}</div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { api } from './api.js'
import MediaBin from './components/MediaBin.vue'
import Timeline from './components/Timeline.vue'
import Inspector from './components/Inspector.vue'
import PreviewCanvas from './components/PreviewCanvas.vue'
import ExportPanel from './components/ExportPanel.vue'

const projectId = ref('demo')
const project = ref(null)
const selectedClipId = ref(null)
const error = ref('')
const saved = ref(true)
const issues = ref([])

const totalDuration = computed(() =>
  (project.value?.clips || []).reduce((s, c) => s + clipDuration(c), 0))

const selectedClip = computed(() =>
  project.value?.clips.find((c) => c.id === selectedClipId.value))

function clipDuration(c) {
  return c.kind === 'photo' ? c.dwell : c.out - c.in
}

function mediaOf(clip) {
  return project.value?.media.find((m) => m.id === clip.mediaId) || null
}

function issuesFor(clipId) {
  return issues.value.filter((i) => i.clipId === clipId)
}

async function openProject() {
  error.value = ''
  try {
    project.value = await api.getProject(projectId.value)
  } catch (e) {
    if (e.status !== 404) { error.value = e.message; return }
    project.value = await api.createProject(projectId.value, '未命名口述纪录片')
  }
  selectedClipId.value = project.value.clips[0]?.id || null
  runValidate()
}

let saveTimer = null
function save() {
  saved.value = false
  clearTimeout(saveTimer)
  saveTimer = setTimeout(async () => {
    try {
      project.value = await api.saveProject(project.value)
      saved.value = true
    } catch (e) {
      error.value = '保存失败: ' + e.message
    }
  }, 600)
}

function onImported(media) {
  project.value.media.push(media)
}

function addClip(media) {
  const n = project.value.clips.length + 1
  const clip = media.kind === 'image'
    ? {
        id: 'c' + Date.now(), mediaId: media.id, kind: 'photo',
        label: media.title || media.id, dwell: 3, fit: 'full',
        crop: { x: 0.5, y: 0.5, confirmed: false },
        border: { blur: 20, padding: 0, color: 'black' },
      }
    : {
        id: 'c' + Date.now(), mediaId: media.id, kind: 'video',
        label: media.title || media.id, in: 0, out: Math.min(media.duration, 10),
        fit: 'full', crop: { x: 0.5, y: 0.5, confirmed: false },
        border: { blur: 20, padding: 0, color: 'black' },
      }
  project.value.clips.push(clip)
  selectedClipId.value = clip.id
  save()
}

function onCropDrag({ x, y }) {
  const c = selectedClip.value
  if (!c) return
  c.crop = { ...c.crop, x, y, confirmed: false } // moved crop must be re-confirmed
  save()
}

async function runValidate(allowPlaceholder = false) {
  if (!project.value) return
  try {
    const r = await api.validate(project.value.id, allowPlaceholder)
    issues.value = r.issues || []
    return r
  } catch (e) {
    error.value = e.message
  }
}

async function runExport(allowPlaceholder) {
  error.value = ''
  try {
    const manifest = await api.export(project.value.id, allowPlaceholder)
    return manifest
  } catch (e) {
    if (e.issues) issues.value = e.issues
    error.value = e.message
    throw e
  }
}

openProject()
</script>

<style scoped>
.app { display: flex; flex-direction: column; height: 100vh; }
header { padding: 8px 16px; background: #22242a; border-bottom: 1px solid #333; }
header h1 { font-size: 16px; margin: 0 0 6px; }
.projbar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.projbar .title { width: 320px; }
main { flex: 1; display: grid; grid-template-columns: 280px 1fr 320px; min-height: 0; }
.left { border-right: 1px solid #333; overflow-y: auto; padding: 10px; }
.center { display: flex; flex-direction: column; min-width: 0; }
.right { border-left: 1px solid #333; overflow-y: auto; padding: 10px; }
.nopreview {
  flex: 1; display: flex; align-items: center; justify-content: center; color: #666;
}
.errorbar {
  position: fixed; bottom: 0; left: 0; right: 0; padding: 8px 16px;
  background: #8a2e2e; white-space: pre-wrap; max-height: 30vh; overflow-y: auto;
}
</style>
