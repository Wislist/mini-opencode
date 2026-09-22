package memory

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// render serializes a note as YAML front matter plus a Markdown body.
//
// The format is deliberately a subset of YAML: one `key: value` per line and
// tags as a comma-separated list. Notes are written by this package and meant
// to be edited by hand, so the format must stay trivially readable and must not
// require a YAML dependency to parse.
func render(note Note) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: " + oneLine(note.Title) + "\n")
	if len(note.Tags) > 0 {
		b.WriteString("tags: " + strings.Join(note.Tags, ", ") + "\n")
	}
	b.WriteString("created_at: " + note.CreatedAt.UTC().Format(time.RFC3339Nano) + "\n")
	b.WriteString("updated_at: " + note.UpdatedAt.UTC().Format(time.RFC3339Nano) + "\n")
	b.WriteString("---\n\n")
	b.WriteString(note.Body)
	b.WriteString("\n")
	return b.String()
}

// readNote parses a memory file. A file without front matter is still a valid
// note: the whole file becomes the body and the filename supplies the name,
// so a hand-written Markdown file dropped into the directory works as-is.
func readNote(path string) (Note, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Note{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Note{}, err
	}

	name := strings.TrimSuffix(filepath.Base(path), FileExt)
	note := Note{
		Name:      name,
		Title:     name,
		CreatedAt: info.ModTime(),
		UpdatedAt: info.ModTime(),
	}

	text := string(data)
	front, body, ok := splitFrontMatter(text)
	if !ok {
		note.Body = strings.TrimSpace(text)
		return note, nil
	}
	for _, line := range strings.Split(front, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch key {
		case "title":
			note.Title = value
		case "tags":
			note.Tags = normalizeTags(strings.Split(value, ","))
		case "created_at":
			if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
				note.CreatedAt = ts
			}
		case "updated_at":
			if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
				note.UpdatedAt = ts
			}
		}
	}
	note.Body = strings.TrimSpace(body)
	if note.Body == "" {
		return Note{}, errors.New("memory: note has an empty body")
	}
	return note, nil
}

// splitFrontMatter separates a leading `---` delimited block from the body.
// It reports false when the document does not open with front matter.
func splitFrontMatter(text string) (front, body string, ok bool) {
	text = strings.TrimPrefix(text, "\ufeff")
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return "", "", false
	}
	rest := strings.TrimPrefix(trimmed, "---")
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")

	end := strings.Index(rest, "\n---")
	if end == -1 {
		return "", "", false
	}
	front = rest[:end]
	body = rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\r\n")
	body = strings.TrimPrefix(body, "\n")
	return front, body, true
}

// oneLine collapses a value to a single line so it cannot break the front
// matter block it is written into.
func oneLine(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.Join(strings.Fields(value), " ")
}

// normalizeTags trims, lowercases and de-duplicates tags while preserving
// order, so tag matching during recall is predictable.
func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		tag = strings.TrimPrefix(tag, "#")
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

// Slugify converts a title or name into a safe filename stem: lowercase ASCII
// alphanumerics joined by single hyphens. Non-ASCII runes are dropped, and a
// title that reduces to nothing yields "", which callers treat as "needs a
// different name".
func Slugify(value string) string {
	var b strings.Builder
	lastHyphen := true // avoids a leading hyphen
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Score ranks notes against a query and returns the best limit of them.
//
// Scoring is intentionally simple and explainable — exact phrase, then token
// overlap across title, tags and body, with title and tag hits weighted above
// body hits. It runs over a handful of small files, so a full-text index would
// add machinery without adding value; the ordering is what matters, and this
// keeps it debuggable.
func Score(notes []Note, query string, limit int) []Note {
	terms := tokenize(query)
	if len(terms) == 0 {
		if len(notes) > limit {
			notes = notes[:limit]
		}
		return notes
	}
	phrase := strings.ToLower(strings.TrimSpace(query))

	type scored struct {
		note  Note
		score float64
		index int
	}
	ranked := make([]scored, 0, len(notes))
	for i, note := range notes {
		score := scoreNote(note, terms, phrase)
		if score <= 0 {
			continue
		}
		ranked = append(ranked, scored{note: note, score: score, index: i})
	}
	// Stable tie-break on the incoming order (already newest-first) so equally
	// relevant notes surface in a predictable order.
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].index < ranked[j].index
		}
		return ranked[i].score > ranked[j].score
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]Note, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.note)
	}
	return out
}

func scoreNote(note Note, terms []string, phrase string) float64 {
	title := strings.ToLower(note.Title)
	body := strings.ToLower(note.Body)
	tags := strings.ToLower(strings.Join(note.Tags, " "))

	var score float64
	if phrase != "" {
		if strings.Contains(title, phrase) {
			score += 6
		}
		if strings.Contains(tags, phrase) {
			score += 4
		}
		if strings.Contains(body, phrase) {
			score += 3
		}
	}
	for _, term := range terms {
		if strings.Contains(title, term) {
			score += 3
		}
		if strings.Contains(tags, term) {
			score += 2
		}
		if strings.Contains(body, term) {
			score += 1
		}
	}
	return score
}

// tokenize splits text into lowercase terms, keeping ASCII words plus each CJK
// rune individually. CJK text is not space-delimited, so treating a run of CJK
// as one token would make every such note score identically; per-rune terms
// give partial credit that actually discriminates.
func tokenize(text string) []string {
	var terms []string
	var word strings.Builder

	flush := func() {
		if word.Len() > 0 {
			terms = append(terms, word.String())
			word.Reset()
		}
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			word.WriteRune(r)
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			flush()
			terms = append(terms, string(r))
		default:
			flush()
		}
	}
	flush()
	return terms
}
