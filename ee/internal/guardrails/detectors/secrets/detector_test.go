// 本文件用构造的假密钥验证规则命中、等级、白名单与去重；不含任何真实凭据。
package secrets_test

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/detectors/secrets"
)

// fake 生成确定性的高熵假值，字符集可指定。
func fake(seed int64, n int, alphabet string) string {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

const (
	alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	lower = "abcdefghijklmnopqrstuvwxyz0123456789"
	hex   = "0123456789abcdef"
)

func newDetector(t *testing.T, opts secrets.Options) *secrets.Detector {
	t.Helper()
	d, err := secrets.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func levels(t *testing.T, d *secrets.Detector, text string) []guardrails.Level {
	t.Helper()
	findings, err := d.Detect(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]guardrails.Level, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Level)
	}
	return out
}

func TestRulesLoad(t *testing.T) {
	d := newDetector(t, secrets.Options{})
	if d.RuleCount() != 240 {
		t.Fatalf("rule count %d, regenerate rules.json or update this test", d.RuleCount())
	}
}

func TestVendorKeysAreHigh(t *testing.T) {
	d := newDetector(t, secrets.Options{})
	for name, text := range map[string]string{
		"openai project":   "OPENAI_API_KEY=sk-proj-" + fake(1, 74, alnum) + "T3BlbkFJ" + fake(2, 74, alnum),
		"anthropic api01":  "key: sk-ant-api01-" + fake(3, 93, alnum) + "AA",
		"gemini new":       "gemini key AQ.Ab8R" + fake(4, 46, alnum),
		"gcp legacy":       "AIza" + fake(5, 35, alnum),
		"alibaba short id": "LTAI" + fake(6, 16, alnum),
		"alibaba long id":  "LTAI" + fake(7, 20, alnum),
		"alibaba sts":      "STS." + fake(8, 20, alnum),
		"tencent":          "SecretId=AKID" + fake(9, 32, alnum),
		"tencent temp":     "AKID" + fake(10, 60, alnum) + "_x-y",
		"volcengine":       "AKLT" + fake(11, 44, alnum),
		"baidu qianfan":    "Bearer bce-v3/ALTAK-" + fake(12, 21, alnum) + "/" + fake(13, 40, hex),
		"groq":             "gsk_" + fake(14, 52, alnum),
		"cerebras":         "csk-" + fake(15, 48, lower),
		"openrouter":       "sk-or-v1-" + fake(16, 64, hex),
		"xai":              "xai-" + fake(17, 80, alnum),
		"replicate":        "r8_" + fake(18, 37, alnum),
		"minimax":          "sk-api-" + fake(19, 40, alnum),
		"bailian plan":     "sk-sp-" + fake(20, 24, alnum),
		"zhipu":            "api_key = " + fake(21, 32, hex) + "." + fake(22, 16, alnum),
		"dingtalk webhook": "https://oapi.dingtalk.com/robot/send?access_token=" + fake(23, 64, hex),
		"feishu webhook":   "https://open.feishu.cn/open-apis/bot/v2/hook/" + fake(24, 36, alnum),
		"wecom webhook":    "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=" + fake(25, 8, hex) + "-" + fake(26, 4, hex) + "-" + fake(27, 4, hex) + "-" + fake(28, 4, hex) + "-" + fake(29, 12, hex),
		"private key":      "-----BEGIN RSA PRIVATE KEY-----\n" + fake(30, 96, alnum+"+/") + "\n-----END RSA PRIVATE KEY-----",
		"azure cask":       fake(31, 52, alnum) + "JQQJ99" + fake(32, 1, alnum) + "B" + fake(33, 12, alnum) + "AAAB" + "ACOG" + fake(34, 4, alnum),
		"alibaba cais":     "SecurityToken=CAIS" + fake(35, 80, alnum+"+/="),
		"xai token":        "xai-token-" + fake(36, 80, alnum),
		"baidu altak":      "AccessKey: ALTAK" + fake(37, 21, alnum),
		"volcengine temp":  "AKTP" + fake(38, 43, alnum+"+/"),
		"minimax plan":     "sk-cp-" + fake(39, 40, alnum),
		"lark webhook":     "https://open.larksuite.com/open-apis/bot/v2/hook/" + fake(45, 36, alnum),
	} {
		t.Run(name, func(t *testing.T) {
			got := levels(t, d, "配置如下：\n"+text+"\n谢谢")
			if len(got) == 0 {
				t.Fatalf("no finding")
			}
			for _, l := range got {
				if l != guardrails.High {
					t.Fatalf("levels %v", got)
				}
			}
		})
	}
}

func TestGenericAndChineseContext(t *testing.T) {
	d := newDetector(t, secrets.Options{})
	for name, text := range map[string]string{
		"deepseek-like sk": "sk-" + fake(40, 32, hex),
		"chinese 密钥是":      "我的密钥是 " + fake(41, 32, alnum),
		"chinese 密码：":      "密码：" + fake(42, 24, alnum),
		"chinese 令牌为":      "令牌为" + fake(43, 40, alnum),
		"english password": "password: " + fake(44, 24, alnum),
	} {
		t.Run(name, func(t *testing.T) {
			got := levels(t, d, text)
			if len(got) != 1 || got[0] != guardrails.Medium {
				t.Fatalf("levels %v", got)
			}
		})
	}
}

func TestVendorHitSuppressesGeneric(t *testing.T) {
	d := newDetector(t, secrets.Options{})
	got := levels(t, d, "api_key: sk-proj-"+fake(50, 74, alnum)+"T3BlbkFJ"+fake(51, 74, alnum))
	if len(got) != 1 || got[0] != guardrails.High {
		t.Fatalf("generic result not deduplicated: %v", got)
	}
}

func TestNoFalsePositives(t *testing.T) {
	d := newDetector(t, secrets.Options{})
	for name, text := range map[string]string{
		"gateway key":        "我的网关密钥是 sk-bf-" + fake(60, 40, alnum) + "，帮我看看配置",
		"low entropy":        "password: aaaaaaaaaaaaaaaaaaaa",
		"placeholder":        "OPENAI_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"allowlisted sample": "AIzaSyabcdefghijklmnopqrstuvwxyz1234567",
		"stop word":          "token = example-token-abcdefghijklmnop",
		"plain chinese":      "请帮我把这段会议纪要整理成三条待办：周五前完成网络设备采购，客服团队扩招两人。",
		"code":               "func main() { key := os.Getenv(\"API_KEY\"); fmt.Println(len(key)) }",
		"env reference":      "api_key = ${OPENAI_API_KEY}",
		// 新增规则的边界反例：前缀对但长度不足、字符集不对或缺关键标记。
		"gemini short":       "AQ.Ab8R" + fake(61, 20, alnum),
		"groq short":         "gsk_" + fake(62, 30, alnum),
		"openrouter short":   "sk-or-v1-" + fake(63, 20, hex),
		"replicate short":    "r8_" + fake(64, 20, alnum),
		"azure no marker":    fake(65, 84, alnum),
		"tencent short":      "AKID" + fake(66, 10, alnum),
		"volcengine short":   "AKLT" + fake(67, 12, alnum),
		"zhipu wrong shape":  fake(68, 20, hex) + "." + fake(69, 16, alnum),
		"dingtalk other url": "https://oapi.dingtalk.com/robot/other?access_token=" + fake(70, 32, hex),
		"wecom bad key":      "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=short",
		"sk generic short":   "sk-" + fake(71, 12, hex),
		"sts short":          "STS." + fake(72, 4, alnum),
	} {
		t.Run(name, func(t *testing.T) {
			if got := levels(t, d, text); len(got) != 0 {
				t.Fatalf("unexpected findings %v", got)
			}
		})
	}
}

func TestIgnoredKeywords(t *testing.T) {
	// 忽略词选一个不在 Gitleaks 停用词表里的串，避免基线就被豁免。
	value := "QaZ" + fake(70, 20, alnum) + "ZQXJV" + fake(71, 8, alnum)
	text := "password: " + value
	if got := levels(t, newDetector(t, secrets.Options{}), text); len(got) != 1 {
		t.Fatalf("baseline %v", got)
	}
	d := newDetector(t, secrets.Options{IgnoredKeywords: []string{" zqxjv ", "Zqxjv"}})
	if got := levels(t, d, text); len(got) != 0 {
		t.Fatalf("ignored keyword not applied: %v", got)
	}
}

func TestCancellation(t *testing.T) {
	d := newDetector(t, secrets.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Detect(ctx, "password: "+fake(80, 24, alnum)); err != context.Canceled {
		t.Fatalf("err=%v", err)
	}
}

func TestConcurrentUse(t *testing.T) {
	d := newDetector(t, secrets.Options{})
	text := "LTAI" + fake(90, 20, alnum)
	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			if got := levels(t, d, text); len(got) != 1 {
				t.Errorf("levels %v", got)
			}
		}()
	}
	for range 8 {
		<-done
	}
}

// 40KB 普通中英混合文本的扫描耗时应在毫秒级；-race 下会放大约十倍，这里只守一个宽松的上限，具体数字见独立验证记录。
func TestPlainTextScanBudget(t *testing.T) {
	d := newDetector(t, secrets.Options{})
	paragraph := "系统设计评审：本周完成网关限流与审计日志接入，key results 已同步到项目看板。The access token rotation policy is documented in the wiki. "
	text := strings.Repeat(paragraph, 40*1024/len(paragraph)+1)
	start := time.Now()
	if got := levels(t, d, text); len(got) != 0 {
		t.Fatalf("unexpected findings %v", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("plain text scan took %v", elapsed)
	}
}
