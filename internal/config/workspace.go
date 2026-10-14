package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

const configFileName = "config.json"

// GlobalConfigPath is the only configuration file that owns providers,
// models, and API keys.
func GlobalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mini-opencode", configFileName), nil
}

// WorkspacePath is the project-local configuration location. It deliberately
// does not participate in provider persistence.
func WorkspacePath(workDir string) string {
	return filepath.Join(workDir, ".mini-opencode", configFileName)
}

// ProjectConfigPath finds the nearest project configuration. A nested project
// configuration applies to descendants; the scan never escapes its Git root.
// For a non-Git directory only that directory itself is considered.
func ProjectConfigPath(workDir string) (path string, found bool, err error) {
	start, err := filepath.Abs(workDir)
	if err != nil {
		return "", false, err
	}
	start = filepath.Clean(start)
	root, hasRoot, err := gitRoot(start)
	if err != nil {
		return "", false, err
	}
	if !hasRoot {
		path = WorkspacePath(start)
		_, err = os.Stat(path)
		if err == nil {
			return path, true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return path, false, nil
		}
		return "", false, err
	}

	for dir := start; ; dir = filepath.Dir(dir) {
		path = WorkspacePath(dir)
		_, statErr := os.Stat(path)
		if statErr == nil {
			return path, true, nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", false, statErr
		}
		if dir == root || filepath.Dir(dir) == dir {
			break
		}
	}
	return WorkspacePath(start), false, nil
}

func gitRoot(start string) (string, bool, error) {
	for dir := start; ; dir = filepath.Dir(dir) {
		info, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil {
			_ = info
			return dir, true, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		if filepath.Dir(dir) == dir {
			return "", false, nil
		}
	}
}

// LoadWorkspace starts with global defaults and lets one project config
// override ordinary project settings. Provider catalog fields are removed from
// project input before the merge, so keys cannot leak across projects.
func LoadWorkspace(workDir string) (Config, error) {
	globalPath, err := GlobalConfigPath()
	if err != nil {
		return Config{}, err
	}
	globalRaw, err := loadRawObject(globalPath)
	if err != nil {
		return Config{}, err
	}
	providerConfigured := providerCatalogConfigured(globalRaw)

	projectPath, found, err := ProjectConfigPath(workDir)
	if err != nil {
		return Config{}, err
	}
	if found {
		projectRaw, err := loadRawObject(projectPath)
		if err != nil {
			return Config{}, err
		}
		delete(projectRaw, "provider")
		delete(projectRaw, "providers")
		delete(projectRaw, "active_provider")
		globalMode, restrictPermissions := configuredPermissionMode(globalRaw)
		deepMerge(globalRaw, projectRaw)
		if projectMode := permissionMode(globalRaw); restrictPermissions && permissionRank(projectMode) > permissionRank(globalMode) {
			setPermissionMode(globalRaw, globalMode)
		}
	}

	data, err := json.Marshal(globalRaw)
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.ProviderConfigured = providerConfigured
	cfg.ProviderConfigurationKnown = true
	if err := normalizeConfig(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// providerCatalogConfigured distinguishes an explicit global provider entry
// from the normalized echo fallback used when no LLM has been configured yet.
func providerCatalogConfigured(raw map[string]json.RawMessage) bool {
	if data, ok := raw["providers"]; ok {
		var providers []ProviderConfig
		if json.Unmarshal(data, &providers) == nil {
			for _, provider := range providers {
				if strings.TrimSpace(provider.Name) != "" {
					return true
				}
			}
		}
	}
	if data, ok := raw["provider"]; ok {
		var provider ProviderConfig
		return json.Unmarshal(data, &provider) == nil && strings.TrimSpace(provider.Name) != ""
	}
	return false
}

// SaveProviderCatalog updates only the global provider catalog. It reads and
// preserves every unrelated global setting, and never serializes a merged
// project Config back into the user's home directory.
func SaveProviderCatalog(globalPath string, source Config) error {
	if err := source.normalizeProviders(); err != nil {
		return err
	}
	raw, err := loadRawObject(globalPath)
	if err != nil {
		return err
	}
	providers, err := json.Marshal(source.Providers)
	if err != nil {
		return err
	}
	active, err := json.Marshal(source.ActiveProvider)
	if err != nil {
		return err
	}
	raw["providers"] = providers
	raw["active_provider"] = active
	delete(raw, "provider")
	return saveRawObject(globalPath, raw)
}

// SaveProjectDisplayNames persists only user-facing labels to the local
// project config. It cannot accidentally create a project provider catalog.
func SaveProjectDisplayNames(workDir, user, assistant string) error {
	path := WorkspacePath(workDir)
	raw, err := loadRawObject(path)
	if err != nil {
		return err
	}
	userJSON, err := json.Marshal(user)
	if err != nil {
		return err
	}
	assistantJSON, err := json.Marshal(assistant)
	if err != nil {
		return err
	}
	raw["user"] = userJSON
	raw["assistant"] = assistantJSON
	delete(raw, "provider")
	delete(raw, "providers")
	delete(raw, "active_provider")
	return saveRawObject(path, raw)
}

func loadRawObject(path string) (map[string]json.RawMessage, error) {
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return raw, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return raw, nil
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	return raw, nil
}

func saveRawObject(path string, raw map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func deepMerge(base, override map[string]json.RawMessage) {
	for key, value := range override {
		var nested map[string]json.RawMessage
		if json.Unmarshal(value, &nested) != nil {
			base[key] = value
			continue
		}
		var existing map[string]json.RawMessage
		if raw, ok := base[key]; ok && json.Unmarshal(raw, &existing) == nil {
			deepMerge(existing, nested)
			merged, _ := json.Marshal(existing)
			base[key] = merged
			continue
		}
		base[key] = value
	}
}

func permissionMode(raw map[string]json.RawMessage) agent.PermissionMode {
	mode, _ := configuredPermissionMode(raw)
	return mode
}

func configuredPermissionMode(raw map[string]json.RawMessage) (agent.PermissionMode, bool) {
	var permissions struct {
		Mode agent.PermissionMode `json:"mode"`
	}
	value, ok := raw["permissions"]
	if !ok || json.Unmarshal(value, &permissions) != nil || strings.TrimSpace(string(permissions.Mode)) == "" {
		return agent.PermissionModeAsk, false
	}
	mode, err := agent.ParsePermissionMode(string(permissions.Mode))
	if err != nil {
		return agent.PermissionModeAsk, true
	}
	return mode, true
}

func setPermissionMode(raw map[string]json.RawMessage, mode agent.PermissionMode) {
	data, _ := json.Marshal(struct {
		Mode agent.PermissionMode `json:"mode"`
	}{Mode: mode})
	raw["permissions"] = data
}

func permissionRank(mode agent.PermissionMode) int {
	switch mode {
	case agent.PermissionModeFullAccess:
		return 2
	case agent.PermissionModeAutoReview:
		return 1
	default:
		return 0
	}
}
