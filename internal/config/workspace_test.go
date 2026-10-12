package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeJSONFile(t *testing.T, path string, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWorkspaceMergesGlobalAndProjectConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(home, ".mini-opencode", "config.json"), `{
  "providers": [{"name":"relay","base_url":"https://relay.example.com/v1","model":"gpt-4o","api_key":"sk-global"}],
  "active_provider":"relay",
  "agent":{"max_turns":20,"compact_threshold":0.8},
  "workspace":{"allowed_roots":["/global/root"]}
}`)
	writeJSONFile(t, filepath.Join(project, ".mini-opencode", "config.json"), `{
  "agent":{"max_turns":7},
  "workspace":{"require_read_before_write":false},
  "memory":{"retrieval":"hybrid"},
  "permissions":{"mode":"ask"}
}`)

	cfg, err := LoadWorkspace(project)
	if err != nil {
		t.Fatalf("LoadWorkspace() error = %v", err)
	}
	if cfg.Provider.Name != "relay" || cfg.Provider.APIKey != "sk-global" {
		t.Fatalf("provider = %+v, want global provider and key", cfg.Provider)
	}
	if cfg.Agent.MaxTurns != 7 || cfg.Agent.CompactThreshold != 0.8 {
		t.Fatalf("agent merge = %+v", cfg.Agent)
	}
	if cfg.Workspace.RequireReadBeforeWrite == nil || *cfg.Workspace.RequireReadBeforeWrite {
		t.Fatal("project workspace override was not applied")
	}
	if !cfg.Memory.HybridEnabled() {
		t.Fatal("project memory override was not applied")
	}
	if len(cfg.Workspace.AllowedRoots) != 1 || cfg.Workspace.AllowedRoots[0] != "/global/root" {
		t.Fatalf("global workspace fields were not preserved: %+v", cfg.Workspace.AllowedRoots)
	}
}

func TestLoadWorkspaceProjectCannotOverrideProviderCatalogOrKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(home, ".mini-opencode", "config.json"), `{
  "providers": [{"name":"global","base_url":"https://global.example.com/v1","model":"global-model","models":["global-model","global-alt"],"context_window":128000,"api_key":"sk-global","api_key_env":"GLOBAL_KEY"}],
  "active_provider":"global"
}`)
	writeJSONFile(t, filepath.Join(project, ".mini-opencode", "config.json"), `{
  "providers": [{"name":"project","base_url":"https://project.example.com/v1","model":"project-model","models":["project-model"],"context_window":999,"api_key":"sk-project","api_key_env":"PROJECT_KEY"}],
  "active_provider":"project",
  "provider":{"name":"project","model":"legacy-project-model","context_window":111,"api_key":"sk-project","api_key_env":"PROJECT_LEGACY_KEY"}
}`)

	cfg, err := LoadWorkspace(project)
	if err != nil {
		t.Fatalf("LoadWorkspace() error = %v", err)
	}
	if cfg.Provider.Name != "global" || cfg.Provider.APIKey != "sk-global" || cfg.Provider.APIKeyEnv != "GLOBAL_KEY" {
		t.Fatalf("project provider override leaked: %+v", cfg.Provider)
	}
	if cfg.Provider.Model != "global-model" || cfg.Provider.ContextWindow != 128000 {
		t.Fatalf("project model settings leaked: %+v", cfg.Provider)
	}
	if got := cfg.Provider.CatalogModels(); len(got) != 2 || got[0] != "global-model" || got[1] != "global-alt" {
		t.Fatalf("project model catalog leaked: %v", got)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "global" {
		t.Fatalf("provider catalog = %+v", cfg.Providers)
	}
}

func TestLoadWorkspaceIgnoresProjectProviderWithoutGlobalCatalog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	writeJSONFile(t, WorkspacePath(project), `{
  "providers": [{"name":"project","base_url":"https://project.example.com/v1","model":"project-model","api_key":"sk-project"}],
  "active_provider":"project",
  "provider":{"name":"legacy-project","api_key":"sk-legacy"},
  "memory":{"top_k":8}
}`)

	cfg, err := LoadWorkspace(project)
	if err != nil {
		t.Fatalf("LoadWorkspace() error = %v", err)
	}
	if cfg.Provider.Name != ProviderTypeEcho || cfg.Provider.APIKey != "" {
		t.Fatalf("project provider became active without a global catalog: %+v", cfg.Provider)
	}
	if cfg.Memory.TopK != 8 {
		t.Fatalf("ordinary project settings were not merged: %+v", cfg.Memory)
	}
}

func TestLoadWorkspaceFindsNearestProjectConfigWithinGitRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(repo, "project")
	child := filepath.Join(project, "src", "nested")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(project, ".mini-opencode", "config.json"), `{"memory":{"top_k":9}}`)
	writeJSONFile(t, filepath.Join(repo, ".mini-opencode", "config.json"), `{"memory":{"top_k":3}}`)
	writeJSONFile(t, filepath.Join(home, ".mini-opencode", "config.json"), `{"memory":{"top_k":1}}`)

	cfg, err := LoadWorkspace(child)
	if err != nil {
		t.Fatalf("LoadWorkspace() error = %v", err)
	}
	if cfg.Memory.TopK != 9 {
		t.Fatalf("nearest project config not selected: top_k=%d", cfg.Memory.TopK)
	}

	repoChild := filepath.Join(repo, "outside")
	if err := os.Mkdir(repoChild, 0700); err != nil {
		t.Fatal(err)
	}
	path, found, err := ProjectConfigPath(repoChild)
	if err != nil || !found || path != WorkspacePath(repo) {
		t.Fatalf("git-root config not selected: path=%q found=%v err=%v", path, found, err)
	}

	nonGitParent := t.TempDir()
	nonGitChild := filepath.Join(nonGitParent, "child")
	if err := os.Mkdir(nonGitChild, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, WorkspacePath(nonGitParent), `{"memory":{"top_k":77}}`)
	if _, found, err := ProjectConfigPath(nonGitChild); err != nil || found {
		t.Fatalf("non-git lookup escaped working directory: found=%v err=%v", found, err)
	}
}

func TestLoadWorkspaceIgnoresLegacyRootConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	writeJSONFile(t, filepath.Join(home, ".mini-opencode", "config.json"), `{"memory":{"top_k":2}}`)
	writeJSONFile(t, filepath.Join(project, "config.json"), `{"memory":{"top_k":99},"providers":[{"name":"legacy","api_key":"sk-legacy"}]}`)

	cfg, err := LoadWorkspace(project)
	if err != nil {
		t.Fatalf("LoadWorkspace() error = %v", err)
	}
	if cfg.Memory.TopK != 2 {
		t.Fatalf("legacy root config was loaded: top_k=%d", cfg.Memory.TopK)
	}
	if cfg.Provider.Name == "legacy" || cfg.Provider.APIKey == "sk-legacy" {
		t.Fatalf("legacy provider leaked into workspace config: %+v", cfg.Provider)
	}
}

func TestLoadWorkspaceProjectCannotRelaxGlobalPermissionMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(home, ".mini-opencode", "config.json"), `{"permissions":{"mode":"ask"}}`)
	writeJSONFile(t, filepath.Join(project, ".mini-opencode", "config.json"), `{"permissions":{"mode":"full-access"}}`)

	cfg, err := LoadWorkspace(project)
	if err != nil {
		t.Fatalf("LoadWorkspace() error = %v", err)
	}
	if string(cfg.Permissions.Mode) != "ask" {
		t.Fatalf("project relaxed permissions to %q", cfg.Permissions.Mode)
	}
}

func TestSaveProviderCatalogOnlyUpdatesGlobalProviderFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalPath, err := GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, globalPath, `{"memory":{"top_k":2},"workspace":{"allowed_roots":["/global"]},"providers":[{"name":"old"}],"active_provider":"old"}`)

	source := Default()
	source.Providers = []ProviderConfig{{Name: "new", BaseURL: "https://new.example.com", APIKey: "sk-inline"}}
	source.ActiveProvider = "new"
	source.Provider = source.Providers[0]
	source.Memory.TopK = 99
	source.Workspace.AllowedRoots = []string{"/project"}
	if err := SaveProviderCatalog(globalPath, source); err != nil {
		t.Fatalf("SaveProviderCatalog() error = %v", err)
	}

	data, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["active_provider"] != "new" {
		t.Fatalf("active_provider = %v", raw["active_provider"])
	}
	if raw["memory"].(map[string]any)["top_k"] != float64(2) {
		t.Fatalf("provider save overwrote memory: %v", raw["memory"])
	}
	if raw["workspace"].(map[string]any)["allowed_roots"].([]any)[0] != "/global" {
		t.Fatalf("provider save overwrote workspace: %v", raw["workspace"])
	}
	loaded, err := Load(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider.Name != "new" || loaded.Provider.APIKey != "sk-inline" {
		t.Fatalf("provider catalog not saved: %+v", loaded.Provider)
	}
	if _, err := os.Stat(filepath.Join(home, ".mini-opencode", "secrets.json")); !os.IsNotExist(err) {
		t.Fatalf("provider save created secrets.json: %v", err)
	}
}

func TestWorkspaceConfigPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	legacy := filepath.Join(dir, "config.json")
	original := []byte(`{"providers":[{"name":"legacy","api_key":"sk-legacy"}],"memory":{"top_k":88}}`)
	if err := os.WriteFile(legacy, original, 0600); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, WorkspacePath(dir), `{"memory":{"top_k":7},"providers":[{"name":"project","api_key":"sk-project"}]}`)
	writeJSONFile(t, filepath.Join(home, ".mini-opencode", "config.json"), `{"providers":[{"name":"global","api_key":"sk-global"}],"active_provider":"global"}`)

	cfg, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Name != "global" || cfg.Provider.APIKey != "sk-global" {
		t.Fatalf("provider did not come from global config: %+v", cfg.Provider)
	}
	if cfg.Memory.TopK != 7 {
		t.Fatalf("project settings were not loaded: %+v", cfg.Memory)
	}
	data, err := os.ReadFile(legacy)
	if err != nil || string(data) != string(original) {
		t.Fatal("legacy file modified")
	}
	if err := os.WriteFile(WorkspacePath(dir), []byte("bad json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkspace(dir); err == nil {
		t.Fatal("invalid canonical config silently fell back")
	}
}

func TestWorkspaceConfigDefaultsAndSaveFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg, err := LoadWorkspace(dir)
	if err != nil || len(cfg.Providers) == 0 {
		t.Fatalf("default: %v", err)
	}
	if _, err := os.Stat(WorkspacePath(dir)); !os.IsNotExist(err) {
		t.Fatal("read created config")
	}
	if err := os.WriteFile(filepath.Join(dir, ".mini-opencode"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Save(WorkspacePath(dir), cfg); err == nil {
		t.Fatal("expected directory creation failure")
	}
	if _, err := LoadWorkspace(dir); err == nil {
		t.Fatal("expected path error")
	}
}
