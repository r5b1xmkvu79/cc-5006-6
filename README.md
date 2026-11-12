# 口述纪录片档案素材剪辑台

把横屏访谈、窄幅旧录像、静态照片和竖向手稿组合到统一输出画布。Go 后端调度真实媒体渲染（ffmpeg），Vue 前端提供构图与时间线。

## 架构

```
cmd/
  server/        HTTP API + 前端静态服务
  genassets/     生成带边缘标记的测试素材
  delivercheck/  交付管线：原件引用、工程重开、成片检查、画幅迁移验证
  migrate/       画幅工程迁移工具（旧格式 → 统一版本化媒体描述）
internal/
  geometry/      构图几何规则（唯一事实来源）
  media/         ffprobe 探测 + EXIF/旋转/像素比例归一化 + 版本化媒体描述
  project/       工程模型、校验、缺失素材占位
  render/        ffmpeg 滤镜图构建与导出
  migrate/       迁移转换器（可重复运行、可回退）
  api/           HTTP API
web/             Vue 3 前端（构图预览、时间线、片段设置）
```

## 核心规则

- **旋转先归一化**：容器旋转矩阵 / EXIF 方向先应用，再做构图；预览与输出共用
  `internal/geometry` 与 `web/src/geometry.js` 同一套规则（金样测试锁定一致）。
- **像素比例随旋转一同归一化**：非方形像素（如 DV 720x576 @ 64:45）先按 SAR
  拉伸到真实显示尺寸（1024x576）再构图；编码尺寸从不直接当显示尺寸。
- **三种构图**：完整保留（letterbox 全显）、裁切填充（cover + 编辑确认位置）、
  背景边框（完整前景 + 虚化底 + 可选边框）。裁切模式永远不声称完整展示；
  裁切位置未经编辑确认时导出被阻止。
- **时间分别建模**：视频片段用取段 in/out，照片用停留时长 dwell。
- **照片缓慢平移**：仅在裁切模式下，缩放 ≥1 且取景框全程在图内才有效，
  前后端同时校验。
- **缺失素材**：工程照常打开，保留占位与原因；正式导出默认阻止，
  显式允许后渲染占位画面并在清单中说明。
- **缩略图**仅用于预览，最终画面始终来自原件。

## 画幅工程迁移（旧格式 → 统一版本化媒体描述）

旧工程（格式 v1）把画幅、旋转分散在扁平字段里，像素比例完全没有记录；
新工程（格式 v2）为每个素材携带统一版本化媒体描述 `media[].description`
（描述版本 v1：编码尺寸、旋转/EXIF、像素比例、真实显示尺寸、时长、音轨）。

```bash
# 迁移（可重复运行；原工程永不修改，输出原子落盘）
go run ./cmd/migrate -in old_project.json -out migrated/project.json

# 重复运行已迁移工程进入核验模式：逐素材对照源文件复核描述，不改写文件
go run ./cmd/migrate -in migrated/project.json -out ignored.json

# 回退：依据迁移文件中的 legacy 信封逐字段还原旧格式
go run ./cmd/migrate -rollback -in migrated/project.json -out restored.json
```

契约与保障：

- **真实显示关系**：导入时按旧解释（旋转后编码尺寸、方形像素假定）解读旧记录，
  再从源文件探测像素比例，推导出真实显示尺寸；编码尺寸不直接当显示尺寸。
- **不接受默认值伪装成功**：源文件缺失/不可探测的素材标记 `pending`，不生成
  描述、不填默认值；CLI 以退出码 3 报告未完成（0 完成/核验通过，1 硬错误）。
- **未识别字段保留供回退**：未知顶层/素材字段原样存入 `legacy` 信封；
  因画幅语义变化而失效的裁切确认也存入信封并复位为待复核（导出会被阻止，
  直至编辑重新确认）。回退结果与旧工程逐字段一致（交付管线核验）。
- **中断安全**：输入只读，输出先写临时文件再原子改名；中断后原工程仍可使用。
- **语义保留判断**：报告按主体边界（片段可见源区域，旧解释 vs 新描述）与
  片段时长逐项给出 `preserved / adjusted / pending` 结论，时长模型不改动。
- **记录**：迁移报告 `<out>.report.json` 与 `deliver/migration/MIGRATION.md`
  记录工具版本（当前 1.0.0）、描述版本、ffprobe 版本、改动范围与回退过程。

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

依次执行：生成测试素材 → Go 单元/端到端测试（画幅、方向、像素比例、时长、
音轨拼接、标记像素核对）→ JS/Go 几何一致性 → 前端构建 → 交付管线
（`deliver/`：原件引用、工程重开、成片与检查报告）→ 画幅工程迁移
（`deliver/migration/`：旧格式样例迁移、重复运行核验、回退逐字段核验、
迁移后工程在目标路径实际导出与帧级检查）。
