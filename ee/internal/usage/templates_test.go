// 本文件补充模板配置中无法通过普通请求样例覆盖的边界。
package usage

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemplateConfigBoundaries(t *testing.T) {
	good := Template{Name: "模板", Config: Config{MaxCostUSD: 1, AllowedModels: []string{"p/model/variant"}}}
	for _, amount := range []float64{math.NaN(), math.Inf(1), -1, 0} {
		bad := good
		bad.Config.MaxCostUSD = amount
		_, err := Normalize(bad)
		require.ErrorIs(t, err, ErrInvalid)
	}
	for _, pattern := range []string{"", "model", "/model", "p/", "p*/model", "p/model name", "p/m*"} {
		bad := good
		bad.Config.AllowedModels = []string{pattern}
		_, err := Normalize(bad)
		require.ErrorIs(t, err, ErrInvalid)
	}
	for _, duration := range []ResetDuration{Day, Week, Month, Year} {
		good.Config.ResetDuration = duration
		_, err := Normalize(good)
		require.NoError(t, err)
	}
	good.Description = strings.Repeat("述", MaxDescription+1)
	_, err := Normalize(good)
	require.ErrorIs(t, err, ErrInvalid)
}
