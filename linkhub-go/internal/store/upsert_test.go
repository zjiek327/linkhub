package store

import (
	"os"
	"testing"

	"linkhub/internal/models"
)

func TestUpsertTemplate(t *testing.T) {
	os.Remove("/tmp/upsert-test.db")
	st, err := Open("/tmp/upsert-test.db")
	if err != nil {
		t.Fatal(err)
	}
	tpl := &models.Template{Key: "test", Name: "测试", Category: "开发板", Icon: "🧩"}
	if err := st.UpsertTemplate(tpl); err != nil {
		t.Fatal("upsert:", err)
	}
	tpls, err := st.ListTemplates()
	if err != nil {
		t.Fatal("list:", err)
	}
	if len(tpls) != 1 {
		t.Fatalf("期望 1 条，得到 %d", len(tpls))
	}
}
