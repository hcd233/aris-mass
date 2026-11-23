# 常用命令

> **使用场景**：需要构建、测试、lint、清理、缓存预热时作为命令参考。

## 硬性约束

- **非用户明确要求，不允许新增 cobra 命令**（`cmd/server/`、`cmd/client/` 下的 `cobra.Command`）。
  需要新的运维动作时，优先考虑复用现有命令、cron 任务或环境变量/配置开关，并向用户说明方案。

- 构建：`make build`（含前端 + 服务端 + 四平台客户端）；单独构建服务端：`make build-server`；单独构建客户端：`make build-client`；四平台交叉编译：`make build-client-all`
- 直接构建服务端：`go build ./cmd/server`；直接构建客户端：`go build ./cmd/client`
- 规范扫描：`make lint`（conv + static 并发执行，底层使用独立入口 `go run ./cmd/lint`，不编译服务端二进制）；单独跑规范扫描：`make lint-conv`，静态检查：`make lint-static`；lint 与 server/client 平行，同属 `cmd/` 三入口之一
- 全量测试：`make test`（等价于 `go test -count=1 ./cmd/... ./internal/... ./test/...`，显式排除 `web/node_modules` 中的嵌套 Go 目录）
- 聚焦测试：`go test -v -count=1 -run TestFunctionName ./test/unit/<topic>/` 或 `./test/e2e/<topic>/`
- 前端 lint：`cd web && npm run lint`；自动修复：`npm run lint:fix`
- 前端类型检查：`cd web && npm run typecheck`（`tsc --noEmit`）
- 前端单测：`cd web && npm run test`（vitest）
- 前端 CI 门禁全量（lint + format:check + typecheck + test，与 `lint.yml` 的 `web-lint` job 一致）：`make web-lint`
- 前端格式化：`cd web && npm run format`（Prettier 写入，或 `make web-format`）；格式检查：`npm run format:check`（CI 用，或 `make web-format-check`）
- 前端构建（同时同步到 `internal/web/dist/`）：`make web-build`；清理产物：`make web-clean`
- 生产构建会自动包含前端：`make build` 在编译 Go 之前先跑 `web-build`
- UPX 极致压缩：`make build-upx`（需安装 upx）
- 编译缓存预热：`make warm-cache`（CI 加速）
- 全量清理：`make clean-all`（含 `go clean -cache`）

## Worktree 磁盘管理

从主工作区根目录调用 `script/worktree.py`，让前端依赖按 `package-lock.json` 哈希共享，避免每个工作树重复安装：

```sh
python3 script/worktree.py add .worktrees/<name> <branch> [base]
python3 script/worktree.py deps .worktrees/<name>
python3 script/worktree.py detach-deps .worktrees/<name>
python3 script/worktree.py clean .worktrees/<name>
python3 script/worktree.py clean --all
python3 script/worktree.py remove .worktrees/<name>
python3 script/worktree.py gc
```

`add` 不会为纯后端工作树安装前端依赖；只有需要前端时才运行 `deps`。依赖按锁文件 SHA-256 存在工作树共享缓存中，脚本会验证 npm 隐藏锁文件；不要在共享软链上直接运行 npm 安装命令。`make build`、`make build-server` 和 `make web-build` 会间接运行 `npm ci`，运行前先 `detach-deps`，完成后再运行 `deps`。其他依赖变更也先 `detach-deps`，再执行 `npm install`，最后运行 `deps`。`clean` 仅处理明确列出的生成目录、未跟踪 build info 和可识别的顶层可执行二进制；遇到软链、已跟踪文件、未知目录或不匹配的文件格式会跳过。`remove` 不会强制删除有未提交改动的工作树，且只回收带脚本标记且没有工作树或软链引用的共享缓存。
