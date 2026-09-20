// 本文件为密钥检测器的测试样本生成假密钥。
//
// 样本要覆盖各厂商的真实格式，但这些字符串不能进仓库：本仓在 GitHub 有公开备份，
// 推送保护会拦下它们，即使放行也会把「你的密钥被公开了」通知给 OpenAI、阿里云、腾讯云等厂商。
// 所以样本文件只写占位符与格式声明，密钥本体在运行时按名字确定性生成——仓库里不存在密钥形态的字符串。
// Python 侧（ee/scripts/guardrails-judge-e2e.py）实现同一算法，两边生成的值必须一致。
package secrets

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

// FakeSpec 声明一个假密钥怎么生成：固定前缀 + 指定字符集的确定性随机串。
type FakeSpec struct {
	Prefix  string `json:"prefix"`
	Charset string `json:"charset"` // alnum / hex / upperdigit / b64
	Length  int    `json:"length"`  // 前缀之后的长度
}

var fakeCharsets = map[string]string{
	"alnum":      "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
	"hex":        "0123456789abcdef",
	"upperdigit": "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
	"b64":        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/",
}

// fakeSecret 按名字生成固定的假密钥。名字相同则结果相同，样本的预期结果才可复现。
// 取值来自 SHA-256(域名前缀 + 名字 + 计数器) 的字节流，避免不同名字之间出现可见规律；
// 字母表顺序这类规律串会撞上 Gitleaks 的全局停用词，样本会假性漏检。
func fakeSecret(name string, spec FakeSpec) (string, error) {
	alphabet, ok := fakeCharsets[spec.Charset]
	if !ok {
		return "", fmt.Errorf("secrets: unknown fake charset %q for %q", spec.Charset, name)
	}
	if spec.Length <= 0 {
		return "", fmt.Errorf("secrets: fake %q needs a positive length", name)
	}
	var b strings.Builder
	b.WriteString(spec.Prefix)
	seed := sha256.Sum256([]byte("bifrost-guardrails-fake-secret:" + name))
	var counter uint32
	for b.Len()-len(spec.Prefix) < spec.Length {
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], counter)
		block := sha256.Sum256(append(seed[:], buf[:]...))
		for _, v := range block {
			if b.Len()-len(spec.Prefix) == spec.Length {
				break
			}
			b.WriteByte(alphabet[int(v)%len(alphabet)])
		}
		counter++
	}
	return b.String(), nil
}

// ExpandFakes 把文本里的 {{名字}} 换成生成的假密钥。未声明的占位符报错，避免样本静默失去密钥。
func ExpandFakes(text string, specs map[string]FakeSpec) (string, error) {
	for name, spec := range specs {
		placeholder := "{{" + name + "}}"
		if !strings.Contains(text, placeholder) {
			continue
		}
		value, err := fakeSecret(name, spec)
		if err != nil {
			return "", err
		}
		text = strings.ReplaceAll(text, placeholder, value)
	}
	if i := strings.Index(text, "{{"); i >= 0 {
		if j := strings.Index(text[i:], "}}"); j >= 0 {
			return "", fmt.Errorf("secrets: undeclared fake %s", text[i:i+j+2])
		}
	}
	return text, nil
}
