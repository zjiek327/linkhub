package store

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"linkhub/internal/models"
)

// 内置模板 YAML 结构（与 Python 版 templates_builtin/*.yaml 对齐）
type templateYAML struct {
	Key                string                   `yaml:"key"`
	Name               string                   `yaml:"name"`
	Category           string                   `yaml:"category"`
	Icon               string                   `yaml:"icon"`
	Description        string                   `yaml:"description"`
	Spec               map[string]interface{}   `yaml:"spec"`
	DefaultConnections []map[string]interface{} `yaml:"default_connections"`
}

// SyncBuiltinTemplates 扫描目录，幂等 upsert 入库
func (s *Store) SyncBuiltinTemplates(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var t templateYAML
		if err := yaml.Unmarshal(data, &t); err != nil || t.Key == "" || t.Name == "" {
			continue
		}
		existing, _ := s.GetTemplateByKey(t.Key)
		tpl := models.Template{
			Key: t.Key, Name: t.Name, Category: t.Category, Icon: t.Icon,
			Description: t.Description, Spec: t.Spec, DefaultConnections: t.DefaultConnections,
			Builtin: true,
		}
		if tpl.Category == "" {
			tpl.Category = "开发板"
		}
		if tpl.Icon == "" {
			tpl.Icon = "🧩"
		}
		if existing == nil || existing.Builtin {
			s.UpsertTemplate(&tpl)
			count++
		}
	}
	return count, nil
}
