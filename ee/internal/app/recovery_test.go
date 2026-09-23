// 本文件验证离线恢复按实例启动时的规则找到配置库，缺少 config.json 时与宿主一样使用目录下的 config.db。
package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/framework/configstore"
)

// recoveryDir 创建 app-dir；files 中的名称以空文件写入，值为 true 时改建同名目录。
func recoveryDir(t *testing.T, files map[string]bool) string {
	t.Helper()
	dir := t.TempDir()
	for name, isDir := range files {
		path := filepath.Join(dir, name)
		if isDir {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRecoveryStoreConfigUsesDefaultDatabase(t *testing.T) {
	for name, withConfigJSON := range map[string]bool{"missing config.json": false, "config.json without config_store": true} {
		t.Run(name, func(t *testing.T) {
			dir := recoveryDir(t, map[string]bool{"config.db": false})
			if withConfigJSON {
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := recoveryStoreConfig(dir)
			if err != nil {
				t.Fatalf("recoveryStoreConfig() error = %v", err)
			}
			sqliteConfig, ok := got.Config.(*configstore.SQLiteConfig)
			if !got.Enabled || got.Type != configstore.ConfigStoreTypeSQLite || !ok || sqliteConfig.Path != filepath.Join(dir, "config.db") {
				t.Fatalf("recoveryStoreConfig() = %+v, want SQLite at %s", got, filepath.Join(dir, "config.db"))
			}
		})
	}
}

func TestRecoveryStoreConfigRejectsMissingOrUnreadableSources(t *testing.T) {
	cases := map[string]struct {
		files map[string]bool
		want  string
	}{
		"missing config.json and config.db": {files: map[string]bool{}, want: "existing config.db is required"},
		"unreadable config.json":            {files: map[string]bool{"config.json": true, "config.db": false}, want: "cannot read existing config.json"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := recoveryStoreConfig(recoveryDir(t, tc.files))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("recoveryStoreConfig() error = %v, want %q", err, tc.want)
			}
		})
	}
}
