package main

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func configuredKnowledgeFolder(c VaultConfig, layer string) string {
	value, fallback := "", ""
	switch layer {
	case "consensus":
		value, fallback = c.ConsensusDirectory, "consensus"
	case "system":
		value, fallback = c.SystemDirectory, "system"
	case "project":
		value, fallback = c.ProjectDirectory, "projects"
	}
	if value == "" {
		return fallback
	}
	return value
}

// The parent Vault path is machine-local; layer folders are portable relative
// paths. One confined root keeps conflict detection, citations and Git imports
// consistent when the same Vault is checked out at another absolute location.
func prepareKnowledgeFolders(c *VaultConfig) error {
	for _, value := range []*string{&c.ConsensusDirectory, &c.SystemDirectory, &c.ProjectDirectory} {
		*value = strings.TrimSpace(strings.ReplaceAll(*value, "\\", "/"))
		if *value == "" {
			continue
		}
		if len(*value) > 200 || path.Clean(*value) != *value || strings.HasPrefix(*value, "/") || *value == "." || *value == ".." || strings.HasPrefix(*value, "../") {
			return errors.New("知识分类目录请填写 Obsidian 根目录下 Duo 文件夹中的相对路径")
		}
		for _, part := range strings.Split(*value, "/") {
			if part == "" || strings.HasPrefix(part, ".") || strings.TrimRight(part, " .") != part || utf8.RuneCountInString(part) > 80 || strings.ContainsAny(part, "<>:\"|?*\x00\r\n\t") {
				return errors.New("知识分类目录包含不支持的名称或路径字符")
			}
			name := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
			if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" || (len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9') {
				return errors.New("知识分类目录不能使用 Windows 保留名称")
			}
		}
	}
	folders := []string{configuredKnowledgeFolder(*c, "consensus"), configuredKnowledgeFolder(*c, "system"), configuredKnowledgeFolder(*c, "project")}
	overlaps := func(a, b string) bool {
		a, b = strings.ToLower(a), strings.ToLower(b)
		return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
	}
	for i, folder := range folders {
		for _, managed := range []string{"tasks", "knowledge", "topics", "conflicts", "run", "note", "task", "insight", "topic", "index", "curated"} {
			if overlaps(folder, managed) {
				return errors.New("知识分类目录不能与任务资料、索引或冲突目录重叠")
			}
		}
		for j := 0; j < i; j++ {
			if overlaps(folder, folders[j]) {
				return errors.New("平台共识、系统知识和项目知识目录不能相同或互相包含")
			}
		}
	}
	return nil
}

func configuredCuratedPath(c VaultConfig, d LibraryDocument) string {
	canonical := curatedPath(d)
	if canonical == "" {
		return ""
	}
	cut := strings.IndexByte(canonical, '/')
	if cut < 0 {
		return ""
	}
	return path.Join(configuredKnowledgeFolder(c, d.Layer), canonical[cut+1:])
}

func ensureKnowledgeLayerFolders(root string, c VaultConfig) error {
	paths := []string{root}
	for _, layer := range []string{"consensus", "system", "project"} {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(configuredKnowledgeFolder(c, layer))))
	}
	for _, folder := range paths {
		if err := plainVaultPath(folder); err != nil {
			return err
		}
	}
	for _, folder := range paths {
		if err := os.MkdirAll(folder, 0700); err != nil {
			return err
		}
		if err := plainVaultPath(folder); err != nil {
			return err
		}
	}
	return nil
}
