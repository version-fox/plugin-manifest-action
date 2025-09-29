# Plugin Manifest Action

用于 vfox 插件的 GitHub Action，自动生成和发布插件 manifest 文件和发布包。

## 功能特性

- 🚀 自动生成 manifest.json 文件
- 📦 自动打包插件文件
- 🏷️ 支持 tag 推送触发发布
- 🔀 支持 PR 合并触发发布（通过 PR 标题识别版本号）
- ⚙️ 可配置的参数

## 使用方法

### 1. 作为 Composite Action 使用

在你的仓库中创建 workflow 文件（如 `.github/workflows/publish.yml`）：

#### 方式 A：直接使用（推荐）

```yaml
name: Publish Plugin

on:
  push:
    tags: ['v*']

permissions:
  contents: write

jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        
      - name: Extract version
        run: |
          VERSION=$(echo ${{ github.ref }} | cut -d'/' -f3 | cut -c2-)
          echo "VERSION=$VERSION" >> $GITHUB_ENV
          
      - name: Publish manifest and release
        uses: version-fox/plugin-manifest-action@main
        with:
          version: ${{ env.VERSION }}
          github-token: ${{ secrets.GITHUB_TOKEN }}
```

#### 方式 B：指定完整路径

```yaml
      - name: Publish manifest and release
        uses: version-fox/plugin-manifest-action/.github/actions/publish-manifest@main
        with:
          version: ${{ env.VERSION }}
          github-token: ${{ secrets.GITHUB_TOKEN }}
```

### 2. 完整的 Workflow 模板

复制 `.github/workflows/publish-manifest.yml` 到你的仓库，支持：

- **Tag 推送触发**: 推送 `v1.2.3` 格式的 tag 自动发布
- **PR 合并触发**: PR 标题为 `Release v1.2.3` 格式时，合并后自动发布

```yaml
name: Generate and Publish Manifest

on:
  push:
    tags: [ 'v*' ]
  pull_request:
    types: [closed]
    branches: [main, master]

permissions:
  contents: write

jobs:
  publish:
    runs-on: ubuntu-latest
    if: github.event_name == 'push' || (github.event.pull_request.merged == true && startsWith(github.event.pull_request.title, 'Release v'))
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Extract version from tag
        if: github.event_name == 'push'
        run: |
          VERSION=$(echo ${{ github.ref }} | cut -d'/' -f3 | cut -c2-)
          echo "VERSION=$VERSION" >> $GITHUB_ENV

      - name: Extract version from PR title
        if: github.event_name == 'pull_request'
        run: |
          PR_TITLE="${{ github.event.pull_request.title }}"
          VERSION=$(echo "$PR_TITLE" | grep -oP 'Release v\K[0-9]+\.[0-9]+\.[0-9]+')
          echo "VERSION=$VERSION" >> $GITHUB_ENV

      - name: Validate version
        run: |
          if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "Invalid version format: $VERSION"
            exit 1
          fi
          echo "Publishing version: $VERSION"

      - name: Publish manifest and release
        uses: version-fox/plugin-manifest-action@main
        with:
          version: ${{ env.VERSION }}
          github-token: ${{ secrets.GITHUB_TOKEN }}
```

## 输入参数

| 参数 | 描述 | 必需 | 默认值 |
|------|------|------|--------|
| `version` | 要发布的版本号（不包含 v 前缀） | ✅ | - |
| `github-token` | GitHub token 用于发布 release | ✅ | `${{ github.token }}` |
| `lua-version` | Lua 版本 | ❌ | `5.3.5` |
| `exclude-files` | 压缩时排除的文件（glob 模式） | ❌ | `*.git* manifest.json` |

## 输出参数

| 参数 | 描述 |
|------|------|
| `manifest-path` | 生成的 manifest.json 文件路径 |
| `zip-path` | 生成的压缩包文件路径 |

## 前置条件

你的插件仓库需要包含：

1. **metadata.lua** 文件 - 定义了 `PLUGIN` 全局变量，包含插件元数据
2. **有效的 Lua 代码** - action 会使用 Lua 加载 metadata 文件

metadata.lua 示例：
```lua
PLUGIN = {
    name = "my-plugin",
    author = "your-name",
    version = "1.0.0",
    description = "Plugin description",
    -- 其他元数据...
}
```

## 触发方式

### 1. Tag 推送
```bash
git tag v1.2.3
git push origin v1.2.3
```

### 2. PR 合并
创建标题为 `Release v1.2.3` 的 PR，合并后自动触发发布流程。

## 发布内容

Action 会创建以下内容：

1. **Plugin Release** - 标签为 `v{version}`，包含插件压缩包
2. **Manifest Release** - 标签为 `manifest`，包含 manifest.json 文件
3. **Artifacts** - 在 workflow 运行中保存 manifest.json 文件

## 许可证

Apache 2.0 License
