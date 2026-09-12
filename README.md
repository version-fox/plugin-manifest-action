# vfox 插件公共发布流程

各插件独立维护源码、版本和 GitHub Release，公共仓库统一维护校验和发布逻辑。
更新这个仓库不会发布任何插件。

## 接入插件

将 [example-workflow.yml](example-workflow.yml) 复制为插件仓库的
`.github/workflows/publish.yaml`。插件侧只保留触发条件和公共流程引用：

```yaml
jobs:
  check:
    if: github.event_name == 'pull_request'
    uses: version-fox/plugin-manifest-action/.github/workflows/check-plugin.yml@v1
  release:
    if: github.event_name != 'pull_request'
    permissions:
      contents: write
    uses: version-fox/plugin-manifest-action/.github/workflows/release-plugin.yml@v1
    with:
      version: ${{ inputs.version || '' }}
```

完整示例包含 PR 检查、手动发布和版本 tag 推送三个入口。PR 只检查，不再通过
PR 标题触发发布。手动入口需要先合入默认分支，GitHub 才会显示 Run workflow 按钮。

检查任务只有 `contents: read` 权限，发布任务使用调用仓库的 `GITHUB_TOKEN` 和
`contents: write` 权限。不需要个人 token、GitHub App 或 Dependabot。仓库规则需要
允许机器人推送版本提交和 tag；公共流程不会绕过分支或标签保护。

## 发布一个插件

1. 将插件改动合入默认分支。
2. 打开 **Actions → Plugin → Run workflow**，选择默认分支。
3. 输入插件版本，例如 `0.5.5`，不带 `v`。

也可以通过 GitHub CLI 触发：

```bash
gh workflow run publish.yaml \
  --repo version-fox/vfox-java --ref main -f version=0.5.5
```

一次运行完成校验、打包、更新 `PLUGIN.version`、创建版本提交和 tag、发布附件及
manifest。版本提交和 tag 原子推送，不依赖机器人推送 tag 后再启动第二条流水线。

如果准备期间默认分支发生变化，流程会停止；重新启动一次发布以使用最新源码。
已有的 tag 发布方式仍然支持：先提交匹配的 `PLUGIN.version`，再推送 `vX.Y.Z`。
tag 发布不会修改标签中的源码。

初版接口只接受稳定版本 `X.Y.Z`，不接受预发布后缀或构建元数据。

## 插件包与检查范围

当前支持 Java、Node.js、Flutter 和模板使用的 `metadata.lua` + `hooks/` 布局。
遇到 `main.lua` 布局会明确报错，避免打包未经检查的入口。

- `metadata.lua` 必须定义 `PLUGIN`，包含 name、version、homepage、license 和
  description。自动修改版本要求文件中只有一个独立的 `PLUGIN.version = "X.Y.Z"`
  赋值，与模板一致。
- 必需文件 `hooks/available.lua`、`hooks/pre_install.lua`、`hooks/env_keys.lua`
  分别直接定义 `PLUGIN:Available`、`PLUGIN:PreInstall`、`PLUGIN:EnvKeys`。
- 包含 `metadata.lua`、`hooks/`、`lib/`，以及可选的 `LICENSE`、`LICENSE.md` 或
  `LICENSE.txt`。运行时数据文件放在 `lib/`。不包含开发文档、IDE 提示、workflow、
  Git 数据；不接受符号链接。
- 使用与 vfox 一致的 GopherLua（Lua 5.1）加载元数据并检查 Lua 语法。
  检查不会执行生命周期 hook 或下载安装 SDK，不能代替插件行为测试。
- ZIP 文件顺序、时间戳和权限固定；相同源码和工具链产生相同附件。
- metadata、tag、文件名和 manifest 的版本必须一致，manifest 包含 ZIP 的 SHA-256。

版本 Release 包含 ZIP 和该版本的 `manifest.json`，发布后不再改写。
独立的 `manifest` Release 保留原有可更新地址：
`releases/download/manifest/manifest.json`。注册表无需迁移，其现有定时同步仍决定
用户何时能从公共注册表看到新插件版本。

## 失败恢复

在原运行记录中选择 **Re-run failed jobs**，保留原来的源码 SHA。
重试只接受原始提交，或由它产生且只修改版本字段的发布提交。
同一版本不能被另一份源码复用。

版本附件先上传到草稿 Release。重试时验证已存在附件的内容，仅补齐草稿中缺失的
附件。正式版本附件不覆盖。版本正式发布并重新下载验证成功后，才更新稳定 manifest。
manifest 更新失败时，可以重跑原任务，无需再发一个插件版本。

同一插件的发布串行执行；旧版本重跑不会使 manifest 回退。如果替换 manifest 时
发生中断，已发布的 Latest 版本仍可用于阻止旧任务写回过期内容。

稳定 manifest 使用 GitHub 的附件替换机制（`gh --clobber`），替换期间可能短暂缺失。
对应版本的 ZIP 和不可变 manifest 仍保留；重跑最新发布任务可恢复稳定 manifest。
如果一个已经正式发布的版本 Release 缺少附件，工具会报错，而不会修改该正式版本。

## 更新公共发布工具

只有修改公共发布逻辑时，才需要在本仓库操作；这不会触发插件发版。

1. 将公共工具改动合入默认分支。
2. 打开 **Actions → Release Tool → Run workflow**，输入工具版本，例如 `1.0.1`。
3. 测试和 workflow 检查通过后，流程更新 `VERSION` 文件，创建版本提交、tag 和
   Release，再更新对应的主版本引用 `v1`。

插件保持引用 `@v1`，下次主动运行时使用新版。`@v1.0.1` 等具体版本标签不移动。
公共 workflow 通过 GitHub 的
[`job.workflow_sha`](https://docs.github.com/en/actions/reference/workflows-and-actions/contexts#job-context)
检出定义当前任务的同一提交，固定版本的 workflow 始终执行其配套代码。
发版只修改 `VERSION`，无需内置 token 不支持的 workflow 写权限。
这套流程面向 GitHub.com。不兼容的变化需要发布新的主版本。
主版本 tag 的更新使用 lease 检测外部并发修改，也拒绝由旧任务回退主版本。

具体版本标签关联 GitHub Release；可移动的主版本引用只创建 Git tag。
不要为可移动的 `v1` 创建不可变的 GitHub Release。

## 首次上线顺序

首次发布前，公共仓库还没有 `v1`：

1. 合入公共工具实现，确认 **Release Tool CI** 通过。
2. 手动运行 **Release Tool**，输入 `1.0.0`，生成 `v1.0.0` 和 `v1`。
3. 创建 Node.js 接入 PR，检查通过后合入，再接入 Java、Flutter 和模板。
   接入改动本身不会发布插件。
4. 下次有实际插件版本需要发布时，使用新手动入口。

公共 `v1` 可用之前，不要先合入引用它的插件 workflow。
模板只负责让新仓库默认接入；已有仓库仍需进行一次迁移。

## 本地开发与验证

使用 [go.mod](go.mod) 声明的 Go 版本。发布实现是一个独立 Go 命令，复用 vfox 使用的
Lua 解析器，无需在 runner 上安装系统 Lua、LuaRocks 或 dkjson。

```bash
go test ./... -race
GOTOOLCHAIN=auto go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
go run ./cmd/plugin-release check \
  --source ../vfox-nodejs --repository version-fox/vfox-nodejs --out /tmp/nodejs-check
```

`check` 只生成本地附件，不修改插件版本、提交、tag 或 GitHub Release。
默认测试使用临时 Git 仓库和内存发布存储，覆盖失败及重试，不连接 GitHub。
公共 CI 还会调用同一公共检查流程，读取 Java、Node.js、Flutter 和模板进行打包检查。
需要验证真实 gh 读取协议时，可显式运行只读检查：

```bash
VFOX_TEST_GITHUB_REPOSITORY=version-fox/vfox-nodejs \
  go test ./internal/release -run TestGitHubReadOnlyProtocol -v
```

## 原有 Composite Action

根目录 [action.yml](action.yml) 保持原实现，兼容仍使用
`uses: version-fox/plugin-manifest-action@main` 的现有仓库。
它不会自动获得新版 workflow 的版本更新、确定性打包和重试保证。
本次迁移通过上面的 reusable workflow 使用新实现，避免改变未迁移仓库的行为。

## 许可证

Apache 2.0。
