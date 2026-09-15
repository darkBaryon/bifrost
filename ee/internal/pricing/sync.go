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
// 四步：算出期望的覆盖行、认出库里哪些行归本功能管、逐行对差落库、把结果推进内存目录。
func (s *Service) Sync(ctx context.Context) (Report, error) {
	var report Report
	desired, unknown, err := s.desiredRows(ctx)
	if err != nil {
		return report, err
	}
	report.Unrecognized = unknown
	managed, protected, err := s.classifyExisting(ctx)
	if err != nil {
		return report, err
	}
	active, err := s.applyDesired(ctx, desired, managed, protected, &report)
	if err != nil {
		return report, err
	}
	// applyDesired 已从 managed 中移走本次仍然存在的行，剩下的就是该删的。
	deleted, err := s.applyDeletions(ctx, managed, &report)
	if err != nil {
		return report, err
	}
	if err = s.updateCatalog(active, deleted); err != nil {
		return report, err
	}
	s.log.Info("pricing sync: created=%d updated=%d deleted=%d unchanged=%d unrecognized=%v", report.Created, report.Updated, report.Deleted, report.Unchanged, report.Unrecognized)
	return report, nil
}

// desiredRows 按接入地址认出自定义厂商，把各家价格展开成覆盖行；第二个返回值是未识别的厂商名。
// 结果按 ID 排序，使多节点同时启动时的写入顺序一致。
func (s *Service) desiredRows(ctx context.Context) ([]OverrideRow, []string, error) {
	providers, err := s.providers.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list providers: %w", err)
	}
	matches, unknown, err := matchVendors(providers, s.file, s.mapping, s.log)
	if err != nil {
		return nil, nil, err
	}
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
				return nil, nil, err
			}
			desired = append(desired, row)
		}
	}
	sort.Slice(desired, func(i, j int) bool { return desired[i].ID < desired[j].ID })
	return desired, unknown, nil
}

// classifyExisting 把全表分成本功能管理的行与不可触碰的行。归本功能管需同时满足保留名称前缀
// 与 UUID 自洽，只对上前缀不足以接管：管理员手工建的同名行因此得到保护，并留一条告警。
func (s *Service) classifyExisting(ctx context.Context) (managed map[string]OverrideRow, protected map[string]bool, err error) {
	rows, err := s.overrides.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list overrides: %w", err)
	}
	managed = map[string]OverrideRow{}
	protected = map[string]bool{}
	for _, row := range rows {
		if strings.HasPrefix(row.Name, namePrefix) && row.ProviderID != "" && row.ID == overrideID(row.ProviderID, row.Pattern) {
			managed[row.ID] = row
			continue
		}
		protected[row.ID] = true
		if strings.HasPrefix(row.Name, namePrefix) {
			s.log.Warn("pricing: reserved prefix row %s has a mismatched UUID; left unchanged", row.ID)
		}
	}
	return managed, protected, nil
}

// applyDesired 逐行落库并从 managed 中移走已处理的行，返回本次生效的全部行。
// 创建撞上唯一约束说明另一节点刚写过同一行，转为更新继续，不算失败。
func (s *Service) applyDesired(ctx context.Context, desired []OverrideRow, managed map[string]OverrideRow, protected map[string]bool, report *Report) ([]OverrideRow, error) {
	active := make([]OverrideRow, 0, len(desired))
	for _, row := range desired {
		if protected[row.ID] {
			s.log.Warn("pricing: override ID %s belongs to an unmanaged row; left unchanged", row.ID)
			continue
		}
		existing, exists := managed[row.ID]
		delete(managed, row.ID)
		if err := s.persistRow(ctx, row, existing, exists, report); err != nil {
			return nil, fmt.Errorf("persist override %s: %w", row.ID, err)
		}
		active = append(active, row)
	}
	return active, nil
}

// persistRow 写入单行并累加对应计数；整行深比较相同则跳过，任何字段变化都会触发更新。
func (s *Service) persistRow(ctx context.Context, row, existing OverrideRow, exists bool, report *Report) error {
	switch {
	case !exists:
		err := s.overrides.Create(ctx, row)
		if errors.Is(err, ErrRowExists) {
			if err = s.overrides.Update(ctx, row); err == nil {
				report.Updated++
			}
			return err
		}
		if err == nil {
			report.Created++
		}
		return err
	case !reflect.DeepEqual(existing, row):
		err := s.overrides.Update(ctx, row)
		if err == nil {
			report.Updated++
		}
		return err
	default:
		report.Unchanged++
		return nil
	}
}

// applyDeletions 删除本功能不再需要的行，返回已删除的 ID。
// 行已被另一节点删除视为成功，同样计入 Deleted，使各节点的报告保持一致。
func (s *Service) applyDeletions(ctx context.Context, stale map[string]OverrideRow, report *Report) ([]string, error) {
	deleted := make([]string, 0, len(stale))
	for id := range stale {
		deleted = append(deleted, id)
	}
	sort.Strings(deleted)
	for _, id := range deleted {
		if err := s.overrides.Delete(ctx, id); err != nil && !errors.Is(err, ErrRowMissing) {
			return nil, fmt.Errorf("delete override %s: %w", id, err)
		}
		report.Deleted++
	}
	return deleted, nil
}

// updateCatalog 把落库结果推进本节点内存目录；上游算费用读的是这份目录，
// 因此顺序固定为先落库再进内存，中途失败由下次启动重新收敛。
func (s *Service) updateCatalog(active []OverrideRow, deleted []string) error {
	if err := s.catalog.Upsert(active...); err != nil {
		return fmt.Errorf("upsert pricing catalog: %w", err)
	}
	for _, id := range deleted {
		s.catalog.Delete(id)
	}
	return nil
}
