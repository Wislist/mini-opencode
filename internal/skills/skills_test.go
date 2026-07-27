package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkillsEmptyWhenMissing(t *testing.T) {
	got, err := LoadSkills(t.TempDir())
	if err != nil {
		t.Fatalf("LoadSkills() error = %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestInstallCuratedLoadsBack(t *testing.T) {
	dir := t.TempDir()
	res, err := Install(dir, "commit", "")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if res.SourceKind != string(SourceCurated) {
		t.Fatalf("source kind = %q", res.SourceKind)
	}
	if res.Name != "commit" {
		t.Fatalf("name = %q", res.Name)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatalf("installed file missing: %v", err)
	}

	loaded, err := LoadSkills(dir)
	if err != nil {
		t.Fatalf("LoadSkills() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "commit" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded[0].Location != res.Path {
		t.Fatalf("location = %q want %q", loaded[0].Location, res.Path)
	}
	if !strings.Contains(loaded[0].Description, "git commit") && loaded[0].Description == "" {
		t.Fatalf("description empty: %q", loaded[0].Description)
	}
}

func TestInstallCuratedRejectsBadName(t *testing.T) {
	if _, err := Install(t.TempDir(), "commit", "../escape"); err == nil {
		t.Fatal("expected error for invalid name")
	}
}

func TestInstallLocalFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "my-skill.md")
	if err := os.WriteFile(src, []byte("# my-skill\nDo a thing.\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(dir, src, "")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if res.SourceKind != string(SourceLocal) {
		t.Fatalf("source kind = %q", res.SourceKind)
	}
	if res.Name != "my-skill" {
		t.Fatalf("name = %q", res.Name)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Do a thing.") {
		t.Fatalf("content not copied: %q", data)
	}
}

func TestInstallLocalDir(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "pack", "skilldir")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, SkillFileName), []byte("# x\nDesc here."), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(dir, srcDir, "")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if res.Name != "skilldir" {
		t.Fatalf("name = %q", res.Name)
	}
}

func TestInstallUnsupportedSource(t *testing.T) {
	if _, err := Install(t.TempDir(), "definitely-not-a-source!!", ""); err == nil {
		t.Fatal("expected error for unsupported source")
	}
}

func TestParseSkill(t *testing.T) {
	name, desc := parseSkill([]byte("# go-agent\nBuild Go agents.\n\nbody"))
	if name != "go-agent" {
		t.Fatalf("name = %q", name)
	}
	if desc != "Build Go agents." {
		t.Fatalf("desc = %q", desc)
	}
}

func TestParseSkillClampsDescription(t *testing.T) {
	long := strings.Repeat("x", MaxDescription+50)
	_, desc := parseSkill([]byte("# s\n" + long))
	if len(desc) > MaxDescription {
		t.Fatalf("desc length = %d", len(desc))
	}
}

func TestParseSkillFallsBackToDirName(t *testing.T) {
	name, _ := parseSkill([]byte("Just a description with no heading."))
	if name != "" {
		t.Fatalf("name = %q want empty", name)
	}
}

func TestParseGitHubRef(t *testing.T) {
	cases := []struct {
		in       string
		wantURL  string
		wantSub  string
		wantRepo string
	}{
		{"owner/repo", "https://github.com/owner/repo.git", "", "repo"},
		{"owner/repo/skills/foo", "https://github.com/owner/repo.git", "skills/foo", "repo"},
		{"https://github.com/owner/repo.git", "https://github.com/owner/repo.git", "", "repo"},
		{"github.com/owner/repo/sub", "https://github.com/owner/repo.git", "sub", "repo"},
		{"git@github.com:owner/repo.git", "https://github.com/owner/repo.git", "", "repo"},
	}
	for _, c := range cases {
		url, sub, repo := parseGitHubRef(c.in)
		if url != c.wantURL || sub != c.wantSub || repo != c.wantRepo {
			t.Errorf("parseGitHubRef(%q) = (%q,%q,%q) want (%q,%q,%q)",
				c.in, url, sub, repo, c.wantURL, c.wantSub, c.wantRepo)
		}
	}
}

func TestParseGitHubRefInvalid(t *testing.T) {
	url, _, _ := parseGitHubRef("not a ref")
	if url != "" {
		t.Fatalf("expected empty url, got %q", url)
	}
}

func TestCuratedNamesNonEmpty(t *testing.T) {
	names := CuratedNames()
	if len(names) == 0 {
		t.Fatal("expected curated skills")
	}
	for _, n := range names {
		if _, ok := Curated(n); !ok {
			t.Errorf("Curated(%q) missing", n)
		}
	}
}
