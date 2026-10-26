package main

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/localswim/localswim/internal/contract"
	"github.com/localswim/localswim/internal/store/schema"
)

type runtimeBoard struct {
	BoardID     string `json:"boardId"`
	Incarnation string `json:"incarnation"`
	Directory   string `json:"directory"`
}
type runtimeConfig struct {
	ConfigVersion     int           `json:"configVersion"`
	InstallationID    string        `json:"installationId"`
	Board             *runtimeBoard `json:"board"`
	EvidenceDirectory string        `json:"evidenceDirectory"`
	ProtectedRoots    []string      `json:"protectedRoots"`
}

func exactKeys(v any, keys ...string) bool {
	m, ok := v.(map[string]any)
	if !ok || len(m) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok = m[key]; !ok {
			return false
		}
	}
	return true
}

func parseConfig(raw []byte) (runtimeConfig, error) {
	var c runtimeConfig
	if len(raw) > 64*1024 {
		return c, errComposition
	}
	v, e := contract.DecodeJSON(raw)
	if e != nil || !exactKeys(v, "configVersion", "installationId", "board", "evidenceDirectory", "protectedRoots") {
		return c, errComposition
	}
	m := v.(map[string]any)
	if m["board"] != nil && !exactKeys(m["board"], "boardId", "incarnation", "directory") {
		return c, errComposition
	}
	if json.Unmarshal(raw, &c) != nil || c.ConfigVersion != 1 || !installationID(c.InstallationID) || c.ProtectedRoots == nil || !safeAbsolute(c.EvidenceDirectory) {
		return c, errComposition
	}
	if c.Board != nil && (!schema.ValidUUID(c.Board.BoardID) || !schema.ValidUUID(c.Board.Incarnation) || !safeAbsolute(c.Board.Directory)) {
		return c, errComposition
	}
	for _, root := range c.ProtectedRoots {
		if !safeAbsolute(root) {
			return runtimeConfig{}, errComposition
		}
	}
	return c, nil
}

func installationID(id string) bool {
	return schema.ValidUUID(id) && id[14] == '4' && strings.ContainsRune("89ab", rune(id[19]))
}

// These are local Windows native paths, never URLs, UNC/device paths, ADS or
// aliases. Native readers/providers separately validate the filesystem objects.
func safeAbsolute(p string) bool {
	if len(p) < 3 || p[1] != ':' || p[2] != '\\' || !((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) || strings.ContainsAny(p[2:], ":/\x00\r\n\t") || filepath.Clean(p) != p {
		return false
	}
	if len(p) == 3 {
		return true
	}
	for _, part := range strings.Split(p[3:], `\`) {
		if part == "" || strings.TrimRight(part, " .") != part || strings.ContainsAny(part, `<>"|?*`) {
			return false
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
			return false
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return false
			}
		}
	}
	return true
}

type rootInvocation struct {
	config string
	args   []string
	serve  bool
}

func parseRoot(args []string) (rootInvocation, error) {
	q := rootInvocation{args: append([]string(nil), args...)}
	if len(args) > 0 && args[0] == "serve" {
		if len(args) != 3 || args[1] != "--config" || !safeAbsolute(args[2]) {
			return q, errComposition
		}
		q.config = args[2]
		q.serve = true
		q.args = nil
		return q, nil
	}
	if len(args) > 0 && args[0] == "--config" {
		if len(args) < 3 || !safeAbsolute(args[1]) {
			return q, errComposition
		}
		q.config = args[1]
		q.args = append([]string(nil), args[2:]...)
	}
	for _, arg := range q.args {
		if arg == "--config" {
			return q, errComposition
		}
	}
	return q, nil
}

func protectedInventory(c runtimeConfig, nativeRoot string) []string {
	roots := append([]string{}, c.ProtectedRoots...)
	roots = append(roots, nativeRoot, c.EvidenceDirectory)
	if c.Board != nil {
		roots = append(roots, c.Board.Directory)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, root := range roots {
		key := strings.ToLower(root)
		if !seen[key] {
			seen[key] = true
			out = append(out, root)
		}
	}
	return out
}

func pathOverlap(a, b string) bool {
	a = strings.TrimRight(strings.ToLower(a), `\`)
	b = strings.TrimRight(strings.ToLower(b), `\`)
	return a == b || strings.HasPrefix(a, b+`\`) || strings.HasPrefix(b, a+`\`)
}

func separateEvidence(c runtimeConfig, nativeRoot string) bool {
	return !pathOverlap(c.EvidenceDirectory, nativeRoot) && (c.Board == nil || !pathOverlap(c.EvidenceDirectory, c.Board.Directory))
}
