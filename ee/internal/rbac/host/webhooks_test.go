// 本文件通过真实Webhook Handler验证重投读取当前端点配置后、入队前判权。
package host

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

type webhookJobRecorder struct {
	configstore.ConfigStore
	jobs []tables.TableWebhookJob
}

func (s *webhookJobRecorder) CreateWebhookJob(_ context.Context, job *tables.TableWebhookJob) error {
	s.jobs = append(s.jobs, *job)
	return nil
}

type webhookHistory struct {
	logstore.LogStore
	delivery logstore.WebhookDelivery
	reads    int
}

func (s *webhookHistory) FindWebhookDeliveryByID(_ context.Context, id string) (*logstore.WebhookDelivery, error) {
	s.reads++
	if id != s.delivery.ID {
		return nil, logstore.ErrNotFound
	}
	result := s.delivery
	return &result, nil
}

func TestWebhookRedeliveryUsesEffectiveEndpoint(t *testing.T) {
	jobs := &webhookJobRecorder{}
	history := &webhookHistory{delivery: logstore.WebhookDelivery{ID: "b8032738-583d-4a2c-8b7b-fd94b6c7f691", EndpointID: "endpoint", WebhookID: "original-webhook", AsyncJobID: "job", Event: tables.WebhookEventAsyncJobCompleted}}
	config := &lib.Config{ConfigStore: jobs, LogsStore: history}
	endpoint := tables.TableWebhookEndpoint{ID: "endpoint", Name: "fixture", URL: "http://127.0.0.1:1/", IncludeResponse: false}
	config.SetWebhookEndpoint(&endpoint)
	repository := &routeRepository{codes: []rbac.Permission{rbac.NotificationsManage}}
	adapter := NewAdapter(rbac.New(repository), config, nil)
	routes := router.New()
	// 重投只入队；记录真实Handler的队列边界，不启动异步发送器或外部接收方。
	handlers.NewWebhookHandler(nil, config, nil).RegisterRoutes(routes, func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(c *fasthttp.RequestCtx) {
			wrap, err := adapter.Prepare(context.Background(), c, identity.Principal{AccountID: "member"})
			if err != nil {
				t.Fatalf("ordinary authorization: %v", err)
			}
			wrap(next)(c)
		}
	})
	if err := adapter.VerifyRoutes(routes); err != nil {
		t.Fatal(err)
	}
	invoke := func(wantStatus, wantJobs int) {
		t.Helper()
		before, _ := config.WebhookEndpointByID(endpoint.ID)
		saved := *before
		c := requestCtx("POST", "/api/webhooks/deliveries/"+history.delivery.ID+"/redeliver", `{}`)
		adapter.RootGuard(routes.Handler)(c)
		if c.Response.StatusCode() != wantStatus {
			t.Fatalf("status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
		}
		if len(jobs.jobs) != wantJobs {
			t.Fatalf("job writes=%d want=%d", len(jobs.jobs), wantJobs)
		}
		after, _ := config.WebhookEndpointByID(endpoint.ID)
		if !reflect.DeepEqual(saved, *after) {
			t.Fatal("redelivery mutated effective endpoint")
		}
		if wantStatus == fasthttp.StatusAccepted {
			var result map[string]any
			if json.Unmarshal(c.Response.Body(), &result) != nil || result["status"] != "queued" {
				t.Fatal("queue acknowledgement missing")
			}
		}
	}
	invoke(fasthttp.StatusAccepted, 1)
	// 历史 delivery 和空请求体保持不变，仅当前端点开关变更；权限按有效配置判定。
	endpoint.IncludeResponse = true
	config.SetWebhookEndpoint(&endpoint)
	invoke(fasthttp.StatusForbidden, 1)
	repository.codes = []rbac.Permission{rbac.NotificationsManage, rbac.LogsRevealContent}
	invoke(fasthttp.StatusAccepted, 2)
	if history.reads != 3 {
		t.Fatal("effective delivery was not loaded on every invocation")
	}
	for _, job := range jobs.jobs {
		if job.ID != history.delivery.WebhookID || job.EndpointID != endpoint.ID {
			t.Fatal("redelivery changed original identity")
		}
	}
}
