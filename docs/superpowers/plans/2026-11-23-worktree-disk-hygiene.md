# 工作树磁盘占用治理实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 回收重复的前端依赖与构建缓存，并让后续工作树按依赖锁文件共享安装、按生命周期回收产物。

**Architecture:** 新增 `script/worktree.py` 统一处理工作树创建、前端依赖、构建产物清理、工作树移除与孤儿共享缓存回收。仅工作树间按 `web/package-lock.json` 的 SHA-256 分组共享依赖，主工作区保留自己的 `node_modules`；缓存同时验证 npm 隐藏锁文件并带有管理标记。缓存变更由进程级文件锁串行化。

**Tech Stack:** Python 3 标准库、Git、npm。

**Spec:** 用户已在对话中批准：清理可再生成目录、只共享锁文件一致的依赖、增加工作树初始化防护，并保留工作树与分支内容。

## Global Constraints

- 现有工作树、分支和非生成文件全部保留。
- `node_modules` 只在 `package-lock.json` 完全一致时共享。
- 工作树不得嵌套在另一个已登记工作树中；清理和移除还要保护已有的嵌套登记项。
- `clean` 只移除白名单中的实体构建目录（含 `build/client/`）、未跟踪 build info 和通过格式识别的顶层可执行文件；不沿软链递归，不删除未知目录。
- `gc` 只移除脚本标记的哈希缓存，并参考工作树锁文件和实际软链目标。
- 工作树移除不使用 `--force`；Git 判定有未提交改动时停止。
- 不修改 Go 源码，不新增或运行测试。
- `web/.next`、`web/out`、`web/dist`、`internal/web/dist`、Go/静态分析缓存与编译二进制可再生成。

## Review Focus

- 锁文件不同的工作树不会复用错误的 `node_modules`；按哈希查缓存并验证 npm 隐藏锁文件。
- 相对软链在多层 `.worktrees/` 分组路径下仍能指向项目内共享缓存；禁止工作树彼此嵌套。
- 共享缓存仅在没有任何现存工作树锁文件或软链引用其哈希时回收；仅删除带脚本标记的缓存。
- 清理命令不会删除工作树源码、未跟踪文件或共享 `node_modules`；不跟随软链，跳过未知目录和 Git 已跟踪路径。
- 工作树移除遇到 Git 脏状态时不会强制删除；省略 `--force` 并检查失败路径。

---

### Task 1: 工作树依赖和缓存管理脚本

**Files:**
- Create: `script/worktree.py`

**Interfaces:**
- `add <path> <branch> [base]` 创建 `.worktrees/` 下的新工作树，并仅在已有兼容依赖缓存时建立软链。
- `deps <path>` 按锁文件哈希共享依赖；缓存未命中时执行一次 `npm ci` 并登记到共享缓存。
- `detach-deps <path>` 只解除当前工作树的共享依赖软链，供依赖更新前使用。
- `clean <path|--all>` 清理白名单中的生成产物和本地构建缓存，不动源码与依赖。
- `remove <path>` 以 Git 默认安全检查移除工作树，成功后回收无引用的共享依赖缓存。
- `gc` 单独回收无现存工作树引用的共享依赖缓存。

- [x] 写入脚本，校验目标路径位于 `.worktrees/` 且 Git 忽略该目录。
- [x] 使用锁文件 SHA-256 作为缓存键；仅工作树间共享，验证 npm 隐藏锁文件并用自动释放的文件锁串行化。
- [x] `clean` 仅处理列明的构建输出、Go/静态分析缓存、build info 和识别出的顶层可执行文件。
- [x] `remove` 不传 `--force`，只在 Git 成功移除后运行共享缓存回收。
- [x] 提供 `detach-deps`，支持先解除软链再更新锁文件与依赖。
- [x] 使用 Python 语法解析和 `--help` 检查命令入口（未运行测试）。

### Task 2: 更新项目工作树规范

**Files:**
- Modify: `docs/agents/workflow.md`
- Modify: `docs/agents/commands.md`

- [x] 将手工 `git worktree add` 与复制依赖的指引替换为新脚本命令。
- [x] 说明只在前端任务中调用 `deps`，Next 16 跨工作树构建继续使用 `--webpack`。
- [x] 说明依赖更新的 detach 流程、清理白名单、完成后执行不带强制选项的 `remove`；默认 Go 缓存保持全局，不在工作树设置 `GOCACHE`。

### Task 3: 清理现有生成物并合并重复依赖

**Files:**
- No tracked source changes; generated directories in existing worktrees only.

- [x] 对 5 个锁文件完全一致的旧工作树运行 `deps`，保留一份安装并将其余工作树链接到哈希缓存。
- [x] 清理主工作区及现存工作树中的确认可再生成产物。
- [x] 复查 5 个工作树都指向同一依赖缓存，锁文件不同的工作树没有被链接到该缓存。
- [x] 重新统计占用并记录释放空间；未删除工作树目录或分支。
