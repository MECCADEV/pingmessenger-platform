package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
	"pingmessenger/internal/notify"
	"pingmessenger/internal/openim"
	"pingmessenger/internal/reports"
)

type createReportRequest struct {
	TargetPingID   string `json:"target_ping_id" validate:"required,len=12,numeric"`
	ConversationID string `json:"conversation_id" validate:"omitempty,min=1,max=256"`
	MessageID      string `json:"message_id" validate:"omitempty,min=1,max=128"`
	MessageSeq     int64  `json:"message_seq" validate:"omitempty,gt=0"`
	Reason         string `json:"reason" validate:"required,oneof=spam abuse harassment inappropriate other"`
	Details        string `json:"details" validate:"omitempty,max=2000"`
}

type updateReportRequest struct {
	Status string `json:"status" validate:"required,oneof=actioned dismissed"`
	Notes  string `json:"notes" validate:"omitempty,max=2000"`
}

func reportMessageMatches(message *openim.MessageSnapshot, messageID, targetOpenIMID string, sequence int64) bool {
	return message != nil && message.Seq == sequence && message.ClientMsgID == messageID && message.SendID == targetOpenIMID
}

func marshalReportSnapshot(message *openim.MessageSnapshot) ([]byte, error) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	var snapshot map[string]any
	if err = json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, err
	}
	var content any
	if len(message.Content) > 0 && json.Unmarshal(message.Content, &content) == nil {
		snapshot["content"] = content
	} else {
		delete(snapshot, "content")
		if len(message.Content) > 0 {
			snapshot["content_base64"] = base64.StdEncoding.EncodeToString(message.Content)
		}
	}
	return json.Marshal(snapshot)
}

func (a *API) registerReportRoutes(r interface {
	POST(string, fasthttp.RequestHandler)
	GET(string, fasthttp.RequestHandler)
	PUT(string, fasthttp.RequestHandler)
}) {
	r.POST("/v1/reports", a.createReport)
	r.POST("/v1/admin/session", a.moderatorSession)
	r.GET("/v1/admin/reports", a.listReports)
	r.GET("/v1/admin/reports/summary", a.reportSummary)
	r.PUT("/v1/admin/reports/{reportID}", a.updateReport)
}

func (a *API) createReport(ctx *fasthttp.RequestCtx) {
	reporterID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "report-create", reporterID, 10, 30) {
		return
	}
	var req createReportRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if (req.MessageID == "") != (req.MessageSeq == 0) || (req.MessageID == "") != (req.ConversationID == "") {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "conversation_id, message_id, and message_seq must be provided together"})
		return
	}
	targetID, err := a.users.FindActiveUserByPingID(context.Background(), req.TargetPingID)
	if err != nil || targetID == reporterID {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "target account is invalid"})
		return
	}
	input := reports.CreateInput{ReporterUserID: reporterID, TargetUserID: targetID, Reason: req.Reason, Details: strings.TrimSpace(req.Details)}
	if req.MessageID != "" {
		fetcher, supportsLookup := a.openIM.(openim.MessageFetcher)
		if !supportsLookup {
			writeJSON(ctx, http.StatusServiceUnavailable, map[string]string{"error": "message verification is unavailable"})
			return
		}
		reporterOpenIMID, reporterErr := a.users.OpenIMUserID(context.Background(), reporterID)
		targetOpenIMID, targetErr := a.users.OpenIMUserID(context.Background(), targetID)
		if reporterErr != nil || targetErr != nil {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "message participants are unavailable"})
			return
		}
		message, fetchErr := fetcher.GetMessageBySeq(context.Background(), reporterOpenIMID, req.ConversationID, req.MessageSeq)
		if fetchErr != nil || !reportMessageMatches(message, req.MessageID, targetOpenIMID, req.MessageSeq) {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "message does not match the target in this conversation"})
			return
		}
		snapshot, marshalErr := marshalReportSnapshot(message)
		if marshalErr != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not preserve reported message"})
			return
		}
		input.ConversationID, input.MessageID = &req.ConversationID, &req.MessageID
		input.MessageSeq, input.MessageSnapshot = &req.MessageSeq, snapshot
	}
	reportID, err := a.reports.Create(context.Background(), input)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeJSON(ctx, http.StatusConflict, map[string]string{"error": "this report was already submitted"})
			return
		}
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not save report"})
		return
	}
	moderatorEmails, notificationErr := a.users.AdminEmails(context.Background())
	status := "pending"
	if notificationErr == nil && len(moderatorEmails) > 0 && a.email != nil {
		status = "sent"
		for _, email := range moderatorEmails {
			sendErr := a.email.Send(context.Background(), &notify.Email{
				To: email, Subject: "New PingMessenger moderation report",
				Text: "A new moderation report is ready for review. Report ID: " + reportID + ". Reason: " + req.Reason + ". Review it in the admin reports queue.",
			})
			if sendErr != nil {
				status = "failed"
				notificationErr = errors.Join(notificationErr, sendErr)
			}
		}
	} else if notificationErr == nil && len(moderatorEmails) == 0 {
		notificationErr = fmt.Errorf("no active administrator has a verified email address")
	} else if notificationErr == nil {
		status = "failed"
		notificationErr = fmt.Errorf("moderation email delivery is unavailable")
	}
	_ = a.reports.SetNotificationResult(context.Background(), reportID, status, notificationErr)
	writeJSON(ctx, http.StatusCreated, map[string]string{"report_id": reportID})
}

func (a *API) authorizedReportAdmin(ctx *fasthttp.RequestCtx) bool {
	userID, _, ok := a.authenticated(ctx)
	if ok {
		isAdmin, err := a.users.IsAdmin(context.Background(), userID)
		if err != nil || !isAdmin {
			writeJSON(ctx, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return false
		}
		ctx.SetUserValue("reportAdminID", userID)
		ctx.SetUserValue("reportModeratorUsername", "")
		return true
	}
	username, authenticated := a.authenticateModerator(ctx)
	if !authenticated {
		if ctx.Response.StatusCode() == http.StatusTooManyRequests {
			return false
		}
		ctx.Response.Header.Set("WWW-Authenticate", `Basic realm="PingMessenger moderators", charset="UTF-8"`)
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "moderator authentication required"})
		return false
	}
	ctx.SetUserValue("reportAdminID", nil)
	ctx.SetUserValue("reportModeratorUsername", username)
	return true
}

func (a *API) authenticateModerator(ctx *fasthttp.RequestCtx) (string, bool) {
	if a.moderatorUsername == "" || a.moderatorPasswordHash == "" {
		return "", false
	}
	value := string(ctx.Request.Header.Peek("Authorization"))
	if !strings.HasPrefix(value, "Basic ") {
		a.enforceRate(ctx, "moderator-auth", "moderator", 5, 20)
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(value, "Basic ")))
	if err != nil {
		a.enforceRate(ctx, "moderator-auth", "moderator", 5, 20)
		return "", false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		a.enforceRate(ctx, "moderator-auth", "moderator", 5, 20)
		return "", false
	}
	userMatches := subtle.ConstantTimeCompare([]byte(username), []byte(a.moderatorUsername)) == 1
	if bcrypt.CompareHashAndPassword([]byte(a.moderatorPasswordHash), []byte(password)) != nil || !userMatches {
		a.enforceRate(ctx, "moderator-auth", "moderator", 5, 20)
		return "", false
	}
	return username, true
}

func (a *API) moderatorSession(ctx *fasthttp.RequestCtx) {
	username, ok := a.authenticateModerator(ctx)
	if !ok {
		if ctx.Response.StatusCode() == http.StatusTooManyRequests {
			return
		}
		ctx.Response.Header.Set("WWW-Authenticate", `Basic realm="PingMessenger moderators", charset="UTF-8"`)
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid moderator credentials"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"username": username, "role": "moderator"})
}

func (a *API) listReports(ctx *fasthttp.RequestCtx) {
	if !a.authorizedReportAdmin(ctx) {
		return
	}
	status := string(ctx.QueryArgs().Peek("status"))
	if status != "" && status != "open" && status != "actioned" && status != "dismissed" {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "status must be open, actioned, or dismissed"})
		return
	}
	limit, err := strconv.Atoi(string(ctx.QueryArgs().Peek("limit")))
	if err != nil || limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "limit must be between 1 and 100"})
		return
	}
	offset, err := strconv.Atoi(string(ctx.QueryArgs().Peek("offset")))
	if err != nil {
		offset = 0
	}
	if offset < 0 {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "offset cannot be negative"})
		return
	}
	items, err := a.reports.List(context.Background(), status, limit, offset)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load reports"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"reports": items, "limit": limit, "offset": offset})
}

func (a *API) reportSummary(ctx *fasthttp.RequestCtx) {
	if !a.authorizedReportAdmin(ctx) {
		return
	}
	summary, err := a.reports.Summary(context.Background())
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load report summary"})
		return
	}
	writeJSON(ctx, http.StatusOK, summary)
}

func (a *API) updateReport(ctx *fasthttp.RequestCtx) {
	if !a.authorizedReportAdmin(ctx) {
		return
	}
	var req updateReportRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	reportID := strings.TrimSpace(ctx.UserValue("reportID").(string))
	adminID, _ := ctx.UserValue("reportAdminID").(string)
	moderatorUsername, _ := ctx.UserValue("reportModeratorUsername").(string)
	if err := a.reports.UpdateStatus(context.Background(), reportID, adminID, moderatorUsername, req.Status, strings.TrimSpace(req.Notes)); err != nil {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "report not found"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"report_id": reportID, "status": req.Status, "reviewed_at": time.Now().UTC()})
}
