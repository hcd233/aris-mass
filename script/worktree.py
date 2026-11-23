#!/usr/bin/env python3
"""Create and maintain lightweight Git worktrees for aris-proxy-api."""

from __future__ import annotations

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
import platform
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys


def run_git(root: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", "-C", str(root), *args],
        check=check,
        capture_output=True,
        text=True,
    )


def find_repo_root() -> Path:
    checkout = Path(__file__).resolve().parents[1]
    common_dir = Path(
        run_git(checkout, "rev-parse", "--path-format=absolute", "--git-common-dir").stdout.strip()
    )
    return common_dir.parent.resolve()


ROOT = find_repo_root()
WORKTREE_BASE = ROOT / ".worktrees"
CACHE_ROOT = WORKTREE_BASE / ".cache" / "web"
LOCK_MARKER = ".aris-worktree-lock-sha256"
INSTALL_MARKER = ".aris-worktree-installing"
BACKUP_LINK = ".aris-worktree-previous-node_modules"
HASH_PATTERN = re.compile(r"[0-9a-f]{64}\Z")
def registered_worktrees() -> list[Path]:
    result = run_git(ROOT, "worktree", "list", "--porcelain")
    paths = [line.removeprefix("worktree ") for line in result.stdout.splitlines() if line.startswith("worktree ")]
    return [Path(path).resolve() for path in paths]


def resolve_path(raw: str) -> Path:
    path = Path(raw)
    if not path.is_absolute():
        path = ROOT / path
    return path.resolve()


def is_within(path: Path, parent: Path) -> bool:
    return path.is_relative_to(parent)


def require_worktree(raw: str, *, allow_root: bool = True) -> Path:
    path = resolve_path(raw)
    worktree_base = WORKTREE_BASE.resolve()
    cache_base = (WORKTREE_BASE / ".cache").resolve()
    if path != ROOT and not is_within(path, worktree_base):
        raise ValueError(f"路径必须位于项目根目录或 .worktrees/ 下：{path}")
    if path != ROOT and is_within(path, cache_base):
        raise ValueError(f"共享缓存目录不是可操作的工作树：{path}")
    if not allow_root and path == ROOT:
        raise ValueError("该命令不能作用于主工作区")
    if path not in registered_worktrees():
        raise ValueError(f"不是已登记的 Git 工作树：{path}")
    return path


def lock_hash(worktree: Path) -> str | None:
    lockfile = worktree / "web" / "package-lock.json"
    if not lockfile.is_file() or lockfile.is_symlink():
        return None
    return hashlib.sha256(lockfile.read_bytes()).hexdigest()


def ensure_cache_layout(*, create: bool = False) -> None:
    for path in (WORKTREE_BASE, WORKTREE_BASE / ".cache", CACHE_ROOT):
        if path.is_symlink():
            raise ValueError(f"共享缓存路径不能是软链：{path}")
    if create:
        CACHE_ROOT.mkdir(parents=True, exist_ok=True)
    if CACHE_ROOT.exists() and not CACHE_ROOT.is_dir():
        raise ValueError(f"共享缓存路径不是目录：{CACHE_ROOT}")


def ensure_digest_directory(digest: str) -> Path:
    directory = CACHE_ROOT / digest
    if directory.is_symlink():
        raise ValueError(f"哈希缓存目录不能是软链：{directory}")
    if directory.exists() and not directory.is_dir():
        raise ValueError(f"哈希缓存路径不是目录：{directory}")
    return directory


@contextmanager
def cache_lock():
    ensure_cache_layout(create=True)
    with (CACHE_ROOT / ".lock").open("a+") as lock_file:
        fcntl.flock(lock_file, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(lock_file, fcntl.LOCK_UN)


def cache_path(digest: str) -> Path:
    if not HASH_PATTERN.fullmatch(digest):
        raise ValueError(f"无效的依赖锁文件哈希：{digest}")
    return CACHE_ROOT / digest / "node_modules"


def installed_dependencies_match(worktree: Path, modules: Path) -> bool:
    try:
        expected_path = worktree / "web" / "package-lock.json"
        installed_path = modules / ".package-lock.json"
        if expected_path.is_symlink() or installed_path.is_symlink():
            return False
        expected = json.loads(expected_path.read_text())
        installed = json.loads(installed_path.read_text())
    except (OSError, json.JSONDecodeError):
        return False

    expected_packages = expected.get("packages", {})
    installed_packages = installed.get("packages", {})
    if installed.get("lockfileVersion") != expected.get("lockfileVersion"):
        return False
    if (installed.get("name"), installed.get("version")) != (
        expected.get("name"),
        expected.get("version"),
    ):
        return False

    def encoded(value: object) -> str:
        return json.dumps(value, sort_keys=True, separators=(",", ":"))

    def supports_current_platform(package: dict[str, object]) -> bool:
        current_os = {"darwin": "darwin", "linux": "linux", "win32": "win32"}.get(sys.platform, sys.platform)
        machine = platform.machine().lower()
        current_cpu = {
            "amd64": "x64",
            "x86_64": "x64",
            "aarch64": "arm64",
            "arm64": "arm64",
            "i386": "ia32",
            "i486": "ia32",
            "i586": "ia32",
            "i686": "ia32",
            "armv7l": "arm",
        }.get(machine, machine)

        def allows(constraints: object, current: str) -> bool:
            if not isinstance(constraints, list) or not constraints:
                return True
            values = [str(value) for value in constraints]
            excluded = {value[1:] for value in values if value.startswith("!")}
            included = {value for value in values if not value.startswith("!")}
            return current not in excluded and (not included or current in included)

        return allows(package.get("os"), current_os) and allows(package.get("cpu"), current_cpu)

    # npm omits optional packages incompatible with this platform, but required packages must all exist.
    for key, value in installed_packages.items():
        if not key:
            continue
        if key not in expected_packages or encoded(value) != encoded(expected_packages[key]):
            return False
        if not key.startswith("node_modules/") or ".." in Path(key).parts:
            return False
        installed_path = modules / key.removeprefix("node_modules/")
        if not installed_path.is_dir():
            return False
    for key, value in expected_packages.items():
        if not key or key in installed_packages:
            continue
        if value.get("optional") is not True or supports_current_platform(value):
            return False
    return True


def cache_is_valid(worktree: Path, digest: str, modules: Path) -> bool:
    directory = ensure_digest_directory(digest)
    if (
        modules != directory / "node_modules"
        or modules.is_symlink()
        or not modules.is_dir()
        or not installed_dependencies_match(worktree, modules)
    ):
        return False

    marker = modules / LOCK_MARKER
    if marker.is_symlink():
        return False
    if marker.exists():
        try:
            if marker.read_text().strip() != digest:
                return False
        except OSError:
            return False
    else:
        marker.write_text(f"{digest}\n")
    return True


def set_dependency_link(link: Path, target: Path) -> None:
    if link.parent.is_symlink() or not link.parent.is_dir():
        raise ValueError(f"前端目录缺失或是软链，拒绝改写依赖路径：{link.parent}")
    target = target.resolve()
    if not target.is_dir() or target.is_symlink():
        raise ValueError(f"依赖缓存目录不存在或不是实体目录：{target}")

    if link.is_symlink():
        try:
            if link.resolve(strict=True) == target:
                return
        except FileNotFoundError:
            pass
    elif link.exists():
        raise ValueError(f"拒绝覆盖工作树现有 node_modules：{link}")

    temporary = link.parent / f".{link.name}.link-{os.getpid()}-{os.urandom(4).hex()}"
    if temporary.exists() or temporary.is_symlink():
        raise ValueError(f"依赖软链临时路径已存在：{temporary}")
    temporary.symlink_to(os.path.relpath(target, start=link.parent), target_is_directory=True)
    try:
        os.replace(temporary, link)
    finally:
        temporary.unlink(missing_ok=True)


def link_if_cached(worktree: Path) -> bool:
    if not CACHE_ROOT.is_dir():
        return False

    with cache_lock():
        digest = lock_hash(worktree)
        if digest is None:
            return False
        shared_modules = cache_path(digest)
        if not cache_is_valid(worktree, digest, shared_modules):
            return False
        if lock_hash(worktree) != digest:
            return False
        set_dependency_link(worktree / "web" / "node_modules", shared_modules)
        return True


def command_add(args: argparse.Namespace) -> None:
    if WORKTREE_BASE.is_symlink():
        raise ValueError(f".worktrees/ 不能是软链：{WORKTREE_BASE}")
    raw_path = Path(args.path)
    path = raw_path if raw_path.is_absolute() else ROOT / raw_path
    path = path.resolve()
    cache_base = (WORKTREE_BASE / ".cache").resolve()
    if not is_within(path, WORKTREE_BASE.resolve()) or is_within(path, cache_base):
        raise ValueError("新工作树必须创建在 .worktrees/ 下且位于共享缓存目录之外")
    if path.exists():
        raise ValueError(f"目标路径已存在：{path}")
    for existing in registered_worktrees():
        if existing != ROOT and (is_within(path, existing) or is_within(existing, path)):
            raise ValueError(f"新工作树不能嵌套在现有工作树中，也不能包住现有工作树：{existing}")
    ignored = run_git(ROOT, "check-ignore", "-q", str(WORKTREE_BASE), check=False)
    if ignored.returncode != 0:
        raise ValueError(".worktrees/ 必须先加入 .gitignore")

    path.parent.mkdir(parents=True, exist_ok=True)
    run_git(ROOT, "worktree", "add", "-b", args.branch, str(path), args.base)
    if link_if_cached(path):
        print(f"已创建工作树并链接匹配的前端依赖：{path}")
    else:
        print(f"已创建工作树：{path}")
        print(f"需要前端依赖时运行：python3 script/worktree.py deps {path.relative_to(ROOT)}")


def remove_partial_install(modules: Path) -> None:
    if modules.is_symlink() or modules.is_file():
        modules.unlink()
    elif modules.is_dir():
        shutil.rmtree(modules)


def write_install_marker(pending: Path, digest: str) -> None:
    temporary = pending.with_name(f"{pending.name}.tmp-{os.getpid()}-{os.urandom(4).hex()}")
    temporary.write_text(f"{digest}\n")
    os.replace(temporary, pending)


def restore_previous_link(modules: Path, backup: Path) -> None:
    if not backup.is_symlink():
        return
    if modules.exists() or modules.is_symlink():
        remove_partial_install(modules)
    os.replace(backup, modules)


def write_cache_marker(modules: Path, digest: str) -> None:
    marker = modules / LOCK_MARKER
    if marker.is_symlink():
        raise ValueError(f"缓存标记不能是软链：{marker}")
    if marker.exists():
        try:
            if marker.read_text().strip() != digest:
                raise ValueError(f"现有依赖目录的缓存标记与锁文件不匹配：{marker}")
        except OSError as exc:
            raise ValueError(f"无法读取依赖缓存标记：{marker}") from exc
        return
    marker.write_text(f"{digest}\n")


def install_dependencies(worktree: Path) -> None:
    if worktree == ROOT:
        raise ValueError("deps 仅管理 .worktrees/ 下的工作树；主工作区依赖保持独立")
    web = worktree / "web"
    if web.is_symlink() or not web.is_dir():
        raise ValueError(f"前端目录缺失或是软链：{web}")
    lockfile = web / "package-lock.json"
    if not lockfile.is_file() or lockfile.is_symlink():
        raise ValueError(f"缺少实体前端锁文件：{lockfile}")

    modules = web / "node_modules"

    with cache_lock():
        digest = lock_hash(worktree)
        if digest is None:
            raise ValueError(f"缺少前端锁文件：{lockfile}")
        digest_directory = ensure_digest_directory(digest)
        shared_modules = cache_path(digest)
        pending = web / INSTALL_MARKER
        backup = web / BACKUP_LINK
        if pending.is_symlink() or (pending.exists() and not pending.is_file()):
            raise ValueError(f"安装状态标记必须是普通文件：{pending}")
        if backup.exists() and not backup.is_symlink():
            raise ValueError(f"依赖恢复路径不是软链，拒绝覆盖：{backup}")

        # Recover a command interrupted after preserving the previous shared link.
        if backup.is_symlink():
            if modules.is_symlink():
                try:
                    already_published = modules.resolve(strict=True) == shared_modules.resolve()
                except FileNotFoundError:
                    already_published = False
                if already_published and lock_hash(worktree) == digest and cache_is_valid(
                    worktree, digest, shared_modules
                ):
                    backup.unlink()
                    pending.unlink(missing_ok=True)
                    print(f"前端依赖已经链接到共享缓存：{shared_modules}")
                    return
                raise ValueError(f"发现未完成的依赖切换，保留现有软链并检查：{backup}")
            if modules.exists() and modules.is_dir() and installed_dependencies_match(worktree, modules):
                pass
            elif pending.is_file():
                restore_previous_link(modules, backup)
                pending.unlink(missing_ok=True)
            else:
                raise ValueError(f"发现未完成的依赖切换，拒绝移除未验证依赖：{backup}")
        elif pending.is_file():
            if modules.is_symlink():
                pending.unlink()
            elif modules.exists() and modules.is_dir() and installed_dependencies_match(worktree, modules):
                pass
            else:
                remove_partial_install(modules)
                pending.unlink()
        elif pending.exists():
            raise ValueError(f"安装状态标记必须是普通文件：{pending}")

        if cache_is_valid(worktree, digest, shared_modules):
            if lock_hash(worktree) != digest:
                raise RuntimeError("依赖锁文件在缓存校验期间发生变化，请重试 deps")
            if modules.exists():
                if modules.is_symlink():
                    set_dependency_link(modules, shared_modules)
                elif not installed_dependencies_match(worktree, modules):
                    raise ValueError(f"工作树已有未验证的 node_modules，未覆盖：{modules}")
                else:
                    remove_partial_install(modules)
                    set_dependency_link(modules, shared_modules)
            else:
                set_dependency_link(modules, shared_modules)
            if backup.is_symlink():
                backup.unlink()
            pending.unlink(missing_ok=True)
            print(f"已链接到共享依赖缓存：{shared_modules}")
            return

        if shared_modules.exists() or shared_modules.is_symlink():
            raise ValueError(f"共享缓存存在但与锁文件不匹配，拒绝覆盖：{shared_modules}")

        if modules.exists() and not modules.is_symlink():
            if modules.is_dir() and not modules.is_symlink() and installed_dependencies_match(worktree, modules):
                if lock_hash(worktree) != digest:
                    raise RuntimeError("依赖锁文件在缓存登记期间发生变化，请重试 deps")
                write_cache_marker(modules, digest)
                digest_directory.mkdir(parents=True, exist_ok=True)
                os.replace(modules, shared_modules)
                if lock_hash(worktree) != digest:
                    restore_previous_link(modules, backup)
                    raise RuntimeError("依赖锁文件在缓存登记期间发生变化，请重试 deps")
                set_dependency_link(modules, shared_modules)
                if backup.is_symlink():
                    backup.unlink()
                pending.unlink(missing_ok=True)
                print(f"已将现有依赖登记到共享缓存：{shared_modules}")
                return
            raise ValueError(f"工作树已有未验证的 node_modules，拒绝覆盖：{modules}")

        write_install_marker(pending, digest)
        if modules.is_symlink():
            if backup.exists() or backup.is_symlink():
                raise ValueError(f"依赖恢复路径已被占用：{backup}")
            os.replace(modules, backup)
        try:
            subprocess.run(["npm", "ci"], cwd=web, check=True)
            if (
                lock_hash(worktree) != digest
                or not modules.is_dir()
                or modules.is_symlink()
                or not installed_dependencies_match(worktree, modules)
            ):
                raise RuntimeError("npm ci 生成的依赖与 package-lock.json 不匹配")
            write_cache_marker(modules, digest)
            digest_directory.mkdir(parents=True, exist_ok=True)
            os.replace(modules, shared_modules)
            if lock_hash(worktree) != digest:
                restore_previous_link(modules, backup)
                raise RuntimeError("依赖锁文件在发布缓存前发生变化，请重试 deps")
            set_dependency_link(modules, shared_modules)
            if backup.is_symlink():
                backup.unlink()
            pending.unlink()
            print(f"已将新安装依赖登记到共享缓存：{shared_modules}")
        except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError):
            if modules.is_symlink():
                if backup.is_symlink():
                    backup.unlink()
            elif backup.is_symlink():
                restore_previous_link(modules, backup)
            else:
                remove_partial_install(modules)
            pending.unlink(missing_ok=True)
            raise


GENERATED_DIRECTORIES = (
    "build/client",
    "web/.next",
    "web/out",
    "web/dist",
    "internal/web/dist",
    ".gocache",
    ".golangci-cache",
)
GENERATED_FILES = ("web/tsconfig.tsbuildinfo",)
BINARY_CANDIDATES = ("aris-mass", "server", "client", "aris", "test_embed", "migrate_think_extract")
MACH_O_MAGICS = {
    b"\xfe\xed\xfa\xce",
    b"\xfe\xed\xfa\xcf",
    b"\xce\xfa\xed\xfe",
    b"\xcf\xfa\xed\xfe",
    b"\xca\xfe\xba\xbe",
    b"\xbe\xba\xfe\xca",
    b"\xca\xfe\xba\xbf",
    b"\xbf\xba\xfe\xca",
}


def has_executable_format(path: Path) -> bool:
    try:
        with path.open("rb") as binary:
            header = binary.read(4096)
            if header.startswith(b"\x7fELF") or header[:4] in MACH_O_MAGICS:
                return True
            if header.startswith(b"MZ") and len(header) >= 64:
                offset = int.from_bytes(header[60:64], "little")
                binary.seek(offset)
                return binary.read(4) == b"PE\0\0"
    except OSError:
        return False
    return False


def has_tracked_files(worktree: Path, relative: str) -> bool:
    result = run_git(worktree, "ls-files", "--", relative)
    return bool(result.stdout.strip())


def safe_parent_chain(worktree: Path, path: Path) -> bool:
    try:
        if not stat.S_ISDIR(worktree.lstat().st_mode):
            return False
    except OSError:
        return False

    parents = list(path.parents)
    try:
        index = parents.index(worktree)
    except ValueError:
        return False
    for parent in reversed(parents[:index]):
        try:
            if not stat.S_ISDIR(parent.lstat().st_mode):
                return False
        except OSError:
            return False
    return True


def clean_candidate(
    worktree: Path,
    relative: str,
    registered: list[Path],
    *,
    directory: bool = False,
    binary: bool = False,
) -> bool:
    path = worktree / relative
    if not safe_parent_chain(worktree, path):
        return False
    try:
        mode = path.lstat().st_mode
    except FileNotFoundError:
        return False
    if stat.S_ISLNK(mode) or has_tracked_files(worktree, relative):
        return False
    if any(
        other != worktree
        and other != ROOT
        and (
            is_within(other, path)
            or (is_within(path, other) and not is_within(worktree, other))
        )
        for other in registered
    ):
        return False

    if directory:
        if not stat.S_ISDIR(mode):
            return False
        shutil.rmtree(path)
        return True
    if not stat.S_ISREG(mode):
        return False
    if binary and (not mode & 0o111 or not has_executable_format(path)):
        return False
    path.unlink()
    return True


def clean_worktree(worktree: Path, registered: list[Path]) -> int:
    removed = 0
    for relative in GENERATED_DIRECTORIES:
        removed += clean_candidate(worktree, relative, registered, directory=True)
    for relative in GENERATED_FILES:
        removed += clean_candidate(worktree, relative, registered)
    for relative in BINARY_CANDIDATES:
        removed += clean_candidate(worktree, relative, registered, binary=True)
    return removed


def command_clean(args: argparse.Namespace) -> None:
    registered = registered_worktrees()
    if args.all:
        paths = [path for path in registered if path.exists()]
    elif args.path:
        paths = [require_worktree(args.path)]
    else:
        raise ValueError("指定工作树路径，或使用 --all")

    total = sum(clean_worktree(path, registered) for path in paths)
    print(f"已安全清理 {len(paths)} 个工作树中的 {total} 项可再生成产物。")


def cache_hash_from_link(link: Path) -> str | None:
    if not link.is_symlink():
        return None
    try:
        target = link.resolve(strict=True)
        relative = target.relative_to(CACHE_ROOT.resolve())
    except (OSError, ValueError):
        return None
    if len(relative.parts) != 2 or relative.parts[1] != "node_modules":
        return None
    digest = relative.parts[0]
    return digest if HASH_PATTERN.fullmatch(digest) else None


def command_gc() -> None:
    ensure_cache_layout()
    if not CACHE_ROOT.is_dir():
        print("没有共享依赖缓存需要回收。")
        return

    with cache_lock():
        worktrees = registered_worktrees()
        live_hashes = {
            digest
            for worktree in worktrees
            if (digest := lock_hash(worktree)) is not None
        }
        live_hashes.update(
            digest
            for worktree in worktrees
            for link in (
                worktree / "web" / "node_modules",
                worktree / "web" / BACKUP_LINK,
            )
            if (digest := cache_hash_from_link(link)) is not None
        )

        removed = 0
        for entry in CACHE_ROOT.iterdir():
            if not HASH_PATTERN.fullmatch(entry.name) or entry.is_symlink() or not entry.is_dir():
                continue
            if entry.name in live_hashes or any(is_within(worktree, entry) for worktree in worktrees):
                continue
            modules = entry / "node_modules"
            marker = modules / LOCK_MARKER
            if modules.is_symlink() or not modules.is_dir() or marker.is_symlink() or not marker.is_file():
                continue
            try:
                if marker.read_text().strip() != entry.name:
                    continue
            except OSError:
                continue
            shutil.rmtree(entry)
            removed += 1
    print(f"已回收 {removed} 个确认由脚本管理且无引用的依赖缓存。")


def command_detach(args: argparse.Namespace) -> None:
    worktree = require_worktree(args.path, allow_root=False)
    web = worktree / "web"
    if web.is_symlink() or not web.is_dir():
        raise ValueError(f"前端目录缺失或是软链：{web}")
    modules = web / "node_modules"
    if modules.is_symlink():
        modules.unlink()
        print(f"已解除当前工作树的依赖软链：{modules}")
    elif modules.exists():
        print(f"当前工作树已有私有依赖目录，未修改：{modules}")
    else:
        print("当前工作树没有前端依赖软链。")


def command_remove(args: argparse.Namespace) -> None:
    worktree = require_worktree(args.path, allow_root=False)
    nested = [path for path in registered_worktrees() if path != worktree and is_within(path, worktree)]
    if nested:
        raise ValueError(f"工作树内仍登记有其他工作树，拒绝移除：{nested[0]}")
    if Path.cwd().resolve() == worktree or is_within(Path.cwd().resolve(), worktree):
        raise ValueError("请先切换到其他目录，再移除当前工作树")
    run_git(ROOT, "worktree", "remove", str(worktree))
    command_gc()
    print(f"已移除工作树：{worktree}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="管理项目工作树、共享前端依赖和生成缓存。")
    commands = parser.add_subparsers(dest="command", required=True)

    add = commands.add_parser("add", help="创建工作树并复用已有依赖缓存")
    add.add_argument("path", help=".worktrees/ 下的目标路径")
    add.add_argument("branch", help="新分支名")
    add.add_argument("base", nargs="?", default="master", help="起始提交或分支，默认 master")
    add.set_defaults(run=command_add)

    deps = commands.add_parser("deps", help="按 package-lock.json 哈希共享或安装前端依赖")
    deps.add_argument("path", help="已登记的非主工作区路径")
    deps.set_defaults(run=lambda args: install_dependencies(require_worktree(args.path, allow_root=False)))

    detach = commands.add_parser("detach-deps", help="解除当前工作树的共享依赖软链以便更新依赖")
    detach.add_argument("path", help="已登记的非主工作区路径")
    detach.set_defaults(run=command_detach)

    clean = commands.add_parser("clean", help="安全清理可再生成的构建产物与缓存")
    clean.add_argument("path", nargs="?", help="已登记的工作树路径")
    clean.add_argument("--all", action="store_true", help="清理所有已登记且仍存在的工作树")
    clean.set_defaults(run=command_clean)

    remove = commands.add_parser("remove", help="安全移除工作树并回收孤儿依赖缓存")
    remove.add_argument("path", help=".worktrees/ 下的工作树路径")
    remove.set_defaults(run=command_remove)

    gc = commands.add_parser("gc", help="回收没有工作树引用的共享依赖缓存")
    gc.set_defaults(run=lambda _args: command_gc())
    return parser


def main() -> int:
    args = build_parser().parse_args()
    try:
        args.run(args)
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as exc:
        message = getattr(exc, "stderr", None) or str(exc)
        print(f"错误：{message.strip()}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
