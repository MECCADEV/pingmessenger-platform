package httpapi

import (
	"context"
	"fmt"
	"log"

	json "github.com/goccy/go-json"
	"pingmessenger/internal/openim"
	"pingmessenger/internal/payments"
)

func (a *API) emitPaymentMessage(ctx context.Context, payment *payments.Payment, kind string) error {
	if a.openIM == nil {
		return fmt.Errorf("OpenIM client is not configured")
	}
	exists, err := a.payments.MessageProjectionExists(ctx, payment.PaymentID, kind, payment.Version)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	fromID, err := a.openIMIdentity(ctx, payment.FromUserID)
	if err != nil {
		return fmt.Errorf("resolve payment sender: %w", err)
	}
	toID, err := a.openIMIdentity(ctx, payment.ToUserID)
	if err != nil {
		return fmt.Errorf("resolve payment recipient: %w", err)
	}
	payload, err := payments.EncodePaymentMessage(payments.NewPaymentMessage(payment, kind))
	if err != nil {
		return err
	}
	statusText := "Payment " + payment.Status
	offlinePushExtra, err := json.Marshal(map[string]string{
		"payment_id": payment.PaymentID,
		"status":     payment.Status,
		"reason":     payment.UpdateReason,
	})
	if err != nil {
		return fmt.Errorf("encode payment push metadata: %w", err)
	}
	extra, err := json.Marshal(map[string]string{
		"source":     "pingmessenger-platform",
		"payment_id": payment.PaymentID,
	})
	if err != nil {
		return fmt.Errorf("encode payment metadata: %w", err)
	}
	response, err := a.openIM.SendCustomMessage(ctx, &openim.SendCustomMessageRequest{
		SendID:      fromID,
		RecvID:      toID,
		ContentType: payments.CustomContentType,
		SessionType: payments.SingleChatSession,
		Content: map[string]any{
			"data":        string(payload),
			"extension":   `{"schema":"pingmessenger.payment.v1","render":"payment_card"}`,
			"description": statusText,
		},
		OfflinePushInfo: map[string]any{
			"title": "Payment update",
			"desc":  statusText,
			"ex":    string(offlinePushExtra),
		},
		Ex: string(extra),
	})
	if err != nil {
		return err
	}
	return a.payments.RecordMessageProjection(ctx, payment.PaymentID, kind, payment.Version, response.ServerMsgID, response.ClientMsgID, response.SendTime)
}

func (a *API) emitPaymentMessageAsync(payment *payments.Payment, kind string) {
	go func() {
		if err := a.emitPaymentMessage(context.Background(), payment, kind); err != nil {
			log.Printf("payment OpenIM projection failed payment=%s kind=%s: %v", payment.PaymentID, kind, err)
		}
	}()
}
