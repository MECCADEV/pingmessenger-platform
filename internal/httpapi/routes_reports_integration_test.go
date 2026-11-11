package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/notify"
	"pingmessenger/internal/openim"
	"pingmessenger/internal/ratelimit"
	"pingmessenger/internal/reports"
)

type reportOpenIMMock struct {
	openim.Client
	message *openim.MessageSnapshot
	err     error
}

func (m *reportOpenIMMock) GetMessageBySeq(_ context.Context, userID, conversationID string, seq int64) (*openim.MessageSnapshot, error) {
	if userID == "" || conversationID != "conversation-1" || seq != 42 {
		return nil, errors.New("unexpected lookup")
	}
	return m.message, m.err
}

type reportEmailMock struct{ sent []*notify.Email }

func (m *reportEmailMock) Send(_ context.Context, message *notify.Email) error {
	m.sent = append(m.sent, message)
	return nil
}

func TestReportHTTPValidatesSnapshotAndAdminReview(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM moderation_reports WHERE reporter_user_id=ANY($1::uuid[]) OR target_user_id=ANY($1::uuid[])`, ids)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, ids)
	})
	insertUser := func(id string, admin bool, openIMID string) {
		t.Helper()
		username := "report-http-" + strings.ReplaceAll(id, "-", "")
		if _, insertErr := pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status,openim_user_id,is_admin) VALUES($1,$2,'test','active',$3,$4)`, id, username, openIMID, admin); insertErr != nil {
			t.Fatalf("insert user: %v", insertErr)
		}
		if _, insertErr := pool.Exec(ctx, `INSERT INTO device_sessions(user_id,refresh_token_hash,platform_id,expires_at) VALUES($1,$2,'ios',now()+interval '1 hour')`, id, uuid.NewString()); insertErr != nil {
			t.Fatalf("insert session: %v", insertErr)
		}
	}
	insertUser(ids[0], false, "im-"+ids[0])
	insertUser(ids[1], false, "im-"+ids[1])
	insertUser(ids[2], true, "im-"+ids[2])
	adminEmail := "moderator+" + strings.ReplaceAll(ids[2], "-", "") + "@example.test"
	if _, err = pool.Exec(ctx, `INSERT INTO user_contacts(user_id,kind,value_normalized,verified_at,is_primary) VALUES($1,'email',$2,now(),true)`, ids[2], adminEmail); err != nil {
		t.Fatal(err)
	}
	var targetPingID string
	if err = pool.QueryRow(ctx, `SELECT ping_id FROM users WHERE id=$1`, ids[1]).Scan(&targetPingID); err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewTokenManager(strings.Repeat("r", 40), "report-http-test", time.Hour, time.Hour)
	access := func(userID string) string {
		t.Helper()
		var sessionID string
		if queryErr := pool.QueryRow(ctx, `SELECT id::text FROM device_sessions WHERE user_id=$1 AND revoked_at IS NULL`, userID).Scan(&sessionID); queryErr != nil {
			t.Fatal(queryErr)
		}
		token, _, issueErr := tokens.IssueAccess(userID, sessionID)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		return token
	}
	serverMessage := &openim.MessageSnapshot{ClientMsgID: "message-42", SendID: "im-" + ids[1], Seq: 42, Content: []byte(`{"text":"server copy"}`)}
	openIM := &reportOpenIMMock{message: serverMessage}
	mailer := &reportEmailMock{}
	api := &API{users: auth.NewUserRepository(pool), reports: reports.NewRepository(pool), openIM: openIM, tokens: tokens, email: mailer, limiter: ratelimit.NewLocal(), validate: validator.New()}
	body := func(messageID string) string {
		encoded, _ := json.Marshal(map[string]any{"target_ping_id": targetPingID, "conversation_id": "conversation-1", "message_id": messageID, "message_seq": 42, "reason": "abuse", "details": "please review"})
		return string(encoded)
	}
	request := func(handler fasthttp.RequestHandler, path, token, payload string) *fasthttp.RequestCtx {
		t.Helper()
		requestCtx := &fasthttp.RequestCtx{}
		requestCtx.Request.Header.SetMethod("POST")
		requestCtx.Request.SetRequestURI(path)
		requestCtx.Request.Header.Set("Authorization", "Bearer "+token)
		if payload != "" {
			requestCtx.Request.SetBodyString(payload)
		}
		handler(requestCtx)
		return requestCtx
	}
	created := request(api.createReport, "/v1/reports", access(ids[0]), body("message-42"))
	if created.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Response.StatusCode(), created.Response.Body())
	}
	var createdBody struct {
		ReportID string `json:"report_id"`
	}
	if err = json.Unmarshal(created.Response.Body(), &createdBody); err != nil || createdBody.ReportID == "" {
		t.Fatalf("create response=%s err=%v", created.Response.Body(), err)
	}
	foundModeratorEmail := false
	for _, sent := range mailer.sent {
		if sent.To == adminEmail {
			foundModeratorEmail = true
		}
	}
	if !foundModeratorEmail {
		t.Fatalf("moderator email %q missing from sends=%+v", adminEmail, mailer.sent)
	}
	var notificationStatus string
	if err = pool.QueryRow(ctx, `SELECT notification_status FROM moderation_reports WHERE id=$1`, createdBody.ReportID).Scan(&notificationStatus); err != nil || notificationStatus != "sent" {
		t.Fatalf("notification status=%q err=%v", notificationStatus, err)
	}

	openIM.message = &openim.MessageSnapshot{ClientMsgID: "different-message", SendID: "im-" + ids[1], Seq: 42}
	forged := request(api.createReport, "/v1/reports", access(ids[0]), body("message-43"))
	if forged.Response.StatusCode() != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("forged report status=%d body=%s", forged.Response.StatusCode(), forged.Response.Body())
	}

	requestCtx := &fasthttp.RequestCtx{}
	requestCtx.Request.Header.SetMethod("GET")
	requestCtx.Request.SetRequestURI("/v1/admin/reports?status=open")
	requestCtx.Request.Header.Set("Authorization", "Bearer "+access(ids[0]))
	api.listReports(requestCtx)
	if requestCtx.Response.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("non-admin list status=%d", requestCtx.Response.StatusCode())
	}

	requestCtx = &fasthttp.RequestCtx{}
	requestCtx.Request.Header.SetMethod("GET")
	requestCtx.Request.SetRequestURI("/v1/admin/reports?status=open")
	requestCtx.Request.Header.Set("Authorization", "Bearer "+access(ids[2]))
	api.listReports(requestCtx)
	if requestCtx.Response.StatusCode() != fasthttp.StatusOK || !strings.Contains(string(requestCtx.Response.Body()), createdBody.ReportID) || !strings.Contains(string(requestCtx.Response.Body()), "server copy") {
		t.Fatalf("admin report list status=%d body=%s", requestCtx.Response.StatusCode(), requestCtx.Response.Body())
	}

	update := &fasthttp.RequestCtx{}
	update.Request.Header.SetMethod("PUT")
	update.Request.SetRequestURI("/v1/admin/reports/" + createdBody.ReportID)
	update.Request.Header.Set("Authorization", "Bearer "+access(ids[2]))
	update.Request.SetBodyString(`{"status":"actioned","notes":"reviewed"}`)
	update.SetUserValue("reportID", createdBody.ReportID)
	api.updateReport(update)
	if update.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("update status=%d body=%s", update.Response.StatusCode(), update.Response.Body())
	}
	items, err := api.reports.List(ctx, "actioned", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.ID == createdBody.ReportID && item.ResolutionNotes == "reviewed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("updated report missing from actioned queue: %+v", items)
	}

	if err := api.ConfigureModeratorCredentials("moderator-e2e", "moderator-e2e-password-2026"); err != nil {
		t.Fatal(err)
	}
	moderatorAuthorization := "Basic " + base64.StdEncoding.EncodeToString([]byte("moderator-e2e:moderator-e2e-password-2026"))
	moderatorList := &fasthttp.RequestCtx{}
	moderatorList.Request.Header.SetMethod("GET")
	moderatorList.Request.SetRequestURI("/v1/admin/reports?status=actioned")
	moderatorList.Request.Header.Set("Authorization", moderatorAuthorization)
	api.listReports(moderatorList)
	if moderatorList.Response.StatusCode() != fasthttp.StatusOK || !strings.Contains(string(moderatorList.Response.Body()), createdBody.ReportID) {
		t.Fatalf("moderator report list status=%d body=%s", moderatorList.Response.StatusCode(), moderatorList.Response.Body())
	}
	moderatorUpdate := &fasthttp.RequestCtx{}
	moderatorUpdate.Request.Header.SetMethod("PUT")
	moderatorUpdate.Request.SetRequestURI("/v1/admin/reports/" + createdBody.ReportID)
	moderatorUpdate.Request.Header.Set("Authorization", moderatorAuthorization)
	moderatorUpdate.Request.SetBodyString(`{"status":"dismissed","notes":"Reviewed in moderator console"}`)
	moderatorUpdate.SetUserValue("reportID", createdBody.ReportID)
	api.updateReport(moderatorUpdate)
	if moderatorUpdate.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("moderator update status=%d body=%s", moderatorUpdate.Response.StatusCode(), moderatorUpdate.Response.Body())
	}
	var reviewer string
	if err = pool.QueryRow(ctx, `SELECT reviewed_by_moderator FROM moderation_reports WHERE id=$1`, createdBody.ReportID).Scan(&reviewer); err != nil || reviewer != "moderator-e2e" {
		t.Fatalf("moderator reviewer identity=%q err=%v", reviewer, err)
	}
	moderatorSummary := &fasthttp.RequestCtx{}
	moderatorSummary.Request.Header.SetMethod("GET")
	moderatorSummary.Request.SetRequestURI("/v1/admin/reports/summary")
	moderatorSummary.Request.Header.Set("Authorization", moderatorAuthorization)
	api.reportSummary(moderatorSummary)
	if moderatorSummary.Response.StatusCode() != fasthttp.StatusOK || !strings.Contains(string(moderatorSummary.Response.Body()), `"dismissed":1`) {
		t.Fatalf("moderator summary status=%d body=%s", moderatorSummary.Response.StatusCode(), moderatorSummary.Response.Body())
	}
}
