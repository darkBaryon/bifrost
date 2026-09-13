// 本文件协调启动期覆盖的逐行落库与内存更新；数据库部分成功由下次启动继续收敛。
package pricing

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// 存储适配将上游冲突和缺行错误映射为以下业务哨兵。
var (
	ErrRowExists  = errors.New("pricing override row already exists")
	ErrRowMissing = errors.New("pricing override row missing")
)

type overrideStore interface {
	List(context.Context) ([]OverrideRow, error)
	Create(context.Context, OverrideRow) error
	Update(context.Context, OverrideRow) error
	Delete(context.Context, string) error
}
type catalog interface {
	Upsert(...OverrideRow) error
	Delete(string)
}
type providerLister interface {
	List(context.Context) ([]Provider, error)
}
type logger interface {
	Info(string, ...interface{})
	Warn(string, ...interface{})
}

// Options 指定部署配置；nil USDToCNY 使用文件默认汇率，VendorMap 须来自 ParseVendorMap（无映射可为 nil）。
type Options struct {
	USDToCNY  *float64
	VendorMap map[string]string
}

// Deps 显式注入存储、厂商读取、内存目录和宿主日志，均不得为 nil。
type Deps struct {
	Overrides overrideStore
	Catalog   catalog
	Providers providerLister
	Log       logger
}

// Report 记录本次同步完成的数据库操作；失败时仅反映已经成功的部分。
type Report struct {
	Created, Updated, Deleted, Unchanged int
	Unrecognized                         []string
}

// Service 持有启动配置，在上游完成 Bootstrap 后执行一次 Sync。
type Service struct {
	overrides overrideStore
	catalog   catalog
	providers providerLister
	log       logger
	file      PriceFile
	mapping   map[string]string
}

// New 校验完整配置；无效文件不会访问任何存储。只拷贝需要覆盖的 Rates，
// 其余价格目录与 VendorMap 只读引用，调用方在 Service 使用期间不得修改。
func New(file PriceFile, opts Options, deps Deps) (*Service, error) {
	if err := file.validate(); err != nil {
		return nil, err
	}
	if deps.Overrides == nil || deps.Catalog == nil || deps.Providers == nil || deps.Log == nil {
		return nil, errors.New("pricing dependencies must not be nil")
	}
	copyFile := file
	copyFile.Rates = map[Currency]float64{}
	for key, value := range file.Rates {
		copyFile.Rates[key] = value
	}
	if opts.USDToCNY != nil {
		if !ValidRate(*opts.USDToCNY) {
			return nil, fmt.Errorf("%w: USD/CNY rate must be finite and positive", ErrConfig)
		}
		copyFile.Rates[CNY] = *opts.USDToCNY
	}
	if err := validateVendorMap(opts.VendorMap, copyFile); err != nil {
		return nil, err
	}
	return &Service{
		overrides: deps.Overrides,
		catalog:   deps.Catalog,
		providers: deps.Providers,
		log:       deps.Log,
		file:      copyFile,
		mapping:   opts.VendorMap,
	}, nil
}

// Sync 先完成全部数据库操作，再增量更新本节点目录；任意失败均返回错误，不回滚已提交行。
// 名称带保留前缀但 UUID 与厂商/模型不符的行只告警，不接管。
func (s *Service) Sync(ctx context.Context) (Report, error) {
	var report Report
	providers, err := s.providers.List(ctx)
	if err != nil {
		return report, fmt.Errorf("list providers: %w", err)
	}
	matches, unknown, err := matchVendors(providers, s.file, s.mapping, s.log)
	if err != nil {
		return report, err
	}
	report.Unrecognized = unknown
	vendors := map[string]Vendor{}
	for _, v := range s.file.Vendors {
		vendors[v.ID] = v
	}
	var desired []OverrideRow
	for _, hit := range matches {
		v := vendors[hit.VendorID]
		for _, m := range v.Models {
			row, err := makeOverride(hit.Provider, v, m, s.file.Rates)
			if err != nil {
				return report, err
			}
			desired = append(desired, row)
		}
	}
	sort.Slice(desired, func(i, j int) bool { return desired[i].ID < desired[j].ID })
	rows, err := s.overrides.List(ctx)
	if err != nil {
		return report, fmt.Errorf("list overrides: %w", err)
	}
	managed := map[string]OverrideRow{}
	protected := map[string]bool{}
	for _, row := range rows {
		if strings.HasPrefix(row.Name, namePrefix) && row.ProviderID != "" && row.ID == overrideID(row.ProviderID, row.Pattern) {
			managed[row.ID] = row
		} else {
			protected[row.ID] = true
			if strings.HasPrefix(row.Name, namePrefix) {
				s.log.Warn("pricing: reserved prefix row %s has a mismatched UUID; left unchanged", row.ID)
			}
		}
	}
	active := make([]OverrideRow, 0, len(desired))
	for _, row := range desired {
		if protected[row.ID] {
			s.log.Warn("pricing: override ID %s belongs to an unmanaged row; left unchanged", row.ID)
			continue
		}
		existing, exists := managed[row.ID]
		delete(managed, row.ID)
		switch {
		case !exists:
			err = s.overrides.Create(ctx, row)
			if errors.Is(err, ErrRowExists) {
				err = s.overrides.Update(ctx, row)
				if err == nil {
					report.Updated++
				}
			} else if err == nil {
				report.Created++
			}
		case !reflect.DeepEqual(existing, row):
			err = s.overrides.Update(ctx, row)
			if err == nil {
				report.Updated++
			}
		default:
			report.Unchanged++
		}
		if err != nil {
			return report, fmt.Errorf("persist override %s: %w", row.ID, err)
		}
		active = append(active, row)
	}
	deleted := make([]string, 0, len(managed))
	for id := range managed {
		deleted = append(deleted, id)
	}
	sort.Strings(deleted)
	for _, id := range deleted {
		err = s.overrides.Delete(ctx, id)
		if err != nil && !errors.Is(err, ErrRowMissing) {
			return report, fmt.Errorf("delete override %s: %w", id, err)
		}
		report.Deleted++
	}
	if err = s.catalog.Upsert(active...); err != nil {
		return report, fmt.Errorf("upsert pricing catalog: %w", err)
	}
	for _, id := range deleted {
		s.catalog.Delete(id)
	}
	s.log.Info("pricing sync: created=%d updated=%d deleted=%d unchanged=%d unrecognized=%v", report.Created, report.Updated, report.Deleted, report.Unchanged, report.Unrecognized)
	return report, nil
}
