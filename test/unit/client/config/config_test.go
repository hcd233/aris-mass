package config

import (
	"os"
	"path/filepath"
	"testing"

	clientconfig "github.com/hcd233/aris-mass/internal/client/config"
	"github.com/hcd233/aris-mass/internal/common/constant"
)

func testPaths(t *testing.T) clientconfig.Paths {
	t.Helper()
	return clientconfig.Paths{Root: filepath.Join(t.TempDir(), constant.ArisClientRootDirName)}
}

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	store := clientconfig.NewStore(paths)
	want := clientconfig.Config{Host: "https://aris.example.com", APIKey: "sk-test"}

	if err := store.Save(t.Context(), want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.Load(t.Context())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("load = %+v, want %+v", got, want)
	}
	info, err := os.Stat(paths.ConfigFile())
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file perm = %o, want 600", perm)
	}
}

// TestStoreLoadsLegacyTracePath 旧版把配置放在 ~/.aris/trace/config.json；
// 升级后首次 load 必须仍能读到，避免要求用户重新 init。
func TestStoreLoadsLegacyTracePath(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	legacy := filepath.Join(paths.Root, "trace", constant.ArisClientConfigFileName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.WriteFile(legacy, []byte(`{"host":"https://legacy.example.com","apiKey":"sk-legacy"}`), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	got, err := clientconfig.NewStore(paths).Load(t.Context())
	if err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	want := clientconfig.Config{Host: "https://legacy.example.com", APIKey: "sk-legacy"}
	if got != want {
		t.Fatalf("load legacy = %+v, want %+v", got, want)
	}
}

// TestStoreFallsBackToLegacyWhenPrimaryCorrupt 新路径存在但内容损坏（例如写到一半）时
// 必须回退旧路径，否则升级用户会因为一次坏写丢掉仍然可用的 legacy 配置。
func TestStoreFallsBackToLegacyWhenPrimaryCorrupt(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile(), []byte(`{"host":`), 0o600); err != nil {
		t.Fatalf("write corrupt primary: %v", err)
	}
	legacy := filepath.Join(paths.Root, "trace", constant.ArisClientConfigFileName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.WriteFile(legacy, []byte(`{"host":"https://legacy.example.com","apiKey":"sk-legacy"}`), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	got, err := clientconfig.NewStore(paths).Load(t.Context())
	if err != nil {
		t.Fatalf("load with corrupt primary: %v", err)
	}
	want := clientconfig.Config{Host: "https://legacy.example.com", APIKey: "sk-legacy"}
	if got != want {
		t.Fatalf("load = %+v, want legacy %+v", got, want)
	}
}
