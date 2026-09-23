// 本文件提供停机后恢复管理员的命令，只连接数据库，不启动HTTP服务或后台任务。
package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	rbacstore "github.com/darkBaryon/bifrost/ee/internal/rbac/persistence"
	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"golang.org/x/term"
)

// maxPasswordLineBytes 限制从标准输入读取的字节数：密码最大长度加上行尾换行符。
// 超长输入最多读到这里，随后由密码长度检查拒绝。
const maxPasswordLineBytes = int64(identity.MaxPasswordBytes + len("\r\n"))

// RecoverAdmin 重置系统初始化时指定的管理员密码，并补回该账号的主管理员角色。
// 使用前必须停止所有连接这个数据库的Bifrost实例；账号和角色表必须已准备好，不在恢复时建表。
func RecoverAdmin(args []string) error {
	if len(args) == 0 || args[0] != "recover-admin" {
		return errors.New("usage: identity recover-admin --app-dir <directory>")
	}
	flags := flag.NewFlagSet("identity recover-admin", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("app-dir", "", "existing configuration directory")
	if flags.Parse(args[1:]) != nil || *dir == "" || flags.NArg() != 0 {
		return errors.New("expected --app-dir and no password arguments")
	}
	storeConfig, e := recoveryStoreConfig(*dir)
	if e != nil {
		return e
	}
	ctx := identity.WithDiagnosticOperation(context.Background(), "identity.recover-admin")
	log := bifrost.NewDefaultLogger(schemas.LogLevelError)
	store, e := configstore.NewConfigStore(ctx, storeConfig, log)
	if e != nil {
		return errors.New("cannot open recovery database")
	}
	defer store.Close(ctx)
	// 恢复使用已有账号，不需要首次初始化密钥，所以这里传空字符串。
	options, e := identityOptions("")
	if e != nil {
		return e
	}
	if e = rbacstore.RequireComplete(ctx, store.DB()); e != nil {
		return errors.New("complete RBAC migration is required for recovery")
	}
	svc, _, e := newConsoleServices(store.DB(), options, log)
	if e != nil {
		return e
	}
	var password string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "New administrator password: ")
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return errors.New("cannot read password")
		}
		password = string(raw)
	} else {
		raw, err := bufio.NewReader(io.LimitReader(os.Stdin, maxPasswordLineBytes)).ReadString('\n')
		if err != nil && err != io.EOF {
			return errors.New("cannot read password")
		}
		password = strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
	}
	if e = svc.Password.RecoverAdmin(ctx, password); e != nil {
		return identity.SafeError(e)
	}
	fmt.Fprintln(os.Stdout, "Administrator password updated; previous sessions revoked.")
	return nil
}

// recoveryStoreConfig 按 app-dir/config.json 找到实例使用的配置库；config.json 不存在或未配置 config_store 时，
// 与宿主启动规则（transports/bifrost-http/lib/config.go 的 LoadConfig）一致，使用目录下现存的 config.db。
// 只接受已存在的数据库，恢复命令不创建新库。
func recoveryStoreConfig(dir string) (*configstore.Config, error) {
	var config struct {
		Store *configstore.Config `json:"config_store"`
	}
	data, e := os.ReadFile(filepath.Join(dir, "config.json"))
	if e != nil && !os.IsNotExist(e) {
		return nil, errors.New("cannot read existing config.json")
	}
	if e == nil && json.Unmarshal(data, &config) != nil {
		return nil, errors.New("invalid config.json")
	}
	if config.Store == nil {
		path := filepath.Join(dir, "config.db")
		if _, e = os.Stat(path); e != nil {
			return nil, errors.New("existing config.db is required")
		}
		config.Store = &configstore.Config{Enabled: true, Type: configstore.ConfigStoreTypeSQLite, Config: &configstore.SQLiteConfig{Path: path}}
	}
	if !config.Store.Enabled {
		return nil, errors.New("database config store is required")
	}
	if sqliteConfig, ok := config.Store.Config.(*configstore.SQLiteConfig); ok {
		if info, err := os.Stat(sqliteConfig.Path); err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("existing SQLite configuration database is required")
		}
	}
	return config.Store, nil
}
