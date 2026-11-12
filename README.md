# 口述纪录片档案素材剪辑台

把横屏访谈、窄幅旧录像、静态照片和竖向手稿组合到统一输出画布。Go 后端调度真实媒体渲染（ffmpeg），Vue 前端提供构图与时间线。

## 架构

```
cmd/
  server/        HTTP API + 前端静态服务
  genassets/     生成带边缘标记的测试素材
  delivercheck/  交付管线：原件引用、工程重开、成片检查
internal/
  geometry/      构图几何规则（唯一事实来源）
  media/         ffprobe 探测 + EXIF/旋转归一化
  project/       工程模型、校验、缺失素材占位
  render/        ffmpeg 滤镜图构建与导出
  api/           HTTP API
web/             Vue 3 前端（构图预览、时间线、片段设置）
```

## 核心规则

- **旋转先归一化**：容器旋转矩阵 / EXIF 方向先应用，再做构图；预览与输出共用
  `internal/geometry` 与 `web/src/geometry.js` 同一套规则（金样测试锁定一致）。
- **三种构图**：完整保留（letterbox 全显）、裁切填充（cover + 编辑确认位置）、
  背景边框（完整前景 + 虚化底 + 可选边框）。裁切模式永远不声称完整展示；
  裁切位置未经编辑确认时导出被阻止。
- **时间分别建模**：视频片段用取段 in/out，照片用停留时长 dwell。
- **照片缓慢平移**：仅在裁切模式下，缩放 ≥1 且取景框全程在图内才有效，
  前后端同时校验。
- **缺失素材**：工程照常打开，保留占位与原因；正式导出默认阻止，
  显式允许后渲染占位画面并在清单中说明。
- **缩略图**仅用于预览，最终画面始终来自原件。

## 运行

```bash
# 工具链（已在本仓库 .toolchain/，或自行准备 go1.23+ 与 ffmpeg7）
export PATH=$PWD/.toolchain/go/go/bin:$PATH
export ORALCUT_FFMPEG=$PWD/.toolchain/ffmpeg/ffmpeg-7.0.2-amd64-static/ffmpeg
export ORALCUT_FFPROBE=$PWD/.toolchain/ffmpeg/ffmpeg-7.0.2-amd64-static/ffprobe

# 后端（服务 web/dist 与 /api）
npm --prefix web install && npm --prefix web run build
go run ./cmd/server -addr :8080 -data data -media-root assets -static web/dist

# 前端开发（代理 /api 到 8080）
npm --prefix web run dev
```

## 验证

```bash
./scripts/run_tests.sh
```

依次执行：生成测试素材 → Go 单元/端到端测试（画幅、方向、时长、音轨拼接、
标记像素核对）→ JS/Go 几何一致性 → 前端构建 → 交付管线
（`deliver/`：原件引用、工程重开、成片与检查报告）。
