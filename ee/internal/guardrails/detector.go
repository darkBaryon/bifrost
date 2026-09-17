// 本文件定义本地检测器契约、风险结果及其有效性检查。
package guardrails

import (
	"context"
	"reflect"
)

// Level 是检测器归一后的风险等级；零值无效，未发现风险用空结果表达。
type Level uint8

// 等级只表达相对高低，具体判定由检测器负责。
const (
	Low    Level = 1
	Medium Level = 2
	High   Level = 3
)

// Finding 只携带风险等级，不保留命中原文。
type Finding struct{ Level Level }

// Detector 是本地检测器契约。实现必须支持并发并响应 ctx 取消；调用为同步，框架不强杀 goroutine。
type Detector interface {
	Detect(ctx context.Context, text string) ([]Finding, error)
}

func validLevel(l Level) bool { return l >= Low && l <= High }

// 注册表允许结构体或指针实现接口，同时拒绝装入接口的 typed nil。
func nilDetector(d Detector) bool {
	if d == nil {
		return true
	}
	v := reflect.ValueOf(d)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
