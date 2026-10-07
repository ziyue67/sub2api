package provider

import (
	"context"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func easyPayNotifyTestProvider() *EasyPay {
	return &EasyPay{config: map[string]string{
		"pid": "1000", "pkey": "MERCHANT_SECRET_KEY",
		"apiBase":   "https://pay.example.com",
		"notifyUrl": "https://site.example.com/api/v1/payment/webhook/easypay",
		"returnUrl": "https://site.example.com/payment/result",
	}}
}

func TestEasyPayNotifyRejectsForgedSignReuseCallback(t *testing.T) {
	t.Parallel()
	e := easyPayNotifyTestProvider()
	createParams := map[string]string{
		"pid": "1000", "type": "alipay", "out_trade_no": "ORDER123",
		"notify_url": e.config["notifyUrl"],
		"return_url": "https://site.example.com/payment/result?trade_status=TRADE_SUCCESS",
		"name":       "balance recharge", "money": "650.00",
	}
	sign := easyPaySign(createParams, e.config["pkey"])
	cb := url.Values{}
	for k, v := range createParams {
		if k != "return_url" {
			cb.Set(k, v)
		}
	}
	cb.Set("return_url", "https://site.example.com/payment/result")
	raw := cb.Encode() + "&trade_status=TRADE_SUCCESS&sign=" + sign + "&sign_type=MD5"
	if _, err := e.VerifyNotification(context.Background(), raw, nil); err == nil {
		t.Fatal("forged sign-reuse callback must be rejected")
	}
}

func TestEasyPayNotifyRejectsOrderURLReplay(t *testing.T) {
	t.Parallel()
	e := easyPayNotifyTestProvider()
	params := map[string]string{
		"pid": "1000", "type": "alipay", "out_trade_no": "ORDER123",
		"notify_url": e.config["notifyUrl"], "return_url": e.config["returnUrl"],
		"name": "balance recharge", "money": "650.00",
	}
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	if _, err := e.VerifyNotification(context.Background(), q.Encode(), nil); err == nil {
		t.Fatal("replayed order URL must be rejected")
	}
}

func TestEasyPayNotifyAcceptsGenuineCallback(t *testing.T) {
	t.Parallel()
	e := easyPayNotifyTestProvider()
	params := map[string]string{
		"pid": "1000", "trade_no": "GATEWAY-1", "out_trade_no": "ORDER123",
		"type": "alipay", "name": "balance recharge", "money": "650.00",
		"trade_status": tradeStatusSuccess,
	}
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	n, err := e.VerifyNotification(context.Background(), q.Encode(), nil)
	if err != nil {
		t.Fatalf("genuine callback rejected: %v", err)
	}
	if n.Status != payment.ProviderStatusSuccess || n.TradeNo != "GATEWAY-1" || n.OrderID != "ORDER123" || n.Amount != 650 {
		t.Fatalf("unexpected notification: %+v", n)
	}
}

func TestEasyPayNotifyRejectsUnknownAndMissingTradeNo(t *testing.T) {
	t.Parallel()
	e := easyPayNotifyTestProvider()
	params := map[string]string{
		"pid": "1000", "out_trade_no": "ORDER123", "type": "alipay",
		"name": "balance recharge", "money": "650.00", "trade_status": tradeStatusSuccess,
	}
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	q.Set("device", "")
	if _, err := e.VerifyNotification(context.Background(), q.Encode(), nil); err == nil {
		t.Fatal("unknown callback parameter must be rejected")
	}
	q.Del("device")
	if _, err := e.VerifyNotification(context.Background(), q.Encode(), nil); err == nil {
		t.Fatal("successful callback without trade_no must be rejected")
	}
}
