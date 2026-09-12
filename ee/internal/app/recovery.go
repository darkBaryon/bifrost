// 本文件提供离线主管理员恢复入口，不启动HTTP服务或后台worker。
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
	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"golang.org/x/term"
)

// RecoverAdmin 只恢复已经存在的chief；调用者须先停止全部使用该库的实例。
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
	data, e := os.ReadFile(filepath.Join(*dir, "config.json"))
	if e != nil {
		return errors.New("cannot read existing config.json")
	}
	var config struct {
		Store *configstore.Config `json:"config_store"`
	}
	if json.Unmarshal(data, &config) != nil {
		return errors.New("invalid config.json")
	}
	if config.Store == nil {
		path := filepath.Join(*dir, "config.db")
		if _, e = os.Stat(path); e != nil {
			return errors.New("existing config.db is required")
		}
		config.Store = &configstore.Config{Enabled: true, Type: configstore.ConfigStoreTypeSQLite, Config: &configstore.SQLiteConfig{Path: path}}
	}
	if !config.Store.Enabled {
		return errors.New("database config store is required")
	}
	if sqliteConfig, ok := config.Store.Config.(*configstore.SQLiteConfig); ok {
		if info, err := os.Stat(sqliteConfig.Path); err != nil || !info.Mode().IsRegular() {
			return errors.New("existing SQLite configuration database is required")
		}
	}
	ctx := identity.WithDiagnosticOperation(context.Background(), "identity.recover-admin")
	log := bifrost.NewDefaultLogger(schemas.LogLevelError)
	store, e := configstore.NewConfigStore(ctx, config.Store, log)
	if e != nil {
		return errors.New("cannot open recovery database")
	}
	defer store.Close(ctx)
	// 恢复只需要密码规则；初始化密钥不参与，传空。
	options, e := identityOptions("")
	if e != nil {
		return e
	}
	svc, e := newIdentityService(store.DB(), options)
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
		raw, err := bufio.NewReader(io.LimitReader(os.Stdin, 1025)).ReadString('\n')
		if err != nil && err != io.EOF {
			return errors.New("cannot read password")
		}
		password = strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
	}
	if e = svc.RecoverAdmin(ctx, password); e != nil {
		return identity.SafeError(e)
	}
	fmt.Fprintln(os.Stdout, "Administrator password updated; previous sessions revoked.")
	return nil
}
